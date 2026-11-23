package lower

import (
	"fmt"
	"go/ast"
	"go/constant"
	"go/token"
	"go/types"

	"github.com/achille-roussel/wasmgc-go/internal/ir"
)

// expr lowers e to an IR expression. want is a hint for untyped constants.
// stmts are preamble statements (e.g. short-circuit materialization).
func (fl *funcLower) expr(e ast.Expr, want ir.Type) (ir.Expr, []ir.Stmt, error) {
	switch e := e.(type) {
	case *ast.BasicLit:
		return fl.basicLit(e, want)
	case *ast.Ident:
		return fl.ident(e)
	case *ast.ParenExpr:
		return fl.expr(e.X, want)
	case *ast.UnaryExpr:
		return fl.unary(e, want)
	case *ast.BinaryExpr:
		return fl.binary(e, want)
	case *ast.CallExpr:
		return fl.callAsValue(e, want)
	case *ast.SelectorExpr:
		return fl.selector(e, want)
	case *ast.IndexExpr:
		return fl.indexExpr(e, want)
	case *ast.SliceExpr:
		return fl.sliceExpr(e, want)
	case *ast.CompositeLit:
		return fl.compositeLit(e, want)
	case *ast.FuncLit:
		return fl.funcLit(e, want)
	case *ast.StarExpr:
		return fl.starExpr(e, want)
	case *ast.TypeAssertExpr:
		return fl.typeAssert(e, want, false)
	default:
		return nil, nil, fl.errorf(e, "unsupported expression %T", e)
	}
}

func (fl *funcLower) ident(e *ast.Ident) (ir.Expr, []ir.Stmt, error) {
	if e.Name == "true" {
		return fl.i32const(1), nil, nil
	}
	if e.Name == "false" {
		return fl.i32const(0), nil, nil
	}
	// Constant from package / iota / const.
	if obj, ok := fl.info.Uses[e]; ok {
		if c, ok := obj.(*types.Const); ok {
			ty, err := fl.mapType(c.Type())
			if err != nil {
				return nil, nil, fl.errorf(e, "%v", err)
			}
			ex, err := fl.constVal(c.Val(), ty)
			return ex, nil, err
		}
		if fn, ok := obj.(*types.Func); ok {
			return fl.topLevelFuncValue(fn)
		}
		if _, ok := obj.(*types.Var); ok {
			if fl.cells != nil {
				if _, boxed := fl.cells[obj]; boxed {
					ex, err := fl.readVar(obj)
					return ex, nil, err
				}
			}
		}
	}
	if g := fl.lookupGlobal(e); g != nil {
		return &ir.Instr{Op: ir.OpGlobalGet, Global: g.Name}, nil, nil
	}
	v, err := fl.lookupVar(e)
	if err != nil {
		return nil, nil, err
	}
	if obj := fl.info.Uses[e]; obj != nil {
		if fl.cells != nil {
			if _, boxed := fl.cells[obj]; boxed {
				ex, err := fl.readVar(obj)
				return ex, nil, err
			}
		}
	}
	return fl.useVar(v), nil, nil
}

func (fl *funcLower) basicLit(e *ast.BasicLit, want ir.Type) (ir.Expr, []ir.Stmt, error) {
	tv, ok := fl.info.Types[e]
	if !ok || tv.Value == nil {
		return nil, nil, fl.errorf(e, "no constant value")
	}
	ty := want
	if ty == nil {
		var err error
		ty, err = fl.mapType(tv.Type)
		if err != nil {
			return nil, nil, fl.errorf(e, "%v", err)
		}
	}
	ex, err := fl.constVal(tv.Value, ty)
	return ex, nil, err
}

func (fl *funcLower) constVal(v constant.Value, want ir.Type) (ir.Expr, error) {
	if want != nil {
		if _, ok := want.(*ir.Ref); ok {
			if v.Kind() == constant.String {
				return &ir.Instr{Op: ir.OpStringLit, Str: constant.StringVal(v)}, nil
			}
		}
	}
	switch v.Kind() {
	case constant.Bool:
		if constant.BoolVal(v) {
			return fl.i32const(1), nil
		}
		return fl.i32const(0), nil
	case constant.String:
		return &ir.Instr{Op: ir.OpStringLit, Str: constant.StringVal(v)}, nil
	case constant.Int:
		if want != nil && irNumWidth(want) == ir.I64 {
			i, _ := constant.Int64Val(v)
			if !okInt64(v) {
				u, _ := constant.Uint64Val(v)
				return fl.i64const(int64(u)), nil
			}
			return fl.i64const(i), nil
		}
		i, ok := constant.Int64Val(v)
		if !ok {
			u, ok := constant.Uint64Val(v)
			if !ok {
				return nil, fmt.Errorf("integer constant out of range")
			}
			return fl.i32const(int64(int32(u))), nil
		}
		return fl.i32const(i), nil
	case constant.Float:
		f, _ := constant.Float64Val(v)
		if want != nil && irNumWidth(want) == ir.F32 {
			return fl.f32const(f), nil
		}
		return fl.f64const(f), nil
	default:
		return nil, fmt.Errorf("unsupported constant kind %v", v.Kind())
	}
}

func okInt64(v constant.Value) bool {
	_, ok := constant.Int64Val(v)
	return ok
}

func (fl *funcLower) unary(e *ast.UnaryExpr, want ir.Type) (ir.Expr, []ir.Stmt, error) {
	switch e.Op {
	case token.ADD:
		return fl.expr(e.X, want)
	case token.SUB:
		x, stmts, err := fl.expr(e.X, want)
		if err != nil {
			return nil, nil, err
		}
		ty := fl.typeOf(e.X)
		irt, _ := fl.mapType(ty)
		if want != nil {
			irt = want
		}
		switch irNumWidth(irt) {
		case ir.I64:
			return &ir.Instr{Op: ir.OpI64Sub, Args: []ir.Expr{fl.i64const(0), x}}, stmts, nil
		case ir.F32:
			return &ir.Instr{Op: ir.OpF32Neg, Args: []ir.Expr{x}}, stmts, nil
		case ir.F64:
			return &ir.Instr{Op: ir.OpF64Neg, Args: []ir.Expr{x}}, stmts, nil
		default:
			return &ir.Instr{Op: ir.OpI32Sub, Args: []ir.Expr{fl.i32const(0), x}}, stmts, nil
		}
	case token.NOT:
		x, stmts, err := fl.boolExpr(e.X)
		if err != nil {
			return nil, nil, err
		}
		return &ir.Instr{Op: ir.OpI32Eqz, Args: []ir.Expr{x}}, stmts, nil
	case token.XOR: // bitwise complement
		x, stmts, err := fl.expr(e.X, want)
		if err != nil {
			return nil, nil, err
		}
		ty := fl.typeOf(e)
		irt, _ := fl.mapType(ty)
		if irNumWidth(irt) == ir.I64 {
			return &ir.Instr{Op: ir.OpI64Xor, Args: []ir.Expr{x, fl.i64const(-1)}}, stmts, nil
		}
		return &ir.Instr{Op: ir.OpI32Xor, Args: []ir.Expr{x, fl.i32const(-1)}}, stmts, nil
	case token.AND:
		return fl.addrOf(e.X, want)
	case token.ARROW:
		return fl.chanRecvExpr(e, want)
	default:
		return nil, nil, fl.errorf(e, "unsupported unary %v", e.Op)
	}
}

func (fl *funcLower) boolExpr(e ast.Expr) (ir.Expr, []ir.Stmt, error) {
	// Short-circuit && and || need if-regions writing a var.
	if b, ok := e.(*ast.BinaryExpr); ok {
		switch b.Op {
		case token.LAND, token.LOR:
			return fl.shortCircuit(b)
		}
	}
	ty := fl.typeOf(e)
	want := ir.TypeI32
	if ty != nil && !isBoolean(ty) {
		// Comparisons produce i32 already via binary.
	}
	return fl.expr(e, want)
}

func (fl *funcLower) shortCircuit(e *ast.BinaryExpr) (ir.Expr, []ir.Stmt, error) {
	res := fl.newVar("sc", ir.TypeI32)
	var out []ir.Stmt

	left, lstmts, err := fl.boolExpr(e.X)
	if err != nil {
		return nil, nil, err
	}
	out = append(out, lstmts...)

	rightSeq := &ir.Seq{}
	right, rstmts, err := fl.boolExpr(e.Y)
	if err != nil {
		return nil, nil, err
	}
	rightSeq.Stmts = append(rightSeq.Stmts, rstmts...)
	rightSeq.Stmts = append(rightSeq.Stmts, fl.assignVar(res, right))

	leftSeq := &ir.Seq{Stmts: []ir.Stmt{fl.assignVar(res, left)}}

	if e.Op == token.LAND {
		// if left { res = right } else { res = left(=false) }
		out = append(out, &ir.If{Cond: left, Then: rightSeq, Else: leftSeq})
	} else {
		// if left { res = left(=true) } else { res = right }
		out = append(out, &ir.If{Cond: left, Then: leftSeq, Else: rightSeq})
	}
	return fl.useVar(res), out, nil
}

func (fl *funcLower) binary(e *ast.BinaryExpr, want ir.Type) (ir.Expr, []ir.Stmt, error) {
	if e.Op == token.LAND || e.Op == token.LOR {
		return fl.shortCircuit(e)
	}
	if e.Op == token.ADD && isString(fl.typeOf(e)) {
		return fl.stringConcat(e)
	}
	if e.Op == token.EQL || e.Op == token.NEQ {
		xt, yt := fl.typeOf(e.X), fl.typeOf(e.Y)
		if isIface(xt) || isIface(yt) ||
			(isNilIdent(e.X) && isIface(yt)) ||
			(isNilIdent(e.Y) && isIface(xt)) {
			return fl.ifaceCompare(e, e.Op)
		}
		// pointer/ref == nil → ref.is_null
		if isNilIdent(e.Y) && xt != nil {
			if irt, err := fl.mapType(xt); err == nil {
				if _, ok := irt.(*ir.Ref); ok {
					return fl.refNilCompare(e.X, irt, e.Op)
				}
			}
		}
		if isNilIdent(e.X) && yt != nil {
			if irt, err := fl.mapType(yt); err == nil {
				if _, ok := irt.(*ir.Ref); ok {
					return fl.refNilCompare(e.Y, irt, e.Op)
				}
			}
		}
		// ref.eq for comparable pointer / string refs
		if xt != nil {
			if irt, err := fl.mapType(xt); err == nil {
				if r, ok := irt.(*ir.Ref); ok && r.Named != "" && r.Named != "$go.string" {
					return fl.refCompare(e, e.Op)
				}
			}
		}
	}

	xt := fl.typeOf(e.X)
	xWant, err := fl.mapType(xt)
	if err != nil {
		// untyped: use want
		xWant = want
	}
	yWant := xWant
	if yt := fl.typeOf(e.Y); yt != nil {
		if m, err := fl.mapType(yt); err == nil {
			yWant = m
		}
	}

	// Wasm i64 shifts take an i64 count; force the RHS to the LHS width for
	// SHL/SHR (Go allows a smaller unsigned count type).
	if e.Op == token.SHL || e.Op == token.SHR {
		yWant = xWant
	}

	x, xstmts, err := fl.expr(e.X, xWant)
	if err != nil {
		return nil, nil, err
	}
	y, ystmts, err := fl.expr(e.Y, yWant)
	if err != nil {
		return nil, nil, err
	}
	var stmts []ir.Stmt
	stmts = append(stmts, xstmts...)
	stmts = append(stmts, ystmts...)

	if e.Op == token.AND_NOT {
		// x &^ y → x & (^y)
		width := irNumWidth(xWant)
		var ynot ir.Expr
		if width == ir.I64 {
			ynot = &ir.Instr{Op: ir.OpI64Xor, Args: []ir.Expr{y, fl.i64const(-1)}}
			return &ir.Instr{Op: ir.OpI64And, Args: []ir.Expr{x, ynot}}, stmts, nil
		}
		ynot = &ir.Instr{Op: ir.OpI32Xor, Args: []ir.Expr{y, fl.i32const(-1)}}
		return &ir.Instr{Op: ir.OpI32And, Args: []ir.Expr{x, ynot}}, stmts, nil
	}

	op, err := fl.binaryOp(e.Op, xt)
	if err != nil {
		return nil, nil, fl.errorf(e, "%v", err)
	}
	return &ir.Instr{Op: op, Args: []ir.Expr{x, y}}, stmts, nil
}

func (fl *funcLower) stringConcat(e *ast.BinaryExpr) (ir.Expr, []ir.Stmt, error) {
	// Cheap path: both sides constant → fold.
	tx, okx := fl.info.Types[e.X]
	ty, oky := fl.info.Types[e.Y]
	if okx && oky && tx.Value != nil && ty.Value != nil &&
		tx.Value.Kind() == constant.String && ty.Value.Kind() == constant.String {
		s := constant.StringVal(tx.Value) + constant.StringVal(ty.Value)
		return &ir.Instr{Op: ir.OpStringLit, Str: s}, nil, nil
	}
	x, xstmts, err := fl.expr(e.X, typeString)
	if err != nil {
		return nil, nil, err
	}
	y, ystmts, err := fl.expr(e.Y, typeString)
	if err != nil {
		return nil, nil, err
	}
	stmts := append(xstmts, ystmts...)
	return &ir.Instr{Op: ir.OpCall, Func: "runtime.concatstrings", Args: []ir.Expr{x, y}}, stmts, nil
}

func (fl *funcLower) binaryOp(op token.Token, goType types.Type) (ir.Op, error) {
	unsigned := isUnsigned(goType)
	width := ir.I32
	if goType != nil {
		if t, err := fl.mapType(goType); err == nil {
			width = irNumWidth(t)
		}
	}
	return binOp(op, width, unsigned)
}

func (fl *funcLower) binaryOpFromAssign(tok token.Token, goType types.Type) (ir.Op, error) {
	var op token.Token
	switch tok {
	case token.ADD_ASSIGN:
		op = token.ADD
	case token.SUB_ASSIGN:
		op = token.SUB
	case token.MUL_ASSIGN:
		op = token.MUL
	case token.QUO_ASSIGN:
		op = token.QUO
	case token.REM_ASSIGN:
		op = token.REM
	case token.AND_ASSIGN:
		op = token.AND
	case token.OR_ASSIGN:
		op = token.OR
	case token.XOR_ASSIGN:
		op = token.XOR
	case token.SHL_ASSIGN:
		op = token.SHL
	case token.SHR_ASSIGN:
		op = token.SHR
	case token.AND_NOT_ASSIGN:
		op = token.AND_NOT
	default:
		return 0, fmt.Errorf("unsupported assign op %v", tok)
	}
	return fl.binaryOp(op, goType)
}

func binOp(op token.Token, width ir.NumKind, unsigned bool) (ir.Op, error) {
	switch width {
	case ir.I64:
		switch op {
		case token.ADD:
			return ir.OpI64Add, nil
		case token.SUB:
			return ir.OpI64Sub, nil
		case token.MUL:
			return ir.OpI64Mul, nil
		case token.QUO:
			if unsigned {
				return ir.OpI64DivU, nil
			}
			return ir.OpI64DivS, nil
		case token.REM:
			if unsigned {
				return ir.OpI64RemU, nil
			}
			return ir.OpI64RemS, nil
		case token.AND:
			return ir.OpI64And, nil
		case token.OR:
			return ir.OpI64Or, nil
		case token.XOR:
			return ir.OpI64Xor, nil
		case token.SHL:
			return ir.OpI64Shl, nil
		case token.SHR:
			if unsigned {
				return ir.OpI64ShrU, nil
			}
			return ir.OpI64ShrS, nil
		case token.AND_NOT:
			return ir.OpI64And, nil // caller must xor rhs with -1 first — handle specially
		case token.EQL:
			return ir.OpI64Eq, nil
		case token.NEQ:
			return ir.OpI64Ne, nil
		case token.LSS:
			if unsigned {
				return ir.OpI64LtU, nil
			}
			return ir.OpI64LtS, nil
		case token.GTR:
			if unsigned {
				return ir.OpI64GtU, nil
			}
			return ir.OpI64GtS, nil
		case token.LEQ:
			if unsigned {
				return ir.OpI64LeU, nil
			}
			return ir.OpI64LeS, nil
		case token.GEQ:
			if unsigned {
				return ir.OpI64GeU, nil
			}
			return ir.OpI64GeS, nil
		}
	case ir.F32:
		switch op {
		case token.ADD:
			return ir.OpF32Add, nil
		case token.SUB:
			return ir.OpF32Sub, nil
		case token.MUL:
			return ir.OpF32Mul, nil
		case token.QUO:
			return ir.OpF32Div, nil
		case token.EQL:
			return ir.OpF32Eq, nil
		case token.NEQ:
			return ir.OpF32Ne, nil
		case token.LSS:
			return ir.OpF32Lt, nil
		case token.GTR:
			return ir.OpF32Gt, nil
		case token.LEQ:
			return ir.OpF32Le, nil
		case token.GEQ:
			return ir.OpF32Ge, nil
		}
	case ir.F64:
		switch op {
		case token.ADD:
			return ir.OpF64Add, nil
		case token.SUB:
			return ir.OpF64Sub, nil
		case token.MUL:
			return ir.OpF64Mul, nil
		case token.QUO:
			return ir.OpF64Div, nil
		case token.EQL:
			return ir.OpF64Eq, nil
		case token.NEQ:
			return ir.OpF64Ne, nil
		case token.LSS:
			return ir.OpF64Lt, nil
		case token.GTR:
			return ir.OpF64Gt, nil
		case token.LEQ:
			return ir.OpF64Le, nil
		case token.GEQ:
			return ir.OpF64Ge, nil
		}
	default: // i32
		switch op {
		case token.ADD:
			return ir.OpI32Add, nil
		case token.SUB:
			return ir.OpI32Sub, nil
		case token.MUL:
			return ir.OpI32Mul, nil
		case token.QUO:
			if unsigned {
				return ir.OpI32DivU, nil
			}
			return ir.OpI32DivS, nil
		case token.REM:
			if unsigned {
				return ir.OpI32RemU, nil
			}
			return ir.OpI32RemS, nil
		case token.AND:
			return ir.OpI32And, nil
		case token.OR:
			return ir.OpI32Or, nil
		case token.XOR:
			return ir.OpI32Xor, nil
		case token.SHL:
			return ir.OpI32Shl, nil
		case token.SHR:
			if unsigned {
				return ir.OpI32ShrU, nil
			}
			return ir.OpI32ShrS, nil
		case token.EQL:
			return ir.OpI32Eq, nil
		case token.NEQ:
			return ir.OpI32Ne, nil
		case token.LSS:
			if unsigned {
				return ir.OpI32LtU, nil
			}
			return ir.OpI32LtS, nil
		case token.GTR:
			if unsigned {
				return ir.OpI32GtU, nil
			}
			return ir.OpI32GtS, nil
		case token.LEQ:
			if unsigned {
				return ir.OpI32LeU, nil
			}
			return ir.OpI32LeS, nil
		case token.GEQ:
			if unsigned {
				return ir.OpI32GeU, nil
			}
			return ir.OpI32GeS, nil
		}
	}
	return 0, fmt.Errorf("unsupported binary op %v for width %v", op, width)
}

func (fl *funcLower) callAsValue(e *ast.CallExpr, want ir.Type) (ir.Expr, []ir.Stmt, error) {
	if fl.isConversion(e) {
		return fl.conversion(e, want)
	}
	if fl.isBuiltinCall(e) {
		return fl.builtinAsValue(e, want)
	}
	if fun, ok := e.Fun.(*ast.SelectorExpr); ok {
		if sel, isIface := fl.ifaceMethod(fun); isIface {
			ex, stmts, err := fl.ifaceCall(e, fun, sel)
			if err != nil {
				return nil, nil, err
			}
			if ex.(*ir.Instr).Type == nil {
				return nil, nil, fl.errorf(e, "interface method returns no results")
			}
			t := fl.newTemp(ex.(*ir.Instr).Type)
			stmts = append(stmts, fl.assignTemp(t, ex))
			return fl.useTemp(t), stmts, nil
		}
	}
	if sig := fl.funcValueSig(e.Fun); sig != nil {
		ex, stmts, err := fl.callFuncValue(e.Fun, e.Args, sig)
		if err != nil {
			return nil, nil, err
		}
		if sig.Results().Len() == 0 {
			return nil, nil, fl.errorf(e, "call used as value but returns no results")
		}
		if sig.Results().Len() > 1 {
			return nil, nil, fl.errorf(e, "multi-value call used in single-value context")
		}
		ty, err := fl.mapType(sig.Results().At(0).Type())
		if err != nil {
			return nil, nil, err
		}
		t := fl.newTemp(ty)
		stmts = append(stmts, fl.assignTemp(t, ex))
		_ = want
		return fl.useTemp(t), stmts, nil
	}
	name, results, err := fl.resolveCallee(e)
	if err != nil {
		return nil, nil, err
	}
	if len(results) == 0 {
		return nil, nil, fl.errorf(e, "call used as value but returns no results")
	}
	if len(results) > 1 {
		return nil, nil, fl.errorf(e, "multi-value call used in single-value context")
	}
	args, stmts, err := fl.lowerArgs(e)
	if err != nil {
		return nil, nil, err
	}
	t := fl.newTemp(results[0])
	call := &ir.Instr{Op: ir.OpCall, Func: name, Args: args}
	stmts = append(stmts, fl.assignTemp(t, call))
	_ = want
	return fl.useTemp(t), stmts, nil
}

// funcValueSig returns the signature when Fun is a func value (not a direct
// known function or method call).
func (fl *funcLower) funcValueSig(fun ast.Expr) *types.Signature {
	switch fun := fun.(type) {
	case *ast.Ident:
		obj := fl.info.Uses[fun]
		if obj == nil {
			return nil
		}
		if _, ok := obj.(*types.Func); ok {
			return nil // direct call
		}
		if sig, ok := obj.Type().Underlying().(*types.Signature); ok {
			return sig
		}
	case *ast.SelectorExpr:
		if sel := fl.info.Selections[fun]; sel != nil {
			switch sel.Kind() {
			case types.FieldVal:
				// Struct field of func type: n.fn().
				if sig, ok := sel.Type().Underlying().(*types.Signature); ok {
					return sig
				}
			default:
				return nil // method call / method value handled elsewhere
			}
		}
		if id, ok := fun.X.(*ast.Ident); ok {
			if _, ok := fl.info.Uses[id].(*types.PkgName); ok {
				return nil // package.Func direct call
			}
		}
		tv := fl.info.Types[fun]
		if tv.Type != nil {
			if sig, ok := tv.Type.Underlying().(*types.Signature); ok {
				return sig
			}
		}
	case *ast.FuncLit, *ast.CallExpr, *ast.IndexExpr, *ast.ParenExpr:
		tv := fl.info.Types[fun]
		if tv.Type == nil {
			return nil
		}
		if sig, ok := tv.Type.Underlying().(*types.Signature); ok {
			return sig
		}
	}
	return nil
}

func (fl *funcLower) isConversion(e *ast.CallExpr) bool {
	if len(e.Args) != 1 {
		return false
	}
	tv, ok := fl.info.Types[e.Fun]
	return ok && tv.IsType()
}

func (fl *funcLower) conversion(e *ast.CallExpr, want ir.Type) (ir.Expr, []ir.Stmt, error) {
	dst := fl.typeOf(e)
	src := fl.typeOf(e.Args[0])
	if isIface(dst) {
		return fl.convertToIface(e.Args[0], src, dst, typeIface)
	}
	if isIface(src) && !isIface(dst) {
		return fl.typeAssert(&ast.TypeAssertExpr{X: e.Args[0], Type: e.Fun}, want, false)
	}
	dstIR, err := fl.mapType(dst)
	if err != nil {
		return nil, nil, fl.errorf(e, "%v", err)
	}
	// (*T)(nil) / (func)(nil) → null ref of the destination type.
	if isUntypedNil(src) {
		if _, ok := dstIR.(*ir.Ref); ok {
			return &ir.Instr{Op: ir.OpRefNull, Type: dstIR}, nil, nil
		}
		return nil, nil, fl.errorf(e, "nil conversion to non-ref type %s", dst)
	}
	srcIR, err := fl.mapType(src)
	if err != nil {
		return nil, nil, fl.errorf(e.Args[0], "%v", err)
	}
	x, stmts, err := fl.expr(e.Args[0], srcIR)
	if err != nil {
		return nil, nil, err
	}
	if ir.TypesEqual(dstIR, srcIR) {
		return x, stmts, nil
	}
	conv, err := numericConv(srcIR, dstIR, isUnsigned(src), isUnsigned(dst))
	if err != nil {
		return nil, nil, fl.errorf(e, "%v", err)
	}
	if conv == 0 {
		return x, stmts, nil
	}
	return &ir.Instr{Op: conv, Args: []ir.Expr{x}}, stmts, nil
}

func (fl *funcLower) refCompare(e *ast.BinaryExpr, op token.Token) (ir.Expr, []ir.Stmt, error) {
	x, xstmts, err := fl.expr(e.X, nil)
	if err != nil {
		return nil, nil, err
	}
	y, ystmts, err := fl.expr(e.Y, nil)
	if err != nil {
		return nil, nil, err
	}
	stmts := append(xstmts, ystmts...)
	eq := &ir.Instr{Op: ir.OpRefEq, Args: []ir.Expr{x, y}}
	if op == token.NEQ {
		return &ir.Instr{Op: ir.OpI32Eqz, Args: []ir.Expr{eq}}, stmts, nil
	}
	return eq, stmts, nil
}

func (fl *funcLower) refNilCompare(x ast.Expr, typ ir.Type, op token.Token) (ir.Expr, []ir.Stmt, error) {
	ex, stmts, err := fl.expr(x, typ)
	if err != nil {
		return nil, nil, err
	}
	isnull := &ir.Instr{Op: ir.OpRefIsNull, Args: []ir.Expr{ex}}
	if op == token.NEQ {
		isnull = &ir.Instr{Op: ir.OpI32Eqz, Args: []ir.Expr{isnull}}
	}
	return isnull, stmts, nil
}

func numericConv(src, dst ir.Type, srcU, dstU bool) (ir.Op, error) {
	sw, dw := irNumWidth(src), irNumWidth(dst)
	if sw == dw {
		return 0, nil
	}
	switch {
	case sw == ir.I32 && dw == ir.I64:
		if srcU {
			return ir.OpI64ExtendI32U, nil
		}
		return ir.OpI64ExtendI32S, nil
	case sw == ir.I64 && dw == ir.I32:
		return ir.OpI32WrapI64, nil
	case sw == ir.I32 && dw == ir.F32:
		if srcU {
			return ir.OpF32ConvertI32U, nil
		}
		return ir.OpF32ConvertI32S, nil
	case sw == ir.I32 && dw == ir.F64:
		if srcU {
			return ir.OpF64ConvertI32U, nil
		}
		return ir.OpF64ConvertI32S, nil
	case sw == ir.I64 && dw == ir.F32:
		if srcU {
			return ir.OpF32ConvertI64U, nil
		}
		return ir.OpF32ConvertI64S, nil
	case sw == ir.I64 && dw == ir.F64:
		if srcU {
			return ir.OpF64ConvertI64U, nil
		}
		return ir.OpF64ConvertI64S, nil
	case sw == ir.F32 && dw == ir.F64:
		return ir.OpF64PromoteF32, nil
	case sw == ir.F64 && dw == ir.F32:
		return ir.OpF32DemoteF64, nil
	case sw == ir.F32 && dw == ir.I32:
		if dstU {
			return ir.OpI32TruncSatF32U, nil
		}
		return ir.OpI32TruncSatF32S, nil
	case sw == ir.F64 && dw == ir.I32:
		if dstU {
			return ir.OpI32TruncSatF64U, nil
		}
		return ir.OpI32TruncSatF64S, nil
	case sw == ir.F32 && dw == ir.I64:
		if dstU {
			return ir.OpI64TruncSatF32U, nil
		}
		return ir.OpI64TruncSatF32S, nil
	case sw == ir.F64 && dw == ir.I64:
		if dstU {
			return ir.OpI64TruncSatF64U, nil
		}
		return ir.OpI64TruncSatF64S, nil
	}
	return 0, fmt.Errorf("unsupported conversion %s → %s", src, dst)
}

// callExpr lowers a call for side effects or to discover result types.
// When asValue is false, emits ExprStmt/Assign for void or ignored results.
func (fl *funcLower) callExpr(e *ast.CallExpr, asValue bool) (resultTypes []ir.Type, stmts []ir.Stmt, err error) {
	if fl.isBuiltinPrint(e) {
		st, err := fl.lowerPrint(e)
		return nil, st, err
	}
	if fl.builtinName(e) == "panic" {
		st, err := fl.builtinPanic(e)
		return nil, st, err
	}
	if fl.builtinName(e) == "close" {
		st, err := fl.builtinClose(e)
		return nil, st, err
	}
	if fl.isBuiltinCall(e) {
		ex, st, err := fl.builtinAsValue(e, nil)
		if err != nil {
			return nil, nil, err
		}
		if !asValue {
			if ex == nil {
				return nil, st, nil
			}
			stmts = append(st, &ir.ExprStmt{X: &ir.Instr{Op: ir.OpDrop, Args: []ir.Expr{ex}}})
			return nil, stmts, nil
		}
		return nil, st, nil
	}
	if sig := fl.funcValueSig(e.Fun); sig != nil {
		ex, st, err := fl.callFuncValue(e.Fun, e.Args, sig)
		if err != nil {
			return nil, nil, err
		}
		var results []ir.Type
		for i := 0; i < sig.Results().Len(); i++ {
			ty, err := fl.mapType(sig.Results().At(i).Type())
			if err != nil {
				return nil, nil, err
			}
			results = append(results, ty)
		}
		if !asValue {
			if len(results) == 0 {
				st = append(st, &ir.ExprStmt{X: ex})
			} else {
				t := fl.newTemp(results[0])
				st = append(st, fl.assignTemp(t, ex))
			}
			return results, st, nil
		}
		return results, st, nil
	}
	if fl.isConversion(e) {
		return nil, nil, fl.errorf(e, "conversion used as statement")
	}
	if fun, ok := e.Fun.(*ast.SelectorExpr); ok {
		if sel, isIface := fl.ifaceMethod(fun); isIface {
			ex, st, err := fl.ifaceCall(e, fun, sel)
			if err != nil {
				return nil, nil, err
			}
			stmts = append(stmts, st...)
			in := ex.(*ir.Instr)
			var results []ir.Type
			if in.Type != nil {
				results = []ir.Type{in.Type}
			}
			if !asValue {
				if len(results) == 0 {
					stmts = append(stmts, &ir.ExprStmt{X: in})
				} else {
					t := fl.newTemp(results[0])
					stmts = append(stmts, fl.assignTemp(t, in))
				}
				return results, stmts, nil
			}
			return results, stmts, nil
		}
	}
	name, results, err := fl.resolveCallee(e)
	if err != nil {
		return nil, nil, err
	}
	args, astmts, err := fl.lowerArgs(e)
	if err != nil {
		return nil, nil, err
	}
	stmts = append(stmts, astmts...)
	call := &ir.Instr{Op: ir.OpCall, Func: name, Args: args}
	if !asValue {
		if len(results) == 0 {
			stmts = append(stmts, &ir.ExprStmt{X: call})
		} else {
			lhs := make([]ir.Dest, len(results))
			for i, ty := range results {
				t := fl.newTemp(ty)
				lhs[i] = ir.Dest{Temp: t, Name: t.Name}
			}
			stmts = append(stmts, &ir.Assign{Lhs: lhs, Rhs: call})
		}
	}
	return results, stmts, nil
}

func (fl *funcLower) callMaterialize(e *ast.CallExpr, resultTypes []ir.Type) ([]*ir.Temp, []ir.Stmt, error) {
	if fl.isBuiltinPrint(e) {
		return nil, nil, fl.errorf(e, "print used as value")
	}
	name, results, err := fl.resolveCallee(e)
	if err != nil {
		return nil, nil, err
	}
	if resultTypes == nil {
		resultTypes = results
	}
	args, stmts, err := fl.lowerArgs(e)
	if err != nil {
		return nil, nil, err
	}
	temps := make([]*ir.Temp, len(resultTypes))
	lhs := make([]ir.Dest, len(resultTypes))
	for i, ty := range resultTypes {
		temps[i] = fl.newTemp(ty)
		lhs[i] = ir.Dest{Temp: temps[i], Name: temps[i].Name}
	}
	call := &ir.Instr{Op: ir.OpCall, Func: name, Args: args}
	stmts = append(stmts, &ir.Assign{Lhs: lhs, Rhs: call})
	return temps, stmts, nil
}

func (fl *funcLower) callAsExpr(e *ast.CallExpr, resultTypes []ir.Type) (ir.Expr, []ir.Stmt, error) {
	if fun, ok := e.Fun.(*ast.SelectorExpr); ok {
		if sel, isIface := fl.ifaceMethod(fun); isIface {
			return fl.ifaceCall(e, fun, sel)
		}
	}
	name, _, err := fl.resolveCallee(e)
	if err != nil {
		return nil, nil, err
	}
	args, stmts, err := fl.lowerArgs(e)
	if err != nil {
		return nil, nil, err
	}
	return &ir.Instr{Op: ir.OpCall, Func: name, Args: args}, stmts, nil
}

func (fl *funcLower) resolveCallee(e *ast.CallExpr) (name string, results []ir.Type, err error) {
	switch fun := e.Fun.(type) {
	case *ast.Ident:
		obj := fl.info.Uses[fun]
		if obj == nil {
			return "", nil, fl.errorf(fun, "unknown function %s", fun.Name)
		}
		fn, ok := obj.(*types.Func)
		if !ok {
			return "", nil, fl.errorf(fun, "%s is not a function", fun.Name)
		}
		sig := fn.Type().(*types.Signature)
		for i := 0; i < sig.Results().Len(); i++ {
			ty, err := fl.mapType(sig.Results().At(i).Type())
			if err != nil {
				return "", nil, fl.errorf(e, "%v", err)
			}
			results = append(results, ty)
		}
		pkg := fn.Pkg()
		if pkg == nil {
			return "", nil, fl.errorf(fun, "builtin %s not lowered here", fun.Name)
		}
		return pkg.Name() + "." + fn.Name(), results, nil
	case *ast.SelectorExpr:
		return fl.resolveMethodCallee(e, fun)
	default:
		return "", nil, fl.errorf(e, "unsupported callee %T", e.Fun)
	}
}

func (fl *funcLower) resolveMethodCallee(e *ast.CallExpr, fun *ast.SelectorExpr) (name string, results []ir.Type, err error) {
	sel := fl.info.Selections[fun]
	if sel == nil {
		// Package-qualified function: pkg.Func
		if id, ok := fun.X.(*ast.Ident); ok {
			if obj, ok := fl.info.Uses[id].(*types.PkgName); ok {
				fn, ok := fl.info.Uses[fun.Sel].(*types.Func)
				if !ok {
					return "", nil, fl.errorf(fun, "not a function")
				}
				sig := fn.Type().(*types.Signature)
				for i := 0; i < sig.Results().Len(); i++ {
					ty, err := fl.mapType(sig.Results().At(i).Type())
					if err != nil {
						return "", nil, err
					}
					results = append(results, ty)
				}
				return obj.Imported().Name() + "." + fn.Name(), results, nil
			}
		}
		return "", nil, fl.errorf(fun, "not a method selection")
	}
	fn, ok := sel.Obj().(*types.Func)
	if !ok {
		return "", nil, fl.errorf(fun, "selection is not a method")
	}
	sig := fn.Type().(*types.Signature)
	for i := 0; i < sig.Results().Len(); i++ {
		ty, err := fl.mapType(sig.Results().At(i).Type())
		if err != nil {
			return "", nil, err
		}
		results = append(results, ty)
	}
	name = methodIRName(fn)
	return name, results, nil
}

// ifaceMethod reports whether the call is through an interface receiver.
func (fl *funcLower) ifaceMethod(fun *ast.SelectorExpr) (*types.Selection, bool) {
	sel := fl.info.Selections[fun]
	if sel == nil || sel.Kind() != types.MethodVal {
		return nil, false
	}
	return sel, isIface(fl.typeOf(fun.X))
}

func methodIRName(fn *types.Func) string {
	sig := fn.Type().(*types.Signature)
	recv := sig.Recv()
	if recv == nil {
		pkg := "main"
		if fn.Pkg() != nil {
			pkg = fn.Pkg().Name()
		}
		return pkg + "." + fn.Name()
	}
	pkg := "main"
	if fn.Pkg() != nil {
		pkg = fn.Pkg().Name()
	}
	rt := recv.Type()
	if p, ok := types.Unalias(rt).(*types.Pointer); ok {
		named, _ := types.Unalias(p.Elem()).(*types.Named)
		tname := "T"
		if named != nil {
			tname = named.Obj().Name()
		}
		return pkg + ".(*" + tname + ")." + fn.Name()
	}
	named, _ := types.Unalias(rt).(*types.Named)
	tname := "T"
	if named != nil {
		tname = named.Obj().Name()
	}
	return pkg + "." + tname + "." + fn.Name()
}

func (fl *funcLower) lowerArgs(e *ast.CallExpr) ([]ir.Expr, []ir.Stmt, error) {
	var sig *types.Signature
	var recvExpr ir.Expr
	var stmts []ir.Stmt

	if sel, ok := e.Fun.(*ast.SelectorExpr); ok {
		if s := fl.info.Selections[sel]; s != nil && s.Kind() == types.MethodVal {
			fn := s.Obj().(*types.Func)
			sig = fn.Type().(*types.Signature)
			recvWant, err := fl.mapType(sig.Recv().Type())
			if err != nil {
				return nil, nil, fl.errorf(sel.X, "%v", err)
			}
			// If method has pointer receiver but X is a value, take address.
			rx, rst, err := fl.methodRecv(sel.X, sig.Recv().Type(), recvWant)
			if err != nil {
				return nil, nil, err
			}
			stmts = append(stmts, rst...)
			recvExpr = rx
		}
	}
	if sig == nil {
		if obj, _ := fl.info.Uses[identOf(e.Fun)].(*types.Func); obj != nil {
			sig, _ = obj.Type().(*types.Signature)
		}
	}

	var args []ir.Expr
	if recvExpr != nil {
		args = append(args, recvExpr)
	}
	paramOff := 0
	if recvExpr != nil {
		paramOff = 0 // sig.Params does not include receiver
	}
	_ = paramOff
	for i, a := range e.Args {
		var want ir.Type
		var dstGo types.Type
		if sig != nil && i < sig.Params().Len() {
			dstGo = sig.Params().At(i).Type()
			var err error
			want, err = fl.mapType(dstGo)
			if err != nil {
				return nil, nil, fl.errorf(a, "%v", err)
			}
		} else if t := fl.typeOf(a); t != nil {
			dstGo = t
			want, _ = fl.mapType(t)
		}
		ex, st, err := fl.coerceExpr(a, dstGo, want)
		if err != nil {
			return nil, nil, err
		}
		stmts = append(stmts, st...)
		args = append(args, ex)
	}
	return args, stmts, nil
}

func (fl *funcLower) methodRecv(x ast.Expr, recvType types.Type, want ir.Type) (ir.Expr, []ir.Stmt, error) {
	xt := fl.typeOf(x)
	needPtr := isPointer(recvType)
	havePtr := isPointer(xt)
	if needPtr && !havePtr {
		return fl.addrOf(x, want)
	}
	if !needPtr && havePtr {
		// Auto-deref for value-receiver method on pointer.
		ex, stmts, err := fl.expr(x, nil)
		if err != nil {
			return nil, nil, err
		}
		t := fl.newTemp(want)
		stmts = append(stmts, fl.assignTemp(t, &ir.Instr{Op: ir.OpDeref, Args: []ir.Expr{ex}, Type: want}))
		return fl.useTemp(t), stmts, nil
	}
	return fl.expr(x, want)
}

func identOf(e ast.Expr) *ast.Ident {
	switch e := e.(type) {
	case *ast.Ident:
		return e
	case *ast.SelectorExpr:
		return e.Sel
	}
	return nil
}
