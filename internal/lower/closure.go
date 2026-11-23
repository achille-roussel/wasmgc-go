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

	"github.com/achille-roussel/wasmgc-go/internal/ir"
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
		child.vars[p] = v
	}
	for i := 0; i < sig.Results().Len(); i++ {
		r := sig.Results().At(i)
		ty, err := child.mapType(r.Type())
		if err != nil {
			return err
		}
		child.results = append(child.results, ty)
		if r.Name() != "" && r.Name() != "_" {
			v := &ir.Var{Name: r.Name(), Typ: ty}
			child.namedResults = append(child.namedResults, v)
			child.vars[r] = v
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
			// before `go func(){ … ch … }`) only updated the plain local. Sync
			// the local into the cell immediately before capture so the child
			// sees the current value.
			if v := fl.vars[c.obj]; v != nil {
				cellTy := cv.Typ.(*ir.Ref).Named
				stmts = append(stmts, &ir.ExprStmt{X: &ir.Instr{
					Op:    ir.OpStructSet,
					Type:  &ir.Ref{Named: cellTy},
					Field: "v",
					Args:  []ir.Expr{fl.useVar(cv), fl.useVar(v)},
				}})
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
	l.ensureClosBase()
	target := l.funcIRNameOf(fn)
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
	var body ir.Region
	if len(results) == 0 {
		body = &ir.Seq{Stmts: []ir.Stmt{&ir.ExprStmt{X: call}, &ir.Return{}}}
	} else {
		body = &ir.Seq{Stmts: []ir.Stmt{&ir.Return{Results: []ir.Expr{call}}}}
	}
	l.mod.Funcs = append(l.mod.Funcs, &ir.Func{
		Name:    wrapper,
		Params:  params,
		Results: results,
		Body:    body,
	})
	_, err := l.ensureFuncType(sig, closBaseName)
	return err
}

// methodValue lowers x.M as a closure capturing x.
func (fl *funcLower) methodValue(e *ast.SelectorExpr, sel *types.Selection, want ir.Type) (ir.Expr, []ir.Stmt, error) {
	fn := sel.Obj().(*types.Func)
	recvExpr := e.X
	recvTy := fl.typeOf(recvExpr)
	recvIR, err := fl.mapType(recvTy)
	if err != nil {
		return nil, nil, fl.errorf(e, "%v", err)
	}
	recvVal, stmts, err := fl.expr(recvExpr, recvIR)
	if err != nil {
		return nil, nil, err
	}
	recvT := fl.newTemp(recvIR)
	stmts = append(stmts, fl.assignTemp(recvT, recvVal))

	// Build a synthetic capture layout: one field for the receiver.
	capField := "recv"
	closName := "$go.clos.method." + sanitizeTypeName(methodIRName(fn))
	fl.pkg.ensureClosBase()
	if fl.mod.LookupType(closName) == nil {
		fl.mod.InternType(&ir.TypeDef{
			Name:  closName,
			Kind:  ir.TypeStruct,
			Super: closBaseName,
			Fields: []ir.Field{
				{Name: "fn", Type: &ir.Ref{Null: true, Heap: ir.HeapFunc}},
				{Name: capField, Type: recvIR},
			},
		})
	}
	// Method value signature is the method type without receiver.
	msig := fn.Type().(*types.Signature)
	valSig := types.NewSignatureType(nil, nil, nil, msig.Params(), msig.Results(), msig.Variadic())
	wrapper := methodIRName(fn) + "$mval"
	if fl.mod.LookupFunc(wrapper) == nil {
		if err := fl.pkg.emitMethodValueWrapper(wrapper, fn, closName, capField, recvIR); err != nil {
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

func (l *pkgLower) emitMethodValueWrapper(wrapper string, fn *types.Func, closName, field string, recvIR ir.Type) error {
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
	call := &ir.Instr{Op: ir.OpCall, Func: methodIRName(fn), Args: callArgs}
	var body ir.Region
	if len(results) == 0 {
		body = &ir.Seq{Stmts: []ir.Stmt{&ir.ExprStmt{X: call}, &ir.Return{}}}
	} else {
		body = &ir.Seq{Stmts: []ir.Stmt{&ir.Return{Results: []ir.Expr{call}}}}
	}
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

// methodExpr lowers T.M (method expression) as a func value.
func (fl *funcLower) methodExpr(fn *types.Func) (ir.Expr, []ir.Stmt, error) {
	// Represented as a top-level-style closure whose underlying call is the method.
	return fl.topLevelFuncValue(fn)
}

// callFuncValue emits call_ref through a func value.
func (fl *funcLower) callFuncValue(fun ast.Expr, args []ast.Expr, sig *types.Signature) (ir.Expr, []ir.Stmt, error) {
	fl.pkg.ensureClosBase()
	clos, stmts, err := fl.expr(fun, &ir.Ref{Null: true, Named: closBaseName})
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
		&ir.Instr{Op: ir.OpRefCast, Type: &ir.Ref{Named: closBaseName}, Args: []ir.Expr{
			&ir.Instr{Op: ir.OpRefAsNonNull, Args: []ir.Expr{fl.useTemp(closT)}},
		}},
	}
	// Actually clos is already ref null $go.clos; for call we need non-null clos of the
	// concrete type. Use as_non_null then cast if needed. Base clos is fine for wrappers
	// that take $go.clos; specific clos subtypes are cast at the wrapper if needed.
	callArgs[0] = &ir.Instr{Op: ir.OpRefAsNonNull, Args: []ir.Expr{fl.useTemp(closT)}}
	for i, a := range args {
		want, err := fl.mapType(sig.Params().At(i).Type())
		if err != nil {
			return nil, nil, err
		}
		ex, astmts, err := fl.coerceExpr(a, fl.typeOf(a), want)
		if err != nil {
			return nil, nil, err
		}
		stmts = append(stmts, astmts...)
		callArgs = append(callArgs, ex)
	}
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
