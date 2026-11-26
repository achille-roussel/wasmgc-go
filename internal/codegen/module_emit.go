package codegen

import (
	"fmt"
	"sort"

	"github.com/achille-roussel/wasmgc-go/internal/ir"
	"github.com/achille-roussel/wasmgc-go/internal/wasm"
)

func (g *gen) emitImports() error {
	if g.needsMemory {
		// go.runtime.write (fd i32, ptr i32, n i32) -> i32
		writeTy := g.internFuncType(
			[]wasm.ValType{wasm.I32(), wasm.I32(), wasm.I32()},
			[]wasm.ValType{wasm.I32()},
		)
		g.importWrite = g.wm.ImportFunc("go", "runtime.write", writeTy)
		g.hasWrite = true
		g.funcs["go.runtime.write"] = g.importWrite
	}
	needExit := g.ehNeeded || g.callsHost("runtime.exit")
	if needExit {
		exitTy := g.internFuncType([]wasm.ValType{wasm.I32()}, nil)
		g.exitIdx = g.wm.ImportFunc("go", "runtime.exit", exitTy)
		g.funcs["go.runtime.exit"] = g.exitIdx
		g.funcs["runtime.exit"] = g.exitIdx
		g.hasExit = true
		g.hostImported["runtime.exit"] = true
	}
	if err := g.emitSchedImports(); err != nil {
		return err
	}
	return nil
}

// hostImportIR maps IR func names to Wasm import names under module "go".
// park/ready/spawn are always imported (JSPI required, no opt-out); the rest
// stay reachability-gated.
var hostImportIR = []struct {
	irName     string
	importName string
	always     bool
}{
	{"runtime.park", "runtime.park", true},
	{"runtime.ready", "runtime.ready", true},
	{"runtime.spawn", "runtime.spawn", true},
	{"runtime.goschedImport", "runtime.gosched", false},
	{"runtime.scheduleTimer", "runtime.scheduleTimer", false},
	{"runtime.clearTimer", "runtime.clearTimer", false},
	{"runtime.walltime", "runtime.walltime", false},
	{"runtime.nanotime", "runtime.nanotime", false},
	{"runtime.getArgc", "runtime.getArgc", false},
	{"runtime.getArg", "runtime.getArg", false},
	{"runtime.getEnv", "runtime.getEnv", false},
	// syscall/js host imports (07); reachability-gated like clocks/args.
	{"syscall.js.jsGet", "js.get", false},
	{"syscall.js.jsSet", "js.set", false},
	{"syscall.js.jsCall", "js.call", false},
	{"syscall.js.jsNew", "js.new", false},
	{"syscall.js.jsValue", "js.value", false},
	{"syscall.js.jsAwait", "js.await", false},
	{"syscall.js.jsHTTPGet", "js.httpGet", false},
	{"syscall.js.jsStringCopy", "js.stringCopy", false},
	{"syscall.js.jsNumber", "js.number", false},
	{"syscall.js.jsTruthy", "js.truthy", false},
	{"syscall.js.jsTypeof", "js.typeof", false},
	{"syscall.js.jsEqual", "js.equal", false},
}

// alwaysSchedImportSigs are Wasm types for the always-on scheduler imports
// when the IR has not linked empty bodies yet (e.g. empty_main).
var alwaysSchedImportSigs = map[string]struct {
	params  []wasm.ValType
	results []wasm.ValType
}{
	"runtime.park":  {params: []wasm.ValType{wasm.I32()}, results: []wasm.ValType{wasm.I32()}},
	"runtime.ready": {params: []wasm.ValType{wasm.I32()}},
	"runtime.spawn": {},
}

func (g *gen) emitSchedImports() error {
	for _, h := range hostImportIR {
		if !h.always && !g.needsHostImport(h.irName) {
			continue
		}
		if g.hostImported[h.irName] {
			continue
		}
		f := g.m.LookupFunc(h.irName)
		var ty uint32
		if f != nil && emptyPrintBody(f) {
			params, err := g.valTypes(paramTypes(f))
			if err != nil {
				return fmt.Errorf("codegen: import %s params: %w", h.irName, err)
			}
			results, err := g.valTypes(f.Results)
			if err != nil {
				return fmt.Errorf("codegen: import %s results: %w", h.irName, err)
			}
			ty = g.internFuncType(params, results)
		} else if h.always {
			sig := alwaysSchedImportSigs[h.irName]
			ty = g.internFuncType(sig.params, sig.results)
		} else {
			continue
		}
		idx := g.wm.ImportFunc("go", h.importName, ty)
		g.funcs[h.irName] = idx
		g.hostImported[h.irName] = true
	}
	return nil
}

func (g *gen) needsHostImport(irName string) bool {
	f := g.m.LookupFunc(irName)
	if f == nil || !emptyPrintBody(f) {
		return false
	}
	if g.callsHost(irName) {
		return true
	}
	// getArg is only invoked from the getArgString codegen fill (reverse bridge).
	if irName == "runtime.getArg" {
		gs := g.m.LookupFunc("runtime.getArgString")
		return gs != nil && emptyPrintBody(gs) && g.callsHost("runtime.getArgString")
	}
	// getEnv is only invoked from the getEnvString codegen fill.
	if irName == "runtime.getEnv" {
		gs := g.m.LookupFunc("runtime.getEnvString")
		return gs != nil && emptyPrintBody(gs) && g.callsHost("runtime.getEnvString")
	}
	// js.value / js.stringCopy are called from string-bridge fills.
	if irName == "syscall.js.jsValue" {
		vs := g.m.LookupFunc("syscall.js.valueOfString")
		return vs != nil && emptyPrintBody(vs) && g.callsHost("syscall.js.valueOfString")
	}
	if irName == "syscall.js.jsStringCopy" {
		vs := g.m.LookupFunc("syscall.js.valueToString")
		return vs != nil && emptyPrintBody(vs) && g.callsHost("syscall.js.valueToString")
	}
	return false
}

func (g *gen) callsHost(name string) bool {
	for _, f := range g.m.Funcs {
		if f.Name == name {
			continue
		}
		found := false
		walkExprs(f.Body, func(e ir.Expr) {
			in, ok := e.(*ir.Instr)
			if ok && in.Op == ir.OpCall && in.Func == name {
				found = true
			}
		})
		if found {
			return true
		}
	}
	return false
}

func (g *gen) emitMemory() {
	g.wm.AddMemory(wasm.Limits{Min: 1})
	g.hasMemory = true
	// Global bump pointer for the linear bridge (docs/design/07).
	g.memBumpIdx = g.wm.AddGlobal(
		wasm.GlobalType{Type: wasm.I32(), Mutable: true},
		wasm.ConstI32(0),
	)
	g.hasMemBump = true
	g.globals["$memBump"] = g.memBumpIdx
}

func (g *gen) emitGlobals() error {
	// Pre-register indices for globals we are about to emit so const exprs
	// can global.get siblings later in the list and self-refs for recursive
	// types (encoding/json RecursiveSlice). Indices match AddGlobal order.
	var pending []*ir.Global
	for _, gl := range g.m.Globals {
		if _, exists := g.globals[gl.Name]; exists {
			continue // e.g. $memBump already added
		}
		pending = append(pending, gl)
	}
	base := g.wm.NextGlobalIndex()
	for i, gl := range pending {
		g.globals[gl.Name] = base + uint32(i)
	}
	for _, gl := range pending {
		vt, err := g.valType(gl.Typ)
		if err != nil {
			return fmt.Errorf("codegen: global %s: %w", gl.Name, err)
		}
		init, err := g.constExpr(gl.Init, gl.Typ)
		if err != nil {
			return fmt.Errorf("codegen: global %s init: %w", gl.Name, err)
		}
		idx := g.wm.AddGlobal(wasm.GlobalType{Type: vt, Mutable: gl.Mut}, init)
		if idx != g.globals[gl.Name] {
			return fmt.Errorf("codegen: global %s index mismatch (got %d want %d)", gl.Name, idx, g.globals[gl.Name])
		}
	}
	return nil
}

func (g *gen) constExpr(e ir.Expr, typ ir.Type) ([]byte, error) {
	if e == nil {
		// Zero value.
		switch t := typ.(type) {
		case *ir.NumType:
			switch t.Kind {
			case ir.I32:
				return wasm.ConstI32(0), nil
			case ir.I64:
				enc := wasm.NewEncoder()
				enc.I64Const(0)
				enc.End()
				return enc.Bytes(), nil
			case ir.F32:
				enc := wasm.NewEncoder()
				enc.F32Const(0)
				enc.End()
				return enc.Bytes(), nil
			case ir.F64:
				enc := wasm.NewEncoder()
				enc.F64Const(0)
				enc.End()
				return enc.Bytes(), nil
			}
		case *ir.Ref:
			// Non-nullable string globals cannot be ref.null (Wasm rejects the
			// constant expression). Emit the Go zero: empty $go.string header
			// over a zero-length $go.bytes array (docs/design/03).
			if !t.Null && t.Named == "$go.string" {
				bytesIdx, err := g.typeIndexOf(&ir.Ref{Named: "$go.bytes"})
				if err != nil {
					return nil, err
				}
				strIdx, err := g.typeIndexOf(t)
				if err != nil {
					return nil, err
				}
				enc := wasm.NewEncoder()
				enc.I32Const(0)
				enc.ArrayNewDefault(bytesIdx)
				enc.I32Const(0)
				enc.I32Const(0)
				enc.StructNew(strIdx)
				enc.End()
				return enc.Bytes(), nil
			}
			ht, err := g.heapType(t)
			if err != nil {
				return nil, err
			}
			return wasm.ConstRefNull(ht), nil
		case *ir.Extern:
			return wasm.ConstRefNull(wasm.HeapTypeExtern()), nil
		}
		return nil, fmt.Errorf("no zero init for %s", typ)
	}
	enc := wasm.NewEncoder()
	fe := &funcEmitter{g: g, enc: enc}
	if err := fe.emitExpr(e); err != nil {
		return nil, err
	}
	enc.End()
	return enc.Bytes(), nil
}

func (g *gen) emitData() error {
	for _, d := range g.m.Data {
		idx := g.wm.AddPassiveData(d.Bytes)
		g.data[d.Name] = idx
	}
	return nil
}

func (g *gen) declareFuncs() error {
	for _, f := range g.m.Funcs {
		if g.hostImported[f.Name] {
			continue // already ImportFunc'd
		}
		params, err := g.valTypes(paramTypes(f))
		if err != nil {
			return fmt.Errorf("codegen: func %s params: %w", f.Name, err)
		}
		results, err := g.valTypes(f.Results)
		if err != nil {
			return fmt.Errorf("codegen: func %s results: %w", f.Name, err)
		}
		ty := g.internFuncType(params, results)
		idx := g.wm.AddFunc(ty)
		g.funcs[f.Name] = idx
	}
	return nil
}

func paramTypes(f *ir.Func) []ir.Type {
	out := make([]ir.Type, len(f.Params))
	for i, p := range f.Params {
		out[i] = p.Typ
	}
	return out
}

func (g *gen) emitFuncBodies() error {
	for _, f := range g.m.Funcs {
		if g.hostImported[f.Name] {
			continue // imports have no code section entry
		}
		switch {
		case f.Name == "runtime.printstring" && g.fillPrintString:
			g.wm.AddCode(g.bodyPrintString())
			continue
		case f.Name == "runtime.printint" && g.fillPrintInt:
			g.wm.AddCode(g.bodyPrintInt())
			continue
		case f.Name == "runtime.printnl" && g.fillPrintNL:
			g.wm.AddCode(g.bodyPrintNL())
			continue
		case f.Name == "syscall.writeHost" && g.fillWriteHost:
			g.wm.AddCode(g.bodyWriteHost())
			continue
		case f.Name == "runtime.getArgString" && g.fillGetArgString:
			g.wm.AddCode(g.bodyGetArgString())
			continue
		case f.Name == "runtime.getEnvString" && g.fillGetEnvString:
			g.wm.AddCode(g.bodyGetEnvString())
			continue
		case f.Name == "syscall.js.valueOfString" && g.fillJSValueOfString:
			g.wm.AddCode(g.bodyJSValueOfString())
			continue
		case f.Name == "syscall.js.valueToString" && g.fillJSValueToString:
			g.wm.AddCode(g.bodyJSValueToString())
			continue
		case f.Name == "runtime.panicIndex" && g.fillPanicIndex:
			if g.hasEH {
				g.wm.AddCode(g.bodyPanicMsg("runtime error: index out of range"))
			} else {
				g.wm.AddCode(bodyUnreachable())
			}
			continue
		case f.Name == "runtime.panicSliceB" && g.fillPanicSliceB:
			if g.hasEH {
				g.wm.AddCode(g.bodyPanicMsg("runtime error: slice bounds out of range"))
			} else {
				g.wm.AddCode(bodyUnreachable())
			}
			continue
		case f.Name == "runtime.panicrangestate" && g.fillPanicRange:
			if g.hasEH {
				g.wm.AddCode(g.bodyPanicMsg("range function continued iteration after loop body panic"))
			} else {
				g.wm.AddCode(bodyUnreachable())
			}
			continue
		case f.Name == "runtime.panicNil" && g.fillPanicNil:
			if g.hasEH {
				g.wm.AddCode(g.bodyPanicMsg("runtime error: invalid memory address or nil pointer dereference"))
			} else {
				g.wm.AddCode(bodyUnreachable())
			}
			continue
		case g.ehFill[f.Name]:
			switch f.Name {
			case "runtime.throwPanic":
				g.wm.AddCode(g.bodyThrowPanic())
			case "runtime.gorecover":
				g.wm.AddCode(g.bodyGorecover())
			case "runtime.deferRun":
				g.wm.AddCode(g.bodyDeferRun())
			case "runtime.unrecoveredPanic":
				g.wm.AddCode(g.bodyUnrecoveredPanic())
			case "runtime.concatstrings":
				g.wm.AddCode(g.bodyConcatStrings())
			case "runtime.semaPtrSame":
				g.wm.AddCode(g.bodySemaPtrSame())
			default:
				g.wm.AddCode(bodyUnreachable())
			}
			continue
		}
		body, err := g.emitFunc(f)
		if err != nil {
			return err
		}
		g.wm.AddCode(body)
	}
	return nil
}

func (g *gen) emitExports() {
	for _, f := range g.m.Funcs {
		if f.Export == "" {
			continue
		}
		idx, ok := g.funcs[f.Name]
		if !ok {
			continue
		}
		g.wm.ExportFunc(f.Export, idx)
	}
	if g.hasMemory {
		g.wm.ExportMemory("memory", 0)
	}
}

func (g *gen) emitNames() {
	ns := &wasm.NameSection{ModuleName: "wasmgc"}
	// Sort by index for deterministic name sections.
	type pair struct {
		idx  uint32
		name string
	}
	var fns []pair
	for name, idx := range g.funcs {
		fns = append(fns, pair{idx, name})
	}
	sort.Slice(fns, func(i, j int) bool { return fns[i].idx < fns[j].idx })
	for _, p := range fns {
		ns.Functions = append(ns.Functions, wasm.FuncName{Index: p.idx, Name: p.name})
	}
	var tys []pair
	for name, idx := range g.types.ByName {
		tys = append(tys, pair{idx, name})
	}
	sort.Slice(tys, func(i, j int) bool { return tys[i].idx < tys[j].idx })
	for _, p := range tys {
		ns.Types = append(ns.Types, wasm.TypeName{Index: p.idx, Name: p.name})
	}
	g.wm.Names = ns
}

func (g *gen) emitFunc(f *ir.Func) (wasm.Code, error) {
	fe := newFuncEmitter(g, f)
	if err := fe.emit(); err != nil {
		return wasm.Code{}, fmt.Errorf("codegen: func %s: %w", f.Name, err)
	}
	return wasm.CodeFrom(fe.locals, fe.enc), nil
}


func (g *gen) addDeclarativeRefFuncs() {
	seen := map[uint32]bool{}
	var idxs []uint32
	add := func(name string) {
		idx, ok := g.funcs[name]
		if !ok || seen[idx] {
			return
		}
		seen[idx] = true
		idxs = append(idxs, idx)
	}
	for _, f := range g.m.Funcs {
		walkExprs(f.Body, func(e ir.Expr) {
			if in, ok := e.(*ir.Instr); ok && in.Op == ir.OpRefFunc {
				add(in.Func)
			}
		})
	}
	for _, gl := range g.m.Globals {
		if gl.Init != nil {
			walkInit(gl.Init, func(e ir.Expr) {
				if in, ok := e.(*ir.Instr); ok && in.Op == ir.OpRefFunc {
					add(in.Func)
				}
			})
		}
	}
	if len(idxs) > 0 {
		g.wm.AddDeclarativeFuncs(idxs...)
	}
}

func walkInit(e ir.Expr, fn func(ir.Expr)) {
	if e == nil {
		return
	}
	fn(e)
	if in, ok := e.(*ir.Instr); ok {
		for _, a := range in.Args {
			walkInit(a, fn)
		}
	}
}
