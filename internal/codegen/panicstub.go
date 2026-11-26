package codegen

import (
	"github.com/achille-roussel/wasmgc-go/internal/ir"
	"github.com/achille-roussel/wasmgc-go/internal/wasm"
)

// panicIndex / panicSliceB stubs for unit tests that skip rtlink.
// With EH enabled they throw; otherwise unreachable (pre-M5 behavior).

func (g *gen) ensurePanicStubs() {
	needIndex, needSliceB, needRange, needNil := g.scanPanicCalls()
	for _, name := range []string{"runtime.panicIndex", "runtime.panicSliceB", "runtime.panicrangestate", "runtime.panicNil"} {
		if f := g.m.LookupFunc(name); f != nil && emptyPrintBody(f) {
			switch name {
			case "runtime.panicIndex":
				needIndex = true
			case "runtime.panicSliceB":
				needSliceB = true
			case "runtime.panicrangestate":
				needRange = true
			case "runtime.panicNil":
				needNil = true
			}
		}
	}
	if needIndex {
		if _, ok := g.funcs["runtime.panicIndex"]; !ok {
			g.panicIndexIdx = g.wm.AddFunc(g.internFuncType([]wasm.ValType{wasm.I64(), wasm.I32()}, nil))
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
	if needRange {
		if _, ok := g.funcs["runtime.panicrangestate"]; !ok {
			g.panicRangeIdx = g.wm.AddFunc(g.internFuncType([]wasm.ValType{wasm.I32()}, nil))
			g.funcs["runtime.panicrangestate"] = g.panicRangeIdx
		}
		if g.m.LookupFunc("runtime.panicrangestate") == nil {
			g.stubNeedPanicRange = true
			g.hasPanicStubs = true
		} else if emptyPrintBody(g.m.LookupFunc("runtime.panicrangestate")) {
			g.fillPanicRange = true
		}
	}
	if needNil {
		if _, ok := g.funcs["runtime.panicNil"]; !ok {
			g.panicNilIdx = g.wm.AddFunc(g.internFuncType(nil, nil))
			g.funcs["runtime.panicNil"] = g.panicNilIdx
		}
		if g.m.LookupFunc("runtime.panicNil") == nil {
			g.stubNeedPanicNil = true
			g.hasPanicStubs = true
		} else if emptyPrintBody(g.m.LookupFunc("runtime.panicNil")) {
			g.fillPanicNil = true
		}
	}
}

func (g *gen) scanPanicCalls() (needIndex, needSliceB, needRange, needNil bool) {
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
			case "runtime.panicrangestate":
				needRange = true
			case "runtime.panicNil":
				needNil = true
			}
		})
	}
	return
}

func (g *gen) emitPanicStubBodies() {
	if g.stubNeedPanicIndex {
		if g.hasEH {
			g.wm.AddCode(g.bodyThrowNullPanic())
		} else {
			g.wm.AddCode(bodyUnreachable())
		}
	}
	if g.stubNeedPanicSliceB {
		if g.hasEH {
			g.wm.AddCode(g.bodyThrowNullPanic())
		} else {
			g.wm.AddCode(bodyUnreachable())
		}
	}
	if g.stubNeedPanicRange {
		if g.hasEH {
			g.wm.AddCode(g.bodyThrowNullPanic())
		} else {
			g.wm.AddCode(bodyUnreachable())
		}
	}
	if g.stubNeedPanicNil {
		if g.hasEH {
			g.wm.AddCode(g.bodyThrowNullPanic())
		} else {
			g.wm.AddCode(bodyUnreachable())
		}
	}
}

func bodyUnreachable() wasm.Code {
	enc := wasm.NewEncoder()
	enc.Unreachable()
	return wasm.CodeFrom(nil, enc)
}

// bodyPanicMsg is used when filling IR-declared empty panicIndex/panicSliceB.
func (g *gen) bodyPanicMsg(msg string) wasm.Code {
	_ = msg
	return g.bodyThrowNullPanic()
}

// bodyThrowNullPanic throws tag $go.panic with a null iface value via
// runtime.throwPanic when available. Safe for any param arity.
func (g *gen) bodyThrowNullPanic() wasm.Code {
	enc := wasm.NewEncoder()
	if idx, ok := g.funcs["runtime.throwPanic"]; ok {
		enc.RefNull(wasm.TypeIndexHeap(g.types.ByName["$go.iface"]))
		enc.Call(idx)
		enc.Unreachable()
		return wasm.CodeFrom(nil, enc)
	}
	enc.Unreachable()
	return wasm.CodeFrom(nil, enc)
}
