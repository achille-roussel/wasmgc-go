package codegen

import (
	"github.com/achille-roussel/wasmgc-go/internal/wasm"
)

// getEnvString(key): copy key into linear memory, query/copy via getEnv, build
// a Go string and found flag (docs/design/07). Returns (string, i32 bool).
//
// Params: 0=key. Locals: 1=data, 2=off, 3=keyLen, 4=i, 5=n, 6=j, 7=arr
func (g *gen) bodyGetEnvString() wasm.Code {
	strIdx := g.types.ByName["$go.string"]
	bytesIdx := g.types.ByName["$go.bytes"]
	getEnv := g.funcs["runtime.getEnv"]
	enc := wasm.NewEncoder()

	const (
		locKey    = 0
		locData   = 1
		locOff    = 2
		locKeyLen = 3
		locI      = 4
		locN      = 5
		locJ      = 6
		locArr    = 7
	)

	strRef := wasm.Ref(wasm.TypeIndexHeap(strIdx))
	bytesRef := wasm.Ref(wasm.TypeIndexHeap(bytesIdx))

	// data/off/len from key string
	enc.LocalGet(locKey)
	enc.StructGet(strIdx, 0)
	enc.LocalSet(locData)
	enc.LocalGet(locKey)
	enc.StructGet(strIdx, 1)
	enc.LocalSet(locOff)
	enc.LocalGet(locKey)
	enc.StructGet(strIdx, 2)
	enc.LocalSet(locKeyLen)

	// Grow memory for key
	enc.Block(wasm.BlockEmpty())
	enc.Loop(wasm.BlockEmpty())
	enc.MemorySize()
	enc.I32Const(65536)
	enc.I32Mul()
	enc.LocalGet(locKeyLen)
	enc.I32GeU()
	enc.BrIf(1)
	enc.I32Const(1)
	enc.MemoryGrow()
	enc.Drop()
	enc.Br(0)
	enc.End()
	enc.End()

	// Copy key bytes to linear memory at 0
	enc.I32Const(0)
	enc.LocalSet(locI)
	enc.Block(wasm.BlockEmpty())
	enc.Loop(wasm.BlockEmpty())
	enc.LocalGet(locI)
	enc.LocalGet(locKeyLen)
	enc.I32GeU()
	enc.BrIf(1)
	enc.LocalGet(locI)
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

	// n = getEnv(0, keyLen, 0, 0) — length query
	enc.I32Const(0)
	enc.LocalGet(locKeyLen)
	enc.I32Const(0)
	enc.I32Const(0)
	enc.Call(getEnv)
	enc.LocalSet(locN)

	// n < 0 → ("", false)
	enc.LocalGet(locN)
	enc.I32Const(0)
	enc.I32LtS()
	enc.If(wasm.BlockEmpty())
	enc.I32Const(0)
	enc.ArrayNewDefault(bytesIdx)
	enc.I32Const(0)
	enc.I32Const(0)
	enc.StructNew(strIdx)
	enc.I32Const(0)
	enc.Return()
	enc.End()

	// n == 0 → ("", true) — present empty value
	enc.LocalGet(locN)
	enc.I32Eqz()
	enc.If(wasm.BlockEmpty())
	enc.I32Const(0)
	enc.ArrayNewDefault(bytesIdx)
	enc.I32Const(0)
	enc.I32Const(0)
	enc.StructNew(strIdx)
	enc.I32Const(1)
	enc.Return()
	enc.End()

	// Grow for value (overwrite key buffer at 0)
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

	// Re-copy key then write value at 0 (key still needed for lookup)
	enc.I32Const(0)
	enc.LocalSet(locI)
	enc.Block(wasm.BlockEmpty())
	enc.Loop(wasm.BlockEmpty())
	enc.LocalGet(locI)
	enc.LocalGet(locKeyLen)
	enc.I32GeU()
	enc.BrIf(1)
	enc.LocalGet(locI)
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

	// getEnv(0, keyLen, 0, n) — value overwrites key buffer
	enc.I32Const(0)
	enc.LocalGet(locKeyLen)
	enc.I32Const(0)
	enc.LocalGet(locN)
	enc.Call(getEnv)
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
	enc.I32Const(1)

	locals := []wasm.Local{
		{Count: 1, Type: bytesRef},   // data
		{Count: 5, Type: wasm.I32()}, // off, keyLen, i, n, j
		{Count: 1, Type: bytesRef},   // arr
	}
	_ = strRef
	return wasm.CodeFrom(locals, enc)
}
