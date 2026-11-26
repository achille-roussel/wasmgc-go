package lower

import (
	"go/ast"
	"go/constant"
	"go/types"
	"strings"

	"github.com/achille-roussel/wasmgc-go/internal/desc"
	"github.com/achille-roussel/wasmgc-go/internal/ir"
)

func (fl *funcLower) isBuiltinPrint(e *ast.CallExpr) bool {
	id, ok := e.Fun.(*ast.Ident)
	if !ok {
		return false
	}
	obj := fl.info.Uses[id]
	if obj == nil {
		return id.Name == "print" || id.Name == "println"
	}
	if b, ok := obj.(*types.Builtin); ok {
		return b.Name() == "print" || b.Name() == "println"
	}
	return false
}

func (fl *funcLower) builtinName(e *ast.CallExpr) string {
	id, ok := e.Fun.(*ast.Ident)
	if !ok {
		return ""
	}
	if obj := fl.info.Uses[id]; obj != nil {
		if b, ok := obj.(*types.Builtin); ok {
			return b.Name()
		}
		// User-defined func/var named print/println/len/… — not a builtin.
		return ""
	}
	// No Uses entry (incomplete type info): fall back to the identifier name.
	return id.Name
}

func (fl *funcLower) isBuiltinCall(e *ast.CallExpr) bool {
	switch fl.builtinName(e) {
	case "make", "new", "append", "len", "cap", "print", "println", "panic", "recover", "close", "delete", "clear",
		"complex", "real", "imag", "min", "max", "copy":
		return true
	}
	return false
}

func (fl *funcLower) printName(e *ast.CallExpr) string {
	return fl.builtinName(e)
}

// lowerPrint emits runtime.printstring / printint / printnl per 04-runtime-and-abi.md.
// println separates args with spaces; print concatenates with no separator.
func (fl *funcLower) lowerPrint(e *ast.CallExpr) ([]ir.Stmt, error) {
	nl := fl.printName(e) == "println"
	var out []ir.Stmt
	for i, arg := range e.Args {
		if i > 0 && nl {
			t := fl.newTemp(typeString)
			out = append(out, fl.assignTemp(t, &ir.Instr{Op: ir.OpStringLit, Str: " "}))
			out = append(out, &ir.ExprStmt{
				X: &ir.Instr{Op: ir.OpCall, Func: "runtime.printstring", Args: []ir.Expr{fl.useTemp(t)}},
			})
		}
		stmts, err := fl.printArg(arg, nl)
		if err != nil {
			return nil, err
		}
		out = append(out, stmts...)
	}
	if nl {
		out = append(out, &ir.ExprStmt{
			X: &ir.Instr{Op: ir.OpCall, Func: "runtime.printnl"},
		})
	}
	return out, nil
}

func (fl *funcLower) printArg(arg ast.Expr, spaceSep bool) ([]ir.Stmt, error) {
	ty := fl.typeOf(arg)
	if ty == nil {
		return nil, fl.errorf(arg, "no type for print arg")
	}
	ty = types.Unalias(ty)
	// print/println(f()) where f returns multiple values — print each result.
	// println separates with spaces; print concatenates (issue35576).
	if tup, ok := ty.(*types.Tuple); ok {
		ce, ok := arg.(*ast.CallExpr)
		if !ok {
			return nil, fl.errorf(arg, "print of type %s not supported", ty)
		}
		temps, st, err := fl.callMaterialize(ce, nil)
		if err != nil {
			return nil, err
		}
		if len(temps) != tup.Len() {
			return nil, fl.errorf(arg, "print: multi-value arity mismatch")
		}
		var out []ir.Stmt
		out = append(out, st...)
		for i, t := range temps {
			if i > 0 && spaceSep {
				sp := fl.newTemp(typeString)
				out = append(out, fl.assignTemp(sp, &ir.Instr{Op: ir.OpStringLit, Str: " "}))
				out = append(out, &ir.ExprStmt{
					X: &ir.Instr{Op: ir.OpCall, Func: "runtime.printstring", Args: []ir.Expr{fl.useTemp(sp)}},
				})
			}
			ps, err := fl.printIRValue(fl.useTemp(t), tup.At(i).Type())
			if err != nil {
				return nil, err
			}
			out = append(out, ps...)
		}
		return out, nil
	}
	return fl.printTypedArg(arg, ty)
}

// printTypedArg evaluates arg and prints it according to Go type ty.
func (fl *funcLower) printTypedArg(arg ast.Expr, ty types.Type) ([]ir.Stmt, error) {
	ty = types.Unalias(ty)
	if isString(ty) {
		ex, stmts, err := fl.expr(arg, typeString)
		if err != nil {
			return nil, err
		}
		t := fl.newTemp(typeString)
		stmts = append(stmts, fl.assignTemp(t, ex))
		stmts = append(stmts, &ir.ExprStmt{
			X: &ir.Instr{Op: ir.OpCall, Func: "runtime.printstring", Args: []ir.Expr{fl.useTemp(t)}},
		})
		return stmts, nil
	}
	if isBoolean(ty) {
		ex, stmts, err := fl.boolExpr(arg)
		if err != nil {
			return nil, err
		}
		stmts = append(stmts, &ir.ExprStmt{
			X: &ir.Instr{Op: ir.OpCall, Func: "runtime.printbool", Args: []ir.Expr{ex}},
		})
		return stmts, nil
	}
	if isIface(ty) {
		ex, stmts, err := fl.expr(arg, typeIface)
		if err != nil {
			return nil, err
		}
		ps, err := fl.printIRValue(ex, ty)
		if err != nil {
			return nil, err
		}
		return append(stmts, ps...), nil
	}
	if isSlice(ty) || isPointer(ty) || isMap(ty) || isChan(ty) || isSignature(ty) ||
		isInteger(ty) || isFloat(ty) || isComplex(ty) {
		want, err := fl.mapType(ty)
		if err != nil {
			return nil, err
		}
		if isComplex(ty) {
			name := "$go.complex128"
			elem := ir.TypeF64
			if b, ok := ty.Underlying().(*types.Basic); ok && b.Kind() == types.Complex64 {
				name = "$go.complex64"
				elem = ir.TypeF32
			}
			fl.ensureComplexType(name, elem)
			want = &ir.Ref{Null: true, Named: name}
		}
		ex, stmts, err := fl.expr(arg, want)
		if err != nil {
			return nil, err
		}
		ps, err := fl.printIRValue(ex, ty)
		if err != nil {
			return nil, err
		}
		return append(stmts, ps...), nil
	}
	return nil, fl.errorf(arg, "print of type %s not supported in M3 (need string or int)", ty)
}

// printIRValue emits runtime.print* for an already-lowered value of Go type ty.
func (fl *funcLower) printIRValue(ex ir.Expr, ty types.Type) ([]ir.Stmt, error) {
	ty = types.Unalias(ty)
	if isString(ty) {
		t := fl.newTemp(typeString)
		return []ir.Stmt{
			fl.assignTemp(t, ex),
			&ir.ExprStmt{X: &ir.Instr{Op: ir.OpCall, Func: "runtime.printstring", Args: []ir.Expr{fl.useTemp(t)}}},
		}, nil
	}
	if isBoolean(ty) {
		return []ir.Stmt{
			&ir.ExprStmt{X: &ir.Instr{Op: ir.OpCall, Func: "runtime.printbool", Args: []ir.Expr{ex}}},
		}, nil
	}
	if isIface(ty) {
		t := fl.newTemp(typeIface)
		isnull := &ir.Instr{Op: ir.OpRefIsNull, Args: []ir.Expr{fl.useTemp(t)}}
		return []ir.Stmt{
			fl.assignTemp(t, ex),
			&ir.ExprStmt{X: &ir.Instr{Op: ir.OpCall, Func: "runtime.printiface", Args: []ir.Expr{isnull}}},
		}, nil
	}
	if isSlice(ty) {
		want, err := fl.mapType(ty)
		if err != nil {
			return nil, err
		}
		t := fl.newTemp(want)
		lenEx := &ir.Instr{Op: ir.OpLen, Type: ir.TypeI32, Args: []ir.Expr{fl.useTemp(t)}}
		capEx := &ir.Instr{Op: ir.OpCap, Type: ir.TypeI32, Args: []ir.Expr{fl.useTemp(t)}}
		isnull := &ir.Instr{Op: ir.OpRefIsNull, Args: []ir.Expr{fl.useTemp(t)}}
		return []ir.Stmt{
			fl.assignTemp(t, ex),
			&ir.ExprStmt{X: &ir.Instr{Op: ir.OpCall, Func: "runtime.printslice", Args: []ir.Expr{lenEx, capEx, isnull}}},
		}, nil
	}
	if isPointer(ty) || isMap(ty) || isChan(ty) || isSignature(ty) {
		want, err := fl.mapType(ty)
		if err != nil {
			return nil, err
		}
		t := fl.newTemp(want)
		isnull := &ir.Instr{Op: ir.OpRefIsNull, Args: []ir.Expr{fl.useTemp(t)}}
		return []ir.Stmt{
			fl.assignTemp(t, ex),
			&ir.ExprStmt{X: &ir.Instr{Op: ir.OpCall, Func: "runtime.printpointer", Args: []ir.Expr{isnull}}},
		}, nil
	}
	if isInteger(ty) {
		want, err := fl.mapType(ty)
		if err != nil {
			return nil, err
		}
		// Unsigned: printuint / printuint64 so ^uint(0) and ^uint64(0) are not
		// sign-printed (issue 21887). Signed i64 uses printint64 — WasmGC int
		// is i32, so wrapping MaxInt64 would print as -1 (issue53600).
		fn := "runtime.printint"
		if isUnsigned(ty) {
			if irNumWidth(want) == ir.I64 {
				fn = "runtime.printuint64"
			} else {
				fn = "runtime.printuint"
			}
		} else if irNumWidth(want) == ir.I64 {
			fn = "runtime.printint64"
		}
		return []ir.Stmt{
			&ir.ExprStmt{X: &ir.Instr{Op: ir.OpCall, Func: fn, Args: []ir.Expr{ex}}},
		}, nil
	}
	if isFloat(ty) {
		want, err := fl.mapType(ty)
		if err != nil {
			return nil, err
		}
		fn := "runtime.printfloat64"
		if irNumWidth(want) == ir.F32 {
			fn = "runtime.printfloat32"
		}
		return []ir.Stmt{
			&ir.ExprStmt{X: &ir.Instr{Op: ir.OpCall, Func: fn, Args: []ir.Expr{ex}}},
		}, nil
	}
	if isComplex(ty) {
		name := "$go.complex128"
		fn := "runtime.printcomplex128"
		elem := ir.TypeF64
		if b, ok := ty.Underlying().(*types.Basic); ok && b.Kind() == types.Complex64 {
			name = "$go.complex64"
			fn = "runtime.printcomplex64"
			elem = ir.TypeF32
		}
		fl.ensureComplexType(name, elem)
		return []ir.Stmt{
			&ir.ExprStmt{X: &ir.Instr{Op: ir.OpCall, Func: fn, Args: []ir.Expr{ex}}},
		}, nil
	}
	return nil, fl.errorf(nil, "print of type %s not supported in M3 (need string or int)", ty)
}

func (fl *funcLower) builtinAsValue(e *ast.CallExpr, want ir.Type) (ir.Expr, []ir.Stmt, error) {
	switch fl.builtinName(e) {
	case "len":
		return fl.builtinLenCap(e, ir.OpLen, want)
	case "cap":
		return fl.builtinLenCap(e, ir.OpCap, want)
	case "make":
		return fl.builtinMake(e, want)
	case "new":
		return fl.builtinNew(e, want)
	case "append":
		return fl.builtinAppend(e, want)
	case "recover":
		return fl.builtinRecover(e)
	case "close":
		st, err := fl.builtinClose(e)
		return nil, st, err
	case "delete":
		st, err := fl.builtinDelete(e)
		return nil, st, err
	case "clear":
		st, err := fl.builtinClear(e)
		return nil, st, err
	case "complex":
		return fl.builtinComplex(e, want)
	case "real":
		return fl.builtinRealImag(e, "real", want)
	case "imag":
		return fl.builtinRealImag(e, "imag", want)
	case "min":
		return fl.builtinMinMax(e, true, want)
	case "max":
		return fl.builtinMinMax(e, false, want)
	case "copy":
		return fl.builtinCopy(e, want)
	case "panic":
		return nil, nil, fl.errorf(e, "panic used as value")
	case "print", "println":
		return nil, nil, fl.errorf(e, "print used as value")
	default:
		return nil, nil, fl.errorf(e, "unknown builtin")
	}
}

func (fl *funcLower) ensureComplexType(name string, elem ir.Type) {
	fl.pkg.mod.InternType(&ir.TypeDef{
		Name: name, Kind: ir.TypeStruct, Super: "$go.object",
		Fields: []ir.Field{
			{Name: "real", Type: elem, Mut: true},
			{Name: "imag", Type: elem, Mut: true},
		},
	})
}

func (fl *funcLower) builtinCopy(e *ast.CallExpr, want ir.Type) (ir.Expr, []ir.Stmt, error) {
	var dstArg, srcArg ast.Expr
	var stmts []ir.Stmt
	switch {
	case len(e.Args) == 2:
		dstArg, srcArg = e.Args[0], e.Args[1]
	case len(e.Args) == 1:
		// copy(f()) where f returns (dst, src).
		ce, ok := e.Args[0].(*ast.CallExpr)
		if !ok {
			return nil, nil, fl.errorf(e, "copy wants 2 args")
		}
		temps, st, err := fl.callMaterialize(ce, nil)
		if err != nil {
			return nil, nil, err
		}
		if len(temps) != 2 {
			return nil, nil, fl.errorf(e, "copy wants 2 args")
		}
		stmts = append(stmts, st...)
		dstIR := temps[0].Typ
		srcIR := temps[1].Typ
		return fl.builtinCopyIR(e, fl.useTemp(temps[0]), fl.useTemp(temps[1]), dstIR, srcIR, stmts, want)
	default:
		return nil, nil, fl.errorf(e, "copy wants 2 args")
	}
	dstTy := fl.typeOf(dstArg)
	srcTy := fl.typeOf(srcArg)
	// []T and string→[]byte both lower to slice structs.
	dstIR, err := fl.mapType(dstTy)
	if err != nil {
		return nil, nil, fl.errorf(dstArg, "%v", err)
	}
	srcIR, err := fl.mapType(srcTy)
	if err != nil {
		return nil, nil, fl.errorf(srcArg, "%v", err)
	}
	dst, st, err := fl.expr(dstArg, dstIR)
	if err != nil {
		return nil, nil, err
	}
	stmts = append(stmts, st...)
	src, st, err := fl.expr(srcArg, srcIR)
	if err != nil {
		return nil, nil, err
	}
	stmts = append(stmts, st...)
	return fl.builtinCopyIR(e, dst, src, dstIR, srcIR, stmts, want)
}

func (fl *funcLower) builtinCopyIR(e *ast.CallExpr, dst, src ir.Expr, dstIR, srcIR ir.Type, stmts []ir.Stmt, want ir.Type) (ir.Expr, []ir.Stmt, error) {

	dstName, dstArr := sliceParts(dstIR)
	srcName, srcArr := sliceParts(srcIR)
	if dstName == "" || srcName == "" {
		// string source: treat as $go.string → copy into []byte
		if r, ok := srcIR.(*ir.Ref); ok && r.Named == "$go.string" {
			srcName, srcArr = "$go.string", "$go.bytes"
		}
		if r, ok := dstIR.(*ir.Ref); ok && strings.HasPrefix(r.Named, "$go.slice.") {
			dstName, dstArr = sliceParts(dstIR)
		}
	}
	if dstArr == "" {
		return nil, nil, fl.errorf(e, "copy: unsupported dst type")
	}
	if srcArr == "" {
		srcArr = dstArr
	}

	dstV := fl.newVar("cdst", dstIR)
	srcV := fl.newVar("csrc", srcIR)
	nV := fl.newVar("cn", ir.TypeI32)
	dstLenV := fl.newVar("cdlen", ir.TypeI32)
	srcLenV := fl.newVar("cslen", ir.TypeI32)
	stmts = append(stmts,
		fl.assignVar(dstV, dst),
		fl.assignVar(srcV, src),
		fl.assignVar(dstLenV, fl.i32const(0)),
		fl.assignVar(srcLenV, fl.i32const(0)),
		fl.assignVar(nV, fl.i32const(0)),
	)

	// len(nil slice) is 0; struct.get on a null slice header traps.
	srcIsString := false
	if r, ok := srcIR.(*ir.Ref); ok && r.Named == "$go.string" {
		srcIsString = true
	}
	if srcIsString {
		stmts = append(stmts, fl.assignVar(srcLenV, &ir.Instr{
			Op: ir.OpStructGet, Type: srcIR, Field: "len", Args: []ir.Expr{fl.useVar(srcV)},
		}))
	} else {
		stmts = append(stmts, &ir.If{
			Cond: &ir.Instr{Op: ir.OpRefIsNull, Args: []ir.Expr{fl.useVar(srcV)}},
			Then: &ir.Seq{},
			Else: &ir.Seq{Stmts: []ir.Stmt{fl.assignVar(srcLenV, &ir.Instr{
				Op: ir.OpStructGet, Type: srcIR, Field: "len", Args: []ir.Expr{fl.useVar(srcV)},
			})}},
		})
	}
	stmts = append(stmts, &ir.If{
		Cond: &ir.Instr{Op: ir.OpRefIsNull, Args: []ir.Expr{fl.useVar(dstV)}},
		Then: &ir.Seq{},
		Else: &ir.Seq{Stmts: []ir.Stmt{fl.assignVar(dstLenV, &ir.Instr{
			Op: ir.OpStructGet, Type: dstIR, Field: "len", Args: []ir.Expr{fl.useVar(dstV)},
		})}},
	})

	// n = min(len(dst), len(src))
	stmts = append(stmts, fl.assignVar(nV, fl.useVar(dstLenV)))
	stmts = append(stmts, &ir.If{
		Cond: &ir.Instr{Op: ir.OpI32LtU, Args: []ir.Expr{fl.useVar(srcLenV), fl.useVar(nV)}},
		Then: &ir.Seq{Stmts: []ir.Stmt{fl.assignVar(nV, fl.useVar(srcLenV))}},
		Else: &ir.Seq{},
	})

	copyBody := &ir.Seq{Stmts: []ir.Stmt{&ir.ExprStmt{X: &ir.Instr{
		Op: ir.OpArrayCopy, Type: &ir.Ref{Named: dstArr}, Type2: &ir.Ref{Named: srcArr},
		// slice/string .data is already (ref $go.bytes); ref.as_non_null
		// requires (ref null T) and is invalid on a non-null field get.
		Args: []ir.Expr{
			&ir.Instr{Op: ir.OpStructGet, Type: dstIR, Field: "data", Args: []ir.Expr{fl.useVar(dstV)}},
			&ir.Instr{Op: ir.OpStructGet, Type: dstIR, Field: "off", Args: []ir.Expr{fl.useVar(dstV)}},
			&ir.Instr{Op: ir.OpStructGet, Type: srcIR, Field: "data", Args: []ir.Expr{fl.useVar(srcV)}},
			&ir.Instr{Op: ir.OpStructGet, Type: srcIR, Field: "off", Args: []ir.Expr{fl.useVar(srcV)}},
			fl.useVar(nV),
		},
	}}}}
	stmts = append(stmts, &ir.If{
		Cond: &ir.Instr{Op: ir.OpI32Eqz, Args: []ir.Expr{fl.useVar(nV)}},
		Then: &ir.Seq{},
		Else: copyBody,
	})
	_ = want
	_ = dstName
	_ = srcName
	return fl.useVar(nV), stmts, nil
}

func (fl *funcLower) builtinMinMax(e *ast.CallExpr, isMin bool, want ir.Type) (ir.Expr, []ir.Stmt, error) {
	if len(e.Args) < 1 {
		return nil, nil, fl.errorf(e, "min/max wants at least 1 arg")
	}
	resTy := fl.typeOf(e)
	irt, err := fl.mapType(resTy)
	if err != nil {
		irt = want
	}
	if want != nil {
		irt = want
	}
	cur, stmts, err := fl.expr(e.Args[0], irt)
	if err != nil {
		return nil, nil, err
	}
	useString := isString(resTy)
	unsigned := isUnsigned(resTy)
	width := irNumWidth(irt)
	for _, arg := range e.Args[1:] {
		y, st, err := fl.expr(arg, irt)
		if err != nil {
			return nil, nil, err
		}
		stmts = append(stmts, st...)
		var pickY ir.Expr
		if useString {
			// Lexicographic compare via runtime.cmpstring → i32 (<0 / 0 / >0).
			cmp := &ir.Instr{Op: ir.OpCall, Func: "runtime.cmpstring", Args: []ir.Expr{cur, y}}
			if isMin {
				// y < cur → cmpstring(cur,y) > 0
				pickY = &ir.Instr{Op: ir.OpI32GtS, Args: []ir.Expr{cmp, fl.i32const(0)}}
			} else {
				// cur < y → cmpstring(cur,y) < 0
				pickY = &ir.Instr{Op: ir.OpI32LtS, Args: []ir.Expr{cmp, fl.i32const(0)}}
			}
		} else if width == ir.F32 || width == ir.F64 {
			// Use Wasm fN.min/fN.max: NaN propagates; −0 < +0 (matches Go).
			op := ir.OpF64Min
			if width == ir.F32 {
				if isMin {
					op = ir.OpF32Min
				} else {
					op = ir.OpF32Max
				}
			} else if !isMin {
				op = ir.OpF64Max
			}
			cur = &ir.Instr{Op: op, Type: irt, Args: []ir.Expr{cur, y}}
			continue
		} else {
			var lt ir.Op
			switch width {
			case ir.I64:
				if unsigned {
					lt = ir.OpI64LtU
				} else {
					lt = ir.OpI64LtS
				}
			default:
				if unsigned {
					lt = ir.OpI32LtU
				} else {
					lt = ir.OpI32LtS
				}
			}
			// min: y < cur → y; max: cur < y → y
			if isMin {
				pickY = &ir.Instr{Op: lt, Args: []ir.Expr{y, cur}}
			} else {
				pickY = &ir.Instr{Op: lt, Args: []ir.Expr{cur, y}}
			}
		}
		// Var (not Temp): assigned in both outer init and Then branch.
		// VarDef comes from lowerFunc preamble via extraLocals.
		sel := fl.newVar("mm", irt)
		stmts = append(stmts, fl.assignVar(sel, cur))
		stmts = append(stmts, &ir.If{
			Cond: pickY,
			Then: &ir.Seq{Stmts: []ir.Stmt{fl.assignVar(sel, y)}},
			Else: &ir.Seq{},
		})
		cur = fl.useVar(sel)
	}
	return cur, stmts, nil
}

func (fl *funcLower) complexResultType(e *ast.CallExpr, want ir.Type) (name string, elem ir.Type) {
	name = "$go.complex128"
	elem = ir.TypeF64
	if resTy := fl.typeOf(e); resTy != nil {
		if b, ok := types.Unalias(resTy).(*types.Basic); ok && b.Kind() == types.Complex64 {
			name = "$go.complex64"
			elem = ir.TypeF32
		}
	}
	if want != nil {
		if r, ok := want.(*ir.Ref); ok && r.Named == "$go.complex64" {
			name = "$go.complex64"
			elem = ir.TypeF32
		}
	}
	return name, elem
}

func (fl *funcLower) builtinComplex(e *ast.CallExpr, want ir.Type) (ir.Expr, []ir.Stmt, error) {
	name, elem := fl.complexResultType(e, want)
	fl.ensureComplexType(name, elem)
	var stmts []ir.Stmt
	var re, im ir.Expr
	switch {
	case len(e.Args) == 2:
		var err error
		var st []ir.Stmt
		re, st, err = fl.expr(e.Args[0], elem)
		if err != nil {
			return nil, nil, err
		}
		stmts = append(stmts, st...)
		im, st, err = fl.expr(e.Args[1], elem)
		if err != nil {
			return nil, nil, err
		}
		stmts = append(stmts, st...)
	case len(e.Args) == 1:
		// complex(f()) where f returns (float, float) — spread multi-value call.
		ce, ok := e.Args[0].(*ast.CallExpr)
		if !ok {
			return nil, nil, fl.errorf(e, "complex wants 2 args")
		}
		temps, st, err := fl.callMaterialize(ce, nil)
		if err != nil {
			return nil, nil, err
		}
		if len(temps) != 2 {
			return nil, nil, fl.errorf(e, "complex wants 2 args")
		}
		stmts = append(stmts, st...)
		re = fl.useTemp(temps[0])
		im = fl.useTemp(temps[1])
		if temps[0].Typ != nil && !ir.TypesEqual(temps[0].Typ, elem) {
			nt := fl.newTemp(elem)
			stmts = append(stmts, fl.assignTemp(nt, re))
			re = fl.useTemp(nt)
		}
		if temps[1].Typ != nil && !ir.TypesEqual(temps[1].Typ, elem) {
			nt := fl.newTemp(elem)
			stmts = append(stmts, fl.assignTemp(nt, im))
			im = fl.useTemp(nt)
		}
	default:
		return nil, nil, fl.errorf(e, "complex wants 2 args")
	}
	return &ir.Instr{
		Op:   ir.OpStructNew,
		Type: &ir.Ref{Null: true, Named: name},
		Args: []ir.Expr{re, im},
	}, stmts, nil
}

func (fl *funcLower) builtinRealImag(e *ast.CallExpr, field string, want ir.Type) (ir.Expr, []ir.Stmt, error) {
	if len(e.Args) != 1 {
		return nil, nil, fl.errorf(e, "%s wants 1 arg", field)
	}
	argTy := fl.typeOf(e.Args[0])
	name := "$go.complex128"
	elem := ir.TypeF64
	if b, ok := types.Unalias(argTy).Underlying().(*types.Basic); ok && b.Kind() == types.Complex64 {
		name = "$go.complex64"
		elem = ir.TypeF32
	}
	fl.ensureComplexType(name, elem)
	x, stmts, err := fl.expr(e.Args[0], &ir.Ref{Null: true, Named: name})
	if err != nil {
		return nil, nil, err
	}
	_ = want
	return &ir.Instr{
		Op:    ir.OpStructGet,
		Type:  &ir.Ref{Null: true, Named: name},
		Field: field,
		Args:  []ir.Expr{x},
	}, stmts, nil
}

func (fl *funcLower) builtinClose(e *ast.CallExpr) ([]ir.Stmt, error) {
	if len(e.Args) != 1 {
		return nil, fl.errorf(e, "close wants 1 arg")
	}
	fl.pkg.ensureChanBaseTypes()
	if fl.pkg.meta != nil {
		fl.pkg.meta.needsSched = true
	}
	ch, stmts, err := fl.expr(e.Args[0], nil)
	if err != nil {
		return nil, err
	}
	stmts = append(stmts, &ir.ExprStmt{X: &ir.Instr{Op: ir.OpChanClose, Args: []ir.Expr{ch}}})
	return stmts, nil
}

func (fl *funcLower) builtinLenCap(e *ast.CallExpr, op ir.Op, want ir.Type) (ir.Expr, []ir.Stmt, error) {
	if len(e.Args) != 1 {
		return nil, nil, fl.errorf(e, "len/cap wants 1 arg")
	}
	argTy := fl.typeOf(e.Args[0])
	// [N]T and *[N]T: len/cap is the constant N. For *[N]T, do not OpLen the
	// (possibly nil) pointer — evaluate the arg for side effects only.
	// len(*p) must not nil-check *p (issue72844); only evaluate p's side effects.
	if n, ok := arrayRangeLen(argTy); ok {
		_, stmts, err := fl.exprArrayLenSideEffects(e.Args[0])
		if err != nil {
			return nil, nil, err
		}
		_ = want
		_ = op
		return fl.i32const(n), stmts, nil
	}
	arg, stmts, err := fl.expr(e.Args[0], nil)
	if err != nil {
		return nil, nil, err
	}
	_ = want
	return &ir.Instr{Op: op, Args: []ir.Expr{arg}}, stmts, nil
}

// exprArrayLenSideEffects evaluates expressions used only for their side
// effects under len/cap of [N]T / *[N]T.
//
// Go rule (issue72844): len(*p)/cap(*p) for a variable p of type *[N]T does
// not evaluate *p (no nil panic). When the operand is not a simple name
// (e.g. len(*f())), * must be evaluated and a nil pointer panics.
func (fl *funcLower) exprArrayLenSideEffects(e ast.Expr) (ir.Expr, []ir.Stmt, error) {
	for {
		if p, ok := e.(*ast.ParenExpr); ok {
			e = p.X
			continue
		}
		break
	}
	if star, ok := e.(*ast.StarExpr); ok {
		if pt := fl.typeOf(star.X); pt != nil && isPtrToArray(pt) {
			if isSimpleName(star.X) {
				return fl.expr(star.X, nil)
			}
			return fl.starExpr(star, nil)
		}
	}
	return fl.expr(e, nil)
}

// isSimpleName reports whether e (parens stripped) is a bare identifier —
// a local, result, or package-level variable name. Used for len/cap/range
// of *p on *[N]T, matching gc's "variable, do not evaluate *" rule.
func isSimpleName(e ast.Expr) bool {
	for {
		if p, ok := e.(*ast.ParenExpr); ok {
			e = p.X
			continue
		}
		break
	}
	_, ok := e.(*ast.Ident)
	return ok
}

func (fl *funcLower) builtinMake(e *ast.CallExpr, want ir.Type) (ir.Expr, []ir.Stmt, error) {
	resTy := fl.typeOf(e)
	irt, err := fl.mapType(resTy)
	if err != nil {
		return nil, nil, fl.errorf(e, "%v", err)
	}
	// Prefer make's result type over want: blank `_` reports i32 via lhsType
	// and would corrupt OpMakeSlice/Map/Chan typing (makeslice.go, issue37975).
	if want != nil && isRefIR(want) {
		irt = want
	}
	if isChan(resTy) {
		fl.pkg.ensureChanBaseTypes()
		if fl.pkg.meta != nil {
			fl.pkg.meta.needsSched = true
		}
		var capEx ir.Expr = fl.i32const(0)
		var stmts []ir.Stmt
		if len(e.Args) >= 2 {
			var cstmts []ir.Stmt
			capEx, cstmts, err = fl.expr(e.Args[1], ir.TypeI32)
			if err != nil {
				return nil, nil, err
			}
			stmts = append(stmts, cstmts...)
			var pstmts []ir.Stmt
			capEx, pstmts = fl.ensureI32LenChecked(e.Args[1], capEx, "chan")
			stmts = append(stmts, pstmts...)
		}
		return &ir.Instr{Op: ir.OpChanMake, Type: irt, Args: []ir.Expr{capEx}}, stmts, nil
	}
	if isMap(resTy) {
		fl.markNeedsMaps()
		var hint ir.Expr = fl.i32const(0)
		var stmts []ir.Stmt
		if len(e.Args) >= 2 {
			var hstmts []ir.Stmt
			hint, hstmts, err = fl.expr(e.Args[1], ir.TypeI32)
			if err != nil {
				return nil, nil, err
			}
			stmts = append(stmts, hstmts...)
			hint = fl.ensureI32Len(e.Args[1], hint)
		}
		in := &ir.Instr{Op: ir.OpMapMake, Type: irt, Args: []ir.Expr{hint}}
		if key, _ := mapKeyElem(resTy); key != nil {
			in.Str = desc.TypeKey(key)
		}
		return in, stmts, nil
	}
	if len(e.Args) < 2 {
		return nil, nil, fl.errorf(e, "make needs type and length")
	}
	if !isSlice(resTy) {
		return nil, nil, fl.errorf(e, "make of non-slice/chan/map not supported")
	}
	lenEx, stmts, err := fl.expr(e.Args[1], ir.TypeI32)
	if err != nil {
		return nil, nil, err
	}
	var pstmts []ir.Stmt
	lenEx, pstmts = fl.ensureI32LenChecked(e.Args[1], lenEx, "len")
	stmts = append(stmts, pstmts...)
	var capEx ir.Expr
	if len(e.Args) >= 3 {
		var cstmts []ir.Stmt
		capEx, cstmts, err = fl.expr(e.Args[2], ir.TypeI32)
		if err != nil {
			return nil, nil, err
		}
		stmts = append(stmts, cstmts...)
		capEx, pstmts = fl.ensureI32LenChecked(e.Args[2], capEx, "cap")
		stmts = append(stmts, pstmts...)
	} else {
		capEx = lenEx
	}
	return &ir.Instr{Op: ir.OpMakeSlice, Type: irt, Args: []ir.Expr{lenEx, capEx}}, stmts, nil
}

// ensureI32Len wraps int64/uint64 make lengths down to i32 (WasmGC int).
// Prefer ensureI32LenChecked when a truncated wide value must panic (make).
func (fl *funcLower) ensureI32Len(arg ast.Expr, ex ir.Expr) ir.Expr {
	if t := fl.typeOf(arg); t != nil {
		if irt, err := fl.mapType(t); err == nil && irNumWidth(irt) == ir.I64 {
			return &ir.Instr{Op: ir.OpI32WrapI64, Args: []ir.Expr{ex}}
		}
	}
	return ex
}

// ensureI32LenChecked wraps i64 lengths to i32 after panicking when the value
// is negative or does not fit in int32 (bug273 biglen/bigcap/bigchancap).
// kind is "len", "cap", or "chan" and selects the panic wording.
func (fl *funcLower) ensureI32LenChecked(arg ast.Expr, ex ir.Expr, kind string) (ir.Expr, []ir.Stmt) {
	t := fl.typeOf(arg)
	if t == nil {
		return ex, nil
	}
	irt, err := fl.mapType(t)
	if err != nil || irNumWidth(irt) != ir.I64 {
		return ex, nil
	}
	unsigned := isUnsigned(t)
	var oob ir.Expr
	if unsigned {
		oob = &ir.Instr{Op: ir.OpI64GtU, Args: []ir.Expr{ex, fl.i64const(0x7fffffff)}}
	} else {
		oob = &ir.Instr{Op: ir.OpI32Or, Args: []ir.Expr{
			&ir.Instr{Op: ir.OpI64LtS, Args: []ir.Expr{ex, fl.i64const(0)}},
			&ir.Instr{Op: ir.OpI64GtS, Args: []ir.Expr{ex, fl.i64const(0x7fffffff)}},
		}}
	}
	wrapped := &ir.Instr{Op: ir.OpI32WrapI64, Args: []ir.Expr{ex}}
	panicFn := "runtime.panicmakeslicelen"
	switch kind {
	case "cap":
		panicFn = "runtime.panicmakeslicecap"
	case "chan":
		panicFn = "runtime.panicmakechansize"
	}
	panicStmts := []ir.Stmt{
		&ir.ExprStmt{X: &ir.Instr{Op: ir.OpCall, Func: panicFn}},
		&ir.Unreachable{},
	}
	return wrapped, []ir.Stmt{&ir.If{Cond: oob, Then: &ir.Seq{Stmts: panicStmts}}}
}

func (fl *funcLower) builtinNew(e *ast.CallExpr, want ir.Type) (ir.Expr, []ir.Stmt, error) {
	if len(e.Args) != 1 {
		return nil, nil, fl.errorf(e, "new wants 1 type arg")
	}
	resTy := fl.typeOf(e)
	irt, err := fl.mapType(resTy)
	if err != nil {
		return nil, nil, fl.errorf(e, "%v", err)
	}
	if want != nil {
		irt = want
	}
	// literal … Str:"new" → RuntimeOps allocates (1-elem array + fat ptr, or struct).
	in := &ir.Instr{Op: ir.OpLiteral, Type: irt, Str: "new"}
	// *[N]T maps to $go.array.*; record N so expandNew can array.new_default N.
	if p, ok := types.Unalias(resTy).(*types.Pointer); ok {
		if a, ok := types.Unalias(p.Elem()).Underlying().(*types.Array); ok {
			in.I32 = int32(a.Len())
		}
	}
	return in, nil, nil
}

func (fl *funcLower) builtinAppend(e *ast.CallExpr, want ir.Type) (ir.Expr, []ir.Stmt, error) {
	if len(e.Args) < 1 {
		return nil, nil, fl.errorf(e, "append needs a slice")
	}
	// append(f()) where f returns (slice, elems...) — spread multi-value call.
	if len(e.Args) == 1 && !e.Ellipsis.IsValid() {
		if ce, ok := e.Args[0].(*ast.CallExpr); ok {
			if tup := fl.callResultTuple(ce); tup != nil && tup.Len() >= 2 {
				temps, st, err := fl.callMaterialize(ce, nil)
				if err != nil {
					return nil, nil, err
				}
				ty, _ := fl.mapType(fl.typeOf(e))
				if want != nil && isRefIR(want) {
					ty = want
				}
				if ty == nil {
					return nil, nil, fl.errorf(e, "append: unknown slice type")
				}
				args := make([]ir.Expr, 0, len(temps))
				for _, t := range temps {
					args = append(args, fl.useTemp(t))
				}
				return &ir.Instr{Op: ir.OpSliceAppend, Type: ty, Args: args}, st, nil
			}
		}
	}
	ty, _ := fl.mapType(fl.typeOf(e))
	// Prefer append's result type over want: blank `_` reports i32 via lhsType
	// and would corrupt OpSliceAppend typing (makeslice.go append(make...)).
	if want != nil && isRefIR(want) {
		ty = want
	}
	slice, stmts, err := fl.expr(e.Args[0], nil)
	if err != nil {
		return nil, nil, err
	}
	args := []ir.Expr{slice}
	var elemGo types.Type
	var elemIR ir.Type
	if st, ok := types.Unalias(fl.typeOf(e)).Underlying().(*types.Slice); ok {
		elemGo = st.Elem()
		elemIR, _ = fl.mapType(elemGo)
	}
	for i, a := range e.Args[1:] {
		isEllipsis := e.Ellipsis.IsValid() && i == len(e.Args)-2
		if isEllipsis {
			at := fl.typeOf(a)
			if isString(at) {
				if tv, ok := fl.info.Types[a]; ok && tv.Value != nil && tv.Value.Kind() == constant.String {
					s := constant.StringVal(tv.Value)
					for j := 0; j < len(s); j++ {
						args = append(args, fl.i32const(int64(s[j])))
					}
					continue
				}
				return fl.appendStringSpread(e, a, args, stmts, ty)
			}
			if isSlice(at) {
				// append(dst, src...): copy elements from src slice.
				return fl.appendSliceSpread(e, a, args, stmts, ty)
			}
		}
		ex, st, err := fl.coerceExpr(a, elemGo, elemIR)
		if err != nil {
			return nil, nil, err
		}
		stmts = append(stmts, st...)
		args = append(args, ex)
	}
	return &ir.Instr{Op: ir.OpSliceAppend, Type: ty, Args: args}, stmts, nil
}

// appendSliceSpread lowers append(dst, src...).
// Element-wise append that grows mid-loop clobbers shared capacity
// (append(s[:i], t...) writes t[0] into s[i] before realloc). When need >
// cap, grow once via make+copy; when it fits, element-wise append is OK and
// preserves backing (Delete clear-tail).
func (fl *funcLower) appendSliceSpread(e *ast.CallExpr, srcArg ast.Expr, prefix []ir.Expr, stmts []ir.Stmt, ty ir.Type) (ir.Expr, []ir.Stmt, error) {
	src, st, err := fl.expr(srcArg, nil)
	if err != nil {
		return nil, nil, err
	}
	stmts = append(stmts, st...)
	srcTy, _ := fl.mapType(fl.typeOf(srcArg))
	dstV := fl.newVar("apdst", ty)
	srcV := fl.newVar("apsrc", srcTy)
	srcLenV := fl.newVar("apslen", ir.TypeI32)
	dstLenV := fl.newVar("apdlen", ir.TypeI32)
	dstCapV := fl.newVar("apdcap", ir.TypeI32)
	needV := fl.newVar("apneed", ir.TypeI32)
	ncapV := fl.newVar("apncap", ir.TypeI32)
	twocapV := fl.newVar("aptwocap", ir.TypeI32)
	outV := fl.newVar("apout", ty)
	tailV := fl.newVar("aptail", ty)
	stmts = append(stmts,
		fl.assignVar(dstV, &ir.Instr{Op: ir.OpSliceAppend, Type: ty, Args: prefix}),
		fl.assignVar(srcV, src),
		fl.assignVar(srcLenV, &ir.Instr{Op: ir.OpLen, Args: []ir.Expr{fl.useVar(srcV)}}),
		fl.assignVar(dstLenV, &ir.Instr{Op: ir.OpLen, Args: []ir.Expr{fl.useVar(dstV)}}),
		fl.assignVar(dstCapV, &ir.Instr{Op: ir.OpCap, Args: []ir.Expr{fl.useVar(dstV)}}),
		fl.assignVar(needV, &ir.Instr{Op: ir.OpI32Add, Args: []ir.Expr{fl.useVar(dstLenV), fl.useVar(srcLenV)}}),
		fl.assignVar(outV, fl.useVar(dstV)),
	)

	growBody := &ir.Seq{Stmts: []ir.Stmt{
		fl.assignVar(twocapV, &ir.Instr{Op: ir.OpI32Mul, Args: []ir.Expr{fl.useVar(dstCapV), fl.i32const(2)}}),
		fl.assignVar(ncapV, fl.useVar(needV)),
		&ir.If{
			Cond: &ir.Instr{Op: ir.OpI32GtU, Args: []ir.Expr{fl.useVar(twocapV), fl.useVar(ncapV)}},
			Then: &ir.Seq{Stmts: []ir.Stmt{fl.assignVar(ncapV, fl.useVar(twocapV))}},
		},
		fl.assignVar(outV, &ir.Instr{Op: ir.OpMakeSlice, Type: ty, Args: []ir.Expr{fl.useVar(needV), fl.useVar(ncapV)}}),
	}}
	_, copyDst, err := fl.builtinCopyIR(e, fl.useVar(outV), fl.useVar(dstV), ty, ty, nil, ir.TypeI32)
	if err != nil {
		return nil, nil, err
	}
	growBody.Stmts = append(growBody.Stmts, copyDst...)
	growBody.Stmts = append(growBody.Stmts, fl.assignVar(tailV, &ir.Instr{Op: ir.OpSliceSlice, Type: ty, Args: []ir.Expr{
		fl.useVar(outV), fl.useVar(dstLenV), fl.useVar(needV),
	}}))
	_, copySrc, err := fl.builtinCopyIR(e, fl.useVar(tailV), fl.useVar(srcV), ty, srcTy, nil, ir.TypeI32)
	if err != nil {
		return nil, nil, err
	}
	growBody.Stmts = append(growBody.Stmts, copySrc...)

	// Fit path: extend len then copy (array.copy is overlap-safe). Element-wise
	// append would clobber a src that aliases dst's unused capacity
	// (InsertOverlap: append(s, s[x:y]...)).
	fitBody := &ir.Seq{Stmts: []ir.Stmt{
		fl.assignVar(outV, &ir.Instr{Op: ir.OpSliceSlice, Type: ty, Args: []ir.Expr{
			fl.useVar(dstV), fl.i32const(0), fl.useVar(needV),
		}}),
		fl.assignVar(tailV, &ir.Instr{Op: ir.OpSliceSlice, Type: ty, Args: []ir.Expr{
			fl.useVar(outV), fl.useVar(dstLenV), fl.useVar(needV),
		}}),
	}}
	_, copyFit, err := fl.builtinCopyIR(e, fl.useVar(tailV), fl.useVar(srcV), ty, srcTy, nil, ir.TypeI32)
	if err != nil {
		return nil, nil, err
	}
	fitBody.Stmts = append(fitBody.Stmts, copyFit...)

	copyBody := &ir.Seq{Stmts: []ir.Stmt{
		&ir.If{
			Cond: &ir.Instr{Op: ir.OpI32LtU, Args: []ir.Expr{fl.useVar(dstCapV), fl.useVar(needV)}},
			Then: growBody,
			Else: fitBody,
		},
	}}
	// append(nil, empty...) must stay nil; SliceSlice(nil,0,0) yields non-nil empty.
	stmts = append(stmts, &ir.If{
		Cond: &ir.Instr{Op: ir.OpI32Eq, Args: []ir.Expr{fl.useVar(srcLenV), fl.i32const(0)}},
		Then: &ir.Seq{}, // outV already dst
		Else: copyBody,
	})
	return fl.useVar(outV), stmts, nil
}

// appendStringSpread lowers append(dst, s...) for a non-constant string by
// appending one byte at a time (fmt/strconv float formatting).
func (fl *funcLower) appendStringSpread(e *ast.CallExpr, strArg ast.Expr, prefix []ir.Expr, stmts []ir.Stmt, ty ir.Type) (ir.Expr, []ir.Stmt, error) {
	str, st, err := fl.expr(strArg, typeString)
	if err != nil {
		return nil, nil, err
	}
	stmts = append(stmts, st...)
	dstV := fl.newVar("apdst", ty)
	strV := fl.newVar("apstr", typeString)
	iV := fl.newVar("api", ir.TypeI32)
	lenV := fl.newVar("aplen", ir.TypeI32)
	stmts = append(stmts, fl.assignVar(dstV, &ir.Instr{Op: ir.OpSliceAppend, Type: ty, Args: prefix}))
	stmts = append(stmts, fl.assignVar(strV, str))
	stmts = append(stmts, fl.assignVar(lenV, &ir.Instr{Op: ir.OpLen, Args: []ir.Expr{fl.useVar(strV)}}))
	stmts = append(stmts, fl.assignVar(iV, fl.i32const(0)))
	breakL := fl.freshLabel("break")
	loopL := fl.freshLabel("L")
	body := &ir.Seq{}
	body.Stmts = append(body.Stmts, &ir.Br{
		Label: breakL,
		Cond:  &ir.Instr{Op: ir.OpI32Eqz, Args: []ir.Expr{&ir.Instr{Op: ir.OpI32LtS, Args: []ir.Expr{fl.useVar(iV), fl.useVar(lenV)}}}},
	})
	byteEx := &ir.Instr{Op: ir.OpStringGet, Args: []ir.Expr{fl.useVar(strV), fl.useVar(iV)}}
	body.Stmts = append(body.Stmts, fl.assignVar(dstV, &ir.Instr{
		Op: ir.OpSliceAppend, Type: ty, Args: []ir.Expr{fl.useVar(dstV), byteEx},
	}))
	body.Stmts = append(body.Stmts, fl.assignVar(iV, &ir.Instr{Op: ir.OpI32Add, Args: []ir.Expr{fl.useVar(iV), fl.i32const(1)}}))
	body.Stmts = append(body.Stmts, &ir.Br{Label: loopL})
	stmts = append(stmts, &ir.Block{Name: breakL, Body: &ir.Seq{Stmts: []ir.Stmt{&ir.Loop{Name: loopL, Body: body}}}})
	return fl.useVar(dstV), stmts, nil
}
