package codegen

import (
	"github.com/achille-roussel/wasmgc-go/internal/wasm"
)

// getArgString(i): query length via getArg(i,0,0), copy host bytes into linear
// memory, then build a Go string (reverse of printstring; docs/design/07).
//
// Params: 0=i. Locals: 1=n, 2=j, 3=arr
func (g *gen) bodyGetArgString() wasm.Code {
	strIdx := g.types.ByName["$go.string"]
	bytesIdx := g.types.ByName["$go.bytes"]
	getArg := g.funcs["runtime.getArg"]
	enc := wasm.NewEncoder()

	const (
		locI   = 0
		locN   = 1
		locJ   = 2
		locArr = 3
	)

	strRef := wasm.Ref(wasm.TypeIndexHeap(strIdx))
	bytesRef := wasm.Ref(wasm.TypeIndexHeap(bytesIdx))

	// n = getArg(i, 0, 0)
	enc.LocalGet(locI)
	enc.I32Const(0)
	enc.I32Const(0)
	enc.Call(getArg)
	enc.LocalSet(locN)

	// n == 0 → empty string
	enc.LocalGet(locN)
	enc.I32Eqz()
	enc.If(wasm.BlockVal(strRef))
	enc.I32Const(0)
	enc.ArrayNewDefault(bytesIdx)
	enc.I32Const(0)
	enc.I32Const(0)
	enc.StructNew(strIdx)
	enc.Else()

	// Grow memory until size*65536 >= n
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

	// getArg(i, 0, n) — write into linear memory at ptr 0
	enc.LocalGet(locI)
	enc.I32Const(0)
	enc.LocalGet(locN)
	enc.Call(getArg)
	enc.Drop()

	// arr = array.new_default n
	enc.LocalGet(locN)
	enc.ArrayNewDefault(bytesIdx)
	enc.LocalSet(locArr)

	// for j = 0; j < n; j++ { arr[j] = i32.load8_u(j) }
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
		{Count: 2, Type: wasm.I32()}, // n, j
		{Count: 1, Type: bytesRef},   // arr
	}
	return wasm.CodeFrom(locals, enc)
}
