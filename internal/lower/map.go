package lower

import (
	"go/ast"
	"go/types"

	"github.com/achille-roussel/wasmgc-go/internal/desc"
	"github.com/achille-roussel/wasmgc-go/internal/ir"
)

func isMap(t types.Type) bool {
	if t == nil {
		return false
	}
	_, ok := types.Unalias(t).Underlying().(*types.Map)
	return ok
}

func mapKeyElem(t types.Type) (key, elem types.Type) {
	if t == nil {
		return nil, nil
	}
	m, ok := types.Unalias(t).Underlying().(*types.Map)
	if !ok {
		return nil, nil
	}
	return m.Key(), m.Elem()
}

func (fl *funcLower) markNeedsMaps() {
	fl.pkg.ensureMapBaseTypes()
	if fl.pkg.meta != nil {
		fl.pkg.meta.needsMaps = true
	}
}

// mapIndexExpr lowers m[k] (single value; missing → zero).
func (fl *funcLower) mapIndexExpr(e *ast.IndexExpr, want ir.Type) (ir.Expr, []ir.Stmt, error) {
	fl.markNeedsMaps()
	mTy := fl.typeOf(e.X)
	mIR, err := fl.mapType(mTy)
	if err != nil {
		return nil, nil, fl.errorf(e.X, "%v", err)
	}
	m, stmts, err := fl.expr(e.X, mIR)
	if err != nil {
		return nil, nil, err
	}
	keyT, elemT := mapKeyElem(mTy)
	emptyIface := types.NewInterfaceType(nil, nil)
	key, kstmts, err := fl.convertToIface(e.Index, fl.typeOf(e.Index), emptyIface, typeIface)
	if err != nil {
		return nil, nil, err
	}
	stmts = append(stmts, kstmts...)
	resTy := want
	if resTy == nil && elemT != nil {
		resTy, _ = fl.mapType(elemT)
	}
	if resTy == nil {
		resTy = typeIface
	}
	in := &ir.Instr{
		Op:   ir.OpMapGet,
		Type: resTy,
		Args: []ir.Expr{m, key},
	}
	if elemT != nil {
		in.Global = desc.TypeKey(elemT)
		in.Field = boxKind(elemT)
	}
	if keyT != nil {
		in.Str = desc.TypeKey(keyT)
	}
	return in, stmts, nil
}

// mapIndexCommaOk lowers v, ok := m[k].
func (fl *funcLower) mapIndexCommaOk(s *ast.AssignStmt, e *ast.IndexExpr, out []ir.Stmt) ([]ir.Stmt, error) {
	fl.markNeedsMaps()
	if len(s.Lhs) != 2 {
		return nil, fl.errorf(s, "map comma-ok wants 2 LHS")
	}
	// Prepare *f()-style LHS before key/lookup (bug242: *f(), ok = m[gint()]).
	preps, pstmts, err := fl.prepareParallelDests(s.Lhs)
	if err != nil {
		return nil, err
	}
	out = append(out, pstmts...)
	mTy := fl.typeOf(e.X)
	mIR, err := fl.mapType(mTy)
	if err != nil {
		return nil, fl.errorf(e.X, "%v", err)
	}
	m, stmts, err := fl.expr(e.X, mIR)
	if err != nil {
		return nil, err
	}
	out = append(out, stmts...)
	keyT, elemT := mapKeyElem(mTy)
	emptyIface := types.NewInterfaceType(nil, nil)
	key, kstmts, err := fl.convertToIface(e.Index, fl.typeOf(e.Index), emptyIface, typeIface)
	if err != nil {
		return nil, err
	}
	out = append(out, kstmts...)
	// Prefer the map element type over lhsType: blank `_` reports i32 and would
	// mis-unbox struct/ref values (map[K]struct{} comma-ok).
	var valTy ir.Type
	if elemT != nil {
		var err error
		valTy, err = fl.mapType(elemT)
		if err != nil {
			return nil, fl.errorf(e, "%v", err)
		}
	} else {
		var err error
		valTy, err = fl.lhsType(s.Lhs[0])
		if err != nil {
			return nil, err
		}
	}
	in := &ir.Instr{
		Op:   ir.OpMapGet,
		Type: valTy,
		Str:  "ok",
		Args: []ir.Expr{m, key},
	}
	if elemT != nil {
		in.Global = desc.TypeKey(elemT)
		in.Field = boxKind(elemT)
	}
	if keyT != nil && in.Str == "ok" {
		// Preserve comma-ok marker; key type rides on a side channel via Field prefix.
		_ = keyT
	}
	vt := fl.newTemp(valTy)
	ot := fl.newTemp(ir.TypeI32)
	out = append(out, &ir.Assign{
		Lhs: []ir.Dest{
			{Temp: vt, Name: vt.Name},
			{Temp: ot, Name: ot.Name},
		},
		Rhs: in,
	})
	// Box when LHS is interface{} (issue16870: ok interface{}; _, ok = m[k]).
	v0, err := fl.maybeBoxExpr(fl.useTemp(vt), elemT, s.Lhs[0])
	if err != nil {
		return nil, err
	}
	v1, err := fl.maybeBoxExpr(fl.useTemp(ot), types.Typ[types.Bool], s.Lhs[1])
	if err != nil {
		return nil, err
	}
	as0, err := fl.commitParallelDest(preps[0], v0)
	if err != nil {
		return nil, err
	}
	as1, err := fl.commitParallelDest(preps[1], v1)
	if err != nil {
		return nil, err
	}
	out = append(out, as0...)
	out = append(out, as1...)
	return out, nil
}

// mapIndexAssign lowers m[k] = v (and m[k] op= v via caller).
func (fl *funcLower) mapIndexAssign(lhs *ast.IndexExpr, rhs ir.Expr, rhsGo types.Type) ([]ir.Stmt, error) {
	fl.markNeedsMaps()
	mTy := fl.typeOf(lhs.X)
	mIR, err := fl.mapType(mTy)
	if err != nil {
		return nil, fl.errorf(lhs.X, "%v", err)
	}
	m, stmts, err := fl.expr(lhs.X, mIR)
	if err != nil {
		return nil, err
	}
	keyT, elemT := mapKeyElem(mTy)
	emptyIface := types.NewInterfaceType(nil, nil)
	key, kstmts, err := fl.convertToIface(lhs.Index, fl.typeOf(lhs.Index), emptyIface, typeIface)
	if err != nil {
		return nil, err
	}
	stmts = append(stmts, kstmts...)
	// Box rhs to any. rhs is already lowered; wrap via iface.make when concrete.
	var val ir.Expr
	if elemT != nil && !isIface(elemT) {
		box := boxKind(elemT)
		itab := desc.ItabGlobal(emptyIface, elemT)
		val = &ir.Instr{
			Op:     ir.OpIfaceMake,
			Type:   typeIface,
			Global: itab,
			Str:    "type:" + desc.TypeKey(elemT),
			Field:  box,
			Args:   []ir.Expr{rhs},
		}
	} else {
		val = rhs
	}
	in := &ir.Instr{
		Op:   ir.OpMapSet,
		Args: []ir.Expr{m, key, val},
	}
	if keyT != nil {
		in.Str = desc.TypeKey(keyT)
	}
	if elemT != nil {
		in.Global = desc.TypeKey(elemT)
		in.Field = boxKind(elemT)
	}
	_ = rhsGo
	stmts = append(stmts, &ir.ExprStmt{X: in})
	return stmts, nil
}

func (fl *funcLower) builtinDelete(e *ast.CallExpr) ([]ir.Stmt, error) {
	var mapArg, keyArg ast.Expr
	var stmts []ir.Stmt
	switch {
	case len(e.Args) == 2:
		mapArg, keyArg = e.Args[0], e.Args[1]
	case len(e.Args) == 1:
		// delete(f()) where f returns (map, key) — issue15992.
		ce, ok := e.Args[0].(*ast.CallExpr)
		if !ok {
			return nil, fl.errorf(e, "delete wants 2 args")
		}
		temps, st, err := fl.callMaterialize(ce, nil)
		if err != nil {
			return nil, err
		}
		if len(temps) != 2 {
			return nil, fl.errorf(e, "delete wants 2 args")
		}
		stmts = append(stmts, st...)
		fl.markNeedsMaps()
		tup := fl.callResultTuple(ce)
		mTy := tup.At(0).Type()
		keyGo := tup.At(1).Type()
		emptyIface := types.NewInterfaceType(nil, nil)
		key := ir.Expr(&ir.Instr{
			Op:     ir.OpIfaceMake,
			Type:   typeIface,
			Global: desc.ItabGlobal(emptyIface, keyGo),
			Str:    "type:" + desc.TypeKey(keyGo),
			Field:  boxKind(keyGo),
			Args:   []ir.Expr{fl.useTemp(temps[1])},
		})
		in := &ir.Instr{Op: ir.OpMapDelete, Args: []ir.Expr{fl.useTemp(temps[0]), key}}
		if keyT, _ := mapKeyElem(mTy); keyT != nil {
			in.Str = desc.TypeKey(keyT)
		}
		stmts = append(stmts, &ir.ExprStmt{X: in})
		return stmts, nil
	default:
		return nil, fl.errorf(e, "delete wants 2 args")
	}
	fl.markNeedsMaps()
	mTy := fl.typeOf(mapArg)
	if !isMap(mTy) {
		return nil, fl.errorf(mapArg, "delete of non-map type %s", mTy)
	}
	mIR, err := fl.mapType(mTy)
	if err != nil {
		return nil, fl.errorf(mapArg, "%v", err)
	}
	m, st, err := fl.expr(mapArg, mIR)
	if err != nil {
		return nil, err
	}
	stmts = append(stmts, st...)
	keyT, _ := mapKeyElem(mTy)
	emptyIface := types.NewInterfaceType(nil, nil)
	key, kstmts, err := fl.convertToIface(keyArg, fl.typeOf(keyArg), emptyIface, typeIface)
	if err != nil {
		return nil, err
	}
	stmts = append(stmts, kstmts...)
	in := &ir.Instr{Op: ir.OpMapDelete, Args: []ir.Expr{m, key}}
	if keyT != nil {
		in.Str = desc.TypeKey(keyT)
	}
	stmts = append(stmts, &ir.ExprStmt{X: in})
	return stmts, nil
}

func (fl *funcLower) builtinClear(e *ast.CallExpr) ([]ir.Stmt, error) {
	if len(e.Args) != 1 {
		return nil, fl.errorf(e, "clear wants 1 arg")
	}
	argTy := fl.typeOf(e.Args[0])
	if isMap(argTy) {
		fl.markNeedsMaps()
		mIR, err := fl.mapType(argTy)
		if err != nil {
			return nil, fl.errorf(e.Args[0], "%v", err)
		}
		m, stmts, err := fl.expr(e.Args[0], mIR)
		if err != nil {
			return nil, err
		}
		// Encode clear as map.delete with a sentinel Str.
		stmts = append(stmts, &ir.ExprStmt{X: &ir.Instr{
			Op:   ir.OpMapDelete,
			Str:  "clear",
			Args: []ir.Expr{m},
		}})
		return stmts, nil
	}
	if isSlice(argTy) {
		// clear(s) zeros s[0:len(s)]; keep len/cap (Go 1.21+).
		sl, ok := types.Unalias(argTy).Underlying().(*types.Slice)
		if !ok {
			return nil, fl.errorf(e, "clear of slice: bad type")
		}
		sIR, err := fl.mapType(argTy)
		if err != nil {
			return nil, fl.errorf(e.Args[0], "%v", err)
		}
		sEx, stmts, err := fl.expr(e.Args[0], sIR)
		if err != nil {
			return nil, err
		}
		elemIR, err := fl.mapType(sl.Elem())
		if err != nil {
			return nil, fl.errorf(e, "%v", err)
		}
		zero, err := fl.zeroValue(elemIR)
		if err != nil {
			return nil, fl.errorf(e, "clear: zero elem: %v", err)
		}
		sv := fl.newVar("clrs", sIR)
		iV := fl.newVar("clri", ir.TypeI32)
		stmts = append(stmts, fl.assignVar(sv, sEx), fl.assignVar(iV, fl.i32const(0)))
		done := fl.freshLabel("clrdone")
		cont := fl.freshLabel("clrcont")
		stmts = append(stmts, &ir.Block{
			Name: done,
			Body: &ir.Loop{
				Name: cont,
				Body: &ir.Seq{Stmts: []ir.Stmt{
					&ir.If{
						Cond: &ir.Instr{Op: ir.OpI32GeU, Args: []ir.Expr{
							fl.useVar(iV),
							&ir.Instr{Op: ir.OpLen, Args: []ir.Expr{fl.useVar(sv)}},
						}},
						Then: &ir.Seq{Stmts: []ir.Stmt{&ir.Br{Label: done}}},
					},
					&ir.ExprStmt{X: &ir.Instr{Op: ir.OpSliceSet, Args: []ir.Expr{fl.useVar(sv), fl.useVar(iV), zero}}},
					fl.assignVar(iV, &ir.Instr{Op: ir.OpI32Add, Args: []ir.Expr{fl.useVar(iV), fl.i32const(1)}}),
					&ir.Br{Label: cont},
				}},
			},
		})
		return stmts, nil
	}
	return nil, fl.errorf(e.Args[0], "clear of type %s not supported", argTy)
}

// rangeMapStmt lowers for range over a map with randomized start + snapshot.
func (fl *funcLower) rangeMapStmt(s *ast.RangeStmt, goLabel string) ([]ir.Stmt, error) {
	fl.markNeedsMaps()
	xType := fl.typeOf(s.X)
	xIR, err := fl.mapType(xType)
	if err != nil {
		return nil, fl.errorf(s.X, "%v", err)
	}
	x, xstmts, err := fl.expr(s.X, xIR)
	if err != nil {
		return nil, err
	}
	var out []ir.Stmt
	out = append(out, xstmts...)

	keyT, elemT := mapKeyElem(xType)
	var keyIR, valIR ir.Type = typeIface, typeIface
	if keyT != nil {
		if t, err := fl.mapType(keyT); err == nil {
			keyIR = t
		}
	}
	if elemT != nil {
		if t, err := fl.mapType(elemT); err == nil {
			valIR = t
		}
	}

	mV := fl.newVar("mmap", xIR)
	out = append(out, fl.assignVar(mV, x))
	itV := fl.newVar("mit", &ir.Ref{Null: true, Named: "$go.mapIter"})
	out = append(out, fl.assignVar(itV, &ir.Instr{
		Op:   ir.OpMapIter,
		Type: itV.Typ,
		Args: []ir.Expr{fl.useVar(mV)},
	}))

	var keyVar, valVar *ir.Var
	if s.Key != nil {
		if id, ok := s.Key.(*ast.Ident); ok && id.Name != "_" {
			v, defs, err := fl.defineLocal(id)
			if err != nil {
				return nil, err
			}
			out = append(out, defs...)
			keyVar = v
			_ = keyIR
		}
	}
	if s.Value != nil {
		if id, ok := s.Value.(*ast.Ident); ok && id.Name != "_" {
			v, defs, err := fl.defineLocal(id)
			if err != nil {
				return nil, err
			}
			out = append(out, defs...)
			valVar = v
			_ = valIR
		}
	}

	var breakL, contL, loopL string
	if goLabel != "" {
		breakL = "$" + goLabel + ".break"
		contL = "$" + goLabel + ".cont"
		loopL = "$" + goLabel
		fl.labels[goLabel] = &labelTargets{breakLabel: breakL, continueLabel: contL}
	} else {
		breakL = fl.freshLabel("break")
		contL = fl.freshLabel("cont")
		loopL = fl.freshLabel("L")
	}
	fl.breakStack = append(fl.breakStack, breakL)
	fl.continueStack = append(fl.continueStack, contL)
	defer func() {
		fl.breakStack = fl.breakStack[:len(fl.breakStack)-1]
		fl.continueStack = fl.continueStack[:len(fl.continueStack)-1]
		if goLabel != "" {
			delete(fl.labels, goLabel)
		}
	}()

	bodySeq := &ir.Seq{}
	next := &ir.Instr{
		Op:   ir.OpMapNext,
		Type: typeIface,
		Args: []ir.Expr{fl.useVar(mV), fl.useVar(itV)},
	}
	if keyT != nil {
		next.Str = desc.TypeKey(keyT)
	}
	if elemT != nil {
		next.Global = desc.TypeKey(elemT)
		next.Field = boxKind(elemT)
	}
	kTmp := fl.newTemp(typeIface)
	vTmp := fl.newTemp(typeIface)
	okTmp := fl.newTemp(ir.TypeI32)
	bodySeq.Stmts = append(bodySeq.Stmts, &ir.Assign{
		Lhs: []ir.Dest{
			{Temp: kTmp, Name: kTmp.Name},
			{Temp: vTmp, Name: vTmp.Name},
			{Temp: okTmp, Name: okTmp.Name},
		},
		Rhs: next,
	})
	bodySeq.Stmts = append(bodySeq.Stmts, &ir.Br{
		Label: breakL,
		Cond:  &ir.Instr{Op: ir.OpI32Eqz, Args: []ir.Expr{fl.useTemp(okTmp)}},
	})
	if keyVar != nil {
		keyGo := fl.goTypeOfLocal(keyVar)
		if keyGo != nil && isIface(keyGo) && !isTypeParam(keyGo) {
			// MapNext already yields iface; keep it when ranging into any/iface.
			bodySeq.Stmts = append(bodySeq.Stmts, fl.assignVar(keyVar, fl.useTemp(kTmp)))
		} else {
			ub := fl.mapUnbox(fl.useTemp(kTmp), keyIR, keyT)
			bodySeq.Stmts = append(bodySeq.Stmts, fl.assignVar(keyVar, ub))
		}
	}
	if valVar != nil {
		valGo := fl.goTypeOfLocal(valVar)
		if valGo != nil && isIface(valGo) && !isTypeParam(valGo) {
			bodySeq.Stmts = append(bodySeq.Stmts, fl.assignVar(valVar, fl.useTemp(vTmp)))
		} else {
			ub := fl.mapUnbox(fl.useTemp(vTmp), valIR, elemT)
			bodySeq.Stmts = append(bodySeq.Stmts, fl.assignVar(valVar, ub))
		}
	}
	fl.markForBodyIterVars(s.Body)
	inner, err := fl.stmtBlock(s.Body)
	if err != nil {
		return nil, err
	}
	bodySeq.Stmts = append(bodySeq.Stmts, inner.(*ir.Seq).Stmts...)
	bodySeq.Stmts = append(bodySeq.Stmts, &ir.Block{Name: contL, Body: &ir.Seq{}})
	bodySeq.Stmts = append(bodySeq.Stmts, &ir.Br{Label: loopL})

	out = append(out, &ir.Block{
		Name: breakL,
		Body: &ir.Seq{Stmts: []ir.Stmt{&ir.Loop{Name: loopL, Body: bodySeq}}},
	})
	return out, nil
}

func (fl *funcLower) mapUnbox(iface ir.Expr, dstTy ir.Type, goTy types.Type) ir.Expr {
	if goTy != nil && isIface(goTy) {
		return iface
	}
	if goTy == nil {
		return iface
	}
	return &ir.Instr{
		Op:     ir.OpIfaceAssert,
		Type:   dstTy,
		Global: desc.TypeKey(goTy),
		Field:  boxKind(goTy),
		Args:   []ir.Expr{iface},
	}
}

// mapLiteral lowers map[K]V{k: v, ...}.
func (fl *funcLower) mapLiteral(e *ast.CompositeLit, irt ir.Type) (ir.Expr, []ir.Stmt, error) {
	fl.markNeedsMaps()
	ty := fl.typeOf(e)
	ut := types.Unalias(ty)
	if n, ok := ut.(*types.Named); ok {
		ut = n.Underlying()
	}
	m, ok := ut.(*types.Map)
	if !ok {
		return nil, nil, fl.errorf(e, "map literal of non-map type %s", ty)
	}
	hint := int64(len(e.Elts))
	tmp := fl.newVar("mlit", irt)
	var stmts []ir.Stmt
	makeIn := &ir.Instr{
		Op:   ir.OpMapMake,
		Type: irt,
		Args: []ir.Expr{fl.i32const(hint)},
		Str:  desc.TypeKey(m.Key()),
	}
	stmts = append(stmts, fl.assignVar(tmp, makeIn))
	emptyIface := types.NewInterfaceType(nil, nil)
	for _, el := range e.Elts {
		kv, ok := el.(*ast.KeyValueExpr)
		if !ok {
			return nil, nil, fl.errorf(el, "map literal element must be key:value")
		}
		key, kstmts, err := fl.convertToIface(kv.Key, fl.typeOf(kv.Key), emptyIface, typeIface)
		if err != nil {
			return nil, nil, err
		}
		stmts = append(stmts, kstmts...)
		val, vstmts, err := fl.convertToIface(kv.Value, fl.typeOf(kv.Value), emptyIface, typeIface)
		if err != nil {
			return nil, nil, err
		}
		stmts = append(stmts, vstmts...)
		stmts = append(stmts, &ir.ExprStmt{X: &ir.Instr{
			Op:     ir.OpMapSet,
			Args:   []ir.Expr{fl.useVar(tmp), key, val},
			Str:    desc.TypeKey(m.Key()),
			Global: desc.TypeKey(m.Elem()),
			Field:  boxKind(m.Elem()),
		}})
	}
	return fl.useVar(tmp), stmts, nil
}
