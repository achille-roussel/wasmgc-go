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
	}
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
func (fl *funcLower) lowerPrint(e *ast.CallExpr) ([]ir.Stmt, error) {
	nl := fl.printName(e) == "println"
	var out []ir.Stmt
	for i, arg := range e.Args {
		if i > 0 {
			t := fl.newTemp(typeString)
			out = append(out, fl.assignTemp(t, &ir.Instr{Op: ir.OpStringLit, Str: " "}))
			out = append(out, &ir.ExprStmt{
				X: &ir.Instr{Op: ir.OpCall, Func: "runtime.printstring", Args: []ir.Expr{fl.useTemp(t)}},
			})
		}
		stmts, err := fl.printArg(arg)
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

func (fl *funcLower) printArg(arg ast.Expr) ([]ir.Stmt, error) {
	ty := fl.typeOf(arg)
	if ty == nil {
		return nil, fl.errorf(arg, "no type for print arg")
	}
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
			X: &ir.Instr{Op: ir.OpCall, Func: "runtime.printint", Args: []ir.Expr{ex}},
		})
		return stmts, nil
	}
	if isInteger(ty) {
		want, err := fl.mapType(ty)
		if err != nil {
			return nil, err
		}
		ex, stmts, err := fl.expr(arg, want)
		if err != nil {
			return nil, err
		}
		if irNumWidth(want) == ir.I64 {
			ex = &ir.Instr{Op: ir.OpI32WrapI64, Args: []ir.Expr{ex}}
		}
		stmts = append(stmts, &ir.ExprStmt{
			X: &ir.Instr{Op: ir.OpCall, Func: "runtime.printint", Args: []ir.Expr{ex}},
		})
		return stmts, nil
	}
	return nil, fl.errorf(arg, "print of type %s not supported in M3 (need string or int)", ty)
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
	if len(e.Args) != 2 {
		return nil, nil, fl.errorf(e, "copy wants 2 args")
	}
	dstTy := fl.typeOf(e.Args[0])
	srcTy := fl.typeOf(e.Args[1])
	// []T and string→[]byte both lower to slice structs.
	dstIR, err := fl.mapType(dstTy)
	if err != nil {
		return nil, nil, fl.errorf(e.Args[0], "%v", err)
	}
	srcIR, err := fl.mapType(srcTy)
	if err != nil {
		return nil, nil, fl.errorf(e.Args[1], "%v", err)
	}
	dst, stmts, err := fl.expr(e.Args[0], dstIR)
	if err != nil {
		return nil, nil, err
	}
	src, st, err := fl.expr(e.Args[1], srcIR)
	if err != nil {
		return nil, nil, err
	}
	stmts = append(stmts, st...)

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
	stmts = append(stmts,
		fl.assignVar(dstV, dst),
		fl.assignVar(srcV, src),
	)

	// n = min(len(dst), len(src))
	dstLen := &ir.Instr{Op: ir.OpStructGet, Type: dstIR, Field: "len", Args: []ir.Expr{fl.useVar(dstV)}}
	var srcLen ir.Expr
	if r, ok := srcIR.(*ir.Ref); ok && r.Named == "$go.string" {
		srcLen = &ir.Instr{Op: ir.OpStructGet, Type: srcIR, Field: "len", Args: []ir.Expr{fl.useVar(srcV)}}
	} else {
		srcLen = &ir.Instr{Op: ir.OpStructGet, Type: srcIR, Field: "len", Args: []ir.Expr{fl.useVar(srcV)}}
	}
	stmts = append(stmts, fl.assignVar(nV, dstLen))
	stmts = append(stmts, &ir.If{
		Cond: &ir.Instr{Op: ir.OpI32LtU, Args: []ir.Expr{srcLen, fl.useVar(nV)}},
		Then: &ir.Seq{Stmts: []ir.Stmt{fl.assignVar(nV, srcLen)}},
		Else: &ir.Seq{},
	})

	dstData := &ir.Instr{Op: ir.OpStructGet, Type: dstIR, Field: "data", Args: []ir.Expr{fl.useVar(dstV)}}
	dstOff := &ir.Instr{Op: ir.OpStructGet, Type: dstIR, Field: "off", Args: []ir.Expr{fl.useVar(dstV)}}
	srcData := &ir.Instr{Op: ir.OpStructGet, Type: srcIR, Field: "data", Args: []ir.Expr{fl.useVar(srcV)}}
	srcOff := &ir.Instr{Op: ir.OpStructGet, Type: srcIR, Field: "off", Args: []ir.Expr{fl.useVar(srcV)}}

	stmts = append(stmts, &ir.If{
		Cond: &ir.Instr{Op: ir.OpI32Eqz, Args: []ir.Expr{fl.useVar(nV)}},
		Then: &ir.Seq{},
		Else: &ir.Seq{Stmts: []ir.Stmt{&ir.ExprStmt{X: &ir.Instr{
			Op: ir.OpArrayCopy, Type: &ir.Ref{Named: dstArr}, Type2: &ir.Ref{Named: srcArr},
			// slice/string .data is already (ref $go.bytes); ref.as_non_null
			// requires (ref null T) and is invalid on a non-null field get.
			Args: []ir.Expr{
				dstData,
				dstOff,
				srcData,
				srcOff,
				fl.useVar(nV),
			},
		}}}},
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
	unsigned := isUnsigned(resTy)
	width := irNumWidth(irt)
	for _, arg := range e.Args[1:] {
		y, st, err := fl.expr(arg, irt)
		if err != nil {
			return nil, nil, err
		}
		stmts = append(stmts, st...)
		var lt ir.Op
		switch width {
		case ir.I64:
			if unsigned {
				lt = ir.OpI64LtU
			} else {
				lt = ir.OpI64LtS
			}
		case ir.F32:
			lt = ir.OpF32Lt
		case ir.F64:
			lt = ir.OpF64Lt
		default:
			if unsigned {
				lt = ir.OpI32LtU
			} else {
				lt = ir.OpI32LtS
			}
		}
		// min: y < cur → y; max: cur < y → y
		var pickY ir.Expr
		if isMin {
			pickY = &ir.Instr{Op: lt, Args: []ir.Expr{y, cur}}
		} else {
			pickY = &ir.Instr{Op: lt, Args: []ir.Expr{cur, y}}
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

func (fl *funcLower) builtinComplex(e *ast.CallExpr, want ir.Type) (ir.Expr, []ir.Stmt, error) {
	if len(e.Args) != 2 {
		return nil, nil, fl.errorf(e, "complex wants 2 args")
	}
	resTy := fl.typeOf(e)
	name := "$go.complex128"
	elem := ir.TypeF64
	if b, ok := types.Unalias(resTy).(*types.Basic); ok && b.Kind() == types.Complex64 {
		name = "$go.complex64"
		elem = ir.TypeF32
	}
	if want != nil {
		if r, ok := want.(*ir.Ref); ok && r.Named == "$go.complex64" {
			name = "$go.complex64"
			elem = ir.TypeF32
		}
	}
	fl.ensureComplexType(name, elem)
	re, stmts, err := fl.expr(e.Args[0], elem)
	if err != nil {
		return nil, nil, err
	}
	im, stmts2, err := fl.expr(e.Args[1], elem)
	if err != nil {
		return nil, nil, err
	}
	stmts = append(stmts, stmts2...)
	return &ir.Instr{
		Op:   ir.OpStructNew,
		Type: &ir.Ref{Named: name},
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
	x, stmts, err := fl.expr(e.Args[0], &ir.Ref{Named: name})
	if err != nil {
		return nil, nil, err
	}
	_ = want
	return &ir.Instr{
		Op:    ir.OpStructGet,
		Type:  &ir.Ref{Named: name},
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
	arg, stmts, err := fl.expr(e.Args[0], nil)
	if err != nil {
		return nil, nil, err
	}
	_ = want
	return &ir.Instr{Op: op, Args: []ir.Expr{arg}}, stmts, nil
}

func (fl *funcLower) builtinMake(e *ast.CallExpr, want ir.Type) (ir.Expr, []ir.Stmt, error) {
	resTy := fl.typeOf(e)
	irt, err := fl.mapType(resTy)
	if err != nil {
		return nil, nil, fl.errorf(e, "%v", err)
	}
	if want != nil {
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
	var capEx ir.Expr
	if len(e.Args) >= 3 {
		var cstmts []ir.Stmt
		capEx, cstmts, err = fl.expr(e.Args[2], ir.TypeI32)
		if err != nil {
			return nil, nil, err
		}
		stmts = append(stmts, cstmts...)
	} else {
		capEx = lenEx
	}
	return &ir.Instr{Op: ir.OpMakeSlice, Type: irt, Args: []ir.Expr{lenEx, capEx}}, stmts, nil
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
	ty := want
	if ty == nil {
		ty, _ = fl.mapType(fl.typeOf(e))
	}
	slice, stmts, err := fl.expr(e.Args[0], nil)
	if err != nil {
		return nil, nil, err
	}
	args := []ir.Expr{slice}
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
		ex, st, err := fl.expr(a, nil)
		if err != nil {
			return nil, nil, err
		}
		stmts = append(stmts, st...)
		args = append(args, ex)
	}
	return &ir.Instr{Op: ir.OpSliceAppend, Type: ty, Args: args}, stmts, nil
}

// appendSliceSpread lowers append(dst, src...) for a slice by appending one
// element at a time.
func (fl *funcLower) appendSliceSpread(e *ast.CallExpr, srcArg ast.Expr, prefix []ir.Expr, stmts []ir.Stmt, ty ir.Type) (ir.Expr, []ir.Stmt, error) {
	src, st, err := fl.expr(srcArg, nil)
	if err != nil {
		return nil, nil, err
	}
	stmts = append(stmts, st...)
	srcTy, _ := fl.mapType(fl.typeOf(srcArg))
	dstV := fl.newVar("apdst", ty)
	srcV := fl.newVar("apsrc", srcTy)
	iV := fl.newVar("api", ir.TypeI32)
	lenV := fl.newVar("aplen", ir.TypeI32)
	stmts = append(stmts, fl.assignVar(dstV, &ir.Instr{Op: ir.OpSliceAppend, Type: ty, Args: prefix}))
	stmts = append(stmts, fl.assignVar(srcV, src))
	stmts = append(stmts, fl.assignVar(lenV, &ir.Instr{Op: ir.OpLen, Args: []ir.Expr{fl.useVar(srcV)}}))
	stmts = append(stmts, fl.assignVar(iV, fl.i32const(0)))
	elemTy, _ := fl.mapType(fl.typeOf(e).Underlying().(*types.Slice).Elem())
	breakL := fl.freshLabel("break")
	loopL := fl.freshLabel("L")
	body := &ir.Seq{}
	body.Stmts = append(body.Stmts, &ir.Br{
		Label: breakL,
		Cond:  &ir.Instr{Op: ir.OpI32Eqz, Args: []ir.Expr{&ir.Instr{Op: ir.OpI32LtS, Args: []ir.Expr{fl.useVar(iV), fl.useVar(lenV)}}}},
	})
	elemEx := &ir.Instr{Op: ir.OpSliceGet, Args: []ir.Expr{fl.useVar(srcV), fl.useVar(iV)}, Type: elemTy}
	body.Stmts = append(body.Stmts, fl.assignVar(dstV, &ir.Instr{
		Op: ir.OpSliceAppend, Type: ty, Args: []ir.Expr{fl.useVar(dstV), elemEx},
	}))
	body.Stmts = append(body.Stmts, fl.assignVar(iV, &ir.Instr{Op: ir.OpI32Add, Args: []ir.Expr{fl.useVar(iV), fl.i32const(1)}}))
	body.Stmts = append(body.Stmts, &ir.Br{Label: loopL})
	stmts = append(stmts, &ir.Block{Name: breakL, Body: &ir.Seq{Stmts: []ir.Stmt{&ir.Loop{Name: loopL, Body: body}}}})
	return fl.useVar(dstV), stmts, nil
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
