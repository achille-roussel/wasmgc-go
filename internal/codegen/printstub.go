package codegen

import (
	"fmt"

	"github.com/achille-roussel/wasmgc-go/internal/ir"
	"github.com/achille-roussel/wasmgc-go/internal/wasm"
)

// Print runtime bodies via go.runtime.write and the linear-memory bridge
// (docs/design/04, 07). Stream F links body-less runtime.printstring from
// std/src/runtime; codegen fills that empty IR body here until
// printstring_wasmgc.wat lands (design 12). SynthPrintRuntime still
// synthesizes entirely missing print* funcs for tests that skip rtlink.

func emptyPrintBody(f *ir.Func) bool {
	if f == nil || f.Body == nil {
		return true
	}
	seq, ok := f.Body.(*ir.Seq)
	return ok && len(seq.Stmts) == 0
}

// printIntrinsic reports whether f is a print builtin whose body codegen must
// supply (missing from IR, or linked body-less from std/src/runtime).
func (g *gen) printIntrinsic(name string) bool {
	f := g.m.LookupFunc(name)
	if f == nil {
		return g.opts.SynthPrintRuntime
	}
	return emptyPrintBody(f)
}

func (g *gen) ensurePrintStubs() error {
	needString, needInt, needNL := g.scanPrintCalls()
	// Also fill body-less decls linked by rtlink even when this func is not
	// directly called (e.g. printint → printstring).
	for _, name := range []string{"runtime.printstring", "runtime.printint", "runtime.printnl"} {
		if f := g.m.LookupFunc(name); f != nil && emptyPrintBody(f) {
			switch name {
			case "runtime.printstring":
				needString = true
			case "runtime.printint":
				needInt = true
			case "runtime.printnl":
				needNL = true
			}
		}
	}
	if f := g.m.LookupFunc("syscall.writeHost"); f != nil && emptyPrintBody(f) {
		g.fillWriteHost = true
	}
	if f := g.m.LookupFunc("runtime.getArgString"); f != nil && emptyPrintBody(f) {
		g.fillGetArgString = true
	}
	if !needString && !needInt && !needNL && !g.fillWriteHost && !g.fillGetArgString {
		return nil
	}
	if g.fillGetArgString && !g.hasMemory {
		return fmt.Errorf("codegen: getArgString requires linear memory")
	}
	if g.fillGetArgString && !g.hostImported["runtime.getArg"] {
		return fmt.Errorf("codegen: getArgString requires go.runtime.getArg import")
	}
	if (needString || needInt || needNL || g.fillWriteHost) && (!g.hasWrite || !g.hasMemory) {
		return fmt.Errorf("codegen: print stubs require memory and go.runtime.write")
	}
	if !needString && !needInt && !needNL && !g.fillWriteHost {
		return nil
	}
	strRef := wasm.Ref(wasm.TypeIndexHeap(g.types.ByName["$go.string"]))

	if needString && g.printIntrinsic("runtime.printstring") {
		if _, ok := g.funcs["runtime.printstring"]; !ok {
			g.printStringIdx = g.wm.AddFunc(g.internFuncType([]wasm.ValType{strRef}, nil))
			g.funcs["runtime.printstring"] = g.printStringIdx
		}
		if g.m.LookupFunc("runtime.printstring") == nil {
			g.stubNeedString = true
			g.hasPrintStubs = true
		} else {
			g.fillPrintString = true
		}
	}
	if needInt && g.printIntrinsic("runtime.printint") {
		if _, ok := g.funcs["runtime.printint"]; !ok {
			g.printIntIdx = g.wm.AddFunc(g.internFuncType([]wasm.ValType{wasm.I32()}, nil))
			g.funcs["runtime.printint"] = g.printIntIdx
		}
		if g.m.LookupFunc("runtime.printint") == nil {
			g.stubNeedInt = true
			g.hasPrintStubs = true
		} else {
			g.fillPrintInt = true
		}
	}
	if needNL && g.printIntrinsic("runtime.printnl") {
		if _, ok := g.funcs["runtime.printnl"]; !ok {
			g.printNLIdx = g.wm.AddFunc(g.internFuncType(nil, nil))
			g.funcs["runtime.printnl"] = g.printNLIdx
		}
		if g.m.LookupFunc("runtime.printnl") == nil {
			g.stubNeedNL = true
			g.hasPrintStubs = true
		} else {
			g.fillPrintNL = true
		}
	}
	return nil
}

func (g *gen) scanPrintCalls() (needString, needInt, needNL bool) {
	for _, f := range g.m.Funcs {
		walkExprs(f.Body, func(e ir.Expr) {
			in, ok := e.(*ir.Instr)
			if !ok || in.Op != ir.OpCall {
				return
			}
			switch in.Func {
			case "runtime.printstring":
				needString = true
			case "runtime.printint":
				needInt = true
			case "runtime.printnl":
				needNL = true
			}
		})
	}
	return
}

func (g *gen) emitPrintStubBodies() error {
	// Stub funcs were AddFunc'd after IR funcs, so append codes in the
	// same order as ensurePrintStubs added them.
	if g.stubNeedString {
		g.wm.AddCode(g.bodyPrintString())
	}
	if g.stubNeedInt {
		g.wm.AddCode(g.bodyPrintInt())
	}
	if g.stubNeedNL {
		g.wm.AddCode(g.bodyPrintNL())
	}
	return nil
}

// printstring(s): copy s's bytes into linear memory at $memBump, call
// go.runtime.write(1, ptr, n), leave bump unchanged for M2 simplicity
// (reset to 0 after each write).
//
// Locals: 0=s, 1=data, 2=off, 3=len, 4=i, 5=ptr
func (g *gen) bodyPrintString() wasm.Code {
	strIdx := g.types.ByName["$go.string"]
	bytesIdx := g.types.ByName["$go.bytes"]
	enc := wasm.NewEncoder()

	const (
		locS    = 0
		locData = 1
		locOff  = 2
		locLen  = 3
		locI    = 4
		locPtr  = 5
	)

	// data = struct.get s.data
	enc.LocalGet(locS)
	enc.StructGet(strIdx, 0)
	enc.LocalSet(locData)
	// off = struct.get s.off
	enc.LocalGet(locS)
	enc.StructGet(strIdx, 1)
	enc.LocalSet(locOff)
	// len = struct.get s.len
	enc.LocalGet(locS)
	enc.StructGet(strIdx, 2)
	enc.LocalSet(locLen)

	// ptr = 0; global.set bump = 0 (start of bridge buffer)
	enc.I32Const(0)
	enc.LocalTee(locPtr)
	enc.GlobalSet(g.memBumpIdx)

	// Ensure memory: pages needed = (len+65535)/65536; grow if memory.size < needed.
	// Simple: if len > 0 && memory.size == 0 { grow 1 } — we start with 1 page.
	// Grow while memory.size*65536 < len.
	enc.Block(wasm.BlockEmpty())
	enc.Loop(wasm.BlockEmpty())
	enc.MemorySize()
	enc.I32Const(65536)
	enc.I32Mul()
	enc.LocalGet(locLen)
	enc.I32GeU()
	enc.BrIf(1) // exit block
	enc.I32Const(1)
	enc.MemoryGrow()
	enc.Drop()
	enc.Br(0)
	enc.End() // loop
	enc.End() // block

	// i = 0
	enc.I32Const(0)
	enc.LocalSet(locI)

	// loop: while i < len { store8(ptr+i, array.get_u(data, off+i)); i++ }
	enc.Block(wasm.BlockEmpty())
	enc.Loop(wasm.BlockEmpty())
	enc.LocalGet(locI)
	enc.LocalGet(locLen)
	enc.I32GeU()
	enc.BrIf(1)

	// addr = ptr + i
	enc.LocalGet(locPtr)
	enc.LocalGet(locI)
	enc.I32Add()
	// byte = array.get_u(data, off+i)
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

	// call write(1, ptr, len)
	enc.I32Const(1)
	enc.LocalGet(locPtr)
	enc.LocalGet(locLen)
	enc.Call(g.importWrite)
	enc.Drop()

	// reset bump
	enc.I32Const(0)
	enc.GlobalSet(g.memBumpIdx)

	locals := []wasm.Local{
		{Count: 1, Type: wasm.Ref(wasm.TypeIndexHeap(bytesIdx))}, // data
		{Count: 4, Type: wasm.I32()},                              // off, len, i, ptr
	}
	return wasm.CodeFrom(locals, enc)
}

// writeHost(fd i32, p []byte) i32: copy slice bytes into linear memory and
// call go.runtime.write(fd, ptr, n). Returns n (bytes written). Nil/empty → 0.
//
// Params: 0=fd, 1=p. Locals: 2=data, 3=off, 4=len, 5=i, 6=ptr
func (g *gen) bodyWriteHost() wasm.Code {
	sliceIdx := g.types.ByName["$go.slice.i8"]
	bytesIdx := g.types.ByName["$go.bytes"]
	enc := wasm.NewEncoder()

	const (
		locFD   = 0
		locP    = 1
		locData = 2
		locOff  = 3
		locLen  = 4
		locI    = 5
		locPtr  = 6
	)

	// nil slice → return 0
	enc.Block(wasm.BlockEmpty())
	enc.LocalGet(locP)
	enc.RefIsNull()
	enc.I32Eqz()
	enc.BrIf(0)
	enc.I32Const(0)
	enc.Return()
	enc.End()

	enc.LocalGet(locP)
	enc.StructGet(sliceIdx, 0)
	enc.LocalSet(locData)
	enc.LocalGet(locP)
	enc.StructGet(sliceIdx, 1)
	enc.LocalSet(locOff)
	enc.LocalGet(locP)
	enc.StructGet(sliceIdx, 2)
	enc.LocalSet(locLen)

	// empty → return 0
	enc.Block(wasm.BlockEmpty())
	enc.LocalGet(locLen)
	enc.BrIf(0)
	enc.I32Const(0)
	enc.Return()
	enc.End()

	enc.I32Const(0)
	enc.LocalTee(locPtr)
	enc.GlobalSet(g.memBumpIdx)

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

	enc.LocalGet(locFD)
	enc.LocalGet(locPtr)
	enc.LocalGet(locLen)
	enc.Call(g.importWrite)
	enc.LocalSet(locLen) // n written
	enc.I32Const(0)
	enc.GlobalSet(g.memBumpIdx)
	enc.LocalGet(locLen)

	locals := []wasm.Local{
		{Count: 1, Type: wasm.Ref(wasm.TypeIndexHeap(bytesIdx))}, // data
		{Count: 4, Type: wasm.I32()},                              // off, len, i, ptr
	}
	return wasm.CodeFrom(locals, enc)
}

// printint(v i32): format decimal digits into a 16-byte stack buffer in
// linear memory, write exactly the digits (no trailing NUL).
//
// Locals: 0=v, 1=neg, 2=n, 3=i, 4=tmp
func (g *gen) bodyPrintInt() wasm.Code {
	enc := wasm.NewEncoder()
	const (
		locV   = 0
		locNeg = 1
		locN   = 2
		locI   = 3
		locTmp = 4
	)

	const bufBase = 64 // fixed offset in linear memory for digit buffer
	const bufLen = 16

	// Ensure one page.
	enc.Block(wasm.BlockEmpty())
	enc.MemorySize()
	enc.I32Const(1)
	enc.I32GeU()
	enc.BrIf(0)
	enc.I32Const(1)
	enc.MemoryGrow()
	enc.Drop()
	enc.End()

	// Special-case 0.
	enc.Block(wasm.BlockEmpty())
	enc.LocalGet(locV)
	enc.I32Eqz()
	enc.If(wasm.BlockEmpty())
	enc.I32Const(bufBase)
	enc.I32Const('0')
	enc.I32Store8(0, 0)
	enc.I32Const(1) // fd
	enc.I32Const(bufBase)
	enc.I32Const(1) // n
	enc.Call(g.importWrite)
	enc.Drop()
	enc.Br(1) // exit outer block
	enc.End() // if

	// neg = v < 0; if neg { v = 0 - v } (careful with MinInt32 — treat as unsigned magnitude via i32)
	enc.LocalGet(locV)
	enc.I32Const(0)
	enc.I32LtS()
	enc.LocalSet(locNeg)
	enc.Block(wasm.BlockEmpty())
	enc.LocalGet(locNeg)
	enc.I32Eqz()
	enc.BrIf(0)
	enc.I32Const(0)
	enc.LocalGet(locV)
	enc.I32Sub()
	enc.LocalSet(locV)
	enc.End()

	// Write digits from the end of the buffer.
	enc.I32Const(bufLen)
	enc.LocalSet(locI)

	enc.Loop(wasm.BlockEmpty())
	// i--
	enc.LocalGet(locI)
	enc.I32Const(1)
	enc.I32Sub()
	enc.LocalSet(locI)

	// buf[i] = '0' + (v % 10)
	enc.I32Const(bufBase)
	enc.LocalGet(locI)
	enc.I32Add()
	enc.LocalGet(locV)
	enc.I32Const(10)
	enc.I32RemU()
	enc.I32Const('0')
	enc.I32Add()
	enc.I32Store8(0, 0)

	// v = v / 10; continue while v != 0
	enc.LocalGet(locV)
	enc.I32Const(10)
	enc.I32DivU()
	enc.LocalTee(locV)
	enc.BrIf(0)
	enc.End()

	// if neg, prepend '-'
	enc.Block(wasm.BlockEmpty())
	enc.LocalGet(locNeg)
	enc.I32Eqz()
	enc.BrIf(0)
	enc.LocalGet(locI)
	enc.I32Const(1)
	enc.I32Sub()
	enc.LocalSet(locI)
	enc.I32Const(bufBase)
	enc.LocalGet(locI)
	enc.I32Add()
	enc.I32Const('-')
	enc.I32Store8(0, 0)
	enc.End()

	// n = bufLen - i; write(1, bufBase+i, n)
	enc.I32Const(bufLen)
	enc.LocalGet(locI)
	enc.I32Sub()
	enc.LocalSet(locN)

	enc.I32Const(1)
	enc.I32Const(bufBase)
	enc.LocalGet(locI)
	enc.I32Add()
	enc.LocalGet(locN)
	enc.Call(g.importWrite)
	enc.Drop()

	enc.End() // outer block for zero case

	locals := []wasm.Local{{Count: 4, Type: wasm.I32()}}
	return wasm.CodeFrom(locals, enc)
}

func (g *gen) bodyPrintNL() wasm.Code {
	enc := wasm.NewEncoder()
	const bufBase = 0
	enc.Block(wasm.BlockEmpty())
	enc.MemorySize()
	enc.I32Const(1)
	enc.I32GeU()
	enc.BrIf(0)
	enc.I32Const(1)
	enc.MemoryGrow()
	enc.Drop()
	enc.End()

	enc.I32Const(bufBase)
	enc.I32Const('\n')
	enc.I32Store8(0, 0)
	enc.I32Const(1)
	enc.I32Const(bufBase)
	enc.I32Const(1)
	enc.Call(g.importWrite)
	enc.Drop()
	return wasm.CodeFrom(nil, enc)
}
