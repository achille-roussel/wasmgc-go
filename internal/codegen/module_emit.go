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
	return nil
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
	for _, gl := range g.m.Globals {
		if _, exists := g.globals[gl.Name]; exists {
			continue // e.g. $memBump already added
		}
		vt, err := g.valType(gl.Typ)
		if err != nil {
			return fmt.Errorf("codegen: global %s: %w", gl.Name, err)
		}
		init, err := g.constExpr(gl.Init, gl.Typ)
		if err != nil {
			return fmt.Errorf("codegen: global %s init: %w", gl.Name, err)
		}
		idx := g.wm.AddGlobal(wasm.GlobalType{Type: vt, Mutable: gl.Mut}, init)
		g.globals[gl.Name] = idx
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
		case f.Name == "runtime.panicIndex" && g.fillPanicIndex:
			g.wm.AddCode(bodyUnreachable())
			continue
		case f.Name == "runtime.panicSliceB" && g.fillPanicSliceB:
			g.wm.AddCode(bodyUnreachable())
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
