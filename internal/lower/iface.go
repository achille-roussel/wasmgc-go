package lower

import (
	"go/ast"
	"go/token"
	"go/types"

	"github.com/achille-roussel/wasmgc-go/internal/desc"
	"github.com/achille-roussel/wasmgc-go/internal/ir"
)

// convertToIface boxes a concrete (or interface) value into wantIface.
func (fl *funcLower) convertToIface(e ast.Expr, src types.Type, dst types.Type, want ir.Type) (ir.Expr, []ir.Stmt, error) {
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
		// iface.assert to interface dst (comma-ok false → panic on fail).
		return &ir.Instr{
			Op:     ir.OpIfaceAssert,
			Type:   typeIface,
			Global: desc.TypeKey(dst),
			Str:    "iface",
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
	recvType := fl.typeOf(fun.X)
	if !isIface(recvType) {
		return nil, nil, fl.errorf(fun, "internal: ifaceCall on non-interface")
	}
	recv, stmts, err := fl.expr(fun.X, typeIface)
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
	ifaceKey := desc.TypeKey(recvType)
	iface, _ := recvType.Underlying().(*types.Interface)
	if iface == nil {
		return nil, nil, fl.errorf(fun, "receiver is not an interface")
	}
	methodIdx := methodIndex(iface, fn)
	sigName := "$go.ifacefn." + sanitizeName(ifaceKey) + "." + fn.Name()
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
	return &ir.Instr{
		Op:    ir.OpIfaceCall,
		Type:  res,
		Type2: &ir.Ref{Named: "$go.itabtype." + sanitizeName(ifaceKey)},
		Func:  fn.Name(),
		Field: itoa(methodIdx),
		Str:   sigName,
		Args:  args,
	}, stmts, nil
}

func methodIndex(iface *types.Interface, fn *types.Func) int {
	for i := 0; i < iface.NumMethods(); i++ {
		if iface.Method(i).Name() == fn.Name() {
			return i
		}
	}
	return 0
}

func sanitizeName(s string) string {
	r := make([]byte, 0, len(s))
	for i := 0; i < len(s); i++ {
		c := s[i]
		switch c {
		case '*':
			r = append(r, []byte("ptr_")...)
		case '.', '/', ' ', '[', ']', '{', '}':
			r = append(r, '_')
		default:
			r = append(r, c)
		}
	}
	return string(r)
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

	x, xstmts, err := fl.expr(e.X, typeIface)
	if err != nil {
		return nil, nil, err
	}
	y, ystmts, err := fl.expr(e.Y, typeIface)
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
	_ = xt
	_ = yt
	return eq, stmts, nil
}

func isNilIdent(e ast.Expr) bool {
	id, ok := e.(*ast.Ident)
	return ok && id.Name == "nil"
}

// coerceExpr lowers e and converts to dstGo when assigning/passing to an interface.
func (fl *funcLower) coerceExpr(e ast.Expr, dstGo types.Type, want ir.Type) (ir.Expr, []ir.Stmt, error) {
	src := fl.typeOf(e)
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
