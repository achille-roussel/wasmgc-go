package lower

import (
	"go/ast"
	"go/token"
	"go/types"

	"github.com/achille-roussel/wasmgc-go/internal/desc"
	"github.com/achille-roussel/wasmgc-go/internal/ir"
)

// boxIRToIface wraps an already-lowered concrete value as dst interface.
func (fl *funcLower) boxIRToIface(x ir.Expr, src, dst types.Type) ir.Expr {
	box := boxKind(src)
	itab := desc.ItabGlobal(dst, src)
	return &ir.Instr{
		Op:     ir.OpIfaceMake,
		Type:   typeIface,
		Global: itab,
		Str:    "type:" + desc.TypeKey(src),
		Field:  box,
		Args:   []ir.Expr{x},
	}
}

// callResultTuple returns the *types.Tuple when e is a multi-result call.
func (fl *funcLower) callResultTuple(e *ast.CallExpr) *types.Tuple {
	if tv, ok := fl.info.Types[e]; ok {
		if tup, ok := tv.Type.(*types.Tuple); ok {
			return tup
		}
	}
	if sig := fl.funcValueSig(e.Fun); sig != nil && sig.Results().Len() > 1 {
		return sig.Results()
	}
	gos := fl.callResultGoTypes(e)
	if len(gos) > 1 {
		vars := make([]*types.Var, len(gos))
		for i, t := range gos {
			vars[i] = types.NewVar(token.NoPos, nil, "", t)
		}
		return types.NewTuple(vars...)
	}
	return nil
}

// callResultGoTypes returns the Go result types of a call expression.
func (fl *funcLower) callResultGoTypes(e *ast.CallExpr) []types.Type {
	if tv, ok := fl.info.Types[e]; ok {
		if tup, ok := tv.Type.(*types.Tuple); ok {
			out := make([]types.Type, tup.Len())
			for i := 0; i < tup.Len(); i++ {
				out[i] = tup.At(i).Type()
			}
			return out
		}
		if tv.Type != nil {
			return []types.Type{tv.Type}
		}
	}
	if sig := fl.funcValueSig(e.Fun); sig != nil {
		out := make([]types.Type, sig.Results().Len())
		for i := 0; i < sig.Results().Len(); i++ {
			out[i] = sig.Results().At(i).Type()
		}
		return out
	}
	if id := identOf(e.Fun); id != nil {
		if obj, _ := fl.info.Uses[id].(*types.Func); obj != nil {
			sig, _ := obj.Type().(*types.Signature)
			if sig != nil {
				if len(fl.pkg.subst) > 0 {
					if s, ok := fl.pkg.subst.Apply(sig).(*types.Signature); ok {
						sig = s
					}
				}
				out := make([]types.Type, sig.Results().Len())
				for i := 0; i < sig.Results().Len(); i++ {
					out[i] = sig.Results().At(i).Type()
				}
				return out
			}
		}
	}
	if sel, ok := e.Fun.(*ast.SelectorExpr); ok {
		if s := fl.info.Selections[sel]; s != nil {
			if fn, ok := s.Obj().(*types.Func); ok {
				sig := fn.Type().(*types.Signature)
				out := make([]types.Type, sig.Results().Len())
				for i := 0; i < sig.Results().Len(); i++ {
					out[i] = sig.Results().At(i).Type()
				}
				return out
			}
		}
		if id, ok := sel.X.(*ast.Ident); ok {
			if _, ok := fl.info.Uses[id].(*types.PkgName); ok {
				if fn, ok := fl.info.Uses[sel.Sel].(*types.Func); ok {
					sig := fn.Type().(*types.Signature)
					out := make([]types.Type, sig.Results().Len())
					for i := 0; i < sig.Results().Len(); i++ {
						out[i] = sig.Results().At(i).Type()
					}
					return out
				}
			}
		}
	}
	return nil
}

// convertToIface boxes a concrete (or interface) value into wantIface.
func (fl *funcLower) convertToIface(e ast.Expr, src types.Type, dst types.Type, want ir.Type) (ir.Expr, []ir.Stmt, error) {
	if src == nil || isUntypedNil(src) || isNilIdent(e) {
		return &ir.Instr{Op: ir.OpRefNull, Type: typeIface}, nil, nil
	}
	if isIface(src) {
		// Interface → interface: assert / re-wrap via descriptor itable vector.
		// For M4: if identical underlying, pass through; else assert to target.
		x, stmts, err := fl.expr(e, typeIface)
		if err != nil {
			return nil, nil, err
		}
		if types.Identical(src.Underlying(), dst.Underlying()) {
			return x, stmts, nil
		}
		// iface→iface conversion (assignment), not a type assertion: a nil
		// source becomes a nil destination without panicking (convert1/2).
		return &ir.Instr{
			Op:     ir.OpIfaceAssert,
			Type:   typeIface,
			Global: desc.TypeKey(dst),
			Str:    "iface.conv",
			Args:   []ir.Expr{x},
		}, stmts, nil
	}

	srcIR, err := fl.mapType(src)
	if err != nil {
		return nil, nil, fl.errorf(e, "%v", err)
	}
	x, stmts, err := fl.expr(e, srcIR)
	if err != nil {
		return nil, nil, err
	}

	// Untyped nil / typed nil pointer → may still build non-nil iface with null data.
	box := boxKind(src)
	itab := desc.ItabGlobal(dst, src)
	return &ir.Instr{
		Op:     ir.OpIfaceMake,
		Type:   typeIface,
		Global: itab,
		Str:    "type:" + desc.TypeKey(src),
		Field:  box,
		Args:   []ir.Expr{x},
	}, stmts, nil
}

func boxKind(t types.Type) string {
	t = types.Unalias(t)
	if isIface(t) {
		return "iface"
	}
	switch u := t.Underlying().(type) {
	case *types.Basic:
		switch u.Kind() {
		case types.Bool, types.Int, types.Int8, types.Int16, types.Int32,
			types.Uint, types.Uint8, types.Uint16, types.Uint32, types.Uintptr:
			return "i32"
		case types.Int64, types.Uint64:
			return "i64"
		case types.Float32:
			return "f32"
		case types.Float64:
			return "f64"
		case types.String:
			return "ref"
		case types.UntypedNil:
			return "nil"
		}
	case *types.Pointer, *types.Slice, *types.Map, *types.Chan, *types.Signature:
		return "ref"
	case *types.Struct:
		return "ref"
	case *types.Array:
		return "ref"
	}
	if _, ok := t.(*types.Named); ok {
		return boxKind(t.Underlying())
	}
	return "ref"
}

func isIface(t types.Type) bool {
	if t == nil {
		return false
	}
	_, ok := types.Unalias(t).Underlying().(*types.Interface)
	return ok
}

func (fl *funcLower) maybeBox(e ast.Expr, src types.Type, want ir.Type, x ir.Expr, stmts []ir.Stmt) (ir.Expr, []ir.Stmt, error) {
	if want == nil || src == nil {
		return x, stmts, nil
	}
	wantIface := false
	if r, ok := want.(*ir.Ref); ok && r.Named == "$go.iface" {
		wantIface = true
	}
	if !wantIface {
		return x, stmts, nil
	}
	if isIface(src) {
		return x, stmts, nil
	}
	// Need destination interface type — recover from want context via go type of e's assign.
	// Caller should use convertToIface with explicit dst. Here we cannot know which iface.
	return x, stmts, nil
}

func (fl *funcLower) typeAssert(e *ast.TypeAssertExpr, want ir.Type, commaOk bool) (ir.Expr, []ir.Stmt, error) {
	x, stmts, err := fl.expr(e.X, typeIface)
	if err != nil {
		return nil, nil, err
	}
	assertTo := fl.typeOf(e.Type)
	if assertTo == nil {
		return nil, nil, fl.errorf(e.Type, "no type for assertion")
	}
	dstIR, err := fl.mapType(assertTo)
	if err != nil {
		return nil, nil, fl.errorf(e.Type, "%v", err)
	}
	str := "concrete"
	if isIface(assertTo) {
		str = "iface"
	}
	if commaOk {
		str += ",ok"
	}
	in := &ir.Instr{
		Op:     ir.OpIfaceAssert,
		Type:   dstIR,
		Global: desc.TypeKey(assertTo),
		Str:    str,
		Args:   []ir.Expr{x},
	}
	if commaOk {
		in.I32 = 1
	}
	return in, stmts, nil
}

func (fl *funcLower) ifaceCall(e *ast.CallExpr, fun *ast.SelectorExpr, sel *types.Selection) (ir.Expr, []ir.Stmt, error) {
	fn := sel.Obj().(*types.Func)
	exprTy := fl.typeOf(fun.X)
	ifaceType := selectionRecvType(exprTy, sel)
	if !isIface(ifaceType) {
		// Direct interface value (no embedding path).
		ifaceType = exprTy
	}
	if !isIface(ifaceType) {
		// Concrete receiver with promoted iface method (e.g. mono of
		// conn.Hello where T became *PoolConn{Conn}): use the method's
		// declared interface type and extract the embedded field.
		if r := fn.Type().(*types.Signature).Recv(); r != nil && isIface(r.Type()) {
			ifaceType = r.Type()
		}
	}
	if !isIface(ifaceType) {
		return nil, nil, fl.errorf(fun, "internal: ifaceCall on non-interface")
	}
	recv, stmts, err := fl.ifaceMethodRecv(fun.X, sel, fn, exprTy, ifaceType)
	if err != nil {
		return nil, nil, err
	}
	sig := fn.Type().(*types.Signature)
	var args []ir.Expr
	args = append(args, recv)
	for i, a := range e.Args {
		var want ir.Type
		if i < sig.Params().Len() {
			want, _ = fl.mapType(sig.Params().At(i).Type())
		}
		// Interface param: convert.
		at := fl.typeOf(a)
		if want != nil && isIface(sig.Params().At(i).Type()) && at != nil && !isIface(at) {
			ex, st, err := fl.convertToIface(a, at, sig.Params().At(i).Type(), typeIface)
			if err != nil {
				return nil, nil, err
			}
			stmts = append(stmts, st...)
			args = append(args, ex)
			continue
		}
		ex, st, err := fl.expr(a, want)
		if err != nil {
			return nil, nil, err
		}
		stmts = append(stmts, st...)
		args = append(args, ex)
	}
	var res ir.Type
	if sig.Results().Len() > 0 {
		var err error
		res, err = fl.mapType(sig.Results().At(0).Type())
		if err != nil {
			return nil, nil, fl.errorf(e, "iface call result: %v", err)
		}
	}
	// Prefer the type-checked call expression type when present.
	if ct := fl.typeOf(e); ct != nil && !isIface(ct) {
		if mapped, err := fl.mapType(ct); err == nil {
			res = mapped
		}
	}
	return makeIfaceCall(fn, ifaceType, args, res), stmts, nil
}

// makeIfaceCall builds an OpIfaceCall through the itable for ifaceType.
func makeIfaceCall(fn *types.Func, ifaceType types.Type, args []ir.Expr, res ir.Type) ir.Expr {
	ifaceKey := desc.TypeKey(ifaceType)
	iface, _ := types.Unalias(ifaceType).Underlying().(*types.Interface)
	san := desc.Sanitize(ifaceKey)
	idx := 0
	if iface != nil {
		idx = methodIndex(iface, fn)
	}
	return &ir.Instr{
		Op:     ir.OpIfaceCall,
		Type:   res,
		Type2:  &ir.Ref{Named: "$go.itabtype." + san},
		Global: ifaceKey, // so desc emits itabtype even without a concrete itab
		Func:   fn.Name(),
		Field:  itoa(idx),
		Str:    "$go.ifacefn." + san + "." + fn.Name(),
		Args:   args,
	}
}

// selectionRecvType walks Selection.Index embedding fields on base and returns
// the type that holds the method (the last type before the method index).
func selectionRecvType(base types.Type, sel *types.Selection) types.Type {
	if base == nil || sel == nil {
		return base
	}
	ty := base
	idx := sel.Index()
	if len(idx) <= 1 {
		return ty
	}
	for _, fi := range idx[:len(idx)-1] {
		if p, ok := types.Unalias(ty).(*types.Pointer); ok {
			ty = p.Elem()
		}
		st := structOf(ty)
		if st == nil || fi < 0 || fi >= st.NumFields() {
			return base
		}
		ty = st.Field(fi).Type()
	}
	return ty
}

// ifaceMethodRecv lowers the interface receiver for an iface call, following
// Selection.Index when it encodes embedding, or LookupFieldOrMethod when mono
// left a type-param Index on a concrete type that embeds the interface.
func (fl *funcLower) ifaceMethodRecv(x ast.Expr, sel *types.Selection, fn *types.Func, exprTy, ifaceType types.Type) (ir.Expr, []ir.Stmt, error) {
	if landed := selectionRecvType(exprTy, sel); isIface(landed) || isIface(exprTy) {
		return fl.methodRecvSel(x, sel, ifaceType, typeIface)
	}
	// Rebuild embedding path from the concrete type.
	if exprTy != nil && !isIface(exprTy) {
		_, index, _ := types.LookupFieldOrMethod(exprTy, true, fn.Pkg(), fn.Name())
		if len(index) > 1 {
			return fl.methodRecvByIndex(x, exprTy, index[:len(index)-1], typeIface)
		}
	}
	return fl.methodRecvSel(x, sel, ifaceType, typeIface)
}

// methodRecvByIndex walks embedding field indices (not including the method
// index) starting from expression x of type base.
func (fl *funcLower) methodRecvByIndex(x ast.Expr, base types.Type, fields []int, want ir.Type) (ir.Expr, []ir.Stmt, error) {
	cur, stmts, err := fl.expr(x, nil)
	if err != nil {
		return nil, nil, err
	}
	ty := base
	if p, ok := types.Unalias(ty).(*types.Pointer); ok {
		ty = p.Elem()
	}
	for _, fi := range fields {
		st := structOf(ty)
		if st == nil || fi < 0 || fi >= st.NumFields() {
			return nil, nil, fl.errorf(x, "iface embedding index %d out of range for %s", fi, ty)
		}
		fld := st.Field(fi)
		stName := ""
		if irt, err := fl.mapType(ty); err == nil {
			stName = refTypeName(irt)
		}
		if stName == "" {
			return nil, nil, fl.errorf(x, "no IR type for %s", ty)
		}
		cur = &ir.Instr{
			Op:    ir.OpStructGet,
			Type:  &ir.Ref{Named: stName},
			Field: fld.Name(),
			Args:  []ir.Expr{cur},
		}
		ty = fld.Type()
		if p, ok := types.Unalias(ty).(*types.Pointer); ok {
			ty = p.Elem()
		}
	}
	_ = want
	return cur, stmts, nil
}

// concreteMethod looks up fn's name on a concrete (non-interface) receiver.
// Used when a type-param / constraint selection still points at the interface
// method after monomorphization substituted a concrete type.
func concreteMethod(recv types.Type, abstract *types.Func) *types.Func {
	if recv == nil || abstract == nil || isIface(recv) {
		return nil
	}
	obj, _, _ := types.LookupFieldOrMethod(recv, true, abstract.Pkg(), abstract.Name())
	fn, ok := obj.(*types.Func)
	if !ok || fn == nil {
		return nil
	}
	if sig, _ := fn.Type().(*types.Signature); sig != nil && sig.Recv() != nil && isIface(sig.Recv().Type()) {
		// Still an interface method (e.g. embedded iface field) — not concrete.
		return nil
	}
	return fn
}

func methodIndex(iface *types.Interface, fn *types.Func) int {
	for i := 0; i < iface.NumMethods(); i++ {
		if iface.Method(i).Name() == fn.Name() {
			return i
		}
	}
	return 0
}

func itoa(n int) string {
	if n == 0 {
		return "0"
	}
	var b [16]byte
	i := len(b)
	for n > 0 {
		i--
		b[i] = byte('0' + n%10)
		n /= 10
	}
	return string(b[i:])
}

func (fl *funcLower) ifaceCompare(e *ast.BinaryExpr, op token.Token) (ir.Expr, []ir.Stmt, error) {
	xt, yt := fl.typeOf(e.X), fl.typeOf(e.Y)
	xNil := isNilIdent(e.X)
	yNil := isNilIdent(e.Y)

	if xNil && yNil {
		v := int64(1)
		if op == token.NEQ {
			v = 0
		}
		return fl.i32const(v), nil, nil
	}
	if yNil {
		x, stmts, err := fl.expr(e.X, typeIface)
		if err != nil {
			return nil, nil, err
		}
		isnull := &ir.Instr{Op: ir.OpRefIsNull, Args: []ir.Expr{x}}
		if op == token.NEQ {
			isnull = &ir.Instr{Op: ir.OpI32Eqz, Args: []ir.Expr{isnull}}
		}
		return isnull, stmts, nil
	}
	if xNil {
		y, stmts, err := fl.expr(e.Y, typeIface)
		if err != nil {
			return nil, nil, err
		}
		isnull := &ir.Instr{Op: ir.OpRefIsNull, Args: []ir.Expr{y}}
		if op == token.NEQ {
			isnull = &ir.Instr{Op: ir.OpI32Eqz, Args: []ir.Expr{isnull}}
		}
		return isnull, stmts, nil
	}

	// Concrete operands (e.g. err != ErrRange where ErrRange is a named int)
	// must be boxed into an interface before iface.eq.
	x, xstmts, err := fl.ifaceCompareOperand(e.X, xt)
	if err != nil {
		return nil, nil, err
	}
	y, ystmts, err := fl.ifaceCompareOperand(e.Y, yt)
	if err != nil {
		return nil, nil, err
	}
	stmts := append(xstmts, ystmts...)
	eq := &ir.Instr{
		Op:   ir.OpConv,
		Type: ir.TypeI32,
		Str:  "iface.eq",
		Args: []ir.Expr{x, y},
	}
	if op == token.NEQ {
		return &ir.Instr{Op: ir.OpI32Eqz, Args: []ir.Expr{eq}}, stmts, nil
	}
	return eq, stmts, nil
}

// ifaceCompareOperand lowers one side of an interface ==/!=, boxing concretes.
func (fl *funcLower) ifaceCompareOperand(e ast.Expr, src types.Type) (ir.Expr, []ir.Stmt, error) {
	if src == nil || isUntypedNil(src) || isNilIdent(e) {
		return &ir.Instr{Op: ir.OpRefNull, Type: typeIface}, nil, nil
	}
	if isIface(src) {
		return fl.expr(e, typeIface)
	}
	// Prefer the interface type of the other side when available; empty
	// interface is correct for error and any comparisons.
	dst := types.Universe.Lookup("any").Type()
	return fl.convertToIface(e, src, dst, typeIface)
}

func isNilIdent(e ast.Expr) bool {
	id, ok := e.(*ast.Ident)
	return ok && id.Name == "nil"
}

// coerceExpr lowers e and converts to dstGo when assigning/passing to an interface.
func (fl *funcLower) coerceExpr(e ast.Expr, dstGo types.Type, want ir.Type) (ir.Expr, []ir.Stmt, error) {
	src := fl.typeOf(e)
	// Apply mono subst so type params (whose Underlying is a constraint
	// interface) are not mistaken for real interface destinations.
	if dstGo != nil && fl.pkg != nil && len(fl.pkg.subst) > 0 {
		dstGo = fl.pkg.subst.Apply(dstGo)
	}
	if dstGo != nil && isIface(dstGo) {
		if src == nil || isUntypedNil(src) {
			return &ir.Instr{Op: ir.OpRefNull, Type: typeIface}, nil, nil
		}
		if !isIface(src) || !types.Identical(src.Underlying(), dstGo.Underlying()) {
			return fl.convertToIface(e, src, dstGo, typeIface)
		}
	}
	// nil → slice/map/chan/pointer/func/unsafe.Pointer
	if (src == nil || isUntypedNil(src)) && want != nil {
		if r, ok := want.(*ir.Ref); ok {
			return &ir.Instr{Op: ir.OpRefNull, Type: r}, nil, nil
		}
	}
	return fl.expr(e, want)
}

func isUntypedNil(t types.Type) bool {
	b, ok := t.(*types.Basic)
	return ok && b.Kind() == types.UntypedNil
}
