package lower

import (
	"go/ast"
	"go/token"
	"go/types"
	"strconv"

	"github.com/achille-roussel/wasmgc-go/internal/ir"
)

// Range-over-func (Go 1.23 iterators). M10 first cut:
//
//	for x := range seq { ... }
//
// becomes a single call to seq with a synthesized yield closure. Control
// exits use false-return from yield (not $go.rangeExit throw — multi-tag EH
// is panic-only today):
//
//	continue → state=READY; return true
//	break    → state=DONE;  return false
//	return   → store results; retFlag=1; state=DONE; return false;
//	           after iterator returns, emit the outer return
//
// State machine matches runtime/abi RF_*: READY → PANIC while body runs →
// READY/DONE on continue/break; EXHAUSTED after iterator returns.
// Double-yield after DONE/EXHAUSTED/PANIC calls runtime.panicrangestate.
//
// Follow-up: defer inside the range body should attach to the outer frame
// (cmd/compile's deferrangefunc); skipped for this cut.

const (
	rfDone         = 0
	rfReady        = 1
	rfPanic        = 2
	rfExhausted    = 3
	rfMissingPanic = 4
)

func (fl *funcLower) rangeFuncStmt(s *ast.RangeStmt, goLabel string) ([]ir.Stmt, error) {
	prevDeferLoop := fl.deferInLoop
	fl.deferInLoop = true
	defer func() { fl.deferInLoop = prevDeferLoop }()

	xType := fl.typeOf(s.X)
	if xType == nil {
		return nil, fl.errorf(s.X, "range: no type")
	}
	iterSig, ok := xType.Underlying().(*types.Signature)
	if !ok || iterSig.Params().Len() != 1 {
		return nil, fl.errorf(s.X, "range over func: expected single-parameter iterator, got %s", xType)
	}
	yieldTy, ok := iterSig.Params().At(0).Type().Underlying().(*types.Signature)
	if !ok {
		return nil, fl.errorf(s.X, "range over func: yield is not a func")
	}

	var out []ir.Stmt

	// Shared early-exit flag + result cells from the outermost range frame.
	var retObj types.Object
	var resultObjs []types.Object
	var resultTys []ir.Type
	var resultGo []types.Type
	if len(fl.rangeFrames) == 0 {
		retObj = fl.syntheticLocal("#rfret", types.Typ[types.Int32], ir.TypeI32)
		fl.markAssigned(retObj)
		if err := fl.ensureLocalCell(retObj, mustCell(fl, ir.TypeI32), ir.TypeI32); err != nil {
			return nil, err
		}
		ws, err := fl.writeVar(retObj, fl.i32const(0))
		if err != nil {
			return nil, err
		}
		out = append(out, ws...)
		if fl.sig != nil {
			for i := 0; i < fl.sig.Results().Len(); i++ {
				r := fl.sig.Results().At(i)
				ty := fl.results[i]
				name := "#rfrv" + strconv.Itoa(i)
				obj := fl.syntheticLocal(name, r.Type(), ty)
				fl.markAssigned(obj)
				cellName, err := fl.pkg.ensureCellType(ty)
				if err != nil {
					return nil, err
				}
				if err := fl.ensureLocalCell(obj, cellName, ty); err != nil {
					return nil, err
				}
				resultObjs = append(resultObjs, obj)
				resultTys = append(resultTys, ty)
				resultGo = append(resultGo, r.Type())
			}
		}
	} else {
		outer := fl.rangeFrames[0]
		retObj = outer.retObj
		resultObjs = outer.resultObjs
		resultTys = outer.resultTys
		resultGo = outer.resultGo
	}

	stateObj := fl.syntheticLocal("#rfstate", types.Typ[types.Int32], ir.TypeI32)
	fl.markAssigned(stateObj)
	if err := fl.ensureLocalCell(stateObj, mustCell(fl, ir.TypeI32), ir.TypeI32); err != nil {
		return nil, err
	}
	ws, err := fl.writeVar(stateObj, fl.i32const(rfReady))
	if err != nil {
		return nil, err
	}
	out = append(out, ws...)

	var breakL, contL string
	if goLabel != "" {
		breakL = "$" + goLabel + ".break"
		contL = "$" + goLabel + ".cont"
		fl.labels[goLabel] = &labelTargets{breakLabel: breakL, continueLabel: contL}
	} else {
		breakL = fl.freshLabel("rfbreak")
		contL = fl.freshLabel("rfcont")
	}

	rf := &rangeFrame{
		stateObj:   stateObj,
		retObj:     retObj,
		resultObjs: resultObjs,
		resultTys:  resultTys,
		resultGo:   resultGo,
		breakL:     breakL,
		contL:      contL,
	}
	fl.rangeFrames = append(fl.rangeFrames, rf)
	defer func() {
		fl.rangeFrames = fl.rangeFrames[:len(fl.rangeFrames)-1]
		if goLabel != "" {
			delete(fl.labels, goLabel)
		}
	}()

	// Captures: free vars of the body + state/ret/result cells.
	free := freeVars(s.Body, fl.info, fl.vars)
	// Assignment-form range vars are free (Uses); := vars are declared.
	if s.Tok != token.DEFINE {
		free = appendRangeAssignFree(free, s, fl.info, fl.vars)
	}
	caps, err := fl.planCaptures(free)
	if err != nil {
		return nil, err
	}
	caps, err = fl.appendRangeFrameCaptures(caps, rf)
	if err != nil {
		return nil, err
	}

	closName, err := fl.pkg.ensureClosType(caps)
	if err != nil {
		return nil, fl.errorf(s, "%v", err)
	}
	funcTy, err := fl.pkg.ensureFuncType(yieldTy, closBaseName)
	if err != nil {
		return nil, fl.errorf(s, "%v", err)
	}
	bodyName := fl.pkg.freshClosName("rfyield")
	if err := fl.emitRangeYieldBody(bodyName, s, yieldTy, closName, caps, rf); err != nil {
		return nil, err
	}
	yieldClos, ystmts, err := fl.buildClosure(closName, funcTy, bodyName, caps)
	if err != nil {
		return nil, err
	}
	out = append(out, ystmts...)

	// Call iterator(yield).
	iterCall, istmts, err := fl.callIterator(s.X, iterSig, yieldClos)
	if err != nil {
		return nil, err
	}
	out = append(out, istmts...)
	out = append(out, &ir.ExprStmt{X: iterCall})

	// After iterator: missing-panic check, mark exhausted, honor pending return.
	stateEx, err := fl.readVar(stateObj)
	if err != nil {
		return nil, err
	}
	out = append(out, &ir.If{
		Cond: &ir.Instr{Op: ir.OpI32Eq, Args: []ir.Expr{stateEx, fl.i32const(rfPanic)}},
		Then: &ir.Seq{Stmts: []ir.Stmt{
			&ir.ExprStmt{X: &ir.Instr{
				Op:   ir.OpCall,
				Func: "runtime.panicrangestate",
				Args: []ir.Expr{fl.i32const(rfMissingPanic)},
			}},
			&ir.Unreachable{},
		}},
	})
	ws, err = fl.writeVar(stateObj, fl.i32const(rfExhausted))
	if err != nil {
		return nil, err
	}
	out = append(out, ws...)

	retEx, err := fl.readVar(retObj)
	if err != nil {
		return nil, err
	}
	if fl.rangeYield {
		// Nested range: propagate outer return by exiting this yield.
		doneStmts, err := fl.writeVar(stateObj, fl.i32const(rfDone))
		if err != nil {
			return nil, err
		}
		then := append([]ir.Stmt{}, doneStmts...)
		then = append(then, &ir.Return{Results: []ir.Expr{fl.i32const(0)}})
		out = append(out, &ir.If{
			Cond: retEx,
			Then: &ir.Seq{Stmts: then},
		})
	} else if len(resultObjs) > 0 || fl.sig != nil && fl.sig.Results().Len() == 0 {
		var retStmts []ir.Stmt
		var results []ir.Expr
		for _, obj := range resultObjs {
			ex, err := fl.readVar(obj)
			if err != nil {
				return nil, err
			}
			results = append(results, ex)
		}
		retStmts = append(retStmts, &ir.Return{Results: results})
		out = append(out, &ir.If{
			Cond: retEx,
			Then: &ir.Seq{Stmts: retStmts},
		})
	}

	return out, nil
}

func mustCell(fl *funcLower, elem ir.Type) string {
	name, err := fl.pkg.ensureCellType(elem)
	if err != nil {
		panic(err)
	}
	return name
}

func (fl *funcLower) syntheticLocal(name string, goTy types.Type, irTy ir.Type) types.Object {
	obj := types.NewVar(token.NoPos, fl.pkg.pkg, name, goTy)
	v := fl.newVar(name, irTy)
	fl.vars[obj] = v
	return obj
}

func appendRangeAssignFree(free []types.Object, s *ast.RangeStmt, info *types.Info, outer map[types.Object]*ir.Var) []types.Object {
	seen := map[types.Object]bool{}
	for _, o := range free {
		seen[o] = true
	}
	add := func(e ast.Expr) {
		id, ok := e.(*ast.Ident)
		if !ok || id.Name == "_" {
			return
		}
		obj := info.Uses[id]
		if obj == nil {
			return
		}
		if _, ok := outer[obj]; !ok {
			return
		}
		if seen[obj] {
			return
		}
		seen[obj] = true
		free = append(free, obj)
	}
	add(s.Key)
	add(s.Value)
	return free
}

func (fl *funcLower) appendRangeFrameCaptures(caps []captureInfo, rf *rangeFrame) ([]captureInfo, error) {
	seen := map[types.Object]bool{}
	for _, c := range caps {
		seen[c.obj] = true
	}
	add := func(obj types.Object) error {
		if obj == nil || seen[obj] {
			return nil
		}
		seen[obj] = true
		extra, err := fl.planCaptures([]types.Object{obj})
		if err != nil {
			return err
		}
		caps = append(caps, extra...)
		return nil
	}
	if err := add(rf.stateObj); err != nil {
		return nil, err
	}
	if err := add(rf.retObj); err != nil {
		return nil, err
	}
	for _, o := range rf.resultObjs {
		if err := add(o); err != nil {
			return nil, err
		}
	}
	return caps, nil
}

func (fl *funcLower) callIterator(x ast.Expr, iterSig *types.Signature, yieldClos ir.Expr) (ir.Expr, []ir.Stmt, error) {
	fl.pkg.ensureClosBase()
	clos, stmts, err := fl.expr(x, &ir.Ref{Null: true, Named: closBaseName})
	if err != nil {
		return nil, nil, err
	}
	closT := fl.newTemp(&ir.Ref{Null: true, Named: closBaseName})
	stmts = append(stmts, fl.assignTemp(closT, clos))
	stmts = append(stmts, &ir.If{
		Cond: &ir.Instr{Op: ir.OpRefIsNull, Args: []ir.Expr{fl.useTemp(closT)}},
		Then: &ir.Seq{Stmts: []ir.Stmt{
			&ir.ExprStmt{X: &ir.Instr{Op: ir.OpCall, Func: "runtime.panicNil"}},
			&ir.Unreachable{},
		}},
	})
	funcTy, err := fl.pkg.ensureFuncType(iterSig, closBaseName)
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
		yieldClos,
		castFn,
	}
	return &ir.Instr{Op: ir.OpCallRef, Type: &ir.Ref{Named: funcTy}, Args: callArgs}, stmts, nil
}

func (fl *funcLower) emitRangeYieldBody(name string, s *ast.RangeStmt, yieldSig *types.Signature, closName string, caps []captureInfo, rf *rangeFrame) error {
	child := &funcLower{
		pkg:         fl.pkg,
		fset:        fl.fset,
		info:        fl.info,
		mod:         fl.mod,
		sig:         yieldSig,
		irName:      name,
		vars:        map[types.Object]*ir.Var{},
		labels:      map[string]*labelTargets{},
		cells:       map[types.Object]*ir.Var{},
		rangeYield:  true,
		rangeFrames: append([]*rangeFrame(nil), fl.rangeFrames...),
	}

	closParam := &ir.Var{Name: "clos", Typ: &ir.Ref{Named: closBaseName}}
	child.params = append(child.params, closParam)

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

	// Yield parameters.
	var paramVars []*ir.Var
	for i := 0; i < yieldSig.Params().Len(); i++ {
		p := yieldSig.Params().At(i)
		ty, err := child.mapType(p.Type())
		if err != nil {
			return err
		}
		pname := p.Name()
		if pname == "" || pname == "_" {
			pname = "yp" + strconv.Itoa(i)
		}
		v := &ir.Var{Name: pname, Typ: ty}
		child.params = append(child.params, v)
		paramVars = append(paramVars, v)
		child.vars[p] = v
	}
	child.results = append(child.results, ir.TypeI32) // bool

	// Bind for x := range … defs to yield params.
	var assignPreamble []ir.Stmt
	if s.Tok == token.DEFINE {
		if id, ok := s.Key.(*ast.Ident); ok && id.Name != "_" {
			if obj := fl.info.Defs[id]; obj != nil && len(paramVars) > 0 {
				child.vars[obj] = paramVars[0]
			}
		}
		if id, ok := s.Value.(*ast.Ident); ok && id.Name != "_" {
			idx := 0
			if s.Key != nil {
				idx = 1
			}
			if obj := fl.info.Defs[id]; obj != nil && idx < len(paramVars) {
				child.vars[obj] = paramVars[idx]
			}
		}
	} else {
		if s.Key != nil && len(paramVars) > 0 {
			as, err := child.assignTo(s.Key, child.useVar(paramVars[0]))
			if err != nil {
				return err
			}
			assignPreamble = append(assignPreamble, as...)
		}
		if s.Value != nil {
			idx := 0
			if s.Key != nil {
				idx = 1
			}
			if idx < len(paramVars) {
				as, err := child.assignTo(s.Value, child.useVar(paramVars[idx]))
				if err != nil {
					return err
				}
				assignPreamble = append(assignPreamble, as...)
			}
		}
	}

	// Entry check (cmd/compile rangefunc):
	//   tmp := state; state = PANIC; if tmp != READY { panicrangestate(tmp) }
	tmpState := child.newVar("rftmp", ir.TypeI32)
	stateEx, err := child.readVar(rf.stateObj)
	if err != nil {
		return err
	}
	entry := []ir.Stmt{
		child.assignVar(tmpState, stateEx),
	}
	setRunning, err := child.writeVar(rf.stateObj, child.i32const(rfPanic))
	if err != nil {
		return err
	}
	entry = append(entry, setRunning...)
	entry = append(entry, &ir.If{
		Cond: &ir.Instr{Op: ir.OpI32Ne, Args: []ir.Expr{child.useVar(tmpState), child.i32const(rfReady)}},
		Then: &ir.Seq{Stmts: []ir.Stmt{
			&ir.ExprStmt{X: &ir.Instr{
				Op:   ir.OpCall,
				Func: "runtime.panicrangestate",
				Args: []ir.Expr{child.useVar(tmpState)},
			}},
			&ir.Unreachable{},
		}},
	})
	entry = append(entry, assignPreamble...)

	child.breakStack = append(child.breakStack, rf.breakL)
	child.continueStack = append(child.continueStack, rf.contL)
	for name, lt := range fl.labels {
		if lt.breakLabel == rf.breakL || lt.continueLabel == rf.contL {
			child.labels[name] = lt
		}
	}

	inner, err := child.stmtBlock(s.Body)
	if err != nil {
		return err
	}

	setReady, err := child.writeVar(rf.stateObj, child.i32const(rfReady))
	if err != nil {
		return err
	}
	setDone, err := child.writeVar(rf.stateObj, child.i32const(rfDone))
	if err != nil {
		return err
	}
	// block $break {
	//   block $cont { body }  // br $cont = continue, br $break = break
	//   state=READY; return true
	// }
	// state=DONE; return false
	contEpi := &ir.Seq{}
	contEpi.Stmts = append(contEpi.Stmts, setReady...)
	contEpi.Stmts = append(contEpi.Stmts, &ir.Return{Results: []ir.Expr{child.i32const(1)}})
	breakEpi := &ir.Seq{}
	breakEpi.Stmts = append(breakEpi.Stmts, setDone...)
	breakEpi.Stmts = append(breakEpi.Stmts, &ir.Return{Results: []ir.Expr{child.i32const(0)}})

	innerStmts := []ir.Stmt{inner}
	if seq, ok := inner.(*ir.Seq); ok {
		innerStmts = seq.Stmts
	}
	bodySeq := &ir.Seq{Stmts: entry}
	bodySeq.Stmts = append(bodySeq.Stmts, &ir.Block{
		Name: rf.breakL,
		Body: &ir.Seq{Stmts: []ir.Stmt{
			&ir.Block{
				Name: rf.contL,
				Body: &ir.Seq{Stmts: innerStmts},
			},
			contEpi,
		}},
	})
	bodySeq.Stmts = append(bodySeq.Stmts, breakEpi)

	for _, v := range child.extraLocals {
		found := false
		for _, st := range preamble {
			if vd, ok := st.(*ir.VarDef); ok && vd.Var == v {
				found = true
				break
			}
		}
		if !found {
			preamble = append(preamble, &ir.VarDef{Var: v})
		}
	}
	for _, ci := range child.cellInits {
		preamble = append(preamble, child.assignVar(ci.cell, &ir.Instr{
			Op:   ir.OpStructNew,
			Type: &ir.Ref{Named: ci.cellTy},
			Args: []ir.Expr{child.useVar(ci.orig)},
		}))
	}
	if child.needsRecoverPrologue {
		preamble = append(preamble, child.recoverPrologue()...)
	} else {
		preamble = append(preamble, child.clearRecoverOk()...)
	}
	bodySeq.Stmts = append(preamble, bodySeq.Stmts...)
	if child.hasDefer {
		var err error
		bodySeq, err = child.applyDeferPass(bodySeq)
		if err != nil {
			return err
		}
	}

	fn := &ir.Func{
		Name:    name,
		Params:  child.params,
		Results: child.results,
		Body:    bodySeq,
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

// rangeYieldReturn rewrites an AST return inside a yield body into storing
// outer results, setting retFlag, DONE, and returning false from yield.
func (fl *funcLower) rangeYieldReturn(s *ast.ReturnStmt) ([]ir.Stmt, error) {
	if len(fl.rangeFrames) == 0 {
		return nil, fl.errorf(s, "range yield return without frame")
	}
	rf := fl.rangeFrames[len(fl.rangeFrames)-1]
	// Use outermost for shared ret/results.
	outer := fl.rangeFrames[0]
	var out []ir.Stmt
	if len(s.Results) > 0 {
		if len(s.Results) != len(outer.resultObjs) {
			return nil, fl.errorf(s, "return value count mismatch for range-over-func")
		}
		for i, r := range s.Results {
			ex, stmts, err := fl.coerceExpr(r, outer.resultGo[i], outer.resultTys[i])
			if err != nil {
				return nil, err
			}
			out = append(out, stmts...)
			ws, err := fl.writeVar(outer.resultObjs[i], ex)
			if err != nil {
				return nil, err
			}
			out = append(out, ws...)
		}
	}
	ws, err := fl.writeVar(outer.retObj, fl.i32const(1))
	if err != nil {
		return nil, err
	}
	out = append(out, ws...)
	ws, err = fl.writeVar(rf.stateObj, fl.i32const(rfDone))
	if err != nil {
		return nil, err
	}
	out = append(out, ws...)
	// Also mark outer frames DONE so a double-yield panics.
	for _, fr := range fl.rangeFrames[:len(fl.rangeFrames)-1] {
		ws, err = fl.writeVar(fr.stateObj, fl.i32const(rfDone))
		if err != nil {
			return nil, err
		}
		out = append(out, ws...)
	}
	out = append(out, &ir.Return{Results: []ir.Expr{fl.i32const(0)}})
	return out, nil
}
