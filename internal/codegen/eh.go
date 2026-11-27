package codegen

import (
	"fmt"

	"github.com/achille-roussel/wasmgc-go/internal/ir"
	"github.com/achille-roussel/wasmgc-go/internal/wasm"
)

// EH stubs for M5: tag $go.panic, $g, and Wasm bodies for throwPanic,
// deferRun, gorecover, unrecoveredPanic. Message panics are ordinary Go.

func (g *gen) ensureEH() error {
	if !g.scanEHNeeds() {
		return nil
	}
	g.hasEH = true
	g.ensureEHTypes()
	if err := g.ensureGGlobal(); err != nil {
		return err
	}
	if err := g.ensurePanicTag(); err != nil {
		return err
	}
	g.markEHFills()
	g.wrapRunForUnrecovered()
	return nil
}

func (g *gen) ensureEHTypes() {
	// Make sure $go.clos / $go.defer / $go.g exist in the Wasm type table
	// even when the IR module did not declare them (bounds-only programs).
	obj := g.types.ByName["$go.object"]
	if _, ok := g.types.ByName["$go.clos"]; !ok {
		g.types.ByName["$go.clos"] = g.wm.AddType(wasm.SubOpen([]uint32{obj}, wasm.StructType(
			wasm.Field(wasm.StorageVal(wasm.RefNull(wasm.HeapTypeFunc())), false),
		)))
	}
	if _, ok := g.types.ByName["$go.defer"]; !ok {
		clos := g.types.ByName["$go.clos"]
		// Self-ref via placeholder then... use rec for link field.
		deferIdx := g.wm.NumTypes()
		comp := wasm.StructType(
			wasm.Field(wasm.StorageVal(wasm.Ref(wasm.TypeIndexHeap(clos))), false),
			wasm.Field(wasm.StorageVal(wasm.RefNull(wasm.TypeIndexHeap(deferIdx))), true),
		)
		g.types.ByName["$go.defer"] = g.wm.AddRecGroup(wasm.Rec(wasm.SubFinal([]uint32{obj}, comp)))
	}
	if _, ok := g.types.ByName["$go.g"]; !ok {
		panicIdx := g.types.ByName["$go.panicObj"]
		// Match proc.go: id, park, panic, recoverOk, sel, waiting, deferDepth, deferStartDepth.
		selIdx, hasSel := g.types.ByName["$go.select"]
		var selField wasm.FieldType
		if hasSel {
			selField = wasm.Field(wasm.StorageVal(wasm.RefNull(wasm.TypeIndexHeap(selIdx))), true)
		} else {
			selField = wasm.Field(wasm.StorageVal(wasm.RefNull(wasm.HeapTypeAny())), true)
		}
		g.types.ByName["$go.g"] = g.wm.AddType(wasm.SubFinal([]uint32{obj}, wasm.StructType(
			wasm.Field(wasm.StorageVal(wasm.I32()), true),
			wasm.Field(wasm.StorageVal(wasm.I32()), true),
			wasm.Field(wasm.StorageVal(wasm.RefNull(wasm.TypeIndexHeap(panicIdx))), true),
			wasm.Field(wasm.StorageVal(wasm.I32()), true),
			selField,
			wasm.Field(wasm.StorageVal(wasm.I32()), true),
			wasm.Field(wasm.StorageVal(wasm.I32()), true), // deferDepth
			wasm.Field(wasm.StorageVal(wasm.I32()), true), // deferStartDepth
		)))
	}
}

func (g *gen) scanEHNeeds() bool {
	need := false
	for _, f := range g.m.Funcs {
		if regionNeedsEH(f.Body) {
			need = true
		}
		walkExprs(f.Body, func(e ir.Expr) {
			in, ok := e.(*ir.Instr)
			if !ok || in.Op != ir.OpCall {
				return
			}
			switch in.Func {
			case "runtime.throwPanic", "runtime.deferRun", "runtime.gorecover",
				"runtime.unrecoveredPanic", "runtime.panicIndex", "runtime.panicIndexU",
				"runtime.panicBounds", "runtime.panicSliceB",
				"runtime.panicNil", "runtime.panicWrapNil", "runtime.panicDivide", "runtime.panicNegativeShift",
				"runtime.panicUncomparable", "runtime.panicTypeAssert", "runtime.panicTypeAssertTypes",
				"runtime.panicTypeAssertNil", "runtime.panicTypeAssertMissing", "runtime.panicrangestate":
				need = true
			}
		})
	}
	g.ehNeeded = need
	if need {
		// unrecoveredPanic writes "panic: ..." to fd 2 via the linear bridge.
		g.needsMemory = true
		g.needsStringTypes = true
	}
	return need
}

func regionNeedsEH(r ir.Region) bool {
	if r == nil {
		return false
	}
	switch r := r.(type) {
	case *ir.Seq:
		for _, s := range r.Stmts {
			if stmtNeedsEH(s) {
				return true
			}
		}
	case *ir.Block:
		return regionNeedsEH(r.Body)
	case *ir.Loop:
		return regionNeedsEH(r.Body)
	case *ir.If:
		return regionNeedsEH(r.Then) || regionNeedsEH(r.Else)
	case *ir.Switch:
		for _, c := range r.Cases {
			if regionNeedsEH(c.Body) {
				return true
			}
		}
		return regionNeedsEH(r.Default)
	case *ir.Try:
		return true
	case *ir.Dispatch:
		for _, a := range r.Arms {
			if regionNeedsEH(a) {
				return true
			}
		}
	}
	return false
}

func stmtNeedsEH(s ir.Stmt) bool {
	if r, ok := s.(ir.Region); ok {
		return regionNeedsEH(r)
	}
	return false
}

func (g *gen) ensurePanicTag() error {
	if g.panicTagDeclared {
		return nil
	}
	panicIdx, ok := g.types.ByName["$go.panicObj"]
	if !ok {
		return fmt.Errorf("codegen: $go.panicObj missing")
	}
	tagTy := g.wm.AddFuncType([]wasm.ValType{wasm.Ref(wasm.TypeIndexHeap(panicIdx))}, nil)
	g.panicTagIdx = g.wm.AddTag(tagTy)
	g.panicTagDeclared = true
	return nil
}

func (g *gen) ensureGGlobal() error {
	if _, ok := g.globals["$g"]; ok {
		return nil
	}
	// Prefer IR-declared $go.g; otherwise synthesize (8 fields, proc.go).
	gIdx, ok := g.types.ByName["$go.g"]
	if !ok {
		obj := g.types.ByName["$go.object"]
		panicIdx := g.types.ByName["$go.panicObj"]
		selIdx, hasSel := g.types.ByName["$go.select"]
		var selField wasm.FieldType
		if hasSel {
			selField = wasm.Field(wasm.StorageVal(wasm.RefNull(wasm.TypeIndexHeap(selIdx))), true)
		} else {
			selField = wasm.Field(wasm.StorageVal(wasm.RefNull(wasm.HeapTypeAny())), true)
		}
		gIdx = g.wm.AddType(wasm.SubFinal([]uint32{obj}, wasm.StructType(
			wasm.Field(wasm.StorageVal(wasm.I32()), true),
			wasm.Field(wasm.StorageVal(wasm.I32()), true),
			wasm.Field(wasm.StorageVal(wasm.RefNull(wasm.TypeIndexHeap(panicIdx))), true),
			wasm.Field(wasm.StorageVal(wasm.I32()), true),
			selField,
			wasm.Field(wasm.StorageVal(wasm.I32()), true),
			wasm.Field(wasm.StorageVal(wasm.I32()), true), // deferDepth
			wasm.Field(wasm.StorageVal(wasm.I32()), true), // deferStartDepth
		)))
		g.types.ByName["$go.g"] = gIdx
	}
	// If IR declares $g with a null/empty init, replace with a real main g so
	// defer/recover can read recoverOk. Sched init may overwrite later.
	if gl := g.m.LookupGlobal("$g"); gl != nil {
		if isNullRefInit(gl.Init) {
			gl.Init = g.synthGInitExpr()
		}
		return nil // emitGlobals will emit it
	}
	enc := wasm.NewEncoder()
	if err := g.writeGInit(enc, gIdx); err != nil {
		return err
	}
	idx := g.wm.AddGlobal(wasm.GlobalType{
		Type:    wasm.Ref(wasm.TypeIndexHeap(gIdx)),
		Mutable: true,
	}, enc.Bytes())
	g.globals["$g"] = idx
	return nil
}

func isNullRefInit(e ir.Expr) bool {
	if e == nil {
		return true
	}
	in, ok := e.(*ir.Instr)
	return ok && in.Op == ir.OpRefNull
}

func (g *gen) synthGInitExpr() ir.Expr {
	gTy := &ir.Ref{Named: "$go.g"}
	args := []ir.Expr{
		&ir.Const{Op: ir.OpI32Const, I64: 1},
		&ir.Const{Op: ir.OpI32Const, I64: 0},
		&ir.Instr{Op: ir.OpRefNull, Type: &ir.Ref{Null: true, Named: "$go.panicObj"}},
		&ir.Const{Op: ir.OpI32Const, I64: 0},
		&ir.Instr{Op: ir.OpRefNull, Type: &ir.Ref{Null: true, Named: "$go.select"}},
		&ir.Const{Op: ir.OpI32Const, I64: 0},
		&ir.Const{Op: ir.OpI32Const, I64: 0}, // deferDepth
		&ir.Const{Op: ir.OpI32Const, I64: 0}, // deferStartDepth
	}
	return &ir.Instr{Op: ir.OpStructNew, Type: gTy, Args: args}
}

func (g *gen) writeGInit(enc *wasm.Encoder, gIdx uint32) error {
	enc.I32Const(1) // id
	enc.I32Const(0) // park
	enc.RefNull(wasm.TypeIndexHeap(g.types.ByName["$go.panicObj"]))
	enc.I32Const(0) // recoverOk
	if selIdx, ok := g.types.ByName["$go.select"]; ok {
		enc.RefNull(wasm.TypeIndexHeap(selIdx))
	} else {
		enc.RefNull(wasm.HeapTypeAny())
	}
	enc.I32Const(0) // waiting
	enc.I32Const(0) // deferDepth
	enc.I32Const(0) // deferStartDepth
	enc.StructNew(gIdx)
	return nil
}

func (g *gen) markEHFills() {
	for _, name := range []string{
		"runtime.throwPanic", "runtime.deferRun", "runtime.gorecover",
		"runtime.unrecoveredPanic", "runtime.concatstrings",
	} {
		f := g.m.LookupFunc(name)
		if f == nil {
			// Do not resurrect deferRun/gorecover after DCE — map/print-only
			// programs never call them. Still synth throw/unrecovered/concat
			// for unit tests that skip rtlink but need the panic catch path.
			switch name {
			case "runtime.deferRun", "runtime.gorecover":
				continue
			}
			g.synthEHFunc(name)
			f = g.m.LookupFunc(name)
		}
		if f != nil && emptyPrintBody(f) {
			g.ehFill[name] = true
			g.hasEHStubs = true
		}
	}
	// semaPtrSame is only linked with the scheduler runtime; fill when present,
	// never synthesize into modules that do not need semaphores.
	if f := g.m.LookupFunc("runtime.semaPtrSame"); f != nil && emptyPrintBody(f) {
		g.ehFill["runtime.semaPtrSame"] = true
		g.hasEHStubs = true
	}
}

func (g *gen) synthEHFunc(name string) {
	iface := typeIfaceRef()
	panicRef := &ir.Ref{Named: "$go.panicObj"}
	deferRef := &ir.Ref{Null: true, Named: "$go.defer"}
	str := &ir.Ref{Named: "$go.string"}
	var f *ir.Func
	switch name {
	case "runtime.throwPanic":
		f = &ir.Func{Name: name, Params: []*ir.Var{{Name: "v", Typ: iface}}, Body: &ir.Seq{}}
	case "runtime.gorecover":
		f = &ir.Func{Name: name, Params: []*ir.Var{{Name: "allow", Typ: ir.TypeI32}}, Results: []ir.Type{iface}, Body: &ir.Seq{}}
	case "runtime.deferRun":
		f = &ir.Func{Name: name, Params: []*ir.Var{{Name: "d", Typ: deferRef}}, Body: &ir.Seq{}}
	case "runtime.unrecoveredPanic":
		f = &ir.Func{Name: name, Params: []*ir.Var{{Name: "p", Typ: panicRef}}, Body: &ir.Seq{}}
	case "runtime.concatstrings":
		f = &ir.Func{Name: name, Params: []*ir.Var{{Name: "a", Typ: str}, {Name: "b", Typ: str}}, Results: []ir.Type{str}, Body: &ir.Seq{}}
	case "runtime.semaPtrSame":
		ptr := &ir.Ref{Null: true, Named: "$go.ptr.i32"}
		f = &ir.Func{Name: name, Params: []*ir.Var{{Name: "a", Typ: ptr}, {Name: "b", Typ: ptr}}, Results: []ir.Type{ir.TypeI32}, Body: &ir.Seq{}}
	default:
		return
	}
	g.m.Funcs = append(g.m.Funcs, f)
	// Declare wasm func index if declareFuncs already ran.
	if _, ok := g.funcs[name]; !ok {
		pts := make([]ir.Type, len(f.Params))
		for i, p := range f.Params {
			pts[i] = p.Typ
		}
		params, _ := g.valTypes(pts)
		results, _ := g.valTypes(f.Results)
		g.funcs[name] = g.wm.AddFunc(g.internFuncType(params, results))
	}
}

func typeIfaceRef() ir.Type {
	return &ir.Ref{Null: true, Named: "$go.iface"}
}

func (g *gen) wrapRunForUnrecovered() {
	// Goroutine entries that can throw $go.panic: main.run and _sched.
	// An unrecovered panic in either exits 2 with "panic: ..." (04).
	g.wrapEntryForUnrecovered("main.run")
	g.wrapEntryForUnrecovered("runtime.sched")
}

func (g *gen) wrapEntryForUnrecovered(name string) {
	fn := g.m.LookupFunc(name)
	if fn == nil || fn.Body == nil {
		return
	}
	if seq, ok := fn.Body.(*ir.Seq); ok {
		for _, s := range seq.Stmts {
			if _, ok := s.(*ir.Try); ok {
				return
			}
		}
	}
	payload := &ir.Var{Name: "pobj", Typ: &ir.Ref{Named: "$go.panicObj"}}
	exn := &ir.Var{Name: "exn", Typ: ir.TypeExn}
	// Try params bind pobj/exn in the handler; do not VarDef them again.
	// Skip unrecoveredPanic when the payload was already recover()'d — a
	// recovered panic can still reach this wrapper after cross-frame defer
	// rethrows; printing/exiting would fail a successful recover.
	rec := &ir.Var{Name: "rec", Typ: ir.TypeI32}
	handler := &ir.Seq{Stmts: []ir.Stmt{
		&ir.VarDef{Var: rec},
		&ir.Assign{Lhs: []ir.Dest{{Name: "rec", Var: rec}}, Rhs: &ir.Instr{
			Op: ir.OpStructGet, Type: &ir.Ref{Named: "$go.panicObj"}, Field: "recovered",
			Args: []ir.Expr{&ir.Name{Raw: payload.Name, Var: payload}},
		}},
		&ir.If{
			Cond: &ir.Name{Raw: "rec", Var: rec},
			Then: &ir.Seq{Stmts: []ir.Stmt{&ir.Return{}}},
			Else: &ir.Seq{Stmts: []ir.Stmt{
				&ir.ExprStmt{X: &ir.Instr{Op: ir.OpCall, Func: "runtime.unrecoveredPanic", Args: []ir.Expr{
					&ir.Name{Raw: payload.Name, Var: payload},
				}}},
				&ir.Unreachable{},
			}},
		},
	}}
	try := &ir.Try{
		Body:    fn.Body,
		Tag:     "$go.panic",
		Params:  []*ir.Var{payload, exn},
		Handler: handler,
	}
	fn.Body = &ir.Seq{Stmts: []ir.Stmt{try}}
}

func (fe *funcEmitter) emitTry(r *ir.Try) error {
	if !fe.g.panicTagDeclared {
		if err := fe.g.ensurePanicTag(); err != nil {
			return err
		}
	}
	if len(r.Params) < 2 {
		return fmt.Errorf("try handler needs payload and exn params")
	}
	payload, exn := r.Params[0], r.Params[1]
	fe.ensureLocal(payload)
	fe.ensureLocal(exn)

	payloadVT, err := fe.g.valType(payload.Typ)
	if err != nil {
		return err
	}
	handlerTy := fe.g.internFuncType(nil, []wasm.ValType{payloadVT, wasm.ExnRef()})

	outer := r.Name
	if outer == "" {
		outer = "$try.outer"
	}
	handler := "$try.handler"

	fe.labels = append(fe.labels, outer)
	fe.enc.Block(wasm.BlockEmpty())

	fe.labels = append(fe.labels, handler)
	fe.enc.Block(wasm.BlockTypeIndex(handlerTy))
	fe.enc.TryTable(wasm.BlockEmpty(), []wasm.CatchClause{{
		Kind:     wasm.CatchRef,
		TagIdx:   fe.g.panicTagIdx,
		LabelIdx: 0,
	}})
	if r.Body != nil {
		if err := fe.emitRegion(r.Body); err != nil {
			return err
		}
	}
	fe.enc.End()
	fe.enc.Unreachable()
	fe.enc.End()
	fe.labels = fe.labels[:len(fe.labels)-1]

	fe.enc.LocalSet(fe.localOf[exn.Name])
	fe.enc.LocalSet(fe.localOf[payload.Name])
	if r.Handler != nil {
		if err := fe.emitRegion(r.Handler); err != nil {
			return err
		}
	}
	fe.enc.End()
	fe.labels = fe.labels[:len(fe.labels)-1]
	// Handler typically ends with `return`, which leaves this empty outer
	// block's continuation reachable for validation with a cleared
	// unreachable flag and an empty stack. Functions with results need a
	// value here — mark the fallthrough unreachable (same as try's normal
	// edge after all returns).
	if len(fe.fn.Results) > 0 {
		fe.enc.Unreachable()
	}
	return nil
}

func (fe *funcEmitter) ensureLocal(v *ir.Var) {
	if _, ok := fe.localOf[v.Name]; ok {
		return
	}
	_ = fe.addLocal(v.Name, v.Typ)
}

func (g *gen) bodyThrowPanic() wasm.Code {
	panicObj := g.types.ByName["$go.panicObj"]
	gTy := g.types.ByName["$go.g"]
	enc := wasm.NewEncoder()
	// panicObj{value, recovered=0, next=$g.panic, depth=$g.deferDepth}
	enc.LocalGet(0)
	enc.I32Const(0)
	enc.GlobalGet(g.globals["$g"])
	enc.StructGet(gTy, 2)
	enc.GlobalGet(g.globals["$g"])
	enc.StructGet(gTy, 6) // deferDepth
	enc.StructNew(panicObj)
	enc.LocalTee(1)
	enc.GlobalGet(g.globals["$g"])
	enc.LocalGet(1)
	enc.StructSet(gTy, 2)
	enc.LocalGet(1)
	enc.Throw(g.panicTagIdx)
	enc.Unreachable()
	return wasm.CodeFrom([]wasm.Local{
		{Count: 1, Type: wasm.Ref(wasm.TypeIndexHeap(panicObj))},
	}, enc)
}

func (g *gen) bodyGorecover() wasm.Code {
	iface := g.types.ByName["$go.iface"]
	panicObj := g.types.ByName["$go.panicObj"]
	gTy := g.types.ByName["$go.g"]
	enc := wasm.NewEncoder()
	enc.LocalGet(0)
	enc.If(wasm.BlockVal(wasm.RefNull(wasm.TypeIndexHeap(iface))))
	enc.GlobalGet(g.globals["$g"])
	enc.StructGet(gTy, 2)
	enc.RefIsNull()
	enc.If(wasm.BlockVal(wasm.RefNull(wasm.TypeIndexHeap(iface))))
	enc.RefNull(wasm.TypeIndexHeap(iface))
	enc.Else()
	enc.GlobalGet(g.globals["$g"])
	enc.StructGet(gTy, 2)
	enc.RefAsNonNull()
	enc.LocalSet(1)
	enc.LocalGet(1)
	enc.StructGet(panicObj, 1)
	enc.I32Eqz()
	enc.If(wasm.BlockVal(wasm.RefNull(wasm.TypeIndexHeap(iface))))
	// Mark recovered but keep $g.panic pointing here so a second recover()
	// in the same deferred frame sees p.recovered and returns nil (recover1).
	// deferRun unwinds the recovered chain after the call returns.
	enc.LocalGet(1)
	enc.I32Const(1)
	enc.StructSet(panicObj, 1)
	enc.LocalGet(1)
	enc.StructGet(panicObj, 0)
	enc.Else()
	enc.RefNull(wasm.TypeIndexHeap(iface))
	enc.End()
	enc.End()
	enc.Else()
	enc.RefNull(wasm.TypeIndexHeap(iface))
	enc.End()
	return wasm.CodeFrom([]wasm.Local{
		{Count: 1, Type: wasm.RefNull(wasm.TypeIndexHeap(panicObj))},
	}, enc)
}

func (g *gen) bodyDeferRun() wasm.Code {
	// Walk LIFO. Each call_ref is wrapped in try_table so a re-panic
	// updates the current panic and the remaining defers still run (04).
	deferTy, ok1 := g.types.ByName["$go.defer"]
	closTy, ok2 := g.types.ByName["$go.clos"]
	gTy := g.types.ByName["$go.g"]
	panicObj := g.types.ByName["$go.panicObj"]
	if !ok1 || !ok2 {
		enc := wasm.NewEncoder()
		enc.Return()
		return wasm.CodeFrom(nil, enc)
	}
	thunkTy := g.internFuncType(
		[]wasm.ValType{wasm.Ref(wasm.TypeIndexHeap(closTy))},
		nil,
	)
	catchTy := g.internFuncType(nil, []wasm.ValType{
		wasm.Ref(wasm.TypeIndexHeap(panicObj)),
		wasm.ExnRef(),
	})
	enc := wasm.NewEncoder()
	// locals: 0=d (param), 1=clos, 2=pobj, 3=exn, 4=prior (panic before call),
	// 5=startPanic ($g.panic at deferRun entry), 6=walk (ancestor walk)
	// Record entry depth then deferDepth++. defer recover() only succeeds when
	// panic.depth < deferStartDepth (panic from an outer frame).
	enc.GlobalGet(g.globals["$g"])
	enc.GlobalGet(g.globals["$g"])
	enc.StructGet(gTy, 6) // deferDepth
	enc.StructSet(gTy, 7) // deferStartDepth =
	enc.GlobalGet(g.globals["$g"])
	enc.GlobalGet(g.globals["$g"])
	enc.StructGet(gTy, 6)
	enc.I32Const(1)
	enc.I32Add()
	enc.StructSet(gTy, 6)
	// Snapshot entry panic. End-of-deferRun must not rethrow an inherited
	// outer panic that was already pending (or an ancestor left after a
	// nested recover) — that aborts a still-running deferred frame
	// (recover.go test111 / badstate). The caller's try handler rethrows
	// when it is responsible for the panic.
	enc.GlobalGet(g.globals["$g"])
	enc.StructGet(gTy, 2)
	enc.LocalSet(5) // startPanic
	enc.Block(wasm.BlockEmpty()) // $done
	enc.Loop(wasm.BlockEmpty())  // $walk
	enc.LocalGet(0)
	enc.RefIsNull()
	enc.BrIf(1) // → $done
	enc.LocalGet(0)
	enc.RefAsNonNull()
	enc.StructGet(deferTy, 0)
	enc.LocalSet(1)
	enc.LocalGet(0)
	enc.RefAsNonNull()
	enc.StructGet(deferTy, 1)
	enc.LocalSet(0)
	// Snapshot panic before the deferred call. A fresh nested panic from
	// this call chains prior as next; abort that prior so recover of the
	// new panic does not resume it (issue43942). A rethrow of the same
	// panicObj keeps prior==pobj and must not abort the outer chain
	// (recover1 nested recover of panic 1 after recovering panic 2).
	enc.GlobalGet(g.globals["$g"])
	enc.StructGet(gTy, 2)
	enc.LocalSet(4)
	enc.GlobalGet(g.globals["$g"])
	enc.I32Const(1)
	enc.StructSet(gTy, 3)
	enc.Block(wasm.BlockEmpty()) // $cont
	enc.Block(wasm.BlockTypeIndex(catchTy)) // $handler
	enc.TryTable(wasm.BlockEmpty(), []wasm.CatchClause{{
		Kind:     wasm.CatchRef,
		TagIdx:   g.panicTagIdx,
		LabelIdx: 0, // → $handler
	}})
	enc.LocalGet(1)
	enc.LocalGet(1)
	enc.StructGet(closTy, 0)
	enc.RefCast(wasm.TypeIndexHeap(thunkTy))
	enc.CallRef(thunkTy)
	enc.End() // try_table
	enc.Br(1) // → $cont (normal)
	enc.Unreachable()
	enc.End() // $handler
	enc.LocalSet(3) // exn
	enc.LocalSet(2) // pobj — already on $g via throwPanic
	enc.LocalGet(3)
	enc.Drop()
	// Abort prior when this catch is a nested panic that chained it.
	enc.LocalGet(4)
	enc.RefIsNull()
	enc.If(wasm.BlockEmpty())
	enc.Else()
	enc.LocalGet(2)
	enc.LocalGet(4)
	enc.RefEq()
	enc.If(wasm.BlockEmpty())
	// Same object rethrown — keep chain.
	enc.Else()
	enc.LocalGet(2)
	enc.StructGet(panicObj, 2) // next
	enc.LocalGet(4)
	enc.RefEq()
	enc.If(wasm.BlockEmpty())
	enc.LocalGet(4)
	enc.RefAsNonNull()
	enc.I32Const(1)
	enc.StructSet(panicObj, 1)
	enc.End()
	enc.End()
	enc.End()
	enc.End() // $cont
	enc.GlobalGet(g.globals["$g"])
	enc.I32Const(0)
	enc.StructSet(gTy, 3)
	// Unwind recovered/aborted panics after each call so a later defer
	// (e.g. defer recover()) can see the next live panic (recover1 test6).
	enc.Block(wasm.BlockEmpty()) // $unwindDone
	enc.Loop(wasm.BlockEmpty())  // $unwind
	enc.GlobalGet(g.globals["$g"])
	enc.StructGet(gTy, 2)
	enc.RefIsNull()
	enc.BrIf(1)
	enc.GlobalGet(g.globals["$g"])
	enc.StructGet(gTy, 2)
	enc.RefAsNonNull()
	enc.StructGet(panicObj, 1)
	enc.I32Eqz()
	enc.BrIf(1)
	enc.GlobalGet(g.globals["$g"])
	enc.GlobalGet(g.globals["$g"])
	enc.StructGet(gTy, 2)
	enc.RefAsNonNull()
	enc.StructGet(panicObj, 2)
	enc.StructSet(gTy, 2)
	enc.Br(0)
	enc.End()
	enc.End()
	enc.Br(0) // → $walk
	enc.End() // $walk
	enc.End() // $done
	// deferDepth--
	enc.GlobalGet(g.globals["$g"])
	enc.GlobalGet(g.globals["$g"])
	enc.StructGet(gTy, 6)
	enc.I32Const(1)
	enc.I32Sub()
	enc.StructSet(gTy, 6)
	// Rethrow a live panic only when it is new relative to entry:
	//   - $g.panic == startPanic → leave for caller (handler rethrows if
	//     this invocation was for that panic; normal return during an
	//     outer panic must not abort the deferred frame — recover test111)
	//   - $g.panic is a proper ancestor of startPanic → nested recover left
	//     the outer panic; leave it for the deferred caller
	//   - otherwise → new unrecovered panic from a defer; rethrow
	enc.GlobalGet(g.globals["$g"])
	enc.StructGet(gTy, 2)
	enc.RefIsNull()
	enc.If(wasm.BlockEmpty())
	enc.Else()
	enc.GlobalGet(g.globals["$g"])
	enc.StructGet(gTy, 2)
	enc.RefAsNonNull()
	enc.StructGet(panicObj, 1) // recovered?
	enc.If(wasm.BlockEmpty())
	enc.Else()
	// live unrecovered
	enc.LocalGet(5) // startPanic
	enc.RefIsNull()
	enc.If(wasm.BlockEmpty())
	// no entry panic → any live panic is new
	enc.GlobalGet(g.globals["$g"])
	enc.StructGet(gTy, 2)
	enc.RefAsNonNull()
	enc.Throw(g.panicTagIdx)
	enc.Unreachable()
	enc.Else()
	enc.GlobalGet(g.globals["$g"])
	enc.StructGet(gTy, 2)
	enc.LocalGet(5)
	enc.RefEq()
	enc.If(wasm.BlockEmpty())
	// same as entry — caller decides
	enc.Else()
	// walk startPanic.next* for $g.panic (proper ancestor?)
	enc.LocalGet(5)
	enc.LocalSet(6) // walk
	enc.Block(wasm.BlockEmpty()) // $ancDone
	enc.Loop(wasm.BlockEmpty())  // $ancWalk
	enc.LocalGet(6)
	enc.RefIsNull()
	enc.BrIf(1)
	enc.LocalGet(6)
	enc.RefAsNonNull()
	enc.StructGet(panicObj, 2) // next
	enc.LocalSet(6)
	enc.LocalGet(6)
	enc.RefIsNull()
	enc.BrIf(1)
	enc.LocalGet(6)
	enc.GlobalGet(g.globals["$g"])
	enc.StructGet(gTy, 2)
	enc.RefEq()
	enc.If(wasm.BlockEmpty())
	enc.Br(2) // ancestor → $ancDone, no throw
	enc.End()
	enc.Br(0)
	enc.End() // $ancWalk
	enc.End() // $ancDone
	// not an ancestor → new panic
	enc.LocalGet(6)
	enc.GlobalGet(g.globals["$g"])
	enc.StructGet(gTy, 2)
	enc.RefEq()
	enc.If(wasm.BlockEmpty())
	// walked onto $g.panic (ancestor case already branched)
	enc.Else()
	enc.GlobalGet(g.globals["$g"])
	enc.StructGet(gTy, 2)
	enc.RefAsNonNull()
	enc.Throw(g.panicTagIdx)
	enc.Unreachable()
	enc.End()
	enc.End() // not same as entry
	enc.End() // startPanic non-null
	enc.End() // not recovered
	enc.End() // panic non-null
	return wasm.CodeFrom([]wasm.Local{
		{Count: 1, Type: wasm.Ref(wasm.TypeIndexHeap(closTy))},
		{Count: 1, Type: wasm.Ref(wasm.TypeIndexHeap(panicObj))},
		{Count: 1, Type: wasm.ExnRef()},
		{Count: 1, Type: wasm.RefNull(wasm.TypeIndexHeap(panicObj))}, // prior
		{Count: 1, Type: wasm.RefNull(wasm.TypeIndexHeap(panicObj))}, // startPanic
		{Count: 1, Type: wasm.RefNull(wasm.TypeIndexHeap(panicObj))}, // walk
	}, enc)
}

func (g *gen) bodyUnrecoveredPanic() wasm.Code {
	// Print "panic: " + string-or-placeholder + "\n\n" to stderr (fd 2), then exit(2).
	strTy := g.types.ByName["$go.string"]
	iface := g.types.ByName["$go.iface"]
	panicObj := g.types.ByName["$go.panicObj"]
	bytesTy := g.types.ByName["$go.bytes"]
	enc := wasm.NewEncoder()

	// Locals after param 0 (panicObj):
	// 1=bytes scratch, 2=data, 3=off, 4=len, 5=i, 6=ptr, 7=str temp
	const (
		locBytes = 1
		locData  = 2
		locOff   = 3
		locLen   = 4
		locI     = 5
		locPtr   = 6
		locStr   = 7
	)

	writeStr := func() {
		enc.LocalGet(locStr)
		enc.StructGet(strTy, 0)
		enc.LocalSet(locData)
		enc.LocalGet(locStr)
		enc.StructGet(strTy, 1)
		enc.LocalSet(locOff)
		enc.LocalGet(locStr)
		enc.StructGet(strTy, 2)
		enc.LocalSet(locLen)
		enc.I32Const(0)
		enc.LocalTee(locPtr)
		if g.hasMemBump {
			enc.GlobalSet(g.memBumpIdx)
		} else {
			enc.Drop()
		}
		enc.Block(wasm.BlockEmpty())
		enc.Loop(wasm.BlockEmpty())
		enc.MemorySize()
		enc.I32Const(65536)
		enc.I32Mul()
		enc.LocalGet(locLen)
		enc.I32GeU()
		enc.BrIf(1)
		enc.I32Const(1)
		enc.MemoryGrow()
		enc.Drop()
		enc.Br(0)
		enc.End()
		enc.End()
		enc.I32Const(0)
		enc.LocalSet(locI)
		enc.Block(wasm.BlockEmpty())
		enc.Loop(wasm.BlockEmpty())
		enc.LocalGet(locI)
		enc.LocalGet(locLen)
		enc.I32GeU()
		enc.BrIf(1)
		enc.LocalGet(locPtr)
		enc.LocalGet(locI)
		enc.I32Add()
		enc.LocalGet(locData)
		enc.LocalGet(locOff)
		enc.LocalGet(locI)
		enc.I32Add()
		enc.ArrayGetU(bytesTy)
		enc.I32Store8(0, 0)
		enc.LocalGet(locI)
		enc.I32Const(1)
		enc.I32Add()
		enc.LocalSet(locI)
		enc.Br(0)
		enc.End()
		enc.End()
		if g.hasWrite {
			enc.I32Const(2) // stderr
			enc.LocalGet(locPtr)
			enc.LocalGet(locLen)
			enc.Call(g.importWrite)
			enc.Drop()
		}
		if g.hasMemBump {
			enc.I32Const(0)
			enc.GlobalSet(g.memBumpIdx)
		}
	}

	printLit := func(s string) {
		enc.I32Const(int32(len(s)))
		enc.ArrayNewDefault(bytesTy)
		enc.LocalSet(locBytes)
		for i := 0; i < len(s); i++ {
			enc.LocalGet(locBytes)
			enc.I32Const(int32(i))
			enc.I32Const(int32(s[i]))
			enc.ArraySet(bytesTy)
		}
		enc.LocalGet(locBytes)
		enc.I32Const(0)
		enc.I32Const(int32(len(s)))
		enc.StructNew(strTy)
		enc.LocalSet(locStr)
		writeStr()
	}

	printLit("panic: ")
	enc.LocalGet(0)
	enc.StructGet(panicObj, 0)
	enc.RefIsNull()
	enc.If(wasm.BlockEmpty())
	printLit("<nil>")
	enc.Else()
	enc.LocalGet(0)
	enc.StructGet(panicObj, 0)
	enc.StructGet(iface, 1)
	enc.RefIsNull()
	enc.If(wasm.BlockEmpty())
	printLit("<panic>")
	enc.Else()
	// String payloads print as-is. runtime.plainError {msg string} is the
	// usual recoverable panic value — extract msg without calling Error().
	enc.LocalGet(0)
	enc.StructGet(panicObj, 0)
	enc.StructGet(iface, 1)
	enc.RefTest(wasm.TypeIndexHeap(strTy))
	enc.If(wasm.BlockEmpty())
	enc.LocalGet(0)
	enc.StructGet(panicObj, 0)
	enc.StructGet(iface, 1)
	enc.RefCast(wasm.TypeIndexHeap(strTy))
	enc.LocalSet(locStr)
	writeStr()
	enc.Else()
	plainTy, hasPlain := g.types.ByName["$go.struct.runtime.plainError"]
	if hasPlain {
		enc.LocalGet(0)
		enc.StructGet(panicObj, 0)
		enc.StructGet(iface, 1)
		enc.RefTest(wasm.TypeIndexHeap(plainTy))
		enc.If(wasm.BlockEmpty())
		enc.LocalGet(0)
		enc.StructGet(panicObj, 0)
		enc.StructGet(iface, 1)
		enc.RefCast(wasm.TypeIndexHeap(plainTy))
		enc.StructGet(plainTy, 0) // msg
		enc.LocalSet(locStr)
		writeStr()
		enc.Else()
		printLit("<non-string>")
		enc.End()
	} else {
		printLit("<non-string>")
	}
	enc.End()
	enc.End()
	enc.End()
	printLit("\n\n")
	if g.hasExit {
		enc.I32Const(2)
		enc.Call(g.exitIdx)
	}
	enc.Unreachable()
	return wasm.CodeFrom([]wasm.Local{
		{Count: 1, Type: wasm.Ref(wasm.TypeIndexHeap(bytesTy))},
		{Count: 1, Type: wasm.Ref(wasm.TypeIndexHeap(bytesTy))},
		{Count: 3, Type: wasm.I32()},
		{Count: 1, Type: wasm.I32()},
		{Count: 1, Type: wasm.Ref(wasm.TypeIndexHeap(strTy))},
	}, enc)
}

func (g *gen) bodyConcatStrings() wasm.Code {
	strTy := g.types.ByName["$go.string"]
	bytesTy := g.types.ByName["$go.bytes"]
	enc := wasm.NewEncoder()
	enc.LocalGet(0)
	enc.StructGet(strTy, 2)
	enc.LocalGet(1)
	enc.StructGet(strTy, 2)
	enc.I32Add()
	enc.LocalSet(2)
	enc.LocalGet(2)
	enc.I32Eqz()
	enc.If(wasm.BlockVal(wasm.Ref(wasm.TypeIndexHeap(strTy))))
	enc.I32Const(0)
	enc.ArrayNewDefault(bytesTy)
	enc.I32Const(0)
	enc.I32Const(0)
	enc.StructNew(strTy)
	enc.Else()
	enc.LocalGet(2)
	enc.ArrayNewDefault(bytesTy)
	enc.LocalSet(3)
	enc.LocalGet(3)
	enc.I32Const(0)
	enc.LocalGet(0)
	enc.StructGet(strTy, 0)
	enc.LocalGet(0)
	enc.StructGet(strTy, 1)
	enc.LocalGet(0)
	enc.StructGet(strTy, 2)
	enc.ArrayCopy(bytesTy, bytesTy)
	enc.LocalGet(3)
	enc.LocalGet(0)
	enc.StructGet(strTy, 2)
	enc.LocalGet(1)
	enc.StructGet(strTy, 0)
	enc.LocalGet(1)
	enc.StructGet(strTy, 1)
	enc.LocalGet(1)
	enc.StructGet(strTy, 2)
	enc.ArrayCopy(bytesTy, bytesTy)
	enc.LocalGet(3)
	enc.I32Const(0)
	enc.LocalGet(2)
	enc.StructNew(strTy)
	enc.End()
	return wasm.CodeFrom([]wasm.Local{
		{Count: 1, Type: wasm.I32()},
		{Count: 1, Type: wasm.Ref(wasm.TypeIndexHeap(bytesTy))},
	}, enc)
}

// bodySemaPtrSame: compare $go.ptr.i32 base (ref.eq) and index (i32.eq).
// Nulls: both null → 1; either null → 0.
func (g *gen) bodySemaPtrSame() wasm.Code {
	ptrTy := g.types.ByName["$go.ptr.i32"]
	enc := wasm.NewEncoder()
	// if a == null
	enc.LocalGet(0)
	enc.RefIsNull()
	enc.If(wasm.BlockVal(wasm.I32()))
	enc.LocalGet(1)
	enc.RefIsNull() // both null → 1
	enc.Else()
	// a non-null; if b null → 0
	enc.LocalGet(1)
	enc.RefIsNull()
	enc.If(wasm.BlockVal(wasm.I32()))
	enc.I32Const(0)
	enc.Else()
	// both non-null: base eq && index eq (cast anyref → eqref for ref.eq)
	enc.LocalGet(0)
	enc.StructGet(ptrTy, 1) // base
	enc.RefCastNull(wasm.HeapTypeEq())
	enc.LocalGet(1)
	enc.StructGet(ptrTy, 1)
	enc.RefCastNull(wasm.HeapTypeEq())
	enc.RefEq()
	enc.LocalGet(0)
	enc.StructGet(ptrTy, 2) // index
	enc.LocalGet(1)
	enc.StructGet(ptrTy, 2)
	enc.I32Eq()
	enc.I32And()
	enc.End()
	enc.End()
	return wasm.CodeFrom(nil, enc)
}
