package codegen

import (
	"github.com/achille-roussel/wasmgc-go/internal/wasm"
)

// valueOfString(s): copy Go string bytes into linear memory, then
// js.value(kind=string, 0, ptr, len) → externref (docs/design/07).
//
// Params: 0=s. Locals: 1=data, 2=off, 3=len, 4=i, 5=ptr
func (g *gen) bodyJSValueOfString() wasm.Code {
	strIdx := g.types.ByName["$go.string"]
	bytesIdx := g.types.ByName["$go.bytes"]
	jsValue := g.funcs["syscall.js.jsValue"]
	enc := wasm.NewEncoder()

	const (
		locS    = 0
		locData = 1
		locOff  = 2
		locLen  = 3
		locI    = 4
		locPtr  = 5
	)

	enc.LocalGet(locS)
	enc.StructGet(strIdx, 0)
	enc.LocalSet(locData)
	enc.LocalGet(locS)
	enc.StructGet(strIdx, 1)
	enc.LocalSet(locOff)
	enc.LocalGet(locS)
	enc.StructGet(strIdx, 2)
	enc.LocalSet(locLen)

	enc.I32Const(0)
	enc.LocalTee(locPtr)
	enc.GlobalSet(g.memBumpIdx)

	// Grow memory while size*65536 < len.
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
	enc.ArrayGetU(bytesIdx)
	enc.I32Store8(0, 0)

	enc.LocalGet(locI)
	enc.I32Const(1)
	enc.I32Add()
	enc.LocalSet(locI)
	enc.Br(0)
	enc.End()
	enc.End()

	// js.value(4 /*string*/, 0.0, ptr, len)
	enc.I32Const(4)
	enc.F64Const(0)
	enc.LocalGet(locPtr)
	enc.LocalGet(locLen)
	enc.Call(jsValue)

	enc.I32Const(0)
	enc.GlobalSet(g.memBumpIdx)

	locals := []wasm.Local{
		{Count: 1, Type: wasm.Ref(wasm.TypeIndexHeap(bytesIdx))},
		{Count: 4, Type: wasm.I32()},
	}
	return wasm.CodeFrom(locals, enc)
}

// valueToString(v): query length via js.stringCopy(v,0,0), copy into linear
// memory, then build a Go string (mirror of getArgString).
//
// Params: 0=v (externref). Locals: 1=n, 2=j, 3=arr
func (g *gen) bodyJSValueToString() wasm.Code {
	strIdx := g.types.ByName["$go.string"]
	bytesIdx := g.types.ByName["$go.bytes"]
	copyFn := g.funcs["syscall.js.jsStringCopy"]
	enc := wasm.NewEncoder()

	const (
		locV   = 0
		locN   = 1
		locJ   = 2
		locArr = 3
	)

	strRef := wasm.Ref(wasm.TypeIndexHeap(strIdx))
	bytesRef := wasm.Ref(wasm.TypeIndexHeap(bytesIdx))

	enc.LocalGet(locV)
	enc.I32Const(0)
	enc.I32Const(0)
	enc.Call(copyFn)
	enc.LocalSet(locN)

	enc.LocalGet(locN)
	enc.I32Eqz()
	enc.If(wasm.BlockVal(strRef))
	enc.I32Const(0)
	enc.ArrayNewDefault(bytesIdx)
	enc.I32Const(0)
	enc.I32Const(0)
	enc.StructNew(strIdx)
	enc.Else()

	enc.Block(wasm.BlockEmpty())
	enc.Loop(wasm.BlockEmpty())
	enc.MemorySize()
	enc.I32Const(65536)
	enc.I32Mul()
	enc.LocalGet(locN)
	enc.I32GeU()
	enc.BrIf(1)
	enc.I32Const(1)
	enc.MemoryGrow()
	enc.Drop()
	enc.Br(0)
	enc.End()
	enc.End()

	enc.LocalGet(locV)
	enc.I32Const(0)
	enc.LocalGet(locN)
	enc.Call(copyFn)
	enc.Drop()

	enc.LocalGet(locN)
	enc.ArrayNewDefault(bytesIdx)
	enc.LocalSet(locArr)

	enc.I32Const(0)
	enc.LocalSet(locJ)
	enc.Block(wasm.BlockEmpty())
	enc.Loop(wasm.BlockEmpty())
	enc.LocalGet(locJ)
	enc.LocalGet(locN)
	enc.I32GeU()
	enc.BrIf(1)

	enc.LocalGet(locArr)
	enc.LocalGet(locJ)
	enc.LocalGet(locJ)
	enc.I32Load8U(0, 0)
	enc.ArraySet(bytesIdx)

	enc.LocalGet(locJ)
	enc.I32Const(1)
	enc.I32Add()
	enc.LocalSet(locJ)
	enc.Br(0)
	enc.End()
	enc.End()

	enc.LocalGet(locArr)
	enc.I32Const(0)
	enc.LocalGet(locN)
	enc.StructNew(strIdx)

	enc.End() // if

	locals := []wasm.Local{
		{Count: 2, Type: wasm.I32()},
		{Count: 1, Type: bytesRef},
	}
	return wasm.CodeFrom(locals, enc)
}
