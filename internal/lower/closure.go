package lower

import (
	"crypto/sha1"
	"encoding/hex"
	"fmt"
	"go/ast"
	"go/token"
	"go/types"
	"strconv"
	"strings"

	"github.com/achille-roussel/wasmgc-go/internal/desc"
	"github.com/achille-roussel/wasmgc-go/internal/ir"
	"github.com/achille-roussel/wasmgc-go/internal/mono"
)

// Closures and func values (docs/design/03).
//
// Func values are (ref null $go.clos). $go.clos is an open struct with a
// funcref $fn field; concrete closures subtype it with typed capture fields
// (cells for variables captured by reference). call_ref passes the closure as
// the first argument. Direct calls of known funcs stay OpCall.

const closBaseName = "$go.clos"

func (l *pkgLower) ensureClosBase() {
	if l.mod.LookupType(closBaseName) != nil {
		return
	}
	l.mod.InternType(&ir.TypeDef{
		Name:  closBaseName,
		Kind:  ir.TypeStruct,
		Super: "$go.object",
		// Open: codegen treats $go.clos specially (SubOpen).
		Fields: []ir.Field{
			{Name: "fn", Type: &ir.Ref{Null: true, Heap: ir.HeapFunc}},
		},
	})
}

// mapSignature lowers a Go func type to (ref null $go.clos).
func (l *pkgLower) mapSignature(sig *types.Signature) (ir.Type, error) {
	l.ensureClosBase()
	_ = sig
	return &ir.Ref{Null: true, Named: closBaseName}, nil
}

// ensureFuncType interns the Wasm function type for a Go signature whose
// body takes a closure as the first parameter (closure / method-value body).
func (l *pkgLower) ensureFuncType(sig *types.Signature, closType string) (string, error) {
	l.ensureClosBase()
	if closType == "" {
		closType = closBaseName
	}
	var b strings.Builder
	b.WriteString("$go.func.")
	b.WriteString(sigKey(sig))
	if closType != closBaseName {
		b.WriteByte('.')
		b.WriteString(sanitizeTypeName(closType))
	}
	name := b.String()
	if l.mod.LookupType(name) != nil {
		return name, nil
	}
	params := []ir.Type{&ir.Ref{Named: closType}}
	if recv := sig.Recv(); recv != nil {
		// Method expressions / values already fold the receiver into captures
		// or as an ordinary first user param; callers pass closType only.
		_ = recv
	}
	for i := 0; i < sig.Params().Len(); i++ {
		ty, err := l.mapType(sig.Params().At(i).Type())
		if err != nil {
			return "", err
		}
		params = append(params, ty)
	}
	var results []ir.Type
	for i := 0; i < sig.Results().Len(); i++ {
		ty, err := l.mapType(sig.Results().At(i).Type())
		if err != nil {
			return "", err
		}
		results = append(results, ty)
	}
	l.mod.InternType(&ir.TypeDef{
		Name:    name,
		Kind:    ir.TypeFunc,
		Params:  params,
		Results: results,
	})
	return name, nil
}

func sigKey(sig *types.Signature) string {
	var b strings.Builder
	if recv := sig.Recv(); recv != nil {
		b.WriteString("r.")
		b.WriteString(recv.Type().String())
		b.WriteByte('.')
	}
	for i := 0; i < sig.Params().Len(); i++ {
		if i > 0 {
			b.WriteByte(',')
		}
		b.WriteString(sig.Params().At(i).Type().String())
	}
	b.WriteByte('>')
	for i := 0; i < sig.Results().Len(); i++ {
		if i > 0 {
			b.WriteByte(',')
		}
		b.WriteString(sig.Results().At(i).Type().String())
	}
	sum := sha1.Sum([]byte(b.String()))
	return hex.EncodeToString(sum[:8])
}

func sanitizeTypeName(name string) string {
	name = strings.TrimPrefix(name, "$")
	return strings.Map(func(r rune) rune {
		if (r >= 'a' && r <= 'z') || (r >= 'A' && r <= 'Z') || (r >= '0' && r <= '9') || r == '_' || r == '.' {
			return r
		}
		return '_'
	}, name)
}

// cellTypeName is $go.cell.<elem> for capture-by-reference cells.
func (l *pkgLower) ensureCellType(elem ir.Type) (string, error) {
	name := "$go.cell." + sanitizeTypeName(ir.TypeString(elem))
	if l.mod.LookupType(name) != nil {
		return name, nil
	}
	l.mod.InternType(&ir.TypeDef{
		Name:  name,
		Kind:  ir.TypeStruct,
		Super: "$go.object",
		Fields: []ir.Field{
			{Name: "v", Type: elem, Mut: true},
		},
	})
	return name, nil
}

// captureInfo describes one free variable captured by a closure.
type captureInfo struct {
	obj    types.Object
	name   string // field name on the clos struct
	typ    ir.Type
	cell   bool   // captured by reference via a cell
	cellTy string // $go.cell.* when cell
	irTyp  ir.Type // type stored in the clos field (cell ref or value)
}

// funcLit lowers a function literal to a closure value.
func (fl *funcLower) funcLit(e *ast.FuncLit, want ir.Type) (ir.Expr, []ir.Stmt, error) {
	sig, ok := fl.typeOf(e).Underlying().(*types.Signature)
	if !ok {
		return nil, nil, fl.errorf(e, "func lit has no signature")
	}
	free := freeVars(e.Body, fl.info, fl.vars)
	// Also consider uses in the type params? No.
	caps, err := fl.planCaptures(free)
	if err != nil {
		return nil, nil, err
	}
	closName, err := fl.pkg.ensureClosType(caps)
	if err != nil {
		return nil, nil, fl.errorf(e, "%v", err)
	}
	// call_ref always uses func (ref $go.clos, ...); bodies take the base type.
	funcTy, err := fl.pkg.ensureFuncType(sig, closBaseName)
	if err != nil {
		return nil, nil, fl.errorf(e, "%v", err)
	}
	bodyName := fl.pkg.freshClosName("clos")
	if err := fl.emitClosBody(bodyName, e, sig, closName, caps); err != nil {
		return nil, nil, err
	}
	return fl.buildClosure(closName, funcTy, bodyName, caps)
}

func (l *pkgLower) freshClosName(prefix string) string {
	if l.meta == nil {
		l.meta = metaOf(l.mod)
	}
	l.meta.closSeq++
	return pkgIRPrefix(l.pkg) + "." + prefix + "." + strconv.Itoa(l.meta.closSeq)
}

func (l *pkgLower) ensureClosType(caps []captureInfo) (string, error) {
	l.ensureClosBase()
	if len(caps) == 0 {
		return closBaseName, nil
	}
	var b strings.Builder
	b.WriteString("$go.clos.")
	for i, c := range caps {
		if i > 0 {
			b.WriteByte('.')
		}
		b.WriteString(c.name)
		if c.cell {
			b.WriteString(".cell")
		}
		b.WriteByte('.')
		b.WriteString(sanitizeTypeName(ir.TypeString(c.irTyp)))
	}
	name := b.String()
	if len(name) > 120 {
		sum := sha1.Sum([]byte(name))
		name = "$go.clos." + hex.EncodeToString(sum[:10])
	}
	if l.mod.LookupType(name) != nil {
		return name, nil
	}
	fields := []ir.Field{
		{Name: "fn", Type: &ir.Ref{Null: true, Heap: ir.HeapFunc}},
	}
	for _, c := range caps {
		fields = append(fields, ir.Field{Name: c.name, Type: c.irTyp, Mut: false})
	}
	l.mod.InternType(&ir.TypeDef{
		Name:   name,
		Kind:   ir.TypeStruct,
		Super:  closBaseName,
		Fields: fields,
	})
	return name, nil
}

func (fl *funcLower) planCaptures(free []types.Object) ([]captureInfo, error) {
	var caps []captureInfo
	for _, obj := range free {
		ty, err := fl.mapType(obj.Type())
		if err != nil {
			return nil, err
		}
		// Capture by reference when the variable is assigned (or address-taken)
		// in the enclosing function; otherwise capture by value.
		byRef := fl.pkg.varNeedsCell(obj)
		c := captureInfo{
			obj:  obj,
			name: "c_" + obj.Name(),
			typ:  ty,
		}
		if byRef {
			cell, err := fl.pkg.ensureCellType(ty)
			if err != nil {
				return nil, err
			}
			c.cell = true
			c.cellTy = cell
			c.irTyp = &ir.Ref{Named: cell}
			// Ensure the enclosing local is a cell.
			if err := fl.ensureLocalCell(obj, cell, ty); err != nil {
				return nil, err
			}
		} else {
			c.irTyp = ty
		}
		caps = append(caps, c)
	}
	return caps, nil
}

func (fl *funcLower) ensureLocalCell(obj types.Object, cellName string, elem ir.Type) error {
	v, ok := fl.vars[obj]
	if !ok {
		return fmt.Errorf("capture %s has no local", obj.Name())
	}
	if fl.cells == nil {
		fl.cells = map[types.Object]*ir.Var{}
	}
	if _, ok := fl.cells[obj]; ok {
		return nil
	}
	cellVar := &ir.Var{Name: v.Name + "$cell", Typ: &ir.Ref{Named: cellName}}
	fl.extraLocals = append(fl.extraLocals, cellVar)
	fl.cells[obj] = cellVar
	// At first use we need the cell allocated; emit at start via cellInit.
	fl.cellInits = append(fl.cellInits, cellInit{obj: obj, cell: cellVar, elem: elem, cellTy: cellName, orig: v})
	return nil
}

type cellInit struct {
	obj    types.Object
	cell   *ir.Var
	orig   *ir.Var
	elem   ir.Type
	cellTy string
}

// freeVars returns objects used in body that are defined outside it and are
// locals/params of an enclosing function (not package-level).
func freeVars(body *ast.BlockStmt, info *types.Info, outer map[types.Object]*ir.Var) []types.Object {
	declared := map[types.Object]bool{}
	ast.Inspect(body, func(n ast.Node) bool {
		switch n := n.(type) {
		case *ast.AssignStmt:
			if n.Tok == token.DEFINE {
				for _, lhs := range n.Lhs {
					if id, ok := lhs.(*ast.Ident); ok {
						if obj := info.Defs[id]; obj != nil {
							declared[obj] = true
						}
					}
				}
			}
		case *ast.ValueSpec:
			for _, name := range n.Names {
				if obj := info.Defs[name]; obj != nil {
					declared[obj] = true
				}
			}
		case *ast.FuncLit:
			// Nested func lit: its own free vars are handled when it is lowered;
			// still walk for outer free vars used only inside? Skip nested bodies
			// for declared tracking but still collect uses.
			return true
		case *ast.RangeStmt:
			if n.Tok == token.DEFINE {
				if id, ok := n.Key.(*ast.Ident); ok {
					if obj := info.Defs[id]; obj != nil {
						declared[obj] = true
					}
				}
				if id, ok := n.Value.(*ast.Ident); ok {
					if obj := info.Defs[id]; obj != nil {
						declared[obj] = true
					}
				}
			}
		}
		return true
	})
	seen := map[types.Object]bool{}
	var out []types.Object
	ast.Inspect(body, func(n ast.Node) bool {
		id, ok := n.(*ast.Ident)
		if !ok {
			return true
		}
		obj := info.Uses[id]
		if obj == nil {
			return true
		}
		if declared[obj] {
			return true
		}
		if _, ok := outer[obj]; !ok {
			return true
		}
		if seen[obj] {
			return true
		}
		seen[obj] = true
		out = append(out, obj)
		return true
	})
	return out
}

func (fl *funcLower) emitClosBody(name string, lit *ast.FuncLit, sig *types.Signature, closName string, caps []captureInfo) error {
	child := &funcLower{
		pkg:    fl.pkg,
		fset:   fl.fset,
		info:   fl.info,
		mod:    fl.mod,
		sig:    sig,
		irName: name,
		vars:   map[types.Object]*ir.Var{},
		labels: map[string]*labelTargets{},
		cells:  map[types.Object]*ir.Var{},
	}
	// Param is the open $go.clos base so call_ref / deferRun share one func type.
	closParam := &ir.Var{Name: "clos", Typ: &ir.Ref{Named: closBaseName}}
	child.params = append(child.params, closParam)

	// Bind captures from clos fields into locals (or cell locals).
	var preamble []ir.Stmt
	closTyped := ir.Expr(child.useVar(closParam))
	if closName != closBaseName {
		closTyped = &ir.Instr{
			Op:   ir.OpRefCast,
			Type: &ir.Ref{Named: closName},
			Args: []ir.Expr{closTyped},
		}
	}
	for _, c := range caps {
		fieldGet := &ir.Instr{
			Op:    ir.OpStructGet,
			Type:  &ir.Ref{Named: closName},
			Field: c.name,
			Args:  []ir.Expr{closTyped},
		}
		if c.cell {
			v := &ir.Var{Name: c.obj.Name(), Typ: c.irTyp}
			child.vars[c.obj] = v
			child.cells[c.obj] = v
			child.extraLocals = append(child.extraLocals, v)
			preamble = append(preamble, &ir.VarDef{Var: v}, child.assignVar(v, fieldGet))
		} else {
			v := &ir.Var{Name: c.obj.Name(), Typ: c.typ}
			child.vars[c.obj] = v
			child.extraLocals = append(child.extraLocals, v)
			preamble = append(preamble, &ir.VarDef{Var: v}, child.assignVar(v, fieldGet))
		}
	}

	// Key locals by the typechecked (pre-subst) signature objects so Uses/Defs
	// lookups match. IR types come from the possibly-substituted sig.
	origSig := sig
	if tv, ok := fl.info.Types[lit]; ok && tv.Type != nil {
		if s, ok := tv.Type.Underlying().(*types.Signature); ok {
			origSig = s
		}
	}
	for i := 0; i < sig.Params().Len(); i++ {
		p := sig.Params().At(i)
		ty, err := child.mapType(p.Type())
		if err != nil {
			return err
		}
		name := p.Name()
		if name == "" {
			name = "_" + strconv.Itoa(i)
		}
		v := &ir.Var{Name: name, Typ: ty}
		child.params = append(child.params, v)
		key := p
		if i < origSig.Params().Len() {
			key = origSig.Params().At(i)
		}
		child.vars[key] = v
	}
	for i := 0; i < sig.Results().Len(); i++ {
		r := sig.Results().At(i)
		ty, err := child.mapType(r.Type())
		if err != nil {
			return err
		}
		child.results = append(child.results, ty)
		name := r.Name()
		if name == "_" {
			name = "_" + strconv.Itoa(i)
		}
		if name != "" {
			v := &ir.Var{Name: name, Typ: ty}
			child.namedResults = append(child.namedResults, v)
			key := r
			if i < origSig.Results().Len() {
				key = origSig.Results().At(i)
			}
			child.vars[key] = v
		}
	}

	body, err := child.stmtBlock(lit.Body)
	if err != nil {
		return err
	}
	seq, ok := body.(*ir.Seq)
	if !ok {
		seq = &ir.Seq{Stmts: []ir.Stmt{body}}
	}
	if len(child.results) == 0 && !endsWithReturn(seq) {
		seq.Stmts = append(seq.Stmts, &ir.Return{})
	}
	// Non-void: same as pkg.lowerFunc — fallthru after a terminating type
	// switch/block needs unreachable so Wasm sees results on every path.
	if len(child.results) > 0 && !endsWithReturn(seq) {
		if len(child.namedResults) > 0 {
			args := make([]ir.Expr, len(child.namedResults))
			for i, v := range child.namedResults {
				args[i] = child.useVar(v)
			}
			seq.Stmts = append(seq.Stmts, &ir.Return{Results: args})
		} else {
			seq.Stmts = append(seq.Stmts, &ir.Unreachable{})
		}
	}
	for _, v := range child.namedResults {
		preamble = append(preamble, &ir.VarDef{Var: v})
	}
	for _, v := range child.extraLocals {
		// already may be in preamble for captures
		found := false
		for _, s := range preamble {
			if vd, ok := s.(*ir.VarDef); ok && vd.Var == v {
				found = true
				break
			}
		}
		if !found {
			preamble = append(preamble, &ir.VarDef{Var: v})
		}
	}
	// Zero-init non-defaultable refs and Go value zeros; *T stays null.
	for _, v := range append(child.namedResults, child.extraLocals...) {
		if r, ok := v.Typ.(*ir.Ref); ok && strings.HasPrefix(r.Named, "$go.cell.") {
			continue
		}
		if goTy := child.goTypeOfLocal(v); goTy != nil {
			if z, ok := child.goZeroValue(goTy, v.Typ); ok {
				preamble = append(preamble, child.assignVar(v, z))
			} else if needsZeroInit(v.Typ) {
				z, err := child.zeroValue(v.Typ)
				if err != nil {
					return err
				}
				preamble = append(preamble, child.assignVar(v, z))
			}
			continue
		}
		if !needsZeroInit(v.Typ) && !isNamedStructRef(v.Typ) {
			continue
		}
		z, err := child.zeroValue(v.Typ)
		if err != nil {
			return err
		}
		preamble = append(preamble, child.assignVar(v, z))
	}
	// Address-taken locals spill into cells (see addrOf); allocate them here.
	for _, ci := range child.cellInits {
		preamble = append(preamble, child.assignVar(ci.cell, &ir.Instr{
			Op:   ir.OpStructNew,
			Type: &ir.Ref{Named: ci.cellTy},
			Args: []ir.Expr{child.useVar(ci.orig)},
		}))
	}
	if child.needsRecoverPrologue {
		preamble = append(preamble, child.recoverPrologue()...)
	}
	seq.Stmts = append(preamble, seq.Stmts...)
	if child.hasDefer {
		seq, err = child.applyDeferPass(seq)
		if err != nil {
			return err
		}
	}
	fn := &ir.Func{
		Name:    name,
		Params:  child.params,
		Results: child.results,
		Body:    seq,
		Locals:  append(child.namedResults, child.extraLocals...),
	}
	if child.allowVar != nil {
		fn.Locals = append(fn.Locals, child.allowVar)
	}
	if child.defersVar != nil {
		fn.Locals = append(fn.Locals, child.defersVar)
	}
	fl.mod.Funcs = append(fl.mod.Funcs, fn)
	return nil
}

func (fl *funcLower) buildClosure(closName, funcTy, bodyName string, caps []captureInfo) (ir.Expr, []ir.Stmt, error) {
	var stmts []ir.Stmt
	args := []ir.Expr{
		&ir.Instr{Op: ir.OpRefFunc, Func: bodyName, Type: &ir.Ref{Named: funcTy}},
	}
	for _, c := range caps {
		if c.cell {
			cv := fl.cells[c.obj]
			if cv == nil {
				return nil, nil, fmt.Errorf("missing cell for %s", c.obj.Name())
			}
			// Cells are allocated in the function preamble with the zero value.
			// Assignments that ran before ensureLocalCell (e.g. `ch := make(…)`
			// before `go func(){ … ch … }`) only updated the plain local — those
			// set plainDirty. Sync local→cell only when dirty so the child sees
			// that value. Do not sync when the cell is already authoritative
			// (closures wrote through it); a stale local would wipe them
			// (e.g. allFields = append(allFields, f) inside a loop of func lits).
			// Skip when vars[obj] is the cell itself (nested closure child that
			// already bound the capture as a cell local — syncing would write
			// the cell ref into field v).
			if v := fl.vars[c.obj]; v != nil && v != cv && fl.plainDirty[c.obj] {
				cellTy := cv.Typ.(*ir.Ref).Named
				stmts = append(stmts, &ir.ExprStmt{X: &ir.Instr{
					Op:    ir.OpStructSet,
					Type:  &ir.Ref{Named: cellTy},
					Field: "v",
					Args:  []ir.Expr{fl.useVar(cv), fl.useVar(v)},
				}})
				delete(fl.plainDirty, c.obj)
			}
			args = append(args, fl.useVar(cv))
		} else {
			v := fl.vars[c.obj]
			if v == nil {
				return nil, nil, fmt.Errorf("missing var for %s", c.obj.Name())
			}
			args = append(args, fl.useVar(v))
		}
	}
	ex := ir.Expr(&ir.Instr{
		Op:   ir.OpStructNew,
		Type: &ir.Ref{Named: closName},
		Args: args,
	})
	// Func values are typed as (ref null $go.clos); upcast concrete subtypes.
	if closName != closBaseName {
		ex = &ir.Instr{Op: ir.OpRefCast, Type: &ir.Ref{Null: true, Named: closBaseName}, Args: []ir.Expr{ex}}
	}
	return ex, stmts, nil
}

// topLevelFuncValue builds an empty-capture closure for a known function.
func (fl *funcLower) topLevelFuncValue(fn *types.Func) (ir.Expr, []ir.Stmt, error) {
	sig := fn.Type().(*types.Signature)
	fl.pkg.ensureClosBase()
	wrapper := fl.pkg.funcIRNameOf(fn) + "$clos"
	if fl.mod.LookupFunc(wrapper) == nil {
		if err := fl.pkg.emitTopLevelClosWrapper(wrapper, fn); err != nil {
			return nil, nil, err
		}
	}
	funcTy, err := fl.pkg.ensureFuncType(sig, closBaseName)
	if err != nil {
		return nil, nil, err
	}
	return &ir.Instr{
		Op:   ir.OpStructNew,
		Type: &ir.Ref{Named: closBaseName},
		Args: []ir.Expr{
			&ir.Instr{Op: ir.OpRefFunc, Func: wrapper, Type: &ir.Ref{Named: funcTy}},
		},
	}, nil, nil
}

func (l *pkgLower) funcIRNameOf(fn *types.Func) string {
	if fn.Pkg() == nil {
		return fn.Name()
	}
	if fn.Type().(*types.Signature).Recv() != nil {
		return methodIRName(fn)
	}
	return pkgIRPrefix(fn.Pkg()) + "." + fn.Name()
}

// emitTopLevelClosWrapper emits a function (clos, args...) that ignores clos
// and calls the underlying top-level / method function.
func (l *pkgLower) emitTopLevelClosWrapper(wrapper string, fn *types.Func) error {
	sig := fn.Type().(*types.Signature)
	return l.emitNamedClosWrapper(wrapper, l.funcIRNameOf(fn), sig)
}

// emitNamedClosWrapper emits a (clos, args...) wrapper that ignores clos and
// calls target. Used for both ordinary top-level funcs and mono instances.
func (l *pkgLower) emitNamedClosWrapper(wrapper, target string, sig *types.Signature) error {
	l.ensureClosBase()
	closParam := &ir.Var{Name: "clos", Typ: &ir.Ref{Named: closBaseName}}
	params := []*ir.Var{closParam}
	var args []ir.Expr
	// Methods: receiver is first real param of the underlying func.
	if recv := sig.Recv(); recv != nil {
		ty, err := l.mapType(recv.Type())
		if err != nil {
			return err
		}
		v := &ir.Var{Name: "recv", Typ: ty}
		params = append(params, v)
		args = append(args, &ir.Name{Raw: v.Name, Var: v})
	}
	for i := 0; i < sig.Params().Len(); i++ {
		p := sig.Params().At(i)
		ty, err := l.mapType(p.Type())
		if err != nil {
			return err
		}
		name := p.Name()
		if name == "" {
			name = "a" + strconv.Itoa(i)
		}
		v := &ir.Var{Name: name, Typ: ty}
		params = append(params, v)
		args = append(args, &ir.Name{Raw: v.Name, Var: v})
	}
	var results []ir.Type
	for i := 0; i < sig.Results().Len(); i++ {
		ty, err := l.mapType(sig.Results().At(i).Type())
		if err != nil {
			return err
		}
		results = append(results, ty)
	}
	call := &ir.Instr{Op: ir.OpCall, Func: target, Args: args}
	body := wrapperCallBody(call, results)
	l.mod.Funcs = append(l.mod.Funcs, &ir.Func{
		Name:    wrapper,
		Params:  params,
		Results: results,
		Body:    body,
	})
	_, err := l.ensureFuncType(sig, closBaseName)
	return err
}

// monoInstFuncValue builds an empty-capture closure for a monomorphized
// generic function (e.g. equal[int] passed to EqualFunc).
func (fl *funcLower) monoInstFuncValue(inst *mono.Instance) (ir.Expr, []ir.Stmt, error) {
	fl.pkg.ensureClosBase()
	wrapper := inst.IRName + "$clos"
	if fl.mod.LookupFunc(wrapper) == nil {
		if err := fl.pkg.emitNamedClosWrapper(wrapper, inst.IRName, inst.Sig); err != nil {
			return nil, nil, err
		}
	}
	funcTy, err := fl.pkg.ensureFuncType(inst.Sig, closBaseName)
	if err != nil {
		return nil, nil, err
	}
	return &ir.Instr{
		Op:   ir.OpStructNew,
		Type: &ir.Ref{Named: closBaseName},
		Args: []ir.Expr{
			&ir.Instr{Op: ir.OpRefFunc, Func: wrapper, Type: &ir.Ref{Named: funcTy}},
		},
	}, nil, nil
}

// methodValue lowers x.M as a closure capturing x.
func (fl *funcLower) methodValue(e *ast.SelectorExpr, sel *types.Selection, want ir.Type) (ir.Expr, []ir.Stmt, error) {
	fn := sel.Obj().(*types.Func)
	recvExpr := e.X
	recvTy := fl.typeOf(recvExpr)
	landed := selectionRecvType(recvTy, sel)
	ifaceRecv := isIface(recvTy) || isIface(landed)
	if !ifaceRecv {
		// Type-param method value after mono: selection still names the
		// constraint method; bind the concrete implementation.
		if m := concreteMethod(recvTy, fn); m != nil {
			fn = m
		}
	}
	captureTy := recvTy
	var captureIR ir.Type
	var stmts []ir.Stmt
	var recvVal ir.Expr
	var err error
	if ifaceRecv {
		ifaceTy := landed
		if !isIface(ifaceTy) {
			ifaceTy = recvTy
		}
		captureTy = ifaceTy
		captureIR = typeIface
		recvVal, stmts, err = fl.methodRecvSel(recvExpr, sel, ifaceTy, typeIface)
		if err != nil {
			return nil, nil, err
		}
	} else {
		captureIR, err = fl.mapType(captureTy)
		if err != nil {
			return nil, nil, fl.errorf(e, "%v", err)
		}
		recvVal, stmts, err = fl.expr(recvExpr, captureIR)
		if err != nil {
			return nil, nil, err
		}
	}
	recvT := fl.newTemp(captureIR)
	stmts = append(stmts, fl.assignTemp(recvT, recvVal))

	// Build a synthetic capture layout: one field for the receiver.
	capField := "recv"
	closName := "$go.clos.method." + sanitizeTypeName(methodIRName(fn))
	if ifaceRecv {
		closName = "$go.clos.method." + sanitizeTypeName(desc.TypeKey(captureTy)+"."+fn.Name())
	}
	fl.pkg.ensureClosBase()
	if fl.mod.LookupType(closName) == nil {
		fl.mod.InternType(&ir.TypeDef{
			Name:  closName,
			Kind:  ir.TypeStruct,
			Super: closBaseName,
			Fields: []ir.Field{
				{Name: "fn", Type: &ir.Ref{Null: true, Heap: ir.HeapFunc}},
				{Name: capField, Type: captureIR},
			},
		})
	}
	// Method value signature is the method type without receiver.
	msig := fn.Type().(*types.Signature)
	valSig := types.NewSignatureType(nil, nil, nil, msig.Params(), msig.Results(), msig.Variadic())
	wrapper := methodIRName(fn) + "$mval"
	if ifaceRecv {
		wrapper = "iface." + sanitizeTypeName(desc.TypeKey(captureTy)+"."+fn.Name()) + "$mval"
	}
	if fl.mod.LookupFunc(wrapper) == nil {
		if err := fl.pkg.emitMethodValueWrapper(wrapper, fn, closName, capField, captureIR, captureTy); err != nil {
			return nil, nil, err
		}
	}
	funcTy, err := fl.pkg.ensureFuncType(valSig, closBaseName)
	if err != nil {
		return nil, nil, err
	}
	ex := ir.Expr(&ir.Instr{
		Op:   ir.OpStructNew,
		Type: &ir.Ref{Named: closName},
		Args: []ir.Expr{
			&ir.Instr{Op: ir.OpRefFunc, Func: wrapper, Type: &ir.Ref{Named: funcTy}},
			fl.useTemp(recvT),
		},
	})
	ex = &ir.Instr{Op: ir.OpRefCast, Type: &ir.Ref{Null: true, Named: closBaseName}, Args: []ir.Expr{ex}}
	_ = want
	return ex, stmts, nil
}

func (l *pkgLower) emitMethodValueWrapper(wrapper string, fn *types.Func, closName, field string, recvIR ir.Type, recvGo types.Type) error {
	msig := fn.Type().(*types.Signature)
	closParam := &ir.Var{Name: "clos", Typ: &ir.Ref{Named: closBaseName}}
	params := []*ir.Var{closParam}
	var callArgs []ir.Expr
	typed := ir.Expr(&ir.Name{Raw: closParam.Name, Var: closParam})
	if closName != closBaseName {
		typed = &ir.Instr{Op: ir.OpRefCast, Type: &ir.Ref{Named: closName}, Args: []ir.Expr{typed}}
	}
	recvGet := &ir.Instr{
		Op:    ir.OpStructGet,
		Type:  &ir.Ref{Named: closName},
		Field: field,
		Args:  []ir.Expr{typed},
	}
	callArgs = append(callArgs, recvGet)
	for i := 0; i < msig.Params().Len(); i++ {
		p := msig.Params().At(i)
		ty, err := l.mapType(p.Type())
		if err != nil {
			return err
		}
		name := p.Name()
		if name == "" {
			name = "a" + strconv.Itoa(i)
		}
		v := &ir.Var{Name: name, Typ: ty}
		params = append(params, v)
		callArgs = append(callArgs, &ir.Name{Raw: v.Name, Var: v})
	}
	var results []ir.Type
	for i := 0; i < msig.Results().Len(); i++ {
		ty, err := l.mapType(msig.Results().At(i).Type())
		if err != nil {
			return err
		}
		results = append(results, ty)
	}
	var call ir.Expr
	if isIface(recvGo) {
		var res ir.Type
		if len(results) > 0 {
			res = results[0]
		}
		call = makeIfaceCall(fn, recvGo, callArgs, res)
	} else {
		target := methodIRName(fn)
		if l.mono != nil {
			origin := fn.Origin()
			if origin == nil {
				origin = fn
			}
			if methodNeedsMono(origin) {
				recv := msig.Recv().Type()
				if len(l.subst) > 0 {
					recv = l.subst.Apply(recv)
				}
				if namedTypeOf(recvGo) != nil {
					recv = recvGo
				}
				if inst, err := l.mono.EnsureMethod(origin, recv); err != nil {
					return err
				} else if inst != nil {
					target = inst.IRName
				}
			}
		}
		call = &ir.Instr{Op: ir.OpCall, Func: target, Args: callArgs}
	}
	body := wrapperCallBody(call, results)
	l.mod.Funcs = append(l.mod.Funcs, &ir.Func{
		Name:    wrapper,
		Params:  params,
		Results: results,
		Body:    body,
	})
	valSig := types.NewSignatureType(nil, nil, nil, msig.Params(), msig.Results(), msig.Variadic())
	_, err := l.ensureFuncType(valSig, closName)
	return err
}

// wrapperCallBody returns a body that calls then returns, materializing
// multi-value results into temps (IR Return cannot nest a multi-result call).
func wrapperCallBody(call ir.Expr, results []ir.Type) ir.Region {
	switch len(results) {
	case 0:
		return &ir.Seq{Stmts: []ir.Stmt{&ir.ExprStmt{X: call}, &ir.Return{}}}
	case 1:
		return &ir.Seq{Stmts: []ir.Stmt{&ir.Return{Results: []ir.Expr{call}}}}
	default:
		temps := make([]*ir.Temp, len(results))
		lhs := make([]ir.Dest, len(results))
		rets := make([]ir.Expr, len(results))
		for i, ty := range results {
			t := &ir.Temp{Name: "%" + strconv.Itoa(i), Typ: ty}
			temps[i] = t
			lhs[i] = ir.Dest{Temp: t, Name: t.Name}
			rets[i] = &ir.Name{Raw: t.Name, Temp: t}
		}
		return &ir.Seq{Stmts: []ir.Stmt{
			&ir.Assign{Lhs: lhs, Rhs: call},
			&ir.Return{Results: rets},
		}}
	}
}

// methodExpr lowers T.M (method expression) as a func value.
// Interface MethodExprs (I.m, error.Error, T.String with iface bound) dispatch
// via OpIfaceCall; concrete ones call the implementation function.
func (fl *funcLower) methodExpr(sel *types.Selection) (ir.Expr, []ir.Stmt, error) {
	fn := sel.Obj().(*types.Func)
	recvTy := sel.Recv()
	if fl.pkg != nil && len(fl.pkg.subst) > 0 {
		recvTy = fl.pkg.subst.Apply(recvTy)
	}
	if isIface(recvTy) {
		return fl.ifaceMethodExpr(fn, recvTy)
	}
	if m := concreteMethod(recvTy, fn); m != nil {
		fn = m
	}
	if fl.pkg != nil && fl.pkg.mono != nil {
		origin := fn.Origin()
		if origin == nil {
			origin = fn
		}
		if methodNeedsMono(origin) {
			recv := fn.Type().(*types.Signature).Recv().Type()
			if len(fl.pkg.subst) > 0 {
				recv = fl.pkg.subst.Apply(recv)
			}
			if n := namedTypeOf(recvTy); n != nil && n.TypeArgs().Len() > 0 {
				recv = recvTy
			}
			inst, err := fl.pkg.mono.EnsureMethod(origin, recv)
			if err != nil {
				return nil, nil, err
			}
			if inst != nil {
				return fl.monoInstFuncValue(inst)
			}
		}
	}
	return fl.topLevelFuncValue(fn)
}

// ifaceMethodExpr builds a closure for I.M whose wrapper uses OpIfaceCall.
func (fl *funcLower) ifaceMethodExpr(fn *types.Func, ifaceType types.Type) (ir.Expr, []ir.Stmt, error) {
	sig := fn.Type().(*types.Signature)
	fl.pkg.ensureClosBase()
	wrapper := "iface." + sanitizeTypeName(desc.TypeKey(ifaceType)+"."+fn.Name()) + "$clos"
	if fl.mod.LookupFunc(wrapper) == nil {
		if err := fl.pkg.emitIfaceMethodClosWrapper(wrapper, fn, ifaceType); err != nil {
			return nil, nil, err
		}
	}
	funcTy, err := fl.pkg.ensureFuncType(sig, closBaseName)
	if err != nil {
		return nil, nil, err
	}
	return &ir.Instr{
		Op:   ir.OpStructNew,
		Type: &ir.Ref{Named: closBaseName},
		Args: []ir.Expr{
			&ir.Instr{Op: ir.OpRefFunc, Func: wrapper, Type: &ir.Ref{Named: funcTy}},
		},
	}, nil, nil
}

// emitIfaceMethodClosWrapper emits (clos, recv iface, args...) → OpIfaceCall.
func (l *pkgLower) emitIfaceMethodClosWrapper(wrapper string, fn *types.Func, ifaceType types.Type) error {
	sig := fn.Type().(*types.Signature)
	l.ensureClosBase()
	closParam := &ir.Var{Name: "clos", Typ: &ir.Ref{Named: closBaseName}}
	params := []*ir.Var{closParam}
	var args []ir.Expr
	recv := &ir.Var{Name: "recv", Typ: typeIface}
	params = append(params, recv)
	args = append(args, &ir.Name{Raw: recv.Name, Var: recv})
	for i := 0; i < sig.Params().Len(); i++ {
		p := sig.Params().At(i)
		ty, err := l.mapType(p.Type())
		if err != nil {
			return err
		}
		name := p.Name()
		if name == "" {
			name = "a" + strconv.Itoa(i)
		}
		v := &ir.Var{Name: name, Typ: ty}
		params = append(params, v)
		args = append(args, &ir.Name{Raw: v.Name, Var: v})
	}
	var results []ir.Type
	for i := 0; i < sig.Results().Len(); i++ {
		ty, err := l.mapType(sig.Results().At(i).Type())
		if err != nil {
			return err
		}
		results = append(results, ty)
	}
	var res ir.Type
	if len(results) > 0 {
		res = results[0]
	}
	call := makeIfaceCall(fn, ifaceType, args, res)
	body := wrapperCallBody(call, results)
	l.mod.Funcs = append(l.mod.Funcs, &ir.Func{
		Name:    wrapper,
		Params:  params,
		Results: results,
		Body:    body,
	})
	_, err := l.ensureFuncType(sig, closBaseName)
	return err
}

// callFuncValue emits call_ref through a func value.
func (fl *funcLower) callFuncValue(e *ast.CallExpr, sig *types.Signature) (ir.Expr, []ir.Stmt, error) {
	fl.pkg.ensureClosBase()
	clos, stmts, err := fl.expr(e.Fun, &ir.Ref{Null: true, Named: closBaseName})
	if err != nil {
		return nil, nil, err
	}
	closT := fl.newTemp(&ir.Ref{Null: true, Named: closBaseName})
	stmts = append(stmts, fl.assignTemp(closT, clos))
	// Nil check → panic (runtime.panicNil boxes the message with a real itab).
	stmts = append(stmts, &ir.If{
		Cond: &ir.Instr{Op: ir.OpRefIsNull, Args: []ir.Expr{fl.useTemp(closT)}},
		Then: &ir.Seq{Stmts: []ir.Stmt{
			&ir.ExprStmt{X: &ir.Instr{Op: ir.OpCall, Func: "runtime.panicNil"}},
			&ir.Unreachable{},
		}},
	})
	funcTy, err := fl.pkg.ensureFuncType(sig, closBaseName)
	if err != nil {
		return nil, nil, err
	}
	fnField := &ir.Instr{
		Op:    ir.OpStructGet,
		Type:  &ir.Ref{Named: closBaseName},
		Field: "fn",
		Args:  []ir.Expr{&ir.Instr{Op: ir.OpRefAsNonNull, Args: []ir.Expr{fl.useTemp(closT)}}},
	}
	fnT := fl.newTemp(&ir.Ref{Null: true, Heap: ir.HeapFunc})
	stmts = append(stmts, fl.assignTemp(fnT, fnField))
	castFn := &ir.Instr{Op: ir.OpRefCast, Type: &ir.Ref{Named: funcTy}, Args: []ir.Expr{fl.useTemp(fnT)}}
	callArgs := []ir.Expr{
		&ir.Instr{Op: ir.OpRefAsNonNull, Args: []ir.Expr{fl.useTemp(closT)}},
	}
	argExprs, astmts, err := fl.lowerArgsWithSig(e, sig)
	if err != nil {
		return nil, nil, err
	}
	stmts = append(stmts, astmts...)
	callArgs = append(callArgs, argExprs...)
	callArgs = append(callArgs, castFn)
	return &ir.Instr{Op: ir.OpCallRef, Type: &ir.Ref{Named: funcTy}, Args: callArgs}, stmts, nil
}

// varNeedsCell reports whether obj must be boxed for capture-by-reference.
// Set on the package after analyzing assignment; default true when assigned.
func (l *pkgLower) varNeedsCell(obj types.Object) bool {
	if l.meta != nil && l.meta.cellVars != nil {
		if v, ok := l.meta.cellVars[obj]; ok {
			return v
		}
	}
	// Conservative: box when the object is addressable and may be mutated.
	// Params and vars that are assigned anywhere in the package scope of the
	// enclosing function are marked during lowerFunc.
	return true
}

func (fl *funcLower) markAssigned(obj types.Object) {
	if fl.pkg.meta == nil {
		fl.pkg.meta = metaOf(fl.mod)
	}
	if fl.pkg.meta.cellVars == nil {
		fl.pkg.meta.cellVars = map[types.Object]bool{}
	}
	fl.pkg.meta.cellVars[obj] = true
}

// markPlainDirty records that obj was written via the plain local (not a cell).
func (fl *funcLower) markPlainDirty(obj types.Object) {
	if obj == nil {
		return
	}
	if fl.plainDirty == nil {
		fl.plainDirty = map[types.Object]bool{}
	}
	fl.plainDirty[obj] = true
}

// readVar returns the IR expr for reading a local, going through a cell if boxed.
func (fl *funcLower) readVar(obj types.Object) (ir.Expr, error) {
	if fl.cells != nil {
		if cv, ok := fl.cells[obj]; ok {
			cellTy := cv.Typ.(*ir.Ref).Named
			return &ir.Instr{
				Op:    ir.OpStructGet,
				Type:  &ir.Ref{Named: cellTy},
				Field: "v",
				Args:  []ir.Expr{fl.useVar(cv)},
			}, nil
		}
	}
	v, ok := fl.vars[obj]
	if !ok {
		return nil, fmt.Errorf("no var for %s", obj.Name())
	}
	return fl.useVar(v), nil
}

func (fl *funcLower) writeVar(obj types.Object, rhs ir.Expr) ([]ir.Stmt, error) {
	if fl.cells != nil {
		if cv, ok := fl.cells[obj]; ok {
			cellTy := cv.Typ.(*ir.Ref).Named
			// Cell is authoritative; plain local is stale until a plain write.
			delete(fl.plainDirty, obj)
			return []ir.Stmt{&ir.ExprStmt{X: &ir.Instr{
				Op:    ir.OpStructSet,
				Type:  &ir.Ref{Named: cellTy},
				Field: "v",
				Args:  []ir.Expr{fl.useVar(cv), rhs},
			}}}, nil
		}
	}
	v, ok := fl.vars[obj]
	if !ok {
		return nil, fmt.Errorf("no var for %s", obj.Name())
	}
	return []ir.Stmt{fl.assignVar(v, rhs)}, nil
}
