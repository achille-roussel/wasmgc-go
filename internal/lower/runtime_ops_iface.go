package lower

import (
	"fmt"
	"strconv"
	"strings"

	"github.com/achille-roussel/wasmgc-go/internal/ir"
)

func (rt *runtimeLower) expandIfaceMake(s *ir.Assign, in *ir.Instr, n *int) ([]ir.Stmt, error) {
	var pre []ir.Stmt
	val, p, err := rt.rewriteExpr(in.Args[0], n)
	if err != nil {
		return nil, err
	}
	pre = append(pre, p...)

	itabG := in.Global
	if itabG == "" {
		return nil, fmt.Errorf("iface.make: missing itab global")
	}
	box := in.Field
	if box == "" {
		box = "ref"
	}

	itabT := rt.newVar("itab", &ir.Ref{Named: "$go.itab"})
	dataT := rt.newVar("data", &ir.Ref{Null: true, Heap: ir.HeapAny})
	pre = append(pre, &ir.VarDef{Var: itabT}, &ir.VarDef{Var: dataT})

	// global.get may be a subtype of $go.itab; cast up for iface field.
	pre = append(pre, assignV(itabT, &ir.Instr{
		Op:   ir.OpRefCast,
		Type: &ir.Ref{Named: "$go.itab"},
		Args: []ir.Expr{&ir.Instr{Op: ir.OpGlobalGet, Global: itabG}},
	}))

	switch box {
	case "nil":
		pre = append(pre, assignV(dataT, &ir.Instr{Op: ir.OpRefNull, Type: &ir.Ref{Null: true, Heap: ir.HeapAny}}))
	case "i32":
		pre = append(pre, assignV(dataT, &ir.Instr{
			Op:   ir.OpStructNew,
			Type: &ir.Ref{Named: "$go.box.i32"},
			Args: []ir.Expr{val},
		}))
	case "i64":
		pre = append(pre, assignV(dataT, &ir.Instr{
			Op:   ir.OpStructNew,
			Type: &ir.Ref{Named: "$go.box.i64"},
			Args: []ir.Expr{val},
		}))
	case "f32":
		pre = append(pre, assignV(dataT, &ir.Instr{
			Op:   ir.OpStructNew,
			Type: &ir.Ref{Named: "$go.box.f32"},
			Args: []ir.Expr{val},
		}))
	case "f64":
		pre = append(pre, assignV(dataT, &ir.Instr{
			Op:   ir.OpStructNew,
			Type: &ir.Ref{Named: "$go.box.f64"},
			Args: []ir.Expr{val},
		}))
	default: // ref — already a reference; bitcast to any
		pre = append(pre, assignV(dataT, &ir.Instr{
			Op:   ir.OpRefCast,
			Type: &ir.Ref{Null: true, Heap: ir.HeapAny},
			Args: []ir.Expr{val},
		}))
	}

	pre = append(pre, &ir.Assign{
		Lhs: s.Lhs,
		Rhs: &ir.Instr{
			Op:   ir.OpStructNew,
			Type: typeIface,
			Args: []ir.Expr{useV(itabT), useV(dataT)},
		},
	})
	return pre, nil
}

func (rt *runtimeLower) expandIfaceCall(s *ir.Assign, in *ir.Instr, n *int) ([]ir.Stmt, error) {
	if len(in.Args) < 1 {
		return nil, fmt.Errorf("iface.call: no receiver")
	}
	var pre []ir.Stmt
	recv, p, err := rt.rewriteExpr(in.Args[0], n)
	if err != nil {
		return nil, err
	}
	pre = append(pre, p...)
	var args []ir.Expr
	for _, a := range in.Args[1:] {
		ex, p, err := rt.rewriteExpr(a, n)
		if err != nil {
			return nil, err
		}
		pre = append(pre, p...)
		args = append(args, ex)
	}

	ifaceTy := typeIface
	itabTy := in.Type2
	if itabTy == nil {
		itabTy = &ir.Ref{Named: "$go.itab"}
	}
	methodIdx := 0
	if in.Field != "" {
		methodIdx, _ = strconv.Atoi(in.Field)
	}
	// itab field 0 is type; methods start at 1
	fieldName := fmt.Sprintf("m%d", methodIdx)
	sigName := in.Str
	if sigName == "" {
		return nil, fmt.Errorf("iface.call: missing funcref type")
	}

	ifaceV := rt.newVar("ifc", ifaceTy)
	itabV := rt.newVar("itb", itabTy)
	dataV := rt.newVar("dat", &ir.Ref{Null: true, Heap: ir.HeapAny})
	fnV := rt.newVar("fn", &ir.Ref{Named: sigName})
	for _, v := range []*ir.Var{ifaceV, itabV, dataV, fnV} {
		pre = append(pre, &ir.VarDef{Var: v})
	}
	pre = append(pre, assignV(ifaceV, recv))
	pre = append(pre, assignV(itabV, &ir.Instr{
		Op:   ir.OpRefCast,
		Type: itabTy,
		Args: []ir.Expr{&ir.Instr{Op: ir.OpStructGet, Type: ifaceTy, Field: "itab", Args: []ir.Expr{useV(ifaceV)}}},
	}))
	pre = append(pre, assignV(dataV, &ir.Instr{
		Op: ir.OpStructGet, Type: ifaceTy, Field: "data", Args: []ir.Expr{useV(ifaceV)},
	}))
	pre = append(pre, assignV(fnV, &ir.Instr{
		Op: ir.OpStructGet, Type: itabTy, Field: fieldName, Args: []ir.Expr{useV(itabV)},
	}))

	callArgs := []ir.Expr{useV(dataV)}
	callArgs = append(callArgs, args...)
	callArgs = append(callArgs, useV(fnV))
	call := &ir.Instr{
		Op:   ir.OpCallRef,
		Type: &ir.Ref{Named: sigName},
		Args: callArgs,
	}
	if s != nil && len(s.Lhs) > 0 {
		// Ensure LHS temps carry a real result type (codegen defaults nil → i32).
		var resTy ir.Type = in.Type
		if td := rt.mod.LookupType(sigName); td != nil && len(td.Results) > 0 {
			resTy = td.Results[0]
		}
		if resTy == nil {
			resTy = &ir.Ref{Named: "$go.string"} // last-resort; better than i32
		}
		lhs := make([]ir.Dest, len(s.Lhs))
		for i := range s.Lhs {
			lhs[i] = s.Lhs[i]
			if lhs[i].Temp != nil {
				nt := &ir.Temp{Name: lhs[i].Temp.Name, Typ: resTy}
				lhs[i].Temp = nt
				lhs[i].Name = nt.Name
			}
			if lhs[i].Var != nil {
				lhs[i].Var.Typ = resTy
			}
		}
		pre = append(pre, &ir.Assign{Lhs: lhs, Rhs: call})
	} else {
		pre = append(pre, &ir.ExprStmt{X: call})
	}
	return pre, nil
}

func (rt *runtimeLower) expandIfaceAssert(s *ir.Assign, in *ir.Instr, n *int) ([]ir.Stmt, error) {
	var pre []ir.Stmt
	recv, p, err := rt.rewriteExpr(in.Args[0], n)
	if err != nil {
		return nil, err
	}
	pre = append(pre, p...)

	str := in.Str
	typeKey := in.Global
	typeGlobal := "$go.type." + strings.TrimPrefix(typeKey, "$go.type.")

	ifaceTy := typeIface
	ifaceV := rt.newVar("ia", ifaceTy)
	pre = append(pre, &ir.VarDef{Var: ifaceV}, assignV(ifaceV, recv))

	if str == "test" {
		// Returns i32 ok only.
		ok := rt.assertTest(ifaceV, typeGlobal)
		pre = append(pre, ok.pre...)
		pre = append(pre, &ir.Assign{Lhs: s.Lhs, Rhs: ok.ok})
		return pre, nil
	}

	commaOK := strings.Contains(str, "ok") || in.I32 == 1
	toIface := strings.HasPrefix(str, "iface")

	if toIface {
		// Interface → interface: for M4, require exact type match via concrete
		// descriptor when asserting to a concrete-shaped iface key, else trap/false.
		// Simplified: treat as concrete assert of the dynamic type implementing J
		// by checking descriptor identity against target iface descriptor — wrong for
		// iface-to-iface. Proper path uses itable vector. Until then, copy on success
		// of "same dynamic type implements" is approximated as always-fail for
		// non-identical, and pass-through when keys match empty/any.
		if commaOK && len(s.Lhs) == 2 {
			pre = append(pre,
				&ir.Assign{Lhs: []ir.Dest{s.Lhs[0]}, Rhs: useV(ifaceV)},
				&ir.Assign{Lhs: []ir.Dest{s.Lhs[1]}, Rhs: &ir.Const{Op: ir.OpI32Const, I64: 1}},
			)
			return pre, nil
		}
		pre = append(pre, &ir.Assign{Lhs: s.Lhs, Rhs: useV(ifaceV)})
		return pre, nil
	}

	// Concrete assert: compare descriptor refs.
	itabV := rt.newVar("it", &ir.Ref{Named: "$go.itab"})
	typV := rt.newVar("ty", &ir.Ref{Named: "$go.type"})
	okV := rt.newVar("ok", ir.TypeI32)
	for _, v := range []*ir.Var{itabV, typV, okV} {
		pre = append(pre, &ir.VarDef{Var: v})
	}
	pre = append(pre,
		assignV(itabV, &ir.Instr{Op: ir.OpStructGet, Type: ifaceTy, Field: "itab", Args: []ir.Expr{useV(ifaceV)}}),
		assignV(typV, &ir.Instr{Op: ir.OpStructGet, Type: &ir.Ref{Named: "$go.itab"}, Field: "type", Args: []ir.Expr{useV(itabV)}}),
		assignV(okV, &ir.Instr{Op: ir.OpRefEq, Args: []ir.Expr{
			useV(typV),
			&ir.Instr{Op: ir.OpGlobalGet, Global: typeGlobal},
		}}),
	)

	dstTy := in.Type
	if dstTy == nil {
		dstTy = &ir.Ref{Null: true, Heap: ir.HeapAny}
	}
	// Use a nullable local so the var is defaultable; non-null dstTy is
	// restored with ref.as_non_null when reading (Wasm GC rejects
	// branch-initialized (ref T) locals on some engines).
	valTy := dstTy
	if r, ok := dstTy.(*ir.Ref); ok && !r.Null && r.Named != "" {
		rc := *r
		rc.Null = true
		valTy = &rc
	}
	dataV := rt.newVar("d", &ir.Ref{Null: true, Heap: ir.HeapAny})
	valV := rt.newVar("v", valTy)
	pre = append(pre, &ir.VarDef{Var: dataV}, &ir.VarDef{Var: valV})
	pre = append(pre, assignV(dataV, &ir.Instr{
		Op: ir.OpStructGet, Type: ifaceTy, Field: "data", Args: []ir.Expr{useV(ifaceV)},
	}))

	thenSeq := &ir.Seq{}
	ubStmts, ubExpr := rt.unboxTo(dstTy, useV(dataV))
	thenSeq.Stmts = append(thenSeq.Stmts, ubStmts...)
	thenSeq.Stmts = append(thenSeq.Stmts, assignV(valV, ubExpr))

	elseSeq := &ir.Seq{}
	if commaOK {
		z, _ := zeroIR(valTy)
		elseSeq.Stmts = append(elseSeq.Stmts, assignV(valV, z))
	} else {
		elseSeq.Stmts = append(elseSeq.Stmts,
			&ir.ExprStmt{X: &ir.Instr{Op: ir.OpCall, Func: "runtime.panicTypeAssert"}},
			&ir.Unreachable{},
		)
	}
	pre = append(pre, &ir.If{Cond: useV(okV), Then: thenSeq, Else: elseSeq})

	readVal := ir.Expr(useV(valV))
	if r, ok := dstTy.(*ir.Ref); ok && !r.Null {
		readVal = &ir.Instr{Op: ir.OpRefAsNonNull, Args: []ir.Expr{readVal}}
	}
	if commaOK && len(s.Lhs) == 2 {
		pre = append(pre,
			&ir.Assign{Lhs: []ir.Dest{s.Lhs[0]}, Rhs: readVal},
			&ir.Assign{Lhs: []ir.Dest{s.Lhs[1]}, Rhs: useV(okV)},
		)
	} else {
		pre = append(pre, &ir.Assign{Lhs: s.Lhs, Rhs: readVal})
	}
	return pre, nil
}

type testResult struct {
	pre []ir.Stmt
	ok  ir.Expr
}

func (rt *runtimeLower) assertTest(ifaceV *ir.Var, typeGlobal string) testResult {
	ifaceTy := typeIface
	itabV := rt.newVar("it", &ir.Ref{Named: "$go.itab"})
	typV := rt.newVar("ty", &ir.Ref{Named: "$go.type"})
	okV := rt.newVar("ok", ir.TypeI32)
	pre := []ir.Stmt{
		&ir.VarDef{Var: itabV}, &ir.VarDef{Var: typV}, &ir.VarDef{Var: okV},
		assignV(okV, &ir.Const{Op: ir.OpI32Const, I64: 0}),
	}
	// Nil interface → not a match.
	match := &ir.Seq{Stmts: []ir.Stmt{
		assignV(itabV, &ir.Instr{Op: ir.OpStructGet, Type: ifaceTy, Field: "itab", Args: []ir.Expr{useV(ifaceV)}}),
		assignV(typV, &ir.Instr{Op: ir.OpStructGet, Type: &ir.Ref{Named: "$go.itab"}, Field: "type", Args: []ir.Expr{useV(itabV)}}),
		assignV(okV, &ir.Instr{Op: ir.OpRefEq, Args: []ir.Expr{
			useV(typV),
			&ir.Instr{Op: ir.OpGlobalGet, Global: typeGlobal},
		}}),
	}}
	pre = append(pre, &ir.If{
		Cond: &ir.Instr{Op: ir.OpRefIsNull, Args: []ir.Expr{useV(ifaceV)}},
		Then: &ir.Seq{},
		Else: match,
	})
	return testResult{pre: pre, ok: useV(okV)}
}

func (rt *runtimeLower) unboxTo(dst ir.Type, data ir.Expr) ([]ir.Stmt, ir.Expr) {
	// Ref types: cast any → dst
	if r, ok := dst.(*ir.Ref); ok {
		if r.Named == "$go.string" || strings.HasPrefix(r.Named, "$go.struct.") ||
			strings.HasPrefix(r.Named, "$go.ptr.") || strings.HasPrefix(r.Named, "$go.slice.") ||
			r.Named == "$go.iface" || r.Heap == ir.HeapAny {
			return nil, &ir.Instr{Op: ir.OpRefCast, Type: dst, Args: []ir.Expr{data}}
		}
		return nil, &ir.Instr{Op: ir.OpRefCast, Type: dst, Args: []ir.Expr{data}}
	}
	// Scalars: unbox from $go.box.*
	switch dst {
	case ir.TypeI64:
		cast := &ir.Instr{Op: ir.OpRefCast, Type: &ir.Ref{Named: "$go.box.i64"}, Args: []ir.Expr{data}}
		return nil, &ir.Instr{Op: ir.OpStructGet, Type: &ir.Ref{Named: "$go.box.i64"}, Field: "v", Args: []ir.Expr{cast}}
	case ir.TypeF32:
		cast := &ir.Instr{Op: ir.OpRefCast, Type: &ir.Ref{Named: "$go.box.f32"}, Args: []ir.Expr{data}}
		return nil, &ir.Instr{Op: ir.OpStructGet, Type: &ir.Ref{Named: "$go.box.f32"}, Field: "v", Args: []ir.Expr{cast}}
	case ir.TypeF64:
		cast := &ir.Instr{Op: ir.OpRefCast, Type: &ir.Ref{Named: "$go.box.f64"}, Args: []ir.Expr{data}}
		return nil, &ir.Instr{Op: ir.OpStructGet, Type: &ir.Ref{Named: "$go.box.f64"}, Field: "v", Args: []ir.Expr{cast}}
	default:
		cast := &ir.Instr{Op: ir.OpRefCast, Type: &ir.Ref{Named: "$go.box.i32"}, Args: []ir.Expr{data}}
		return nil, &ir.Instr{Op: ir.OpStructGet, Type: &ir.Ref{Named: "$go.box.i32"}, Field: "v", Args: []ir.Expr{cast}}
	}
}

func zeroIR(t ir.Type) (ir.Expr, error) {
	switch t := t.(type) {
	case *ir.NumType:
		switch t.Kind {
		case ir.I64:
			return &ir.Const{Op: ir.OpI64Const, I64: 0}, nil
		case ir.F32:
			return &ir.Const{Op: ir.OpF32Const, F64: 0}, nil
		case ir.F64:
			return &ir.Const{Op: ir.OpF64Const, F64: 0}, nil
		default:
			return &ir.Const{Op: ir.OpI32Const, I64: 0}, nil
		}
	case *ir.Ref:
		if t.Named == "$go.string" {
			return &ir.Instr{Op: ir.OpStringLit, Str: ""}, nil
		}
		return &ir.Instr{Op: ir.OpRefNull, Type: t}, nil
	}
	return &ir.Const{Op: ir.OpI32Const, I64: 0}, nil
}

func (rt *runtimeLower) expandIfaceEq(s *ir.Assign, in *ir.Instr, n *int) ([]ir.Stmt, error) {
	if len(in.Args) != 2 {
		return nil, fmt.Errorf("iface.eq wants 2 args")
	}
	var pre []ir.Stmt
	a, p, err := rt.rewriteExpr(in.Args[0], n)
	if err != nil {
		return nil, err
	}
	pre = append(pre, p...)
	b, p, err := rt.rewriteExpr(in.Args[1], n)
	if err != nil {
		return nil, err
	}
	pre = append(pre, p...)

	ifaceTy := typeIface
	av := rt.newVar("ea", ifaceTy)
	bv := rt.newVar("eb", ifaceTy)
	res := rt.newVar("eq", ir.TypeI32)
	for _, v := range []*ir.Var{av, bv, res} {
		pre = append(pre, &ir.VarDef{Var: v})
	}
	pre = append(pre, assignV(av, a), assignV(bv, b))

	// if a is null: result = b is null
	aNull := &ir.Instr{Op: ir.OpRefIsNull, Args: []ir.Expr{useV(av)}}
	bNull := &ir.Instr{Op: ir.OpRefIsNull, Args: []ir.Expr{useV(bv)}}

	bothNil := &ir.Seq{Stmts: []ir.Stmt{assignV(res, bNull)}}
	// else if b is null: false
	bIsNil := &ir.Seq{Stmts: []ir.Stmt{assignV(res, &ir.Const{Op: ir.OpI32Const, I64: 0})}}
	// else: compare type descriptors and call equal
	itA := rt.newVar("ita", &ir.Ref{Named: "$go.itab"})
	itB := rt.newVar("itb", &ir.Ref{Named: "$go.itab"})
	tyA := rt.newVar("tya", &ir.Ref{Named: "$go.type"})
	tyB := rt.newVar("tyb", &ir.Ref{Named: "$go.type"})
	dA := rt.newVar("da", &ir.Ref{Null: true, Heap: ir.HeapAny})
	dB := rt.newVar("db", &ir.Ref{Null: true, Heap: ir.HeapAny})
	cmpBody := &ir.Seq{}
	for _, v := range []*ir.Var{itA, itB, tyA, tyB, dA, dB} {
		cmpBody.Stmts = append(cmpBody.Stmts, &ir.VarDef{Var: v})
	}
	cmpBody.Stmts = append(cmpBody.Stmts,
		assignV(itA, &ir.Instr{Op: ir.OpStructGet, Type: ifaceTy, Field: "itab", Args: []ir.Expr{useV(av)}}),
		assignV(itB, &ir.Instr{Op: ir.OpStructGet, Type: ifaceTy, Field: "itab", Args: []ir.Expr{useV(bv)}}),
		assignV(tyA, &ir.Instr{Op: ir.OpStructGet, Type: &ir.Ref{Named: "$go.itab"}, Field: "type", Args: []ir.Expr{useV(itA)}}),
		assignV(tyB, &ir.Instr{Op: ir.OpStructGet, Type: &ir.Ref{Named: "$go.itab"}, Field: "type", Args: []ir.Expr{useV(itB)}}),
		assignV(dA, &ir.Instr{Op: ir.OpStructGet, Type: ifaceTy, Field: "data", Args: []ir.Expr{useV(av)}}),
		assignV(dB, &ir.Instr{Op: ir.OpStructGet, Type: ifaceTy, Field: "data", Args: []ir.Expr{useV(bv)}}),
	)
	sameTy := &ir.Instr{Op: ir.OpRefEq, Args: []ir.Expr{useV(tyA), useV(tyB)}}
	// ref.eq requires eqref; iface data is anyref.
	eqT := &ir.Ref{Null: true, Heap: ir.HeapEq}
	dataEq := &ir.Instr{Op: ir.OpRefEq, Args: []ir.Expr{
		&ir.Instr{Op: ir.OpRefCast, Type: eqT, Args: []ir.Expr{useV(dA)}},
		&ir.Instr{Op: ir.OpRefCast, Type: eqT, Args: []ir.Expr{useV(dB)}},
	}}
	// Identity compare on data covers *T and same-box values. Distinct boxes
	// with equal contents need $go.eq.* (scalar unbox) — deferred when equal
	// is looked up from the type descriptor.
	cmpBody.Stmts = append(cmpBody.Stmts, &ir.If{
		Cond: sameTy,
		Then: &ir.Seq{Stmts: []ir.Stmt{assignV(res, dataEq)}},
		Else: &ir.Seq{Stmts: []ir.Stmt{assignV(res, &ir.Const{Op: ir.OpI32Const, I64: 0})}},
	})

	pre = append(pre, &ir.If{
		Cond: aNull,
		Then: bothNil,
		Else: &ir.Seq{Stmts: []ir.Stmt{&ir.If{Cond: bNull, Then: bIsNil, Else: cmpBody}}},
	})
	pre = append(pre, &ir.Assign{Lhs: s.Lhs, Rhs: useV(res)})
	return pre, nil
}

func isNumType(t ir.Type) bool {
	_, ok := t.(*ir.NumType)
	return ok
}

func (rt *runtimeLower) expandStringSlice(s *ir.Assign, in *ir.Instr, n *int) ([]ir.Stmt, error) {
	if len(in.Args) < 3 {
		return nil, fmt.Errorf("string.slice wants s, lo, hi")
	}
	var pre []ir.Stmt
	str, p, err := rt.rewriteExpr(in.Args[0], n)
	if err != nil {
		return nil, err
	}
	pre = append(pre, p...)
	lo, p, err := rt.rewriteExpr(in.Args[1], n)
	if err != nil {
		return nil, err
	}
	pre = append(pre, p...)
	hi, p, err := rt.rewriteExpr(in.Args[2], n)
	if err != nil {
		return nil, err
	}
	pre = append(pre, p...)

	st := &ir.Ref{Named: "$go.string"}
	sv := rt.newVar("ss", st)
	loV := rt.newVar("lo", ir.TypeI32)
	hiV := rt.newVar("hi", ir.TypeI32)
	offV := rt.newVar("off", ir.TypeI32)
	lenV := rt.newVar("len", ir.TypeI32)
	dataV := rt.newVar("data", &ir.Ref{Named: "$go.bytes"})
	nlenV := rt.newVar("nlen", ir.TypeI32)
	noffV := rt.newVar("noff", ir.TypeI32)
	for _, v := range []*ir.Var{sv, loV, hiV, offV, lenV, dataV, nlenV, noffV} {
		pre = append(pre, &ir.VarDef{Var: v})
	}
	pre = append(pre,
		assignV(sv, str), assignV(loV, lo), assignV(hiV, hi),
		assignV(dataV, &ir.Instr{Op: ir.OpStructGet, Type: st, Field: "data", Args: []ir.Expr{useV(sv)}}),
		assignV(offV, &ir.Instr{Op: ir.OpStructGet, Type: st, Field: "off", Args: []ir.Expr{useV(sv)}}),
		assignV(lenV, &ir.Instr{Op: ir.OpStructGet, Type: st, Field: "len", Args: []ir.Expr{useV(sv)}}),
	)
	// bounds: 0 <= lo <= hi <= len
	pre = append(pre, &ir.If{
		Cond: &ir.Instr{Op: ir.OpI32GtU, Args: []ir.Expr{useV(loV), useV(hiV)}},
		Then: &ir.Seq{Stmts: rt.panicSliceB()},
	})
	pre = append(pre, &ir.If{
		Cond: &ir.Instr{Op: ir.OpI32GtU, Args: []ir.Expr{useV(hiV), useV(lenV)}},
		Then: &ir.Seq{Stmts: rt.panicSliceB()},
	})
	pre = append(pre,
		assignV(nlenV, &ir.Instr{Op: ir.OpI32Sub, Args: []ir.Expr{useV(hiV), useV(loV)}}),
		assignV(noffV, &ir.Instr{Op: ir.OpI32Add, Args: []ir.Expr{useV(offV), useV(loV)}}),
		&ir.Assign{Lhs: s.Lhs, Rhs: &ir.Instr{
			Op:   ir.OpStructNew,
			Type: st,
			Args: []ir.Expr{useV(dataV), useV(noffV), useV(nlenV)},
		}},
	)
	return pre, nil
}
