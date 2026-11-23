package lower

import (
	"fmt"
	"go/ast"
	"go/token"
	"go/types"
	"strconv"
	"strings"

	"github.com/achille-roussel/wasmgc-go/internal/ir"
	"github.com/achille-roussel/wasmgc-go/internal/mono"
)

type pkgLower struct {
	fset  *token.FileSet
	files []*ast.File
	pkg   *types.Package
	info  *types.Info
	mod   *ir.Module
	meta  *meta
	mono  *mono.Table
	subst mono.Subst // active while lowering a specialization

	initCount int
	initLower *funcLower                  // shared across package var inits
	globals   map[types.Object]*ir.Global // package-level vars
}

func (l *pkgLower) lower() error {
	if l.globals == nil {
		l.globals = map[types.Object]*ir.Global{}
	}

	var initStmts []ir.Stmt
	for _, f := range l.files {
		for _, d := range f.Decls {
			gd, ok := d.(*ast.GenDecl)
			if !ok || gd.Tok != token.VAR {
				continue
			}
			for _, sp := range gd.Specs {
				vs := sp.(*ast.ValueSpec)
				for i, name := range vs.Names {
					if name.Name == "_" {
						continue
					}
					obj := l.info.Defs[name]
					if obj == nil {
						continue
					}
					ty, err := l.mapType(obj.Type())
					if err != nil {
						return l.errorf(name, "%v", err)
					}
					gname := pkgIRPrefix(l.pkg) + "." + name.Name
					g := &ir.Global{Name: gname, Typ: ty, Mut: true}
					l.mod.Globals = append(l.mod.Globals, g)
					l.globals[obj] = g
					if vs.Values != nil && i < len(vs.Values) {
						// One shared init lowerer so temp/var names stay unique
						// across all package-level var initializers (they become
						// a single init.N function).
						if l.initLower == nil {
							l.initLower = l.newInitLower()
						}
						fl := l.initLower
						ex, stmts, err := fl.coerceExpr(vs.Values[i], obj.Type(), ty)
						if err != nil {
							return err
						}
						initStmts = append(initStmts, stmts...)
						initStmts = append(initStmts, &ir.ExprStmt{X: &ir.Instr{
							Op:     ir.OpGlobalSet,
							Global: gname,
							Args:   []ir.Expr{ex},
						}})
					}
				}
			}
		}
	}

	type fnDecl struct {
		file *ast.File
		decl *ast.FuncDecl
	}
	var funcs []fnDecl
	var inits []fnDecl
	var bodyless []fnDecl
	for _, f := range l.files {
		for _, d := range f.Decls {
			fd, ok := d.(*ast.FuncDecl)
			if !ok {
				continue
			}
			if fd.Body == nil {
				// Body-less: asm / intrinsic / wasmimport candidate (design 12).
				bodyless = append(bodyless, fnDecl{f, fd})
				continue
			}
			if fd.Name.Name == "init" && fd.Recv == nil && (fd.Type.Params == nil || len(fd.Type.Params.List) == 0) {
				inits = append(inits, fnDecl{f, fd})
				continue
			}
			funcs = append(funcs, fnDecl{f, fd})
		}
	}

	if len(initStmts) > 0 {
		name := l.initName()
		var preamble []ir.Stmt
		if l.initLower != nil {
			for _, v := range l.initLower.extraLocals {
				preamble = append(preamble, &ir.VarDef{Var: v})
			}
		}
		body := &ir.Seq{Stmts: append(preamble, append(initStmts, &ir.Return{})...)}
		locals := []*ir.Var(nil)
		if l.initLower != nil {
			locals = l.initLower.extraLocals
		}
		l.mod.Funcs = append(l.mod.Funcs, &ir.Func{Name: name, Locals: locals, Body: body})
		l.meta.initOrder = append(l.meta.initOrder, name)
	}

	for _, id := range inits {
		name := l.initName()
		fn, err := l.lowerFunc(id.decl, name)
		if err != nil {
			return err
		}
		l.mod.Funcs = append(l.mod.Funcs, fn)
		l.meta.initOrder = append(l.meta.initOrder, name)
	}
	for _, fd := range funcs {
		// Skip generic origins: specialized copies are emitted by emitMonoInstances.
		// Methods of generic types have TypeParams on the receiver, not the method.
		if obj, _ := l.info.Defs[fd.decl.Name].(*types.Func); obj != nil {
			if isGenericOrigin(obj) {
				continue
			}
		}
		name := l.funcIRName(fd.decl)
		if name == "main.main" {
			l.meta.hasMain = true
		}
		fn, err := l.lowerFunc(fd.decl, name)
		if err != nil {
			return err
		}
		l.mod.Funcs = append(l.mod.Funcs, fn)
	}
	for _, fd := range bodyless {
		name := l.funcName(fd.decl.Name.Name)
		fn, err := l.lowerBodyless(fd.decl, name)
		if err != nil {
			return err
		}
		l.mod.Funcs = append(l.mod.Funcs, fn)
	}
	return l.emitMonoInstances()
}

func (l *pkgLower) newInitLower() *funcLower {
	return &funcLower{
		pkg:    l,
		fset:   l.fset,
		info:   l.info,
		mod:    l.mod,
		irName: pkgIRPrefix(l.pkg) + ".init",
		vars:   map[types.Object]*ir.Var{},
		labels: map[string]*labelTargets{},
	}
}

func (l *pkgLower) initName() string {
	n := l.initCount
	l.initCount++
	return pkgIRPrefix(l.pkg) + ".init." + strconv.Itoa(n)
}

func (l *pkgLower) funcName(name string) string {
	return pkgIRPrefix(l.pkg) + "." + name
}

// pkgIRPrefix is the IR qualifier for funcs/globals/types of pkg.
// Packages whose Path differs from Name (unicode/utf8, internal/strconv) use a
// sanitized path so internal/strconv and strconv do not collide on "strconv".
func pkgIRPrefix(pkg *types.Package) string {
	if pkg == nil {
		return "main"
	}
	if pkg.Name() == "main" {
		return "main"
	}
	path := pkg.Path()
	if path == "" || path == pkg.Name() {
		return pkg.Name()
	}
	return strings.ReplaceAll(path, "/", ".")
}

func (l *pkgLower) funcIRName(fd *ast.FuncDecl) string {
	obj, _ := l.info.Defs[fd.Name].(*types.Func)
	if obj == nil {
		return l.funcName(fd.Name.Name)
	}
	if fd.Recv != nil {
		return methodIRName(obj)
	}
	return l.funcName(fd.Name.Name)
}

func (l *pkgLower) lowerFunc(fd *ast.FuncDecl, irName string) (*ir.Func, error) {
	fl, err := l.newFuncLower(fd, irName)
	if err != nil {
		return nil, err
	}

	// Pre-scan for recover so the prologue is installed before body lowering
	// finishes (recover sites set the flag during expr lowering too).
	if containsRecover(fd.Body, fl.info) {
		fl.needsRecoverPrologue = true
		fl.allowVar = fl.newVar("allow", ir.TypeI32)
	}
	if containsPanicCall(fd.Body, fl.info) {
		fl.mayPanic = true
	}

	body, err := fl.stmtBlock(fd.Body)
	if err != nil {
		return nil, err
	}
	seq, ok := body.(*ir.Seq)
	if !ok {
		seq = &ir.Seq{Stmts: []ir.Stmt{body}}
	}
	// Ensure void functions end with return when falling off the end.
	if len(fl.results) == 0 && !endsWithReturn(seq) {
		seq.Stmts = append(seq.Stmts, &ir.Return{})
	}

	// Clone by-value struct params/receivers when the callee assigns a field
	// (docs/design/03): caller must not observe the write.
	var clones []ir.Stmt
	for i := 0; i < fl.sig.Params().Len(); i++ {
		p := fl.sig.Params().At(i)
		if isPointer(p.Type()) || !isStruct(p.Type()) {
			continue
		}
		if !fl.paramFieldAssigned(p, fd.Body) {
			continue
		}
		clone, err := fl.cloneStruct(fl.vars[p])
		if err != nil {
			return nil, err
		}
		clones = append(clones, clone...)
	}
	if recv := fl.sig.Recv(); recv != nil && !isPointer(recv.Type()) && isStruct(recv.Type()) {
		if fl.paramFieldAssigned(recv, fd.Body) {
			clone, err := fl.cloneStruct(fl.vars[recv])
			if err != nil {
				return nil, err
			}
			clones = append(clones, clone...)
		}
	}

	var preamble []ir.Stmt
	for _, v := range fl.namedResults {
		preamble = append(preamble, &ir.VarDef{Var: v})
	}
	for _, v := range fl.extraLocals {
		preamble = append(preamble, &ir.VarDef{Var: v})
	}
	// Zero-init non-defaultable locals (e.g. $go.string named results) and
	// user named structs ($go.struct.*): Wasm GC rejects uninitialized (ref T)
	// locals; named struct values are heap objects even when typed refnull.
	// Capture cells are initialized via cellInits (struct.new), not null.
	for _, v := range append(fl.namedResults, fl.extraLocals...) {
		if r, ok := v.Typ.(*ir.Ref); ok && strings.HasPrefix(r.Named, "$go.cell.") {
			continue
		}
		if !needsZeroInit(v.Typ) && !isNamedStructRef(v.Typ) {
			continue
		}
		z, err := fl.zeroValue(v.Typ)
		if err != nil {
			return nil, err
		}
		preamble = append(preamble, fl.assignVar(v, z))
	}
	// Allocate capture cells and copy initial values.
	for _, ci := range fl.cellInits {
		preamble = append(preamble, fl.assignVar(ci.cell, &ir.Instr{
			Op:   ir.OpStructNew,
			Type: &ir.Ref{Named: ci.cellTy},
			Args: []ir.Expr{fl.useVar(ci.orig)},
		}))
	}
	preamble = append(preamble, clones...)
	if fl.needsRecoverPrologue && !fl.hasDefer {
		preamble = append(preamble, fl.recoverPrologue()...)
	}
	if len(preamble) > 0 {
		seq.Stmts = append(preamble, seq.Stmts...)
	}

	if fl.hasDefer {
		seq, err = fl.applyDeferPass(seq)
		if err != nil {
			return nil, err
		}
		fl.pkg.meta.needsEH = true
	}
	if fl.mayPanic || fl.needsRecoverPrologue {
		fl.pkg.meta.needsEH = true
	}

	locals := append(fl.namedResults, fl.extraLocals...)
	if fl.allowVar != nil {
		locals = append(locals, fl.allowVar)
	}
	if fl.defersVar != nil {
		locals = append(locals, fl.defersVar)
	}
	return &ir.Func{
		Name:    irName,
		Params:  fl.params,
		Results: fl.results,
		Body:    seq,
		Locals:  locals,
	}, nil
}

func containsRecover(body *ast.BlockStmt, info *types.Info) bool {
	found := false
	ast.Inspect(body, func(n ast.Node) bool {
		call, ok := n.(*ast.CallExpr)
		if !ok {
			return true
		}
		id, ok := call.Fun.(*ast.Ident)
		if !ok {
			return true
		}
		if obj := info.Uses[id]; obj != nil {
			if b, ok := obj.(*types.Builtin); ok && b.Name() == "recover" {
				found = true
				return false
			}
		}
		if id.Name == "recover" {
			found = true
			return false
		}
		return true
	})
	return found
}

func containsPanicCall(body *ast.BlockStmt, info *types.Info) bool {
	found := false
	ast.Inspect(body, func(n ast.Node) bool {
		call, ok := n.(*ast.CallExpr)
		if !ok {
			return true
		}
		id, ok := call.Fun.(*ast.Ident)
		if !ok {
			return true
		}
		if obj := info.Uses[id]; obj != nil {
			if b, ok := obj.(*types.Builtin); ok && b.Name() == "panic" {
				found = true
				return false
			}
		}
		if id.Name == "panic" {
			found = true
			return false
		}
		return true
	})
	return found
}

// lowerBodyless emits an IR func with an empty body for asm/intrinsic binding.
func (l *pkgLower) lowerBodyless(fd *ast.FuncDecl, irName string) (*ir.Func, error) {
	fl, err := l.newFuncLower(fd, irName)
	if err != nil {
		return nil, err
	}
	return &ir.Func{
		Name:    irName,
		Params:  fl.params,
		Results: fl.results,
		Body:    &ir.Seq{},
	}, nil
}

func (l *pkgLower) newFuncLower(fd *ast.FuncDecl, irName string) (*funcLower, error) {
	obj, _ := l.info.Defs[fd.Name].(*types.Func)
	if obj == nil {
		return nil, l.errorf(fd, "no type for function %s", fd.Name.Name)
	}
	sig, _ := obj.Type().(*types.Signature)
	if sig == nil {
		return nil, l.errorf(fd, "no signature for %s", fd.Name.Name)
	}
	if isGenericOrigin(obj) {
		return nil, l.errorf(fd, "generic function %s not supported (see internal/mono)", irName)
	}

	fl := &funcLower{
		pkg:    l,
		fset:   l.fset,
		info:   l.info,
		mod:    l.mod,
		obj:    obj,
		sig:    sig,
		irName: irName,
		vars:   map[types.Object]*ir.Var{},
		labels: map[string]*labelTargets{},
	}

	if recv := sig.Recv(); recv != nil {
		ty, err := l.mapType(recv.Type())
		if err != nil {
			return nil, l.errorf(fd, "recv: %v", err)
		}
		name := recv.Name()
		if name == "" || name == "_" {
			name = "recv"
		}
		v := &ir.Var{Name: name, Typ: ty}
		fl.params = append(fl.params, v)
		fl.vars[recv] = v
	}

	for i := 0; i < sig.Params().Len(); i++ {
		p := sig.Params().At(i)
		ty, err := l.mapType(p.Type())
		if err != nil {
			return nil, l.errorf(fd, "param %s: %v", p.Name(), err)
		}
		name := p.Name()
		if name == "" {
			name = "_" + strconv.Itoa(i)
		}
		v := &ir.Var{Name: name, Typ: ty}
		fl.params = append(fl.params, v)
		fl.vars[p] = v
	}
	for i := 0; i < sig.Results().Len(); i++ {
		r := sig.Results().At(i)
		ty, err := l.mapType(r.Type())
		if err != nil {
			return nil, l.errorf(fd, "result: %v", err)
		}
		fl.results = append(fl.results, ty)
		if r.Name() != "" {
			v := &ir.Var{Name: r.Name(), Typ: ty}
			fl.namedResults = append(fl.namedResults, v)
			fl.vars[r] = v
		}
	}
	return fl, nil
}

func (fl *funcLower) paramFieldAssigned(obj types.Object, body *ast.BlockStmt) bool {
	assigned := false
	ast.Inspect(body, func(n ast.Node) bool {
		if assigned {
			return false
		}
		as, ok := n.(*ast.AssignStmt)
		if !ok {
			return true
		}
		for _, lhs := range as.Lhs {
			if fl.assignsFieldOf(lhs, obj) {
				assigned = true
				return false
			}
		}
		return true
	})
	return assigned
}

func (fl *funcLower) assignsFieldOf(lhs ast.Expr, obj types.Object) bool {
	switch lhs := lhs.(type) {
	case *ast.SelectorExpr:
		if id, ok := lhs.X.(*ast.Ident); ok {
			if fl.info.Uses[id] == obj || fl.info.Defs[id] == obj {
				return true
			}
		}
		return fl.assignsFieldOf(lhs.X, obj)
	case *ast.ParenExpr:
		return fl.assignsFieldOf(lhs.X, obj)
	}
	return false
}

func (fl *funcLower) cloneStruct(v *ir.Var) ([]ir.Stmt, error) {
	name := refTypeName(v.Typ)
	td := fl.mod.LookupType(name)
	if td == nil {
		return nil, fmt.Errorf("clone: unknown type %s", name)
	}
	var args []ir.Expr
	for _, f := range td.Fields {
		args = append(args, &ir.Instr{
			Op:    ir.OpStructGet,
			Type:  &ir.Ref{Named: name},
			Field: f.Name,
			Args:  []ir.Expr{fl.useVar(v)},
		})
	}
	return []ir.Stmt{fl.assignVar(v, &ir.Instr{
		Op:   ir.OpStructNew,
		Type: &ir.Ref{Named: name},
		Args: args,
	})}, nil
}

func endsWithReturn(s *ir.Seq) bool {
	if s == nil || len(s.Stmts) == 0 {
		return false
	}
	_, ok := s.Stmts[len(s.Stmts)-1].(*ir.Return)
	return ok
}

func (l *pkgLower) errorf(n ast.Node, format string, args ...any) error {
	msg := fmt.Sprintf(format, args...)
	if l.fset != nil && n != nil {
		return fmt.Errorf("%s: lower: %s", l.fset.Position(n.Pos()), msg)
	}
	path := "main"
	if l.pkg != nil {
		path = l.pkg.Path()
	}
	return fmt.Errorf("%s: lower: %s", path, msg)
}

func (l *pkgLower) errorPos(pos token.Pos, format string, args ...any) error {
	msg := fmt.Sprintf(format, args...)
	if l.fset != nil && pos.IsValid() {
		return fmt.Errorf("%s: lower: %s", l.fset.Position(pos), msg)
	}
	path := "main"
	if l.pkg != nil {
		path = l.pkg.Path()
	}
	return fmt.Errorf("%s: lower: %s", path, msg)
}
