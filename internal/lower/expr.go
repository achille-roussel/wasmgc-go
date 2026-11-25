package lower

import (
	"fmt"
	"go/ast"
	"go/constant"
	"go/token"
	"go/types"
	"math"
	"strings"

	"github.com/achille-roussel/wasmgc-go/internal/ir"
	"github.com/achille-roussel/wasmgc-go/internal/mono"
)

// expr lowers e to an IR expression. want is a hint for untyped constants.
// stmts are preamble statements (e.g. short-circuit materialization).
func (fl *funcLower) expr(e ast.Expr, want ir.Type) (ir.Expr, []ir.Stmt, error) {
	switch e := e.(type) {
	case *ast.BasicLit:
		return fl.basicLit(e, want)
	case *ast.Ident:
		return fl.ident(e, want)
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
		if ex, stmts, ok, err := fl.genericInstFuncValue(e); ok {
			return ex, stmts, err
		}
		return fl.indexExpr(e, want)
	case *ast.IndexListExpr:
		if ex, stmts, ok, err := fl.genericInstFuncValue(e); ok {
			return ex, stmts, err
		}
		return nil, nil, fl.errorf(e, "unsupported IndexListExpr")
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

func (fl *funcLower) ident(e *ast.Ident, want ir.Type) (ir.Expr, []ir.Stmt, error) {
	if e.Name == "true" {
		return fl.i32const(1), nil, nil
	}
	if e.Name == "false" {
		return fl.i32const(0), nil, nil
	}
	if e.Name == "nil" {
		// Typed nil from context (interface, pointer, slice, map, chan, func).
		if want != nil {
			return &ir.Instr{Op: ir.OpRefNull, Type: want}, nil, nil
		}
		if tv, ok := fl.info.Types[e]; ok && tv.Type != nil && tv.Type != types.Typ[types.UntypedNil] {
			ty, err := fl.mapType(tv.Type)
			if err != nil {
				return nil, nil, fl.errorf(e, "%v", err)
			}
			return &ir.Instr{Op: ir.OpRefNull, Type: ty}, nil, nil
		}
		return nil, nil, fl.errorf(e, "untyped nil needs a typed context")
	}
	// Constant from package / iota / const.
	if obj, ok := fl.info.Uses[e]; ok {
		if c, ok := obj.(*types.Const); ok {
			ty, err := fl.mapType(c.Type())
			if err != nil {
				return nil, nil, fl.errorf(e, "%v", err)
			}
			// Prefer contextual want for untyped numeric consts: untyped float
			// defaults to f64 but must be f32 in a float32 context; untyped int
			// defaults to i32 but must be f64/i64 when the expression wants that.
			ty = preferNumericWant(ty, want, c.Type())
			ex, err := fl.constVal(c.Val(), ty)
			return ex, nil, err
		}
		if fn, ok := obj.(*types.Func); ok {
			return fl.topLevelFuncValue(fn)
		}
		// Locals/params before package globals. Mono bodies (e.g. maps.Equal)
		// reuse param names like m1/m2 that tests also declare as package vars;
		// lookupGlobal's name fallback must not win over the IR local.
		if _, ok := obj.(*types.Var); ok {
			if fl.cells != nil {
				if _, boxed := fl.cells[obj]; boxed {
					ex, err := fl.readVar(obj)
					return ex, nil, err
				}
			}
			if fl.vars != nil {
				if v, ok := fl.vars[obj]; ok {
					ex := ir.Expr(fl.useVar(v))
					if want != nil && irNumWidth(want) == ir.I64 && v.Typ != nil && irNumWidth(v.Typ) == ir.I32 {
						unsigned := false
						if vr, ok := obj.(*types.Var); ok {
							unsigned = isUnsigned(vr.Type())
						}
						if unsigned {
							ex = &ir.Instr{Op: ir.OpI64ExtendI32U, Args: []ir.Expr{ex}}
						} else {
							ex = &ir.Instr{Op: ir.OpI64ExtendI32S, Args: []ir.Expr{ex}}
						}
					}
					return ex, nil, nil
				}
			}
		}
	}
	if g := fl.lookupGlobal(e); g != nil {
		obj := fl.info.Uses[e]
		if obj == nil {
			obj = fl.info.Defs[e]
		}
		if obj != nil {
			return fl.readGlobal(obj, g), nil, nil
		}
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
	ex := ir.Expr(fl.useVar(v))
	// Widen i32 locals when the context wants i64 (e.g. shift counts into uint64).
	if want != nil && irNumWidth(want) == ir.I64 && v.Typ != nil && irNumWidth(v.Typ) == ir.I32 {
		unsigned := false
		if obj := fl.info.Uses[e]; obj != nil {
			if vr, ok := obj.(*types.Var); ok {
				unsigned = isUnsigned(vr.Type())
			}
		}
		if unsigned {
			ex = &ir.Instr{Op: ir.OpI64ExtendI32U, Args: []ir.Expr{ex}}
		} else {
			ex = &ir.Instr{Op: ir.OpI64ExtendI32S, Args: []ir.Expr{ex}}
		}
	}
	return ex, nil, nil
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
	// Untyped int/float/complex → complex64/128 context (e.g. complex64 == 5).
	if name := complexIRName(want); name != "" {
		elem := ir.TypeF64
		if name == "$go.complex64" {
			elem = ir.TypeF32
		}
		fl.ensureComplexType(name, elem)
		var reEx, imEx ir.Expr
		switch v.Kind() {
		case constant.Complex:
			if name == "$go.complex64" {
				re32, _ := constant.Float32Val(constant.Real(v))
				im32, _ := constant.Float32Val(constant.Imag(v))
				reEx, imEx = fl.f32const(float64(re32)), fl.f32const(float64(im32))
			} else {
				re, _ := constant.Float64Val(constant.Real(v))
				im, _ := constant.Float64Val(constant.Imag(v))
				reEx, imEx = fl.f64const(re), fl.f64const(im)
			}
		case constant.Int, constant.Float:
			if name == "$go.complex64" {
				f, _ := constant.Float32Val(constant.ToFloat(v))
				reEx, imEx = fl.f32const(float64(f)), fl.f32const(0)
			} else {
				f, _ := constant.Float64Val(constant.ToFloat(v))
				reEx, imEx = fl.f64const(f), fl.f64const(0)
			}
		default:
			return nil, fmt.Errorf("cannot convert %v to %s", v, name)
		}
		return &ir.Instr{
			Op: ir.OpStructNew, Type: &ir.Ref{Null: true, Named: name},
			Args: []ir.Expr{reEx, imEx},
		}, nil
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
		if want != nil && irNumWidth(want) == ir.F64 {
			f, _ := constant.Float64Val(constant.ToFloat(v))
			return fl.f64const(f), nil
		}
		if want != nil && irNumWidth(want) == ir.F32 {
			// Use Float32Val so rounding matches host Go (float_lit2 edges).
			f, _ := constant.Float32Val(constant.ToFloat(v))
			return fl.f32const(float64(f)), nil
		}
		if want != nil && irNumWidth(want) == ir.I64 {
			if i, ok := constant.Int64Val(v); ok {
				return fl.i64const(i), nil
			}
			if u, ok := constant.Uint64Val(v); ok {
				return fl.i64const(int64(u)), nil
			}
			// Bigger than uint64 (math tables): keep low 64 bits.
			w := constant.BinaryOp(v, token.AND, constant.MakeUint64(^uint64(0)))
			u, _ := constant.Uint64Val(w)
			return fl.i64const(int64(u)), nil
		}
		if i, ok := constant.Int64Val(v); ok {
			return fl.i32const(i), nil
		}
		if u, ok := constant.Uint64Val(v); ok {
			return fl.i32const(int64(int32(u))), nil
		}
		w := constant.BinaryOp(v, token.AND, constant.MakeUint64(0xffffffff))
		u, _ := constant.Uint64Val(w)
		return fl.i32const(int64(int32(u))), nil
	case constant.Float:
		// go/types may keep Kind=Float on a value already typed as int/Duration
		// (e.g. time.Duration(daysPerYear*…*1e9)). Honor integer want.
		if want != nil {
			switch irNumWidth(want) {
			case ir.I64:
				if i, ok := constant.Int64Val(constant.ToInt(v)); ok {
					return fl.i64const(i), nil
				}
				if u, ok := constant.Uint64Val(constant.ToInt(v)); ok {
					return fl.i64const(int64(u)), nil
				}
				f, _ := constant.Float64Val(v)
				return fl.i64const(int64(f)), nil
			case ir.I32:
				if i, ok := constant.Int64Val(constant.ToInt(v)); ok {
					return fl.i32const(i), nil
				}
				f, _ := constant.Float64Val(v)
				return fl.i32const(int64(int32(f))), nil
			case ir.F32:
				f, _ := constant.Float32Val(v)
				return fl.f32const(float64(f)), nil
			}
		}
		f, _ := constant.Float64Val(v)
		return fl.f64const(f), nil
	case constant.Complex:
		// Untyped complex used as integer (e.g. shift count 0i) or float.
		if want != nil {
			if _, ok := want.(*ir.NumType); ok {
				switch irNumWidth(want) {
				case ir.I32, ir.I64, ir.F32, ir.F64:
					return fl.constVal(constant.Real(v), want)
				}
			}
		}
		// Stored complex is a struct of two floats (docs/design/03).
		name := "$go.complex128"
		var reEx, imEx ir.Expr
		if want != nil {
			if r, ok := want.(*ir.Ref); ok && r.Named == "$go.complex64" {
				name = "$go.complex64"
				re32, _ := constant.Float32Val(constant.Real(v))
				im32, _ := constant.Float32Val(constant.Imag(v))
				reEx, imEx = fl.f32const(float64(re32)), fl.f32const(float64(im32))
			}
		}
		if reEx == nil {
			re, _ := constant.Float64Val(constant.Real(v))
			im, _ := constant.Float64Val(constant.Imag(v))
			reEx, imEx = fl.f64const(re), fl.f64const(im)
		}
		elem := ir.TypeF64
		if name == "$go.complex64" {
			elem = ir.TypeF32
		}
		fl.pkg.mod.InternType(&ir.TypeDef{
			Name: name, Kind: ir.TypeStruct, Super: "$go.object",
			Fields: []ir.Field{
				{Name: "real", Type: elem, Mut: true},
				{Name: "imag", Type: elem, Mut: true},
			},
		})
		return &ir.Instr{
			Op:   ir.OpStructNew,
			Type: &ir.Ref{Named: name},
			Args: []ir.Expr{reEx, imEx},
		}, nil
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
		// Prefer the type-checker's constant value so `-0.0` matches host Go
		// (positive zero) instead of emitting f64.neg of 0.0.
		if tv, ok := fl.info.Types[e]; ok && tv.Value != nil {
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
		x, stmts, err := fl.expr(e.X, want)
		if err != nil {
			return nil, nil, err
		}
		ty := fl.typeOf(e.X)
		if isComplex(ty) || complexIRName(want) != "" {
			return fl.complexUnaryNeg(x, ty, want, stmts)
		}
		irt, _ := fl.mapType(ty)
		if want != nil {
			irt = want
		}
		var ex ir.Expr
		switch irNumWidth(irt) {
		case ir.I64:
			ex = &ir.Instr{Op: ir.OpI64Sub, Args: []ir.Expr{fl.i64const(0), x}}
		case ir.F32:
			return &ir.Instr{Op: ir.OpF32Neg, Args: []ir.Expr{x}}, stmts, nil
		case ir.F64:
			return &ir.Instr{Op: ir.OpF64Neg, Args: []ir.Expr{x}}, stmts, nil
		default:
			ex = &ir.Instr{Op: ir.OpI32Sub, Args: []ir.Expr{fl.i32const(0), x}}
		}
		// Sub-width integers must truncate after negation (issue9604).
		return fl.truncateInt(ex, ty), stmts, nil
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
		var ex ir.Expr
		if irNumWidth(irt) == ir.I64 {
			ex = &ir.Instr{Op: ir.OpI64Xor, Args: []ir.Expr{x, fl.i64const(-1)}}
		} else {
			ex = &ir.Instr{Op: ir.OpI32Xor, Args: []ir.Expr{x, fl.i32const(-1)}}
		}
		// Sub-width integers (uint16 etc.) must truncate after ^ (issue9604).
		return fl.truncateInt(ex, ty), stmts, nil
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
	// Untagged switch case with an interface value (issue 3980): compare to
	// boxed true rather than treating the iface ref as an i32 condition.
	if ty != nil && isIface(ty) && !isBoolean(ty) {
		want, err := fl.mapType(ty)
		if err != nil {
			return nil, nil, err
		}
		x, stmts, err := fl.expr(e, want)
		if err != nil {
			return nil, nil, err
		}
		tru := fl.boxIRToIface(fl.i32const(1), types.Typ[types.Bool], ty)
		return &ir.Instr{Op: ir.OpConv, Type: ir.TypeI32, Str: "iface.eq", Args: []ir.Expr{x, tru}}, stmts, nil
	}
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
	// Materialize left into a local so Cond and Then/Else do not share one
	// Instr tree (RuntimeOps mutates Instrs in place when expanding high ops).
	leftV := fl.newVar("scL", ir.TypeI32)
	out = append(out, fl.assignVar(leftV, left))
	leftRef := fl.useVar(leftV)

	rightSeq := &ir.Seq{}
	right, rstmts, err := fl.boolExpr(e.Y)
	if err != nil {
		return nil, nil, err
	}
	rightSeq.Stmts = append(rightSeq.Stmts, rstmts...)
	rightSeq.Stmts = append(rightSeq.Stmts, fl.assignVar(res, right))

	leftSeq := &ir.Seq{Stmts: []ir.Stmt{fl.assignVar(res, leftRef)}}

	if e.Op == token.LAND {
		// if left { res = right } else { res = left(=false) }
		out = append(out, &ir.If{Cond: leftRef, Then: rightSeq, Else: leftSeq})
	} else {
		// if left { res = left(=true) } else { res = right }
		out = append(out, &ir.If{Cond: leftRef, Then: leftSeq, Else: rightSeq})
	}
	return fl.useVar(res), out, nil
}

func (fl *funcLower) binary(e *ast.BinaryExpr, want ir.Type) (ir.Expr, []ir.Stmt, error) {
	if e.Op == token.LAND || e.Op == token.LOR {
		return fl.shortCircuit(e)
	}
	// Const-fold when go/types already evaluated the expression (e.g. 1<<52
	// used as a float64 operand — shifting a float is invalid Wasm).
	if tv, ok := fl.info.Types[e]; ok && tv.Value != nil {
		ty := want
		if ty == nil {
			if t, err := fl.mapType(tv.Type); err == nil {
				ty = t
			}
		}
		if ty != nil {
			if ex, err := fl.constVal(tv.Value, ty); err == nil {
				return ex, nil, nil
			}
		}
	}
	// Complex arithmetic / compare — not shifts (untyped complex counts like
	// 0i are integers in a shift context).
	switch e.Op {
	case token.SHL, token.SHR:
		// fall through to integer shift path
	default:
		xt0, yt0 := fl.typeOf(e.X), fl.typeOf(e.Y)
		if isComplex(xt0) || isComplex(yt0) || isComplex(fl.typeOf(e)) {
			return fl.complexBinary(e, want)
		}
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
		// pointer/ref/externref == nil → ref.is_null
		if isNilIdent(e.Y) && xt != nil {
			if irt, err := fl.mapType(xt); err == nil {
				if _, ok := irt.(*ir.Ref); ok {
					return fl.refNilCompare(e.X, irt, e.Op)
				}
				if _, ok := irt.(*ir.Extern); ok {
					return fl.refNilCompare(e.X, ir.TypeExtern, e.Op)
				}
			}
		}
		if isNilIdent(e.X) && yt != nil {
			if irt, err := fl.mapType(yt); err == nil {
				if _, ok := irt.(*ir.Ref); ok {
					return fl.refNilCompare(e.Y, irt, e.Op)
				}
				if _, ok := irt.(*ir.Extern); ok {
					return fl.refNilCompare(e.Y, ir.TypeExtern, e.Op)
				}
			}
		}
		// string == / != → runtime.stringeq (not ref.eq / i32.ne).
		if isString(xt) || isString(yt) {
			return fl.stringCompare(e, e.Op)
		}
		// Struct value == / != → field-wise $go.eq.* (not ref identity).
		if xt != nil && isStruct(xt) {
			return fl.structCompare(e, e.Op)
		}
		// Array value == / != → element-wise $go.eq.* (not ref identity).
		if xt != nil && isArray(xt) {
			return fl.arrayCompare(e, e.Op)
		}
		// Fat *T pointers are distinct objects per &expr; compare base+index
		// (same as runtime.semaPtrSame), not fat-pointer object identity.
		if xt != nil {
			if irt, err := fl.mapType(xt); err == nil {
				if r, ok := irt.(*ir.Ref); ok && strings.HasPrefix(r.Named, "$go.ptr.") {
					return fl.fatPtrCompare(e, e.Op, r.Named)
				}
			}
		}
		// ref.eq for comparable pointer refs (object pointers, maps, chans, …)
		if xt != nil {
			if irt, err := fl.mapType(xt); err == nil {
				if r, ok := irt.(*ir.Ref); ok && r.Named != "" && r.Named != "$go.string" {
					return fl.refCompare(e, e.Op)
				}
			}
		}
	}
	// string < <= > >= → runtime.cmpstring
	if e.Op == token.LSS || e.Op == token.LEQ || e.Op == token.GTR || e.Op == token.GEQ {
		xt, yt := fl.typeOf(e.X), fl.typeOf(e.Y)
		if isString(xt) || isString(yt) {
			return fl.stringOrderedCompare(e, e.Op)
		}
	}

	xt := fl.typeOf(e.X)
	xWant, err := fl.mapType(xt)
	if err != nil {
		// untyped: use want
		xWant = want
	}
	yWant := xWant
	var yNat ir.Type
	if yt := fl.typeOf(e.Y); yt != nil {
		if m, err := fl.mapType(yt); err == nil {
			yWant = m
			yNat = m
		}
	}
	// Prefer the expression's type when it is numeric (not bool from ==/<).
	if et := fl.typeOf(e); et != nil && !isBoolean(et) {
		if m, err := fl.mapType(et); err == nil && m != nil {
			xWant, yWant = m, m
		}
	}
	// Untyped consts used with uint64 / int64 must become i64.
	if irNumWidth(xWant) == ir.I64 || irNumWidth(yWant) == ir.I64 || irNumWidth(want) == ir.I64 {
		xWant, yWant = ir.TypeI64, ir.TypeI64
	}
	// Untyped numeric literal on one side must match the other operand's
	// concrete numeric width (e.g. float64 n == 1, or n-1 with float T).
	xWant, yWant = unifyUntypedBinaryWants(xWant, yWant, xt, fl.typeOf(e.Y))

	// Wasm i64 shifts take an i64 count. Lower the count at its natural
	// (often i32) width, then extend — forcing yWant=i64 misses paths that
	// ignore want (and double-extend when children already widened).
	// Shifts are integer-only: ignore float want for operands (1<<52 in
	// a float64 multiply must not lower 1 as f64.const). Untyped float /
	// complex counts (1., 0i) must still become integers.
	if e.Op == token.SHL || e.Op == token.SHR {
		if irNumWidth(xWant) == ir.F32 || irNumWidth(xWant) == ir.F64 || xWant == nil {
			xWant = ir.TypeI32
			if xt != nil {
				if m, err := fl.mapType(xt); err == nil && (irNumWidth(m) == ir.I32 || irNumWidth(m) == ir.I64) {
					xWant = m
				}
			}
		}
		if yNat != nil {
			yWant = yNat
		}
		if irNumWidth(yWant) == ir.F32 || irNumWidth(yWant) == ir.F64 || !isNumericIR(yWant) {
			yWant = ir.TypeI32
		}
	}

	// Go shifts: if the count is a constant ≥ bit width, << yields 0
	// (Wasm masks the count; host Go does not). Covers MaxUint shift tests.
	if e.Op == token.SHL || e.Op == token.SHR {
		if tv, ok := fl.info.Types[e.Y]; ok && tv.Value != nil {
			bitW := int64(32)
			if irNumWidth(xWant) == ir.I64 {
				bitW = 64
			}
			cnt := constant.ToInt(tv.Value)
			if cnt.Kind() != constant.Unknown &&
				constant.Compare(cnt, token.GEQ, constant.MakeInt64(bitW)) {
				// Still evaluate LHS for side effects.
				x, xstmts, err := fl.expr(e.X, xWant)
				if err != nil {
					return nil, nil, err
				}
				if e.Op == token.SHL || isUnsigned(xt) {
					if irNumWidth(xWant) == ir.I64 {
						return fl.i64const(0), xstmts, nil
					}
					return fl.i32const(0), xstmts, nil
				}
				// Signed >> with huge count: 0 if x≥0 else -1 ≡ x>>(width-1).
				sh := bitW - 1
				if irNumWidth(xWant) == ir.I64 {
					return &ir.Instr{Op: ir.OpI64ShrS, Args: []ir.Expr{x, fl.i64const(sh)}}, xstmts, nil
				}
				return &ir.Instr{Op: ir.OpI32ShrS, Args: []ir.Expr{x, fl.i32const(sh)}}, xstmts, nil
			}
		}
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

	// Variable shift counts: Wasm masks to width bits (x<<64 ≡ x<<0 on i64).
	// Go yields 0 for << / unsigned >>, and signed fill for signed >>, when
	// count ≥ bit width. Constant ≥width cases are folded above.
	if e.Op == token.SHL || e.Op == token.SHR {
		if tv, ok := fl.info.Types[e.Y]; !ok || tv.Value == nil {
			return fl.shiftBounded(e.Op, x, y, xWant, yWant, xt, stmts)
		}
		// Constant count < width: align count width to value width for IR shifts.
		if irNumWidth(xWant) == ir.I64 && irNumWidth(yWant) == ir.I32 {
			if yt := fl.typeOf(e.Y); yt != nil && isUnsigned(yt) {
				y = &ir.Instr{Op: ir.OpI64ExtendI32U, Args: []ir.Expr{y}}
			} else {
				y = &ir.Instr{Op: ir.OpI64ExtendI32S, Args: []ir.Expr{y}}
			}
		} else if irNumWidth(xWant) == ir.I32 && irNumWidth(yWant) == ir.I64 {
			y = &ir.Instr{Op: ir.OpI32WrapI64, Args: []ir.Expr{y}}
		}
	}

	op, err := fl.binaryOp(e.Op, xt)
	if err != nil {
		return nil, nil, fl.errorf(e, "%v", err)
	}
	// If operands were widened to i64, pick the i64 opcode even when Go
	// types look untyped/int (math/bits uint64 const folding).
	if irNumWidth(xWant) == ir.I64 {
		if op2, err2 := binOp(e.Op, ir.I64, isUnsigned(xt) || isUnsigned(fl.typeOf(e.Y))); err2 == nil {
			op = op2
		}
	}
	// Integer / and % must Go-panic on y==0 (Wasm traps otherwise).
	// Skip the check when the divisor is a non-zero constant so helpers like
	// printuint(v/10) do not pull runtime.panicDivide into every println.
	if (e.Op == token.QUO || e.Op == token.REM) && isInteger(xt) && !nonzeroIntConst(fl, e.Y) {
		yVar := fl.newVar("divy", yWant)
		stmts = append(stmts, fl.assignVar(yVar, y))
		y = fl.useVar(yVar)
		var isZero ir.Expr
		if irNumWidth(yWant) == ir.I64 {
			isZero = &ir.Instr{Op: ir.OpI64Eqz, Args: []ir.Expr{y}}
		} else {
			isZero = &ir.Instr{Op: ir.OpI32Eqz, Args: []ir.Expr{y}}
		}
		stmts = append(stmts, &ir.If{
			Cond: isZero,
			Then: &ir.Seq{Stmts: []ir.Stmt{
				&ir.ExprStmt{X: &ir.Instr{Op: ir.OpCall, Func: "runtime.panicDivide"}},
				&ir.Unreachable{},
			}},
		})
	}
	// Signed MinInt / -1 (and %) is defined in Go as quotient=MinInt, rem=0.
	// Wasm i32/i64.div_s traps on that case — special-case before the op.
	if (e.Op == token.QUO || e.Op == token.REM) && isInteger(xt) && !isUnsigned(xt) {
		xVar := fl.newVar("divx", xWant)
		yVar := fl.newVar("divy2", yWant)
		res := fl.newVar("divr", xWant)
		stmts = append(stmts, fl.assignVar(xVar, x), fl.assignVar(yVar, y))
		x, y = fl.useVar(xVar), fl.useVar(yVar)
		var min ir.Expr
		var neg1 ir.Expr
		var isMin ir.Expr
		var isNeg1 ir.Expr
		if irNumWidth(xWant) == ir.I64 {
			min = fl.i64const(math.MinInt64)
			neg1 = fl.i64const(-1)
			isMin = &ir.Instr{Op: ir.OpI64Eq, Args: []ir.Expr{x, min}}
			isNeg1 = &ir.Instr{Op: ir.OpI64Eq, Args: []ir.Expr{y, neg1}}
		} else {
			// Sub-width signed ints live in i32; MinInt for the Go width.
			bits := goIntBits(xt)
			minVal := int64(-1) << (bits - 1)
			if bits == 0 || bits >= 32 {
				minVal = math.MinInt32
			}
			min = fl.i32const(minVal)
			neg1 = fl.i32const(-1)
			isMin = &ir.Instr{Op: ir.OpI32Eq, Args: []ir.Expr{x, min}}
			isNeg1 = &ir.Instr{Op: ir.OpI32Eq, Args: []ir.Expr{y, neg1}}
		}
		overflow := &ir.Instr{Op: ir.OpI32And, Args: []ir.Expr{isMin, isNeg1}}
		var special ir.Expr
		if e.Op == token.REM {
			if irNumWidth(xWant) == ir.I64 {
				special = fl.i64const(0)
			} else {
				special = fl.i32const(0)
			}
		} else {
			special = x // MinInt
		}
		normal := ir.Expr(&ir.Instr{Op: op, Args: []ir.Expr{x, y}})
		stmts = append(stmts, &ir.If{
			Cond: overflow,
			Then: &ir.Seq{Stmts: []ir.Stmt{fl.assignVar(res, special)}},
			Else: &ir.Seq{Stmts: []ir.Stmt{fl.assignVar(res, normal)}},
		})
		var ex ir.Expr = fl.useVar(res)
		if isArithmeticOp(e.Op) {
			ex = fl.truncateInt(ex, xt)
		}
		return ex, stmts, nil
	}
	ex := ir.Expr(&ir.Instr{Op: op, Args: []ir.Expr{x, y}})
	// Sub-width integers live in i32/i64 registers; truncate so uint8(255)+1
	// wraps to 0 (Wasm add does not).
	if isArithmeticOp(e.Op) {
		ex = fl.truncateInt(ex, xt)
	}
	return ex, stmts, nil
}

func isArithmeticOp(op token.Token) bool {
	switch op {
	case token.ADD, token.SUB, token.MUL, token.QUO, token.REM,
		token.AND, token.OR, token.XOR, token.AND_NOT, token.SHL, token.SHR:
		return true
	}
	return false
}

// nonzeroIntConst reports whether e is a compile-time integer ≠ 0.
func nonzeroIntConst(fl *funcLower, e ast.Expr) bool {
	if fl == nil || fl.info == nil || e == nil {
		return false
	}
	tv, ok := fl.info.Types[e]
	if !ok || tv.Value == nil {
		return false
	}
	v := constant.ToInt(tv.Value)
	if v.Kind() == constant.Unknown {
		return false
	}
	return !constant.Compare(v, token.EQL, constant.MakeInt64(0))
}

// truncateInt narrows an i32/i64 arithmetic result to the Go integer type's
// bit width (sign- or zero-extension). No-op for full-width and non-integers.
func (fl *funcLower) truncateInt(ex ir.Expr, goType types.Type) ir.Expr {
	bits := goIntBits(goType)
	if bits == 0 {
		return ex
	}
	unsigned := isUnsigned(goType)
	width := irNumWidth(nil)
	if t, err := fl.mapType(goType); err == nil {
		width = irNumWidth(t)
	}
	if width == ir.I64 {
		if bits >= 64 {
			return ex
		}
		if unsigned {
			mask := int64((uint64(1) << bits) - 1)
			return &ir.Instr{Op: ir.OpI64And, Args: []ir.Expr{ex, fl.i64const(mask)}}
		}
		sh := int64(64 - bits)
		return &ir.Instr{Op: ir.OpI64ShrS, Args: []ir.Expr{
			&ir.Instr{Op: ir.OpI64Shl, Args: []ir.Expr{ex, fl.i64const(sh)}},
			fl.i64const(sh),
		}}
	}
	// i32 register
	if bits >= 32 {
		return ex
	}
	if unsigned {
		mask := int64((uint64(1) << bits) - 1)
		return &ir.Instr{Op: ir.OpI32And, Args: []ir.Expr{ex, fl.i32const(mask)}}
	}
	sh := int64(32 - bits)
	return &ir.Instr{Op: ir.OpI32ShrS, Args: []ir.Expr{
		&ir.Instr{Op: ir.OpI32Shl, Args: []ir.Expr{ex, fl.i32const(sh)}},
		fl.i32const(sh),
	}}
}

// goIntBits returns the bit width of a Go integer type, or 0 if not applicable.
func goIntBits(t types.Type) int {
	if t == nil {
		return 0
	}
	switch basicKind(t) {
	case types.Int8, types.Uint8: // Byte == Uint8
		return 8
	case types.Int16, types.Uint16:
		return 16
	case types.Int32, types.Uint32: // Rune == Int32
		return 32
	case types.Int64, types.Uint64:
		return 64
	case types.Int, types.Uint, types.Uintptr:
		return 32 // wasmgc word size
	}
	return 0
}

// shiftBounded emits Go's shift semantics for a non-constant count:
// if count ≥ bit width → 0 (<< / unsigned >>) or signed fill (signed >>);
// else the native Wasm shift. Count width (yWant) may differ from x (e.g.
// int << uint64); compare in the count's width, convert for the Wasm shift.
func (fl *funcLower) shiftBounded(op token.Token, x, y ir.Expr, xWant, yWant ir.Type, xt types.Type, stmts []ir.Stmt) (ir.Expr, []ir.Stmt, error) {
	is64 := irNumWidth(xWant) == ir.I64
	yIs64 := irNumWidth(yWant) == ir.I64
	bitW := int64(goIntBits(xt))
	if bitW == 0 {
		bitW = 32
		if is64 {
			bitW = 64
		}
	}
	unsigned := isUnsigned(xt)
	shiftOp, err := binOp(op, irNumWidth(xWant), unsigned)
	if err != nil {
		return nil, nil, err
	}

	res := fl.newVar("sh", xWant)
	xv := fl.newVar("shx", xWant)
	yTy := ir.TypeI32
	if yIs64 {
		yTy = ir.TypeI64
	}
	yv := fl.newVar("shy", yTy)
	stmts = append(stmts, fl.assignVar(xv, x), fl.assignVar(yv, y))

	var ge ir.Expr
	if yIs64 {
		ge = &ir.Instr{Op: ir.OpI64GeU, Args: []ir.Expr{fl.useVar(yv), fl.i64const(bitW)}}
	} else {
		ge = &ir.Instr{Op: ir.OpI32GeU, Args: []ir.Expr{fl.useVar(yv), fl.i32const(bitW)}}
	}

	var thenVal ir.Expr
	if op == token.SHL || unsigned {
		if is64 {
			thenVal = fl.i64const(0)
		} else {
			thenVal = fl.i32const(0)
		}
	} else {
		// Signed >> with huge count ≡ x >> (width-1).
		sh := bitW - 1
		if is64 {
			thenVal = &ir.Instr{Op: ir.OpI64ShrS, Args: []ir.Expr{fl.useVar(xv), fl.i64const(sh)}}
		} else {
			thenVal = &ir.Instr{Op: ir.OpI32ShrS, Args: []ir.Expr{fl.useVar(xv), fl.i32const(sh)}}
		}
	}
	var shiftCount ir.Expr = fl.useVar(yv)
	if is64 && !yIs64 {
		shiftCount = &ir.Instr{Op: ir.OpI64ExtendI32U, Args: []ir.Expr{shiftCount}}
	} else if !is64 && yIs64 {
		// Wasm i32.shl/shr take i32 counts; ≥width already handled above.
		shiftCount = &ir.Instr{Op: ir.OpI32WrapI64, Args: []ir.Expr{shiftCount}}
	}
	elseVal := &ir.Instr{Op: shiftOp, Args: []ir.Expr{fl.useVar(xv), shiftCount}}
	stmts = append(stmts, &ir.If{
		Cond: ge,
		Then: &ir.Seq{Stmts: []ir.Stmt{fl.assignVar(res, thenVal)}},
		Else: &ir.Seq{Stmts: []ir.Stmt{fl.assignVar(res, elseVal)}},
	})
	out := fl.truncateInt(fl.useVar(res), xt)
	return out, stmts, nil
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
	// Type must be set: len(a+b) / (a+b)[i:] expand OpLen via lenCapArgType,
	// which otherwise defaults to $go.slice.i32 and fails IR verify.
	return &ir.Instr{Op: ir.OpCall, Func: "runtime.concatstrings", Type: typeString, Args: []ir.Expr{x, y}}, stmts, nil
}

func (fl *funcLower) stringCompare(e *ast.BinaryExpr, op token.Token) (ir.Expr, []ir.Stmt, error) {
	x, xstmts, err := fl.expr(e.X, typeString)
	if err != nil {
		return nil, nil, err
	}
	y, ystmts, err := fl.expr(e.Y, typeString)
	if err != nil {
		return nil, nil, err
	}
	stmts := append(xstmts, ystmts...)
	eq := &ir.Instr{Op: ir.OpCall, Func: "runtime.stringeq", Args: []ir.Expr{x, y}}
	if op == token.NEQ {
		return &ir.Instr{Op: ir.OpI32Eqz, Args: []ir.Expr{eq}}, stmts, nil
	}
	return eq, stmts, nil
}

func (fl *funcLower) stringOrderedCompare(e *ast.BinaryExpr, op token.Token) (ir.Expr, []ir.Stmt, error) {
	x, xstmts, err := fl.expr(e.X, typeString)
	if err != nil {
		return nil, nil, err
	}
	y, ystmts, err := fl.expr(e.Y, typeString)
	if err != nil {
		return nil, nil, err
	}
	stmts := append(xstmts, ystmts...)
	cmp := &ir.Instr{Op: ir.OpCall, Func: "runtime.cmpstring", Args: []ir.Expr{x, y}}
	zero := fl.i32const(0)
	switch op {
	case token.LSS:
		return &ir.Instr{Op: ir.OpI32LtS, Args: []ir.Expr{cmp, zero}}, stmts, nil
	case token.LEQ:
		return &ir.Instr{Op: ir.OpI32LeS, Args: []ir.Expr{cmp, zero}}, stmts, nil
	case token.GTR:
		return &ir.Instr{Op: ir.OpI32GtS, Args: []ir.Expr{cmp, zero}}, stmts, nil
	case token.GEQ:
		return &ir.Instr{Op: ir.OpI32GeS, Args: []ir.Expr{cmp, zero}}, stmts, nil
	default:
		return nil, nil, fl.errorf(e, "internal: bad string ordered op %v", op)
	}
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

// unsafeBuiltinCall lowers unsafe.Sizeof / Alignof / Offsetof to constants
// and unsafe.String / Slice / SliceData / StringData to header ops (docs/design/05).
// ok=false means not an unsafe builtin.
func (fl *funcLower) unsafeBuiltinCall(e *ast.CallExpr, want ir.Type) (ir.Expr, []ir.Stmt, bool, error) {
	sel, ok := e.Fun.(*ast.SelectorExpr)
	if !ok {
		return nil, nil, false, nil
	}
	id, ok := sel.X.(*ast.Ident)
	if !ok {
		return nil, nil, false, nil
	}
	pn, ok := fl.info.Uses[id].(*types.PkgName)
	if !ok || pn.Imported() == nil || pn.Imported().Path() != "unsafe" {
		return nil, nil, false, nil
	}
	b, ok := fl.info.Uses[sel.Sel].(*types.Builtin)
	if !ok {
		return nil, nil, false, nil
	}
	switch b.Name() {
	case "Sizeof", "Alignof", "Offsetof":
		return fl.unsafeSizeofFamily(e, b.Name(), want)
	case "SliceData":
		return fl.unsafeSliceData(e, want)
	case "StringData":
		return fl.unsafeStringData(e, want)
	case "String":
		return fl.unsafeString(e, want)
	case "Slice":
		return fl.unsafeSlice(e, want)
	default:
		return nil, nil, false, nil
	}
}

func (fl *funcLower) unsafeSizeofFamily(e *ast.CallExpr, name string, want ir.Type) (ir.Expr, []ir.Stmt, bool, error) {
	if len(e.Args) != 1 {
		return nil, nil, true, fl.errorf(e, "unsafe.%s wants 1 arg", name)
	}
	arg := e.Args[0]
	tv, ok := fl.info.Types[arg]
	if !ok || tv.Type == nil {
		return nil, nil, true, fl.errorf(arg, "unsafe.%s: no type", name)
	}
	argTy := tv.Type
	if len(fl.pkg.subst) > 0 {
		argTy = fl.pkg.subst.Apply(argTy)
	}
	argTy = types.Unalias(argTy)
	if mono.ContainsTypeParam(argTy) {
		return nil, nil, true, fl.errorf(arg, "unsafe.%s: unsubstituted type param", name)
	}
	var n int64
	switch name {
	case "Sizeof":
		n = Sizes.Sizeof(argTy)
	case "Alignof":
		n = Sizes.Alignof(argTy)
	case "Offsetof":
		s, ok := arg.(*ast.SelectorExpr)
		if !ok {
			return nil, nil, true, fl.errorf(arg, "unsafe.Offsetof requires selector")
		}
		seln := fl.info.Selections[s]
		if seln == nil {
			return nil, nil, true, fl.errorf(arg, "unsafe.Offsetof: no selection")
		}
		off, err := fl.unsafeOffsetof(seln)
		if err != nil {
			return nil, nil, true, fl.errorf(arg, "unsafe.Offsetof: %v", err)
		}
		n = off
	}
	_ = want
	return fl.i32const(n), nil, true, nil
}

// applySubst applies the current mono subst map (if any).
func (fl *funcLower) applySubst(t types.Type) types.Type {
	if t != nil && fl.pkg != nil && len(fl.pkg.subst) > 0 {
		return fl.pkg.subst.Apply(t)
	}
	return t
}

// unsafeOffsetof walks Selection.Index (including promoted/embedded fields)
// summing StdSizes offsets along the path. Field types are substituted so
// mono instances of generic structs do not panic inside StdSizes.
func (fl *funcLower) unsafeOffsetof(sel *types.Selection) (int64, error) {
	idx := sel.Index()
	if len(idx) == 0 {
		return 0, fmt.Errorf("empty selection index")
	}
	t := fl.applySubst(sel.Recv())
	var total int64
	for _, fi := range idx {
		for {
			p, ok := types.Unalias(t).Underlying().(*types.Pointer)
			if !ok {
				break
			}
			t = fl.applySubst(p.Elem())
		}
		st, ok := types.Unalias(t).Underlying().(*types.Struct)
		if !ok {
			return 0, fmt.Errorf("not a struct field")
		}
		if fi < 0 || fi >= st.NumFields() {
			return 0, fmt.Errorf("bad index")
		}
		off, next, err := fl.structFieldOffset(st, fi)
		if err != nil {
			return 0, err
		}
		total += off
		t = next
	}
	return total, nil
}

// structFieldOffset returns the byte offset of field fi and that field's
// (substituted) type. Mirrors types.StdSizes.Offsetsof with subst applied.
func (fl *funcLower) structFieldOffset(st *types.Struct, fi int) (int64, types.Type, error) {
	var o int64
	for i := 0; i < st.NumFields(); i++ {
		ft := fl.applySubst(st.Field(i).Type())
		if mono.ContainsTypeParam(ft) {
			return 0, nil, fmt.Errorf("unsubstituted type param")
		}
		a := Sizes.Alignof(ft)
		o = alignInt64(o, a)
		if i == fi {
			return o, ft, nil
		}
		o += Sizes.Sizeof(ft)
	}
	return 0, nil, fmt.Errorf("bad index")
}

func alignInt64(o, a int64) int64 {
	if a <= 0 {
		return o
	}
	return (o + a - 1) &^ (a - 1)
}

// unsafeSliceData lowers unsafe.SliceData(s) to a fat pointer at s's off.
func (fl *funcLower) unsafeSliceData(e *ast.CallExpr, want ir.Type) (ir.Expr, []ir.Stmt, bool, error) {
	if len(e.Args) != 1 {
		return nil, nil, true, fl.errorf(e, "unsafe.SliceData wants 1 arg")
	}
	arg := e.Args[0]
	src := fl.typeOf(arg)
	if src == nil || !isSlice(src) {
		return nil, nil, true, fl.errorf(arg, "unsafe.SliceData: not a slice")
	}
	sl, ok := types.Unalias(src).Underlying().(*types.Slice)
	if !ok {
		return nil, nil, true, fl.errorf(arg, "unsafe.SliceData: not a slice")
	}
	kind, err := fatPtrKind(sl.Elem())
	if err != nil {
		return nil, nil, true, fl.errorf(arg, "unsafe.SliceData: %v", err)
	}
	fl.pkg.ensureFatPtrTypes(kind)
	ptrTy := want
	if ptrTy == nil {
		ptrTy = &ir.Ref{Null: true, Named: "$go.ptr." + kind}
	}
	x, stmts, err := fl.expr(arg, nil)
	if err != nil {
		return nil, nil, true, err
	}
	// OpAddr on the slice; expandAddr unwraps data/off into the fat pointer.
	return &ir.Instr{
		Op:    ir.OpAddr,
		Args:  []ir.Expr{x, fl.i32const(0)},
		Field: "elem",
		Str:   "elem",
		Type:  ptrTy,
	}, stmts, true, nil
}

// unsafeStringData lowers unsafe.StringData(s) to a *byte fat pointer at s's off.
func (fl *funcLower) unsafeStringData(e *ast.CallExpr, want ir.Type) (ir.Expr, []ir.Stmt, bool, error) {
	if len(e.Args) != 1 {
		return nil, nil, true, fl.errorf(e, "unsafe.StringData wants 1 arg")
	}
	arg := e.Args[0]
	if !isString(fl.typeOf(arg)) {
		return nil, nil, true, fl.errorf(arg, "unsafe.StringData: not a string")
	}
	fl.pkg.ensureFatPtrTypes("i32")
	ptrTy := want
	if ptrTy == nil {
		ptrTy = &ir.Ref{Null: true, Named: "$go.ptr.i32"}
	}
	x, stmts, err := fl.expr(arg, nil)
	if err != nil {
		return nil, nil, true, err
	}
	// expandAddr unwraps $go.string to data/off.
	return &ir.Instr{
		Op:    ir.OpAddr,
		Args:  []ir.Expr{x, fl.i32const(0)},
		Field: "elem",
		Str:   "elem",
		Type:  ptrTy,
	}, stmts, true, nil
}

// unsafeString lowers unsafe.String(p *byte, n) to a $go.string header.
func (fl *funcLower) unsafeString(e *ast.CallExpr, want ir.Type) (ir.Expr, []ir.Stmt, bool, error) {
	if len(e.Args) != 2 {
		return nil, nil, true, fl.errorf(e, "unsafe.String wants 2 args")
	}
	ptrEx, stmts, err := fl.expr(e.Args[0], &ir.Ref{Null: true, Named: "$go.ptr.i32"})
	if err != nil {
		return nil, nil, true, err
	}
	nEx, nstmts, err := fl.expr(e.Args[1], ir.TypeI32)
	if err != nil {
		return nil, nil, true, err
	}
	stmts = append(stmts, nstmts...)
	nEx = fl.ensureI32Len(e.Args[1], nEx)
	fl.pkg.ensureFatPtrTypes("i32")
	pt := &ir.Ref{Named: "$go.ptr.i32"}
	nn := &ir.Instr{Op: ir.OpRefAsNonNull, Args: []ir.Expr{ptrEx}}
	base := &ir.Instr{Op: ir.OpStructGet, Type: pt, Field: "base", Args: []ir.Expr{nn}}
	index := &ir.Instr{Op: ir.OpStructGet, Type: pt, Field: "index", Args: []ir.Expr{nn}}
	// base is anyref holding $go.bytes; cast for the string header.
	data := &ir.Instr{Op: ir.OpRefCast, Type: &ir.Ref{Named: "$go.bytes"}, Args: []ir.Expr{base}}
	_ = want
	return &ir.Instr{
		Op:   ir.OpStructNew,
		Type: typeString,
		Args: []ir.Expr{data, index, nEx},
	}, stmts, true, nil
}

// unsafeSlice lowers unsafe.Slice(p *T, n) to a slice header over p's array.
func (fl *funcLower) unsafeSlice(e *ast.CallExpr, want ir.Type) (ir.Expr, []ir.Stmt, bool, error) {
	if len(e.Args) != 2 {
		return nil, nil, true, fl.errorf(e, "unsafe.Slice wants 2 args")
	}
	ptrT := fl.typeOf(e.Args[0])
	if ptrT == nil || !isPointer(ptrT) {
		return nil, nil, true, fl.errorf(e.Args[0], "unsafe.Slice: not a pointer")
	}
	elem := types.Unalias(ptrT).Underlying().(*types.Pointer).Elem()
	kind, err := fatPtrKind(elem)
	if err != nil {
		return nil, nil, true, fl.errorf(e.Args[0], "unsafe.Slice: %v", err)
	}
	fl.pkg.ensureFatPtrTypes(kind)
	ptrTy := &ir.Ref{Null: true, Named: "$go.ptr." + kind}
	ptrEx, stmts, err := fl.expr(e.Args[0], ptrTy)
	if err != nil {
		return nil, nil, true, err
	}
	nEx, nstmts, err := fl.expr(e.Args[1], ir.TypeI32)
	if err != nil {
		return nil, nil, true, err
	}
	stmts = append(stmts, nstmts...)
	nEx = fl.ensureI32Len(e.Args[1], nEx)
	arrName, _, err := fl.pkg.ensureArrayType(elem)
	if err != nil {
		return nil, nil, true, err
	}
	sliceName := sliceTypeName(arrName)
	pt := &ir.Ref{Named: "$go.ptr." + kind}
	nn := &ir.Instr{Op: ir.OpRefAsNonNull, Args: []ir.Expr{ptrEx}}
	base := &ir.Instr{Op: ir.OpStructGet, Type: pt, Field: "base", Args: []ir.Expr{nn}}
	index := &ir.Instr{Op: ir.OpStructGet, Type: pt, Field: "index", Args: []ir.Expr{nn}}
	data := &ir.Instr{Op: ir.OpRefCast, Type: &ir.Ref{Named: arrName}, Args: []ir.Expr{base}}
	sliceTy := want
	if sliceTy == nil {
		sliceTy = &ir.Ref{Null: true, Named: sliceName}
	}
	return &ir.Instr{
		Op:   ir.OpStructNew,
		Type: sliceTy,
		Args: []ir.Expr{data, index, nEx, nEx},
	}, stmts, true, nil
}

func (fl *funcLower) callAsValue(e *ast.CallExpr, want ir.Type) (ir.Expr, []ir.Stmt, error) {
	if fl.isConversion(e) {
		return fl.conversion(e, want)
	}
	if fl.isBuiltinCall(e) {
		return fl.builtinAsValue(e, want)
	}
	if ex, stmts, ok, err := fl.unsafeBuiltinCall(e, want); ok || err != nil {
		return ex, stmts, err
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
		ex, stmts, err := fl.callFuncValue(e, sig)
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
	apply := func(sig *types.Signature) *types.Signature {
		if sig != nil && fl.pkg != nil && len(fl.pkg.subst) > 0 {
			if s, ok := fl.pkg.subst.Apply(sig).(*types.Signature); ok {
				return s
			}
		}
		return sig
	}
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
			return apply(sig)
		}
	case *ast.SelectorExpr:
		if sel := fl.info.Selections[fun]; sel != nil {
			switch sel.Kind() {
			case types.FieldVal:
				// Struct field of func type: n.fn().
				if sig, ok := sel.Type().Underlying().(*types.Signature); ok {
					return apply(sig)
				}
			case types.MethodExpr:
				// T.M / (*T).M / I.M: signature includes the receiver so
				// T.M(nil) can coerce untyped nil via lowerArgsWithSig.
				if sig, ok := sel.Type().Underlying().(*types.Signature); ok {
					return apply(sig)
				}
			default:
				return nil // method call / method value handled elsewhere
			}
		}
		if id, ok := fun.X.(*ast.Ident); ok {
			if _, ok := fl.info.Uses[id].(*types.PkgName); ok {
				// package.Func direct call vs package.Var of func type
				// (e.g. internal.TransformMarshalError(...)).
				obj := fl.info.Uses[fun.Sel]
				if _, isFn := obj.(*types.Func); isFn {
					return nil
				}
				if obj != nil {
					if sig, ok := obj.Type().Underlying().(*types.Signature); ok {
						return apply(sig)
					}
				}
				return nil
			}
		}
		tv := fl.info.Types[fun]
		if tv.Type != nil {
			if sig, ok := tv.Type.Underlying().(*types.Signature); ok {
				return apply(sig)
			}
		}
	case *ast.IndexExpr, *ast.IndexListExpr:
		// Max[int](...) is a direct generic call, not a func-value call.
		if fl.indexExprGenericFunc(fun) != nil {
			return nil
		}
		tv := fl.info.Types[fun]
		if tv.Type == nil {
			return nil
		}
		if sig, ok := tv.Type.Underlying().(*types.Signature); ok {
			return apply(sig)
		}
	case *ast.FuncLit, *ast.CallExpr, *ast.ParenExpr, *ast.TypeAssertExpr:
		// Includes f.(func(T))(x) — Interface().(func(...))(...) and similar.
		tv := fl.info.Types[fun]
		if tv.Type == nil {
			return nil
		}
		if sig, ok := tv.Type.Underlying().(*types.Signature); ok {
			return apply(sig)
		}
	}
	return nil
}

// genericInstFuncValue lowers F[T] / F[T,U] used as a function value
// (e.g. EqualFunc(m, m, equal[int])), not as a call.
func (fl *funcLower) genericInstFuncValue(e ast.Expr) (ir.Expr, []ir.Stmt, bool, error) {
	origin := fl.indexExprGenericFunc(e)
	if origin == nil {
		return nil, nil, false, nil
	}
	if fl.pkg.mono == nil {
		return nil, nil, true, fl.errorf(e, "generic func value without mono table")
	}
	id := identOf(e)
	if id == nil {
		return nil, nil, true, fl.errorf(e, "generic func value: no function ident")
	}
	instRec, has := fl.info.Instances[id]
	if !has {
		return nil, nil, true, fl.errorf(id, "generic func value %s missing Instance", id.Name)
	}
	args := fl.concreteArgs(instRec.TypeArgs)
	if args == nil {
		return nil, nil, true, fl.errorf(id, "generic func value %s has unsubstituted type args", id.Name)
	}
	inst, err := fl.pkg.mono.EnsureFunc(origin, args)
	if err != nil {
		return nil, nil, true, fl.errorf(id, "%v", err)
	}
	ex, stmts, err := fl.monoInstFuncValue(inst)
	return ex, stmts, true, err
}

// indexExprGenericFunc returns the generic Func origin when fun is F[T] / F[T,U]
// naming a generic function (instantiation used as a call callee), including
// package-qualified forms like pkg.F[T].
func (fl *funcLower) indexExprGenericFunc(fun ast.Expr) *types.Func {
	var x ast.Expr
	switch fun := fun.(type) {
	case *ast.IndexExpr:
		x = fun.X
	case *ast.IndexListExpr:
		x = fun.X
	default:
		return nil
	}
	var id *ast.Ident
	switch x := x.(type) {
	case *ast.Ident:
		id = x
	case *ast.SelectorExpr:
		// pkg.F[T]: only package-qualified function names.
		if pkgID, ok := x.X.(*ast.Ident); ok {
			if _, isPkg := fl.info.Uses[pkgID].(*types.PkgName); isPkg {
				id = x.Sel
			}
		}
		if id == nil {
			return nil
		}
	default:
		return nil
	}
	fn, ok := fl.info.Uses[id].(*types.Func)
	if !ok {
		return nil
	}
	origin := fn.Origin()
	if origin == nil {
		origin = fn
	}
	sig, _ := origin.Type().(*types.Signature)
	if sig == nil || sig.TypeParams().Len() == 0 {
		return nil
	}
	return origin
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
		// error(nil) / any(nil) → null iface (before convertToIface mapType(nil)).
		if isUntypedNil(src) || isNilIdent(e.Args[0]) {
			return &ir.Instr{Op: ir.OpRefNull, Type: typeIface}, nil, nil
		}
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
	// Constant args: emit at destination width via constVal (float32(real(C))
	// / float32(1.5) / complex64(0)). Avoids f64.const+demote and skips
	// builtins that ignore want (real/imag of untyped complex).
	if tv, ok := fl.info.Types[e.Args[0]]; ok && tv.Value != nil && isNumericIR(dstIR) {
		if ex, err := fl.constVal(tv.Value, dstIR); err == nil {
			return ex, nil, nil
		}
	}
	if tv, ok := fl.info.Types[e.Args[0]]; ok && tv.Value != nil && complexIRName(dstIR) != "" {
		if ex, err := fl.constVal(tv.Value, dstIR); err == nil {
			return ex, nil, nil
		}
	}
	// Untyped numeric args without a folded value: prefer dest width.
	argWant := preferNumericWant(srcIR, dstIR, src)
	x, stmts, err := fl.expr(e.Args[0], argWant)
	if err != nil {
		return nil, nil, err
	}
	actualSrc := srcIR
	if isUntypedBasic(src) {
		if tv, ok := fl.info.Types[e.Args[0]]; ok && tv.Value != nil {
			actualSrc = argWant
		}
	}
	if ir.TypesEqual(dstIR, actualSrc) {
		// Same Wasm register (e.g. int8 ↔ uint8 → i32) still needs a Go-width
		// truncate so uint8(-1) becomes 255 (issue 20530).
		if isInteger(dst) {
			return fl.truncateInt(x, dst), stmts, nil
		}
		return x, stmts, nil
	}
	// unsafe.Pointer ↔ *T / ref: Wasm GC has no integer address; this is ref.cast
	// (docs/design/05-unsafe-and-reflect.md).
	if isUnsafePointer(src) || isUnsafePointer(dst) {
		if isRefIR(dstIR) {
			return &ir.Instr{Op: ir.OpRefCast, Type: dstIR, Args: []ir.Expr{x}}, stmts, nil
		}
		if isRefIR(srcIR) {
			anyT := &ir.Ref{Null: true, Heap: ir.HeapAny}
			return &ir.Instr{Op: ir.OpRefCast, Type: anyT, Args: []ir.Expr{x}}, stmts, nil
		}
	}
	// string(rune) / string(byte) / string(int…)
	if isString(dst) && isInteger(src) {
		ex, st, err := fl.intToString(x, src)
		return ex, append(stmts, st...), err
	}
	// string ↔ []byte / [N]byte (docs/design/03: allocate+copy).
	// Must run before the generic ref.cast below — both are Ref IR types but
	// $go.string and $go.slice.i8 are not Wasm subtypes of each other.
	// Only byte-backed slices share headers; []rune needs a real decode/encode.
	if isString(dst) && isByteSliceOrArray(src) {
		ex, st, err := fl.bytesToString(x, src, srcIR)
		return ex, append(stmts, st...), err
	}
	if isByteSliceOrArray(dst) && isString(src) {
		ex, st, err := fl.stringToBytes(x, dst, dstIR)
		return ex, append(stmts, st...), err
	}
	if isString(dst) && isRuneSlice(src) {
		call := &ir.Instr{
			Op: ir.OpCall, Func: "runtime.RunesToString",
			Type: typeString, Args: []ir.Expr{x},
		}
		return call, stmts, nil
	}
	if isRuneSlice(dst) && isString(src) {
		call := &ir.Instr{
			Op: ir.OpCall, Func: "runtime.StringToRunes",
			Type: dstIR, Args: []ir.Expr{x},
		}
		return call, stmts, nil
	}
	// complex64 ↔ complex128: promote/demote real and imag parts.
	// Must run before the generic ref.cast path: both are Ref IR types but
	// $go.complex64 and $go.complex128 are not Wasm subtypes, so a bare
	// ref.cast traps with illegal cast.
	if isComplex(src) && isComplex(dst) {
		ex, st, err := fl.complexConvert(x, src, dst, srcIR, dstIR)
		return ex, append(stmts, st...), err
	}
	// *T ↔ *U / named struct refs with convertible underlying types (e.g.
	// (*marshalersOption)(*Marshalers) in encoding/json/v2).
	if isRefIR(srcIR) && isRefIR(dstIR) {
		return &ir.Instr{Op: ir.OpRefCast, Type: dstIR, Args: []ir.Expr{x}}, stmts, nil
	}
	// int/float → complex: real part, imag 0 (and the reverse is unsupported
	// except via real()/imag() builtins).
	if isComplex(dst) && (isInteger(src) || isFloat(src) || isUntypedBasic(src)) {
		name, elem := complexTypeShape(dst, dstIR)
		fl.ensureComplexType(name, elem)
		re := x
		if isNumericIR(srcIR) && srcIR != elem {
			if conv, err := numericConv(srcIR, elem, isUnsigned(src), false); err == nil && conv != 0 {
				re = &ir.Instr{Op: conv, Args: []ir.Expr{x}}
			}
		}
		var zero ir.Expr
		if elem == ir.TypeF32 {
			zero = fl.f32const(0)
		} else {
			zero = fl.f64const(0)
		}
		return &ir.Instr{
			Op: ir.OpStructNew, Type: &ir.Ref{Null: true, Named: name},
			Args: []ir.Expr{re, zero},
		}, stmts, nil
	}
	if !isNumericIR(actualSrc) || !isNumericIR(dstIR) {
		return nil, nil, fl.errorf(e, "unsupported conversion %s → %s", src, dst)
	}
	conv, err := numericConv(actualSrc, dstIR, isUnsigned(src), isUnsigned(dst))
	if err != nil {
		return nil, nil, fl.errorf(e, "%v", err)
	}
	if conv != 0 {
		x = &ir.Instr{Op: conv, Args: []ir.Expr{x}}
	}
	if isInteger(dst) {
		x = fl.truncateInt(x, dst)
	}
	return x, stmts, nil
}

func isNumericIR(t ir.Type) bool {
	_, ok := t.(*ir.NumType)
	return ok
}

// preferNumericWant replaces the default IR width of an untyped numeric
// constant with the contextual want when both are numeric Wasm types,
// or when the context is complex64/128 (untyped int/float → complex).
func preferNumericWant(ty, want ir.Type, goTy types.Type) ir.Type {
	if want == nil || ty == nil || !isUntypedBasic(goTy) {
		return ty
	}
	if wr, ok := want.(*ir.Ref); ok && (wr.Named == "$go.complex64" || wr.Named == "$go.complex128") {
		return want
	}
	if !isNumericIR(want) || !isNumericIR(ty) {
		return ty
	}
	if ir.TypesEqual(ty, want) {
		return ty
	}
	return want
}

// complexIRName returns the $go.complex{64,128} name for want, or "".
func complexIRName(want ir.Type) string {
	if r, ok := want.(*ir.Ref); ok {
		if r.Named == "$go.complex64" || r.Named == "$go.complex128" {
			return r.Named
		}
	}
	return ""
}

// unifyUntypedBinaryWants widens an untyped numeric operand to the other
// side's concrete float/int width (float64 == 1, n-1 with float T, …).
func unifyUntypedBinaryWants(xWant, yWant ir.Type, xt, yt types.Type) (ir.Type, ir.Type) {
	if isUntypedBasic(yt) && isNumericIR(xWant) && isNumericIR(yWant) && !ir.TypesEqual(xWant, yWant) {
		// Prefer float or wider int from the typed side.
		if irNumWidth(xWant) == ir.F32 || irNumWidth(xWant) == ir.F64 ||
			irNumWidth(xWant) == ir.I64 || (irNumWidth(xWant) == ir.I32 && irNumWidth(yWant) != ir.I32) {
			yWant = xWant
		}
	}
	if isUntypedBasic(xt) && isNumericIR(yWant) && isNumericIR(xWant) && !ir.TypesEqual(xWant, yWant) {
		if irNumWidth(yWant) == ir.F32 || irNumWidth(yWant) == ir.F64 ||
			irNumWidth(yWant) == ir.I64 || (irNumWidth(yWant) == ir.I32 && irNumWidth(xWant) != ir.I32) {
			xWant = yWant
		}
	}
	return xWant, yWant
}

// bytesToString builds a $go.string from a []byte / [N]byte value.
// Allocates and copies (docs/design/03): encoder buffers mutate after
// string([]byte), and sharing would rewrite retained names (json map keys).
func (fl *funcLower) bytesToString(x ir.Expr, src types.Type, srcIR ir.Type) (ir.Expr, []ir.Stmt, error) {
	var stmts []ir.Stmt
	bytesTy := &ir.Ref{Null: true, Named: "$go.bytes"}
	empty := &ir.Instr{Op: ir.OpStringLit, Str: ""}
	if isSlice(src) {
		st := srcIR
		if _, ok := st.(*ir.Ref); !ok {
			st = &ir.Ref{Null: true, Named: "$go.slice.i8"}
		}
		tmp := fl.newVar("b2s", typeString)
		srcV := fl.newVar("b2sSrc", st)
		lenV := fl.newVar("b2sLen", ir.TypeI32)
		dstV := fl.newVar("b2sDst", bytesTy)
		// Entry-assign empty string so (ref $go.string) is never left
		// uninitialized if a caller (e.g. package-var init) skips zero-init.
		stmts = append(stmts, fl.assignVar(tmp, empty), fl.assignVar(srcV, x))
		// string(nil []byte) is "".
		nilThen := &ir.Seq{Stmts: []ir.Stmt{fl.assignVar(tmp, empty)}}
		nonNil := &ir.Seq{}
		nonNil.Stmts = append(nonNil.Stmts,
			fl.assignVar(lenV, &ir.Instr{Op: ir.OpStructGet, Type: st, Field: "len", Args: []ir.Expr{fl.useVar(srcV)}}),
		)
		zeroThen := &ir.Seq{Stmts: []ir.Stmt{fl.assignVar(tmp, empty)}}
		copyThen := &ir.Seq{}
		copyThen.Stmts = append(copyThen.Stmts,
			fl.assignVar(dstV, &ir.Instr{Op: ir.OpArrayNewDefault, Type: bytesTy, Args: []ir.Expr{fl.useVar(lenV)}}),
			&ir.ExprStmt{X: &ir.Instr{
				Op: ir.OpArrayCopy, Type: bytesTy, Type2: bytesTy,
				Args: []ir.Expr{
					fl.useVar(dstV),
					fl.i32const(0),
					&ir.Instr{Op: ir.OpStructGet, Type: st, Field: "data", Args: []ir.Expr{fl.useVar(srcV)}},
					&ir.Instr{Op: ir.OpStructGet, Type: st, Field: "off", Args: []ir.Expr{fl.useVar(srcV)}},
					fl.useVar(lenV),
				},
			}},
			fl.assignVar(tmp, &ir.Instr{
				Op:   ir.OpStructNew,
				Type: typeString,
				Args: []ir.Expr{
					&ir.Instr{Op: ir.OpRefAsNonNull, Args: []ir.Expr{fl.useVar(dstV)}},
					fl.i32const(0),
					fl.useVar(lenV),
				},
			}),
		)
		nonNil.Stmts = append(nonNil.Stmts, &ir.If{
			Cond: &ir.Instr{Op: ir.OpI32Eqz, Args: []ir.Expr{fl.useVar(lenV)}},
			Then: zeroThen,
			Else: copyThen,
		})
		stmts = append(stmts, &ir.If{
			Cond: &ir.Instr{Op: ir.OpRefIsNull, Args: []ir.Expr{fl.useVar(srcV)}},
			Then: nilThen,
			Else: nonNil,
		})
		return fl.useVar(tmp), stmts, nil
	}
	// [N]byte / *[N]byte: copy into a fresh $go.bytes.
	n := int64(0)
	if a, ok := types.Unalias(src).Underlying().(*types.Array); ok {
		n = a.Len()
	} else if p, ok := types.Unalias(src).Underlying().(*types.Pointer); ok {
		if a, ok := types.Unalias(p.Elem()).Underlying().(*types.Array); ok {
			n = a.Len()
		}
	}
	if n == 0 {
		return empty, stmts, nil
	}
	arrName := "$go.bytes"
	if r, ok := srcIR.(*ir.Ref); ok && r.Named != "" && r.Named != "$go.bytes" {
		// *[N]byte / [N]byte may be $go.array.* — still i8 storage for byte arrays.
		if strings.HasPrefix(r.Named, "$go.array.") || r.Named == "$go.bytes" {
			arrName = r.Named
		}
	}
	tmp := fl.newVar("b2s", typeString)
	dstV := fl.newVar("b2sDst", bytesTy)
	srcArr := x
	stmts = append(stmts,
		fl.assignVar(dstV, &ir.Instr{Op: ir.OpArrayNewDefault, Type: bytesTy, Args: []ir.Expr{fl.i32const(n)}}),
		&ir.ExprStmt{X: &ir.Instr{
			Op: ir.OpArrayCopy, Type: bytesTy, Type2: &ir.Ref{Named: arrName},
			Args: []ir.Expr{fl.useVar(dstV), fl.i32const(0), srcArr, fl.i32const(0), fl.i32const(n)},
		}},
		fl.assignVar(tmp, &ir.Instr{
			Op:   ir.OpStructNew,
			Type: typeString,
			Args: []ir.Expr{
				&ir.Instr{Op: ir.OpRefAsNonNull, Args: []ir.Expr{fl.useVar(dstV)}},
				fl.i32const(0),
				fl.i32const(n),
			},
		}),
	)
	return fl.useVar(tmp), stmts, nil
}

func (fl *funcLower) stringToBytes(x ir.Expr, dst types.Type, dstIR ir.Type) (ir.Expr, []ir.Stmt, error) {
	// Share string backing as a []byte slice header.
	st := typeString
	data := &ir.Instr{Op: ir.OpStructGet, Type: st, Field: "data", Args: []ir.Expr{x}}
	off := &ir.Instr{Op: ir.OpStructGet, Type: st, Field: "off", Args: []ir.Expr{x}}
	len_ := &ir.Instr{Op: ir.OpStructGet, Type: st, Field: "len", Args: []ir.Expr{x}}
	sliceTy := dstIR
	if _, ok := sliceTy.(*ir.Ref); !ok {
		sliceTy = &ir.Ref{Null: true, Named: "$go.slice.i8"}
	}
	return &ir.Instr{
		Op: ir.OpStructNew, Type: sliceTy,
		Args: []ir.Expr{data, off, len_, len_}, // cap = len
	}, nil, nil
}

// intToString lowers string(r) via unicode/utf8.AppendRune then []byte→string.
func (fl *funcLower) intToString(x ir.Expr, src types.Type) (ir.Expr, []ir.Stmt, error) {
	var stmts []ir.Stmt
	// Widen to i32 rune.
	r := x
	if irNumWidth(mustMapBasic(src)) == ir.I64 {
		r = &ir.Instr{Op: ir.OpI32WrapI64, Args: []ir.Expr{x}}
	}
	nilSlice := &ir.Instr{Op: ir.OpRefNull, Type: &ir.Ref{Null: true, Named: "$go.slice.i8"}}
	call := &ir.Instr{
		Op:   ir.OpCall,
		Func: "unicode.utf8.AppendRune",
		Args: []ir.Expr{nilSlice, r},
		Type: &ir.Ref{Null: true, Named: "$go.slice.i8"},
	}
	tmp := fl.newVar("rs", &ir.Ref{Null: true, Named: "$go.slice.i8"})
	stmts = append(stmts, fl.assignVar(tmp, call))
	ex, st, err := fl.bytesToString(fl.useVar(tmp), types.NewSlice(types.Typ[types.Byte]), &ir.Ref{Null: true, Named: "$go.slice.i8"})
	if err != nil {
		return nil, nil, err
	}
	stmts = append(stmts, st...)
	return ex, stmts, nil
}

func mustMapBasic(t types.Type) ir.Type {
	if b, ok := types.Unalias(t).Underlying().(*types.Basic); ok {
		switch b.Kind() {
		case types.Int64, types.Uint64:
			return ir.TypeI64
		}
	}
	return ir.TypeI32
}

// complexTypeShape returns the IR struct name and float element type for a
// Go complex type (or want hint).
func complexTypeShape(t types.Type, want ir.Type) (name string, elem ir.Type) {
	name, elem = "$go.complex128", ir.TypeF64
	if want != nil {
		if r, ok := want.(*ir.Ref); ok && r.Named == "$go.complex64" {
			return "$go.complex64", ir.TypeF32
		}
		if r, ok := want.(*ir.Ref); ok && r.Named == "$go.complex128" {
			return "$go.complex128", ir.TypeF64
		}
	}
	if t != nil {
		if b, ok := types.Unalias(t).Underlying().(*types.Basic); ok && b.Kind() == types.Complex64 {
			return "$go.complex64", ir.TypeF32
		}
	}
	return name, elem
}

func (fl *funcLower) complexUnaryNeg(x ir.Expr, ty types.Type, want ir.Type, stmts []ir.Stmt) (ir.Expr, []ir.Stmt, error) {
	name, elem := complexTypeShape(ty, want)
	fl.ensureComplexType(name, elem)
	neg := ir.OpF64Neg
	if elem == ir.TypeF32 {
		neg = ir.OpF32Neg
	}
	re := &ir.Instr{Op: ir.OpStructGet, Type: &ir.Ref{Named: name}, Field: "real", Args: []ir.Expr{x}}
	im := &ir.Instr{Op: ir.OpStructGet, Type: &ir.Ref{Named: name}, Field: "imag", Args: []ir.Expr{x}}
	return &ir.Instr{
		Op: ir.OpStructNew, Type: &ir.Ref{Null: true, Named: name},
		Args: []ir.Expr{
			&ir.Instr{Op: neg, Args: []ir.Expr{re}},
			&ir.Instr{Op: neg, Args: []ir.Expr{im}},
		},
	}, stmts, nil
}

func (fl *funcLower) complexBinary(e *ast.BinaryExpr, want ir.Type) (ir.Expr, []ir.Stmt, error) {
	xt, yt := fl.typeOf(e.X), fl.typeOf(e.Y)
	et := fl.typeOf(e)
	// Result shape: comparisons → bool; arithmetic → complex of result type.
	name, elem := complexTypeShape(et, want)
	if e.Op == token.EQL || e.Op == token.NEQ {
		// Prefer the wider/typed operand's shape for both sides.
		name, elem = complexTypeShape(xt, want)
		if !isComplex(xt) {
			name, elem = complexTypeShape(yt, want)
		}
		// If one side is complex64 and the other untyped→128, use 64 when
		// the typed operand is 64.
		if isComplex(xt) {
			name, elem = complexTypeShape(xt, nil)
		} else if isComplex(yt) {
			name, elem = complexTypeShape(yt, nil)
		}
	}
	fl.ensureComplexType(name, elem)
	cWant := &ir.Ref{Null: true, Named: name}
	x, xstmts, err := fl.expr(e.X, cWant)
	if err != nil {
		return nil, nil, err
	}
	y, ystmts, err := fl.expr(e.Y, cWant)
	if err != nil {
		return nil, nil, err
	}
	stmts := append(xstmts, ystmts...)

	xre := &ir.Instr{Op: ir.OpStructGet, Type: cWant, Field: "real", Args: []ir.Expr{x}}
	xim := &ir.Instr{Op: ir.OpStructGet, Type: cWant, Field: "imag", Args: []ir.Expr{x}}
	yre := &ir.Instr{Op: ir.OpStructGet, Type: cWant, Field: "real", Args: []ir.Expr{y}}
	yim := &ir.Instr{Op: ir.OpStructGet, Type: cWant, Field: "imag", Args: []ir.Expr{y}}

	add, sub, mul, div, eq := ir.OpF64Add, ir.OpF64Sub, ir.OpF64Mul, ir.OpF64Div, ir.OpF64Eq
	if elem == ir.TypeF32 {
		add, sub, mul, div, eq = ir.OpF32Add, ir.OpF32Sub, ir.OpF32Mul, ir.OpF32Div, ir.OpF32Eq
	}

	switch e.Op {
	case token.EQL, token.NEQ:
		reEq := &ir.Instr{Op: eq, Args: []ir.Expr{xre, yre}}
		imEq := &ir.Instr{Op: eq, Args: []ir.Expr{xim, yim}}
		both := &ir.Instr{Op: ir.OpI32And, Args: []ir.Expr{reEq, imEq}}
		if e.Op == token.NEQ {
			both = &ir.Instr{Op: ir.OpI32Eqz, Args: []ir.Expr{both}}
		}
		return both, stmts, nil
	case token.ADD:
		return &ir.Instr{
			Op: ir.OpStructNew, Type: cWant,
			Args: []ir.Expr{
				&ir.Instr{Op: add, Args: []ir.Expr{xre, yre}},
				&ir.Instr{Op: add, Args: []ir.Expr{xim, yim}},
			},
		}, stmts, nil
	case token.SUB:
		return &ir.Instr{
			Op: ir.OpStructNew, Type: cWant,
			Args: []ir.Expr{
				&ir.Instr{Op: sub, Args: []ir.Expr{xre, yre}},
				&ir.Instr{Op: sub, Args: []ir.Expr{xim, yim}},
			},
		}, stmts, nil
	case token.MUL:
		// (a+bi)(c+di) = (ac-bd) + (ad+bc)i
		ac := &ir.Instr{Op: mul, Args: []ir.Expr{xre, yre}}
		bd := &ir.Instr{Op: mul, Args: []ir.Expr{xim, yim}}
		ad := &ir.Instr{Op: mul, Args: []ir.Expr{xre, yim}}
		bc := &ir.Instr{Op: mul, Args: []ir.Expr{xim, yre}}
		return &ir.Instr{
			Op: ir.OpStructNew, Type: cWant,
			Args: []ir.Expr{
				&ir.Instr{Op: sub, Args: []ir.Expr{ac, bd}},
				&ir.Instr{Op: add, Args: []ir.Expr{ad, bc}},
			},
		}, stmts, nil
	case token.QUO:
		// (a+bi)/(c+di) = ((ac+bd)/(c²+d²)) + ((bc-ad)/(c²+d²))i
		ac := &ir.Instr{Op: mul, Args: []ir.Expr{xre, yre}}
		bd := &ir.Instr{Op: mul, Args: []ir.Expr{xim, yim}}
		bc := &ir.Instr{Op: mul, Args: []ir.Expr{xim, yre}}
		ad := &ir.Instr{Op: mul, Args: []ir.Expr{xre, yim}}
		cc := &ir.Instr{Op: mul, Args: []ir.Expr{yre, yre}}
		dd := &ir.Instr{Op: mul, Args: []ir.Expr{yim, yim}}
		den := &ir.Instr{Op: add, Args: []ir.Expr{cc, dd}}
		reN := &ir.Instr{Op: add, Args: []ir.Expr{ac, bd}}
		imN := &ir.Instr{Op: sub, Args: []ir.Expr{bc, ad}}
		return &ir.Instr{
			Op: ir.OpStructNew, Type: cWant,
			Args: []ir.Expr{
				&ir.Instr{Op: div, Args: []ir.Expr{reN, den}},
				&ir.Instr{Op: div, Args: []ir.Expr{imN, den}},
			},
		}, stmts, nil
	default:
		return nil, nil, fl.errorf(e, "unsupported complex op %v", e.Op)
	}
}

func (fl *funcLower) complexConvert(x ir.Expr, src, dst types.Type, srcIR, dstIR ir.Type) (ir.Expr, []ir.Stmt, error) {
	srcName, srcElem := "$go.complex128", ir.TypeF64
	if b, ok := types.Unalias(src).Underlying().(*types.Basic); ok && b.Kind() == types.Complex64 {
		srcName, srcElem = "$go.complex64", ir.TypeF32
	}
	dstName, dstElem := "$go.complex128", ir.TypeF64
	if b, ok := types.Unalias(dst).Underlying().(*types.Basic); ok && b.Kind() == types.Complex64 {
		dstName, dstElem = "$go.complex64", ir.TypeF32
	}
	fl.ensureComplexType(srcName, srcElem)
	fl.ensureComplexType(dstName, dstElem)
	re := &ir.Instr{Op: ir.OpStructGet, Type: &ir.Ref{Named: srcName}, Field: "real", Args: []ir.Expr{x}}
	im := &ir.Instr{Op: ir.OpStructGet, Type: &ir.Ref{Named: srcName}, Field: "imag", Args: []ir.Expr{x}}
	if srcElem != dstElem {
		var op ir.Op
		if srcElem == ir.TypeF32 {
			op = ir.OpF64PromoteF32
		} else {
			op = ir.OpF32DemoteF64
		}
		re = &ir.Instr{Op: op, Args: []ir.Expr{re}}
		im = &ir.Instr{Op: op, Args: []ir.Expr{im}}
	}
	_ = srcIR
	_ = dstIR
	return &ir.Instr{
		Op: ir.OpStructNew, Type: &ir.Ref{Named: dstName},
		Args: []ir.Expr{re, im},
	}, nil, nil
}

func isUnsafePointer(t types.Type) bool {
	if t == nil {
		return false
	}
	b, ok := types.Unalias(t).Underlying().(*types.Basic)
	return ok && b.Kind() == types.UnsafePointer
}

func isRefIR(t ir.Type) bool {
	_, ok := t.(*ir.Ref)
	return ok
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

// fatPtrCompare lowers *T == / != for fat pointers ($go.ptr.*) as
// base ref.eq ∧ index i32.eq, with both-null → true and either-null → false.
// Field gets run only when both sides are non-null (same shape as semaPtrSame).
func (fl *funcLower) fatPtrCompare(e *ast.BinaryExpr, op token.Token, ptrName string) (ir.Expr, []ir.Stmt, error) {
	ptrTy := &ir.Ref{Named: ptrName, Null: true}
	x, xstmts, err := fl.expr(e.X, ptrTy)
	if err != nil {
		return nil, nil, err
	}
	y, ystmts, err := fl.expr(e.Y, ptrTy)
	if err != nil {
		return nil, nil, err
	}
	stmts := append(xstmts, ystmts...)

	xv := fl.newVar("p", ptrTy)
	yv := fl.newVar("q", ptrTy)
	res := fl.newVar("peq", ir.TypeI32)
	stmts = append(stmts, fl.assignVar(xv, x), fl.assignVar(yv, y))

	eqRef := &ir.Ref{Null: true, Heap: ir.HeapEq}
	fieldsEq := &ir.Instr{Op: ir.OpI32And, Args: []ir.Expr{
		&ir.Instr{Op: ir.OpRefEq, Args: []ir.Expr{
			&ir.Instr{Op: ir.OpRefCast, Type: eqRef, Args: []ir.Expr{
				&ir.Instr{Op: ir.OpStructGet, Type: ptrTy, Field: "base", Args: []ir.Expr{fl.useVar(xv)}},
			}},
			&ir.Instr{Op: ir.OpRefCast, Type: eqRef, Args: []ir.Expr{
				&ir.Instr{Op: ir.OpStructGet, Type: ptrTy, Field: "base", Args: []ir.Expr{fl.useVar(yv)}},
			}},
		}},
		&ir.Instr{Op: ir.OpI32Eq, Args: []ir.Expr{
			&ir.Instr{Op: ir.OpStructGet, Type: ptrTy, Field: "index", Args: []ir.Expr{fl.useVar(xv)}},
			&ir.Instr{Op: ir.OpStructGet, Type: ptrTy, Field: "index", Args: []ir.Expr{fl.useVar(yv)}},
		}},
	}}
	stmts = append(stmts, &ir.If{
		Cond: &ir.Instr{Op: ir.OpRefIsNull, Args: []ir.Expr{fl.useVar(xv)}},
		Then: &ir.Seq{Stmts: []ir.Stmt{fl.assignVar(res, &ir.Instr{Op: ir.OpRefIsNull, Args: []ir.Expr{fl.useVar(yv)}})}},
		Else: &ir.Seq{Stmts: []ir.Stmt{&ir.If{
			Cond: &ir.Instr{Op: ir.OpRefIsNull, Args: []ir.Expr{fl.useVar(yv)}},
			Then: &ir.Seq{Stmts: []ir.Stmt{fl.assignVar(res, fl.i32const(0))}},
			Else: &ir.Seq{Stmts: []ir.Stmt{fl.assignVar(res, fieldsEq)}},
		}}},
	})
	out := fl.useVar(res)
	if op == token.NEQ {
		return &ir.Instr{Op: ir.OpI32Eqz, Args: []ir.Expr{out}}, stmts, nil
	}
	return out, stmts, nil
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
	if ex, st, ok, err := fl.unsafeBuiltinCall(e, nil); ok || err != nil {
		if err != nil {
			return nil, nil, err
		}
		if !asValue {
			stmts = append(st, &ir.ExprStmt{X: &ir.Instr{Op: ir.OpDrop, Args: []ir.Expr{ex}}})
			return nil, stmts, nil
		}
		t := fl.newTemp(ir.TypeI32)
		st = append(st, fl.assignTemp(t, ex))
		return []ir.Type{ir.TypeI32}, st, nil
	}
	if sig := fl.funcValueSig(e.Fun); sig != nil {
		ex, st, err := fl.callFuncValue(e, sig)
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
				// Discard every result (WriteString → (int, error), etc.).
				lhs := make([]ir.Dest, len(results))
				for i, ty := range results {
					t := fl.newTemp(ty)
					lhs[i] = ir.Dest{Temp: t, Name: t.Name}
				}
				st = append(st, &ir.Assign{Lhs: lhs, Rhs: ex})
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
			fn := sel.Obj().(*types.Func)
			sig := fn.Type().(*types.Signature)
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
					stmts = append(stmts, &ir.ExprStmt{X: ex})
				} else {
					lhs := make([]ir.Dest, len(results))
					for i, ty := range results {
						t := fl.newTemp(ty)
						lhs[i] = ir.Dest{Temp: t, Name: t.Name}
					}
					stmts = append(stmts, &ir.Assign{Lhs: lhs, Rhs: ex})
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
	if ex, stmts, ok, err := fl.unsafeBuiltinCall(e, nil); ok || err != nil {
		if err != nil {
			return nil, nil, err
		}
		t := fl.newTemp(ir.TypeI32)
		stmts = append(stmts, fl.assignTemp(t, ex))
		return []*ir.Temp{t}, stmts, nil
	}
	if sig := fl.funcValueSig(e.Fun); sig != nil {
		ex, stmts, err := fl.callFuncValue(e, sig)
		if err != nil {
			return nil, nil, err
		}
		if resultTypes == nil {
			for i := 0; i < sig.Results().Len(); i++ {
				ty, err := fl.mapType(sig.Results().At(i).Type())
				if err != nil {
					return nil, nil, fl.errorf(e, "%v", err)
				}
				resultTypes = append(resultTypes, ty)
			}
		}
		temps := make([]*ir.Temp, len(resultTypes))
		lhs := make([]ir.Dest, len(resultTypes))
		for i, ty := range resultTypes {
			temps[i] = fl.newTemp(ty)
			lhs[i] = ir.Dest{Temp: temps[i], Name: temps[i].Name}
		}
		stmts = append(stmts, &ir.Assign{Lhs: lhs, Rhs: ex})
		return temps, stmts, nil
	}
	if fun, ok := e.Fun.(*ast.SelectorExpr); ok {
		if sel, isIface := fl.ifaceMethod(fun); isIface {
			ex, stmts, err := fl.ifaceCall(e, fun, sel)
			if err != nil {
				return nil, nil, err
			}
			if resultTypes == nil {
				fn := sel.Obj().(*types.Func)
				sig := fn.Type().(*types.Signature)
				for i := 0; i < sig.Results().Len(); i++ {
					ty, err := fl.mapType(sig.Results().At(i).Type())
					if err != nil {
						return nil, nil, fl.errorf(e, "%v", err)
					}
					resultTypes = append(resultTypes, ty)
				}
			}
			temps := make([]*ir.Temp, len(resultTypes))
			lhs := make([]ir.Dest, len(resultTypes))
			for i, ty := range resultTypes {
				temps[i] = fl.newTemp(ty)
				lhs[i] = ir.Dest{Temp: temps[i], Name: temps[i].Name}
			}
			stmts = append(stmts, &ir.Assign{Lhs: lhs, Rhs: ex})
			return temps, stmts, nil
		}
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
	if name, results, ok, err := fl.resolveGenericCallee(e); ok || err != nil {
		return name, results, err
	}
	// Unwrap Max[int](...) / Max[A,B](...) to the function Ident.
	fun := e.Fun
	switch x := fun.(type) {
	case *ast.IndexExpr:
		fun = x.X
	case *ast.IndexListExpr:
		fun = x.X
	}
	switch fun := fun.(type) {
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
		return pkgIRPrefix(pkg) + "." + fn.Name(), results, nil
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
			if _, ok := fl.info.Uses[id].(*types.PkgName); ok {
				selObj := fl.info.Uses[fun.Sel]
				fn, ok := selObj.(*types.Func)
				if !ok {
					return "", nil, fl.errorf(fun, "not a function: %s.%s obj=%T", id.Name, fun.Sel.Name, selObj)
				}
				sig := fn.Type().(*types.Signature)
				for i := 0; i < sig.Results().Len(); i++ {
					ty, err := fl.mapType(sig.Results().At(i).Type())
					if err != nil {
						return "", nil, err
					}
					results = append(results, ty)
				}
				return pkgIRPrefix(fn.Pkg()) + "." + fn.Name(), results, nil
			}
		}
		return "", nil, fl.errorf(fun, "not a method selection")
	}
	fn, ok := sel.Obj().(*types.Func)
	if !ok {
		return "", nil, fl.errorf(fun, "selection is not a method")
	}
	// After mono subst, type-param receivers become concrete while the
	// selection still names the constraint method — call the implementation.
	recvTy := fl.typeOf(fun.X)
	if recvTy != nil {
		if m := concreteMethod(recvTy, fn); m != nil {
			fn = m
		}
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
			if recvTy != nil {
				// Prefer the concrete receiver after subst (Ledger[string,int]).
				if n := namedTypeOf(recvTy); n != nil && n.TypeArgs().Len() > 0 {
					recv = recvTy
				}
			}
			if _, err := fl.pkg.mono.EnsureMethod(origin, recv); err != nil {
				return "", nil, fl.errorf(fun, "%v", err)
			}
		}
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

// ifaceMethod reports whether the call is through an interface receiver
// (direct iface value, or method promoted from an embedded interface field).
func (fl *funcLower) ifaceMethod(fun *ast.SelectorExpr) (*types.Selection, bool) {
	sel := fl.info.Selections[fun]
	if sel == nil || sel.Kind() != types.MethodVal {
		return nil, false
	}
	recvTy := fl.typeOf(fun.X)
	if isIface(recvTy) {
		return sel, true
	}
	if landed := selectionRecvType(recvTy, sel); isIface(landed) {
		return sel, true
	}
	// After mono, Selection.Index is from the type-param view (no embedding),
	// but the concrete type may promote the method from an embedded iface.
	fn, _ := sel.Obj().(*types.Func)
	if fn != nil && recvTy != nil {
		if abs := fn.Type().(*types.Signature).Recv(); abs != nil && isIface(abs.Type()) {
			if concreteMethod(recvTy, fn) == nil {
				return sel, true
			}
		}
	}
	return sel, false
}

func methodIRName(fn *types.Func) string {
	sig := fn.Type().(*types.Signature)
	recv := sig.Recv()
	if recv == nil {
		return pkgIRPrefix(fn.Pkg()) + "." + fn.Name()
	}
	pkg := pkgIRPrefix(fn.Pkg())
	rt := recv.Type()
	ptr := false
	if p, ok := types.Unalias(rt).(*types.Pointer); ok {
		ptr = true
		rt = p.Elem()
	}
	named, _ := types.Unalias(rt).(*types.Named)
	tname := "T"
	if named != nil {
		tname = named.Obj().Name() + mono.NamedIRSuffix(named) + mono.LocalTypeSuffix(named.Obj())
	}
	if ptr {
		return pkg + ".(*" + tname + ")." + fn.Name()
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
			// Promoted methods: walk Selection.Index embedding path so
			// d.previousOffsetEnd passes &d.decodeBuffer, not d.
			rx, rst, err := fl.methodRecvSel(sel.X, s, sig.Recv().Type(), recvWant)
			if err != nil {
				return nil, nil, err
			}
			stmts = append(stmts, rst...)
			recvExpr = rx
		}
	}
	if sig == nil {
		if id := identOf(e.Fun); id != nil {
			if inst, ok := fl.info.Instances[id]; ok {
				if s, ok := inst.Type.(*types.Signature); ok {
					sig = s
				}
			}
			if sig == nil {
				obj := fl.info.Uses[id]
				if fn, ok := obj.(*types.Func); ok {
					sig, _ = fn.Type().(*types.Signature)
				} else if obj != nil {
					// Package / local var of func type.
					sig, _ = obj.Type().Underlying().(*types.Signature)
				}
			}
			// Origin / Instance sig may still mention type params; apply mono subst.
			if sig != nil && len(fl.pkg.subst) > 0 {
				if s, ok := fl.pkg.subst.Apply(sig).(*types.Signature); ok {
					sig = s
				}
			}
		}
	}

	args, astmts, err := fl.lowerArgsWithSig(e, sig)
	if err != nil {
		return nil, nil, err
	}
	stmts = append(stmts, astmts...)
	if recvExpr != nil {
		args = append([]ir.Expr{recvExpr}, args...)
	}
	return args, stmts, nil
}

// lowerArgsWithSig lowers call arguments for a known signature (no method recv).
func (fl *funcLower) lowerArgsWithSig(e *ast.CallExpr, sig *types.Signature) ([]ir.Expr, []ir.Stmt, error) {
	var args []ir.Expr
	var stmts []ir.Stmt

	nParams := 0
	variadic := false
	if sig != nil {
		nParams = sig.Params().Len()
		variadic = sig.Variadic()
	}
	nFixed := nParams
	if variadic {
		nFixed = nParams - 1
	}

	nArgs := len(e.Args)
	if variadic && !e.Ellipsis.IsValid() && nArgs < nFixed {
		return nil, nil, fl.errorf(e, "not enough arguments")
	}

	// f(g()) / fmt.Sprintf(g()) where g returns multiple values that fan out
	// into f's parameters (and, when f is variadic, into the ... slice elems).
	if sig != nil && nArgs == 1 && !e.Ellipsis.IsValid() {
		if ce, ok := e.Args[0].(*ast.CallExpr); ok {
			if tup := fl.callResultTuple(ce); tup != nil && tup.Len() > 1 {
				nRes := tup.Len()
				spreadOK := (!variadic && nRes == nParams) || (variadic && nRes >= nFixed)
				if spreadOK {
					temps, st, err := fl.callMaterialize(ce, nil)
					if err != nil {
						return nil, nil, err
					}
					stmts = append(stmts, st...)
					srcGos := fl.callResultGoTypes(ce)
					coerce := func(i int, dstGo types.Type, want ir.Type) (ir.Expr, error) {
						var ex ir.Expr = fl.useTemp(temps[i])
						var srcGo types.Type
						if i < len(srcGos) {
							srcGo = srcGos[i]
						}
						if dstGo != nil && isIface(dstGo) && srcGo != nil && !isIface(srcGo) {
							ex = fl.boxIRToIface(ex, srcGo, dstGo)
						}
						if want != nil && temps[i].Typ != nil && !ir.TypesEqual(temps[i].Typ, want) {
							nt := fl.newTemp(want)
							stmts = append(stmts, fl.assignTemp(nt, ex))
							ex = fl.useTemp(nt)
						}
						return ex, nil
					}
					for i := 0; i < nFixed && i < len(temps); i++ {
						dstGo := sig.Params().At(i).Type()
						want, err := fl.mapType(dstGo)
						if err != nil {
							return nil, nil, fl.errorf(ce, "%v", err)
						}
						ex, err := coerce(i, dstGo, want)
						if err != nil {
							return nil, nil, err
						}
						args = append(args, ex)
					}
					if variadic {
						sliceGo := sig.Params().At(nFixed).Type()
						sliceIR, err := fl.mapType(sliceGo)
						if err != nil {
							return nil, nil, fl.errorf(e, "%v", err)
						}
						elemGo := sliceGo.Underlying().(*types.Slice).Elem()
						elemIR, err := fl.mapType(elemGo)
						if err != nil {
							return nil, nil, fl.errorf(e, "%v", err)
						}
						var elems []ir.Expr
						for i := nFixed; i < len(temps); i++ {
							ex, err := coerce(i, elemGo, elemIR)
							if err != nil {
								return nil, nil, err
							}
							elems = append(elems, ex)
						}
						if len(elems) == 0 {
							args = append(args, &ir.Instr{Op: ir.OpRefNull, Type: sliceIR})
						} else {
							args = append(args, &ir.Instr{Op: ir.OpLiteral, Type: sliceIR, Args: elems})
						}
					}
					return args, stmts, nil
				}
			}
		}
	}

	limit := nArgs
	if variadic {
		limit = nFixed
		if limit > nArgs {
			limit = nArgs
		}
	}
	for i := 0; i < limit; i++ {
		a := e.Args[i]
		var want ir.Type
		var dstGo types.Type
		if sig != nil && i < nFixed {
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

	if variadic {
		sliceGo := sig.Params().At(nFixed).Type()
		sliceIR, err := fl.mapType(sliceGo)
		if err != nil {
			return nil, nil, fl.errorf(e, "%v", err)
		}
		if e.Ellipsis.IsValid() {
			if nArgs != nFixed+1 {
				return nil, nil, fl.errorf(e, "invalid use of ... with %d args", nArgs)
			}
			a := e.Args[nFixed]
			ex, st, err := fl.coerceExpr(a, sliceGo, sliceIR)
			if err != nil {
				return nil, nil, err
			}
			stmts = append(stmts, st...)
			args = append(args, ex)
		} else {
			elemGo := sliceGo.Underlying().(*types.Slice).Elem()
			elemIR, err := fl.mapType(elemGo)
			if err != nil {
				return nil, nil, fl.errorf(e, "%v", err)
			}
			var elems []ir.Expr
			for i := nFixed; i < nArgs; i++ {
				a := e.Args[i]
				ex, st, err := fl.coerceExpr(a, elemGo, elemIR)
				if err != nil {
					return nil, nil, err
				}
				stmts = append(stmts, st...)
				elems = append(elems, ex)
			}
			if len(elems) == 0 {
				args = append(args, &ir.Instr{Op: ir.OpRefNull, Type: sliceIR})
			} else {
				args = append(args, &ir.Instr{Op: ir.OpLiteral, Type: sliceIR, Args: elems})
			}
		}
	} else {
		for i := limit; i < nArgs; i++ {
			a := e.Args[i]
			var want ir.Type
			var dstGo types.Type
			if t := fl.typeOf(a); t != nil {
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
		// Forming x.M / calling x.M when x is a nil pointer and M has a value
		// receiver must panic (Go spec) — emit panicNil before deref/use so
		// recover can catch it (method5 shouldPanic), not a raw null trap.
		ptrIR, err := fl.mapType(xt)
		if err != nil {
			return nil, nil, err
		}
		ex, stmts, err := fl.expr(x, ptrIR)
		if err != nil {
			return nil, nil, err
		}
		ptrT := fl.newTemp(ptrIR)
		stmts = append(stmts, fl.assignTemp(ptrT, ex))
		stmts = append(stmts, &ir.If{
			Cond: &ir.Instr{Op: ir.OpRefIsNull, Args: []ir.Expr{fl.useTemp(ptrT)}},
			Then: &ir.Seq{Stmts: []ir.Stmt{
				&ir.ExprStmt{X: &ir.Instr{Op: ir.OpCall, Func: "runtime.panicNil"}},
				&ir.Unreachable{},
			}},
		})
		// *NamedStruct / *[N]T share the object ref with T — no OpDeref.
		if isObjectPointer(xt) {
			if want != nil {
				return &ir.Instr{Op: ir.OpRefCast, Type: want, Args: []ir.Expr{fl.useTemp(ptrT)}}, stmts, nil
			}
			return fl.useTemp(ptrT), stmts, nil
		}
		t := fl.newTemp(want)
		stmts = append(stmts, fl.assignTemp(t, &ir.Instr{Op: ir.OpDeref, Args: []ir.Expr{fl.useTemp(ptrT)}, Type: want}))
		return fl.useTemp(t), stmts, nil
	}
	return fl.expr(x, want)
}

// panicIfNilRef appends `if ref.is_null v { runtime.panicNil(); unreachable }`.
func (fl *funcLower) panicIfNilRef(v ir.Expr) ir.Stmt {
	return &ir.If{
		Cond: &ir.Instr{Op: ir.OpRefIsNull, Args: []ir.Expr{v}},
		Then: &ir.Seq{Stmts: []ir.Stmt{
			&ir.ExprStmt{X: &ir.Instr{Op: ir.OpCall, Func: "runtime.panicNil"}},
			&ir.Unreachable{},
		}},
	}
}

// methodRecvSel lowers the receiver for a MethodVal selection, following
// Selection.Index() embedding fields (all but the last, which is the method
// index) so promoted methods receive the embedded field, not the outer value.
func (fl *funcLower) methodRecvSel(x ast.Expr, sel *types.Selection, recvType types.Type, want ir.Type) (ir.Expr, []ir.Stmt, error) {
	idx := sel.Index()
	if len(idx) <= 1 {
		// Method of X itself (no embedding path).
		return fl.methodRecv(x, recvType, want)
	}
	base, stmts, err := fl.expr(x, nil)
	if err != nil {
		return nil, nil, err
	}
	xt := fl.typeOf(x)
	ty := xt
	cur := base
	if p, ok := types.Unalias(ty).(*types.Pointer); ok {
		// nil *T base: forming t.M must panic (method5 shouldPanic on *T3).
		ptrIR, err := fl.mapType(xt)
		if err != nil {
			return nil, nil, err
		}
		ptrT := fl.newTemp(ptrIR)
		stmts = append(stmts, fl.assignTemp(ptrT, cur))
		stmts = append(stmts, fl.panicIfNilRef(fl.useTemp(ptrT)))
		cur = fl.useTemp(ptrT)
		ty = p.Elem()
		// *NamedStruct is already the object ref; no extra deref.
	}
	// Walk embedding fields (Index[:len-1]); last entry is the method index.
	// Keep the Go type of `cur` in curTy (may be *D while ty tracks the
	// deref'd embed for further field walks). Track the last struct.get so
	// pointer methods on scalar embeds can OpAddr the field (a.g on C int).
	curTy := xt
	var lastOwner string
	var lastField string
	var lastParent ir.Expr
	embedPath := idx[:len(idx)-1]
	for ei, fi := range embedPath {
		st := structOf(ty)
		if st == nil || fi < 0 || fi >= st.NumFields() {
			return nil, nil, fl.errorf(x, "method embedding index %d out of range for %s", fi, ty)
		}
		fld := st.Field(fi)
		stName := ""
		if irt, err := fl.mapType(ty); err == nil {
			stName = refTypeName(irt)
		}
		if stName == "" {
			return nil, nil, fl.errorf(x, "no IR type for %s", ty)
		}
		fldIR, err := fl.mapType(fld.Type())
		if err != nil {
			return nil, nil, err
		}
		if irStructHasField(fl.mod, stName, fld.Name()) {
			lastParent = cur
			lastOwner = stName
			lastField = fld.Name()
			cur = &ir.Instr{
				Op:    ir.OpStructGet,
				Type:  &ir.Ref{Named: stName},
				Field: fld.Name(),
				Args:  []ir.Expr{cur},
			}
		} else {
			// Zero-sized embeds omitted from IR — synthesize a zero.
			lastOwner, lastField, lastParent = "", "", nil
			z, zerr := fl.pkg.mexprEmbedZero(fldIR)
			if zerr != nil {
				return nil, nil, fl.errorf(x, "%v", zerr)
			}
			cur = z
		}
		curTy = fld.Type()
		ty = fld.Type()
		if p, ok := types.Unalias(ty).(*types.Pointer); ok {
			fldT := fl.newTemp(fldIR)
			stmts = append(stmts, fl.assignTemp(fldT, cur))
			cur = fl.useTemp(fldT)
			curTy = fld.Type() // still *U
			ty = p.Elem()
			lastOwner, lastField, lastParent = "", "", nil
			// Further embedding through *U requires a live pointer (T1.*T3.T4).
			// Final *U kept as nil is OK for pointer-recv methods (a.i).
			if ei+1 < len(embedPath) {
				stmts = append(stmts, fl.panicIfNilRef(cur))
			}
		}
	}
	// Value method on pointer embed: nil *D must panic before deref (a.h).
	if isPointer(curTy) && !isPointer(recvType) {
		stmts = append(stmts, fl.panicIfNilRef(cur))
	}
	// Adapt landed embed (curTy) to the method's declared receiver (recvType).
	needPtr := isPointer(recvType)
	havePtr := isPointer(curTy)
	switch {
	case needPtr && !havePtr:
		// Pointer method on value embed. Scalars need OpAddr of the field
		// slot; object embeds share the ref.
		if isObjectPointer(types.NewPointer(curTy)) || (want != nil && refTypeName(want) != "" && !strings.HasPrefix(refTypeName(want), "$go.ptr.")) {
			if want != nil {
				if _, ok := want.(*ir.Ref); ok {
					cur = &ir.Instr{Op: ir.OpRefCast, Type: want, Args: []ir.Expr{cur}}
				}
			}
		} else if lastParent != nil && lastField != "" {
			ptrWant := want
			if ptrWant == nil {
				var err error
				ptrWant, err = fl.mapType(recvType)
				if err != nil {
					return nil, nil, err
				}
			}
			cur = &ir.Instr{
				Op:    ir.OpAddr,
				Args:  []ir.Expr{lastParent},
				Field: lastField,
				Str:   lastOwner,
				Type:  ptrWant,
			}
		} else if want != nil {
			if _, ok := want.(*ir.Ref); ok {
				cur = &ir.Instr{Op: ir.OpRefCast, Type: want, Args: []ir.Expr{cur}}
			}
		}
	case !needPtr && havePtr:
		if isObjectPointer(curTy) {
			if want != nil {
				if _, ok := want.(*ir.Ref); ok {
					cur = &ir.Instr{Op: ir.OpRefCast, Type: want, Args: []ir.Expr{cur}}
				}
			}
		} else {
			// Fat-pointer embed → value method: deref to i32/i64/…
			derefWant := want
			if derefWant == nil {
				var err error
				derefWant, err = fl.mapType(recvType)
				if err != nil {
					return nil, nil, err
				}
			}
			t := fl.newTemp(derefWant)
			stmts = append(stmts, fl.assignTemp(t, &ir.Instr{Op: ir.OpDeref, Type: derefWant, Args: []ir.Expr{cur}}))
			cur = fl.useTemp(t)
		}
	default:
		if want != nil {
			if _, ok := want.(*ir.Ref); ok {
				cur = &ir.Instr{Op: ir.OpRefCast, Type: want, Args: []ir.Expr{cur}}
			}
		}
	}
	return cur, stmts, nil
}

func identOf(e ast.Expr) *ast.Ident {
	switch e := e.(type) {
	case *ast.Ident:
		return e
	case *ast.SelectorExpr:
		return e.Sel
	case *ast.IndexExpr:
		// Equal[map[int]int](nil) — Instances is keyed on the function Ident.
		return identOf(e.X)
	case *ast.IndexListExpr:
		return identOf(e.X)
	}
	return nil
}
