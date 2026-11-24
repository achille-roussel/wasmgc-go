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
	// globalCellElem marks package vars stored as $go.cell.* (address-taken
	// scalars/refs that need a fat-pointer base). The value is the element IR type.
	globalCellElem map[types.Object]ir.Type
}

func (l *pkgLower) lower() error {
	if l.globals == nil {
		l.globals = map[types.Object]*ir.Global{}
	}
	if l.globalCellElem == nil {
		l.globalCellElem = map[types.Object]ir.Type{}
	}
	addrTaken := l.collectAddrTakenGlobals()

	// Pass 1: declare every package var as a global so forward refs
	// (e.g. unicode.TurkishCase = _TurkishCase) resolve via lookupGlobal.
	// Collect initializers keyed by RHS so Pass 2 can emit them in
	// types.Info.InitOrder (blanks participate — issue5244).
	type varInit struct {
		name  *ast.Ident
		obj   types.Object
		ty    ir.Type // element / value IR type (not the cell wrapper)
		gname string
		val   ast.Expr // nil → emit Go zero value (needed for struct refs)
	}
	type multiInit struct {
		names []*ast.Ident
		val   ast.Expr
	}
	type rhsInit struct {
		multi  *multiInit
		single *varInit // named or blank side-effect (obj may be nil)
	}
	rhsPending := map[ast.Expr]*rhsInit{}
	var zeroPending []varInit
	for _, f := range l.files {
		for _, d := range f.Decls {
			gd, ok := d.(*ast.GenDecl)
			if !ok || gd.Tok != token.VAR {
				continue
			}
			for _, sp := range gd.Specs {
				vs := sp.(*ast.ValueSpec)
				multi := len(vs.Names) > 1 && len(vs.Values) == 1
				if multi {
					rhsPending[vs.Values[0]] = &rhsInit{
						multi: &multiInit{names: vs.Names, val: vs.Values[0]},
					}
				}
				for i, name := range vs.Names {
					if name.Name == "_" {
						// var _ = f() still runs f for side effects (blank.go);
						// scheduled via InitOrder so deps like g in issue5244
						// are initialized first.
						if !multi && vs.Values != nil && i < len(vs.Values) {
							rhsPending[vs.Values[i]] = &rhsInit{
								single: &varInit{name: name, val: vs.Values[i]},
							}
						}
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
					gTyp := ty
					if addrTaken[obj] {
						cellName, err := l.ensureCellType(ty)
						if err != nil {
							return l.errorf(name, "%v", err)
						}
						gTyp = &ir.Ref{Named: cellName, Null: true}
						l.globalCellElem[obj] = ty
					}
					g := &ir.Global{Name: gname, Typ: gTyp, Mut: true}
					l.mod.Globals = append(l.mod.Globals, g)
					l.globals[obj] = g
					if multi {
						// Initialized via rhsPending multi entry.
						continue
					}
					if vs.Values != nil && i < len(vs.Values) {
						rhsPending[vs.Values[i]] = &rhsInit{
							single: &varInit{
								name: name, obj: obj, ty: ty, gname: gname, val: vs.Values[i],
							},
						}
					} else if addrTaken[obj] || needsHeapZero(obj.Type(), ty) {
						// Wasm defaults refnull globals to null, but Go's zero
						// for a struct value is a usable empty object (e.g.
						// sync.Map / sync.Mutex package vars). Address-taken
						// scalars always need a cell allocated in init.
						zeroPending = append(zeroPending, varInit{
							name: name, obj: obj, ty: ty, gname: gname,
						})
					}
				}
			}
		}
	}

	// Pass 2: zero-init first, then InitOrder (includes blank side-effects).
	var initStmts []ir.Stmt
	emitSingle := func(vi *varInit) error {
		if l.initLower == nil {
			l.initLower = l.newInitLower()
		}
		fl := l.initLower
		var ex ir.Expr
		var stmts []ir.Stmt
		var err error
		if vi.name != nil && vi.name.Name == "_" {
			want := fl.typeOf(vi.val)
			var irt ir.Type
			if want != nil {
				irt, err = fl.mapType(want)
				if err != nil {
					return err
				}
			}
			ex, stmts, err = fl.expr(vi.val, irt)
			if err != nil {
				return err
			}
			initStmts = append(initStmts, stmts...)
			if ex != nil {
				initStmts = append(initStmts, &ir.ExprStmt{X: &ir.Instr{Op: ir.OpDrop, Args: []ir.Expr{ex}}})
			}
			return nil
		}
		if vi.val != nil {
			ex, stmts, err = fl.coerceExpr(vi.val, vi.obj.Type(), vi.ty)
		} else {
			ex, err = fl.zeroValue(vi.ty)
		}
		if err != nil {
			return err
		}
		initStmts = append(initStmts, stmts...)
		initStmts = append(initStmts, fl.storeGlobalInit(vi.obj, vi.gname, ex))
		return nil
	}
	for i := range zeroPending {
		if err := emitSingle(&zeroPending[i]); err != nil {
			return err
		}
	}
	emitted := map[ast.Expr]bool{}
	for _, init := range l.info.InitOrder {
		if init == nil || init.Rhs == nil {
			continue
		}
		if emitted[init.Rhs] {
			continue
		}
		ri := rhsPending[init.Rhs]
		if ri == nil {
			continue
		}
		emitted[init.Rhs] = true
		if ri.multi != nil {
			if l.initLower == nil {
				l.initLower = l.newInitLower()
			}
			stmts, err := l.lowerPkgMultiValueSpec(l.initLower, ri.multi.names, ri.multi.val)
			if err != nil {
				return err
			}
			initStmts = append(initStmts, stmts...)
			continue
		}
		if ri.single != nil {
			if err := emitSingle(ri.single); err != nil {
				return err
			}
		}
	}
	// Any initializer not present in InitOrder (should be rare) — keep
	// declaration-order fallback so we do not drop side effects.
	for rhs, ri := range rhsPending {
		if emitted[rhs] {
			continue
		}
		if ri.multi != nil {
			if l.initLower == nil {
				l.initLower = l.newInitLower()
			}
			stmts, err := l.lowerPkgMultiValueSpec(l.initLower, ri.multi.names, ri.multi.val)
			if err != nil {
				return err
			}
			initStmts = append(initStmts, stmts...)
			continue
		}
		if ri.single != nil {
			if err := emitSingle(ri.single); err != nil {
				return err
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
			fl := l.initLower
			for _, v := range fl.extraLocals {
				preamble = append(preamble, &ir.VarDef{Var: v})
			}
			// Same zero-init as lowerFunc: non-defaultable locals (e.g. $go.string
			// temps from string([]byte{…}) in package vars) must be assigned at
			// entry or V8 rejects the synthetic init ("uninitialized
			// non-defaultable local"). Branch-only assigns in bytesToString are
			// not enough for Wasm GC validation.
			for _, v := range fl.extraLocals {
				if r, ok := v.Typ.(*ir.Ref); ok && strings.HasPrefix(r.Named, "$go.cell.") {
					continue
				}
				if goTy := fl.goTypeOfLocal(v); goTy != nil {
					if z, ok := fl.goZeroValue(goTy, v.Typ); ok {
						preamble = append(preamble, fl.assignVar(v, z))
					} else if needsZeroInit(v.Typ) {
						z, err := fl.zeroValue(v.Typ)
						if err != nil {
							return err
						}
						preamble = append(preamble, fl.assignVar(v, z))
					}
					continue
				}
				if !needsZeroInit(v.Typ) && !isNamedStructRef(v.Typ) {
					continue
				}
				z, err := fl.zeroValue(v.Typ)
				if err != nil {
					return err
				}
				preamble = append(preamble, fl.assignVar(v, z))
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

// lowerPkgMultiValueSpec lowers `var a, b = f()` / `var v, ok = m[k]` (and
// blank forms like `var _, _ = bal()`) at package scope.
func (l *pkgLower) lowerPkgMultiValueSpec(fl *funcLower, names []*ast.Ident, rhs ast.Expr) ([]ir.Stmt, error) {
	lhs := make([]ast.Expr, len(names))
	for i, n := range names {
		lhs[i] = n
	}
	fake := &ast.AssignStmt{Tok: token.ASSIGN, Lhs: lhs, Rhs: []ast.Expr{rhs}}
	if ta, ok := rhs.(*ast.TypeAssertExpr); ok {
		return fl.multiAssignTypeAssert(fake, ta, nil)
	}
	if u, ok := rhs.(*ast.UnaryExpr); ok && u.Op == token.ARROW {
		return fl.multiAssignRecv(fake, u, nil)
	}
	if ix, ok := rhs.(*ast.IndexExpr); ok && isMap(fl.typeOf(ix.X)) {
		return fl.mapIndexCommaOk(fake, ix, nil)
	}
	call, ok := rhs.(*ast.CallExpr)
	if !ok {
		return nil, l.errorf(rhs, "multi-value package init requires a call, type assert, or channel receive")
	}
	temps, stmts, err := fl.callMaterialize(call, nil)
	if err != nil {
		return nil, err
	}
	srcGos := fl.callResultGoTypes(call)
	for i, t := range temps {
		if i >= len(names) {
			break
		}
		name := names[i]
		var ex ir.Expr = fl.useTemp(t)
		if name.Name == "_" {
			stmts = append(stmts, &ir.ExprStmt{X: &ir.Instr{Op: ir.OpDrop, Args: []ir.Expr{ex}}})
			continue
		}
		obj := l.info.Defs[name]
		if obj == nil {
			stmts = append(stmts, &ir.ExprStmt{X: &ir.Instr{Op: ir.OpDrop, Args: []ir.Expr{ex}}})
			continue
		}
		var srcGo types.Type
		if i < len(srcGos) {
			srcGo = srcGos[i]
		}
		dstGo := obj.Type()
		if isIface(dstGo) && srcGo != nil && !isIface(srcGo) {
			ex = fl.boxIRToIface(ex, srcGo, dstGo)
		}
		g, ok := l.globals[obj]
		if !ok {
			stmts = append(stmts, &ir.ExprStmt{X: &ir.Instr{Op: ir.OpDrop, Args: []ir.Expr{ex}}})
			continue
		}
		stmts = append(stmts, fl.storeGlobalInit(obj, g.Name, ex))
	}
	return stmts, nil
}

// collectAddrTakenGlobals finds package-level vars whose address is taken and
// that need a fat-pointer base (everything except *Struct / *[N]T object pointers).
// Wasm i32/i64/f32/f64 globals cannot be fat-pointer bases (base is anyref), so
// those vars are spilled into $go.cell.* heap objects.
func (l *pkgLower) collectAddrTakenGlobals() map[types.Object]bool {
	out := map[types.Object]bool{}
	pkgScope := l.pkg.Scope()
	for _, f := range l.files {
		ast.Inspect(f, func(n ast.Node) bool {
			u, ok := n.(*ast.UnaryExpr)
			if !ok || u.Op != token.AND {
				return true
			}
			x := ast.Expr(u.X)
			for {
				if p, ok := x.(*ast.ParenExpr); ok {
					x = p.X
					continue
				}
				break
			}
			id, _ := x.(*ast.Ident)
			if id == nil {
				return true
			}
			obj := l.info.Uses[id]
			if obj == nil {
				obj = l.info.Defs[id]
			}
			v, ok := obj.(*types.Var)
			if !ok || v == nil || v.Pkg() != l.pkg || v.Parent() != pkgScope {
				return true
			}
			if !isObjectPointer(types.NewPointer(v.Type())) {
				out[v] = true
			}
			return true
		})
	}
	return out
}

// storeGlobalInit writes the first value of a package global during init.
// Address-taken scalars are wrapped in a freshly allocated $go.cell.*.
func (fl *funcLower) storeGlobalInit(obj types.Object, gname string, ex ir.Expr) ir.Stmt {
	if _, ok := fl.pkg.globalCellElem[obj]; ok {
		g := fl.pkg.globals[obj]
		cellTy := g.Typ.(*ir.Ref).Named
		return &ir.ExprStmt{X: &ir.Instr{
			Op:     ir.OpGlobalSet,
			Global: gname,
			Args: []ir.Expr{&ir.Instr{
				Op:   ir.OpStructNew,
				Type: &ir.Ref{Named: cellTy},
				Args: []ir.Expr{ex},
			}},
		}}
	}
	return &ir.ExprStmt{X: &ir.Instr{
		Op:     ir.OpGlobalSet,
		Global: gname,
		Args:   []ir.Expr{ex},
	}}
}

// readGlobal loads a package var, unwrapping $go.cell.*.v when applicable.
func (fl *funcLower) readGlobal(obj types.Object, g *ir.Global) ir.Expr {
	if _, ok := fl.pkg.globalCellElem[obj]; ok {
		cellTy := g.Typ.(*ir.Ref).Named
		return &ir.Instr{
			Op:    ir.OpStructGet,
			Type:  &ir.Ref{Named: cellTy},
			Field: "v",
			Args:  []ir.Expr{&ir.Instr{Op: ir.OpGlobalGet, Global: g.Name}},
		}
	}
	return &ir.Instr{Op: ir.OpGlobalGet, Global: g.Name}
}

// writeGlobal stores into a package var, updating $go.cell.*.v when applicable.
func (fl *funcLower) writeGlobal(obj types.Object, g *ir.Global, rhs ir.Expr) []ir.Stmt {
	if _, ok := fl.pkg.globalCellElem[obj]; ok {
		cellTy := g.Typ.(*ir.Ref).Named
		return []ir.Stmt{&ir.ExprStmt{X: &ir.Instr{
			Op:    ir.OpStructSet,
			Type:  &ir.Ref{Named: cellTy},
			Field: "v",
			Args: []ir.Expr{
				&ir.Instr{Op: ir.OpGlobalGet, Global: g.Name},
				rhs,
			},
		}}}
	}
	return []ir.Stmt{&ir.ExprStmt{X: &ir.Instr{
		Op:     ir.OpGlobalSet,
		Global: g.Name,
		Args:   []ir.Expr{rhs},
	}}}
}

// needsHeapZero reports whether a package var with no explicit initializer
// still needs an init that writes the Go zero value. Wasm leaves refnull
// globals as null, which matches pointer/slice/map/chan/iface/func zeros,
// but not named-struct or string values.
func needsHeapZero(goTy types.Type, irTy ir.Type) bool {
	if goTy == nil || irTy == nil {
		return false
	}
	goTy = types.Unalias(goTy)
	switch goTy.Underlying().(type) {
	case *types.Pointer, *types.Slice, *types.Map, *types.Chan, *types.Signature, *types.Interface:
		return false
	}
	switch t := irTy.(type) {
	case *ir.Ref:
		if t.Named == "$go.string" || t.Named == "$go.complex64" || t.Named == "$go.complex128" {
			return true
		}
		if strings.HasPrefix(t.Named, "$go.struct.") {
			return true
		}
	}
	return false
}

func (l *pkgLower) initName() string {
	// Continue past any inits already in the module (AppendFiles / Ensure*
	// may lower runtime pieces in several passes).
	prefix := pkgIRPrefix(l.pkg) + ".init."
	for {
		name := prefix + strconv.Itoa(l.initCount)
		l.initCount++
		if l.mod.LookupFunc(name) == nil {
			return name
		}
	}
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
	// Switch/type-switch may already append unreachable; do not add a second.
	if len(fl.results) == 0 && !endsWithReturn(seq) && !endsWithUnreachable(seq) {
		seq.Stmts = append(seq.Stmts, &ir.Return{})
	}
	// Non-void: falling off the end after an infinite loop (for { return })
	// needs unreachable so Wasm validation sees a result on every path.
	// Named results: implicit return of the result locals.
	if len(fl.results) > 0 && !endsWithReturn(seq) && !endsWithUnreachable(seq) {
		if len(fl.namedResults) > 0 {
			args := make([]ir.Expr, len(fl.namedResults))
			for i, v := range fl.namedResults {
				args[i] = fl.useVar(v)
			}
			seq.Stmts = append(seq.Stmts, &ir.Return{Results: args})
		} else {
			seq.Stmts = append(seq.Stmts, &ir.Unreachable{})
		}
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
	// Zero-init non-defaultable locals and Go value zeros that Wasm leaves
	// null (named struct values, strings). *T shares $go.struct.* IR with T —
	// use the Go type so pointer locals stay null (e.g. var errInit *SemanticError).
	for _, v := range append(fl.namedResults, fl.extraLocals...) {
		if r, ok := v.Typ.(*ir.Ref); ok && strings.HasPrefix(r.Named, "$go.cell.") {
			continue
		}
		if goTy := fl.goTypeOfLocal(v); goTy != nil {
			if z, ok := fl.goZeroValue(goTy, v.Typ); ok {
				preamble = append(preamble, fl.assignVar(v, z))
			} else if needsZeroInit(v.Typ) {
				z, err := fl.zeroValue(v.Typ)
				if err != nil {
					return nil, err
				}
				preamble = append(preamble, fl.assignVar(v, z))
			}
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
		if name == "" || name == "_" {
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
		name := r.Name()
		if name == "_" {
			// Blank named results still need locals for naked returns;
			// uniquify so IR verify accepts multiple `_` results.
			name = "_" + strconv.Itoa(i)
		}
		if name != "" {
			v := &ir.Var{Name: name, Typ: ty}
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
