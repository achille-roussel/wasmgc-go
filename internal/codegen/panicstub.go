package codegen

import (
	"github.com/achille-roussel/wasmgc-go/internal/ir"
	"github.com/achille-roussel/wasmgc-go/internal/wasm"
)

// panicIndex / panicSliceB are body-less until M5 EH. Codegen fills empty
// IR bodies (linked by rtlink) with unreachable, and synthesizes missing
// decls when unit tests skip rtlink but RuntimeOps still emits the calls.

func (g *gen) ensurePanicStubs() {
	needIndex, needSliceB := g.scanPanicCalls()
	for _, name := range []string{"runtime.panicIndex", "runtime.panicSliceB"} {
		if f := g.m.LookupFunc(name); f != nil && emptyPrintBody(f) {
			switch name {
			case "runtime.panicIndex":
				needIndex = true
			case "runtime.panicSliceB":
				needSliceB = true
			}
		}
	}
	if needIndex {
		if _, ok := g.funcs["runtime.panicIndex"]; !ok {
			g.panicIndexIdx = g.wm.AddFunc(g.internFuncType(nil, nil))
			g.funcs["runtime.panicIndex"] = g.panicIndexIdx
		}
		if g.m.LookupFunc("runtime.panicIndex") == nil {
			g.stubNeedPanicIndex = true
			g.hasPanicStubs = true
		} else if emptyPrintBody(g.m.LookupFunc("runtime.panicIndex")) {
			g.fillPanicIndex = true
		}
	}
	if needSliceB {
		if _, ok := g.funcs["runtime.panicSliceB"]; !ok {
			g.panicSliceBIdx = g.wm.AddFunc(g.internFuncType(nil, nil))
			g.funcs["runtime.panicSliceB"] = g.panicSliceBIdx
		}
		if g.m.LookupFunc("runtime.panicSliceB") == nil {
			g.stubNeedPanicSliceB = true
			g.hasPanicStubs = true
		} else if emptyPrintBody(g.m.LookupFunc("runtime.panicSliceB")) {
			g.fillPanicSliceB = true
		}
	}
}

func (g *gen) scanPanicCalls() (needIndex, needSliceB bool) {
	for _, f := range g.m.Funcs {
		walkExprs(f.Body, func(e ir.Expr) {
			in, ok := e.(*ir.Instr)
			if !ok || in.Op != ir.OpCall {
				return
			}
			switch in.Func {
			case "runtime.panicIndex":
				needIndex = true
			case "runtime.panicSliceB":
				needSliceB = true
			}
		})
	}
	return
}

func (g *gen) emitPanicStubBodies() {
	if g.stubNeedPanicIndex {
		g.wm.AddCode(bodyUnreachable())
	}
	if g.stubNeedPanicSliceB {
		g.wm.AddCode(bodyUnreachable())
	}
}

func bodyUnreachable() wasm.Code {
	enc := wasm.NewEncoder()
	enc.Unreachable()
	return wasm.CodeFrom(nil, enc)
}
