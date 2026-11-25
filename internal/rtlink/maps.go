package rtlink

import (
	"fmt"
	"os"
	"path/filepath"

	"github.com/achille-roussel/wasmgc-go/internal/ir"
	"github.com/achille-roussel/wasmgc-go/internal/lower"
)

// EnsureMaps links runtime map.go (+ rand for seeds) when the module uses
// map ops or calls runtime.map*. Fills body-less f32bits/f64bits with
// reinterpret IR (design 12 nosuspend intrinsic stand-in).
func EnsureMaps(mod *ir.Module, rewriteDir string) error {
	if mod == nil {
		return nil
	}
	if !scanMapNeeds(mod) {
		return nil
	}
	if mod.LookupFunc("runtime.makemap") != nil && mod.LookupFunc("runtime.mapAccess") != nil {
		fillFloatBits(mod)
		specializeMapHashes(mod)
		return nil
	}
	if rewriteDir == "" {
		return fmt.Errorf("rtlink: rewrite dir unset; cannot link runtime maps")
	}
	// TypeCheckFiles is isolated — include every dep map.go needs even when
	// those symbols are already linked into the module (sched/select/print).
	// rtype.go: mapKeyHash uses ReflectType/ReflectTypeName for unhashable keys.
	// panic.go: mapKeyHash uses throwPanic/panicErr (and panic_index may need it).
	files := []string{"map.go", "rand.go", "fatal.go", "bridge.go", "print.go", "stringeq.go", "rtype.go", "panic.go"}
	needPanic := mod.LookupFunc("runtime.panicUncomparable") == nil &&
		mod.LookupFunc("runtime.deferRun") == nil
	needPanicIndex := mod.LookupFunc("runtime.panicIndex") == nil
	// panic.go is always typechecked with map.go; if panic symbols are already
	// in the module, drop re-linked panic funcs after AppendFiles (keep index).
	panicTypecheckOnly := !needPanic
	if needPanicIndex {
		files = append(files, "panic_index.go")
	}
	var filenames []string
	var srcs []string
	seen := map[string]bool{}
	for _, name := range files {
		if seen[name] {
			continue
		}
		seen[name] = true
		path := filepath.Join(rewriteDir, "runtime", name)
		src, err := os.ReadFile(path)
		if err != nil {
			return fmt.Errorf("rtlink: read %s: %w", path, err)
		}
		filenames = append(filenames, path)
		srcs = append(srcs, string(src))
	}
	fset, astFiles, pkg, info, err := lower.TypeCheckFiles(filenames, srcs)
	if err != nil {
		return fmt.Errorf("rtlink: typecheck map runtime: %w", err)
	}
	stripMapFuncs(mod)
	stripMapRand(mod)
	stripPrintFuncs(mod)
	stripMapSupport(mod)
	stripReflectDecls(mod)
	prior := append([]*ir.Func(nil), mod.Funcs...)
	if err := lower.AppendFiles(mod, fset, astFiles, pkg, info); err != nil {
		return fmt.Errorf("rtlink: lower map runtime: %w", err)
	}
	if panicTypecheckOnly {
		// panic.go was typecheck-only: drop its re-linked symbols, restore the
		// prior copies, and keep panic_index helpers from the append.
		index := map[string]*ir.Func{}
		for _, f := range mod.Funcs {
			switch f.Name {
			case "runtime.panicIndex", "runtime.panicSliceToArray", "runtime.itoa", "runtime.digit":
				index[f.Name] = f
			}
		}
		stripPanicFuncs(mod)
		for _, f := range prior {
			if mod.LookupFunc(f.Name) == nil {
				mod.Funcs = append(mod.Funcs, f)
			}
		}
		for _, name := range []string{"runtime.panicIndex", "runtime.panicSliceToArray", "runtime.itoa", "runtime.digit"} {
			if f := index[name]; f != nil && mod.LookupFunc(name) == nil {
				mod.Funcs = append(mod.Funcs, f)
			}
		}
	}
	fillFloatBits(mod)
	specializeMapHashes(mod)
	return nil
}

var mapSyms = []string{
	"runtime.makemap", "runtime.mapAccess", "runtime.mapAssign", "runtime.mapDelete",
	"runtime.mapClear", "runtime.mapLen", "runtime.mapIterStart", "runtime.mapIterNext",
	"runtime.mapKeyHash", "runtime.strhash", "runtime.f32hash", "runtime.f64hash",
	"runtime.f32bits", "runtime.f64bits",
	"runtime.hashBool", "runtime.hashInt", "runtime.hashInt64", "runtime.hashUint",
	"runtime.hashUint64", "runtime.hashFloat32", "runtime.hashFloat64",
	"runtime.hashComplex64", "runtime.hashComplex128", "runtime.hashString",
}

func scanMapNeeds(mod *ir.Module) bool {
	for _, f := range mod.Funcs {
		need := false
		walkCalls(f.Body, func(name string) {
			switch name {
			case "runtime.makemap", "runtime.mapAccess", "runtime.mapAssign",
				"runtime.mapDelete", "runtime.mapClear", "runtime.mapLen",
				"runtime.mapIterStart", "runtime.mapIterNext", "runtime.mapKeyHash":
				need = true
			}
		})
		walkHighOps(f.Body, func(op ir.Op) {
			switch op {
			case ir.OpMapMake, ir.OpMapGet, ir.OpMapSet, ir.OpMapDelete,
				ir.OpMapLen, ir.OpMapIter, ir.OpMapNext:
				need = true
			}
		})
		if need {
			return true
		}
	}
	// Also detect $go.hmap types in the module (make already emits OpMapMake).
	return false
}

func stripMapFuncs(mod *ir.Module) {
	names := map[string]bool{}
	for _, s := range mapSyms {
		names[s] = true
	}
	// Helpers and methods from map.go not in mapSyms.
	for _, s := range []string{
		"runtime.mapCapForHint",
		"runtime.mapCheckWrite",
		"runtime.(*hmap).growTo",
		"runtime.(*hmap).insertNew",
		"runtime.(*hmap).maybeGrow",
	} {
		names[s] = true
	}
	out := mod.Funcs[:0]
	for _, f := range mod.Funcs {
		if names[f.Name] {
			continue
		}
		out = append(out, f)
	}
	mod.Funcs = out
}

// stripMapRand drops fastrand symbols so AppendFiles can re-emit rand.go
// (already linked by sched/select) without duplicate func/global errors.
func stripMapRand(mod *ir.Module) {
	drop := map[string]bool{
		"runtime.fastrand":  true,
		"runtime.fastrandn": true,
	}
	out := mod.Funcs[:0]
	for _, f := range mod.Funcs {
		if drop[f.Name] {
			continue
		}
		out = append(out, f)
	}
	mod.Funcs = out
	gOut := mod.Globals[:0]
	for _, g := range mod.Globals {
		if g.Name == "runtime.fastrandState" {
			continue
		}
		gOut = append(gOut, g)
	}
	mod.Globals = gOut
}

// stripMapSupport drops fatal/exit/throw so AppendFiles can re-emit them.
func stripMapSupport(mod *ir.Module) {
	drop := map[string]bool{
		"runtime.throw": true,
		"runtime.fatal": true,
		"runtime.exit":  true,
	}
	out := mod.Funcs[:0]
	for _, f := range mod.Funcs {
		if drop[f.Name] {
			continue
		}
		out = append(out, f)
	}
	mod.Funcs = out
}

// stripReflectDecls drops Reflect* / Rtype methods so EnsureMaps can re-link
// rtype.go (mapKeyHash needs ReflectTypeName) without duplicate funcs.
func stripReflectDecls(mod *ir.Module) {
	out := mod.Funcs[:0]
	for _, f := range mod.Funcs {
		if len(f.Name) >= 16 && f.Name[:16] == "runtime.Reflect" {
			continue
		}
		if f.Name == "runtime.(*Rtype).Kind" || f.Name == "runtime.(*Rtype).ID" {
			continue
		}
		out = append(out, f)
	}
	mod.Funcs = out
}

// EnsureFloatBits rewrites math.Float*bits / Float*frombits (and runtime /
// internal/strconv helpers) to Wasm reinterpret ops. The std math bodies use
// unsafe.Pointer casts that lower to fat-pointer loads and trap with
// "illegal cast" on Wasm GC when maps/reflect were never linked — so this
// must run for every program that imports math, not only EnsureMaps/Reflect.
func EnsureFloatBits(mod *ir.Module) {
	fillFloatBits(mod)
}

// fillFloatBits replaces empty f32bits/f64bits bodies with reinterpret.
// Also fills internal.strconv.float*bits / float*frombits (deps.go overlay)
// and math.Float*bits / Float*frombits (unsafe pointer cast is not valid on
// Wasm GC — those bodies become f64.reinterpret_i64 / i64.reinterpret_f64).
func fillFloatBits(mod *ir.Module) {
	fillReinterpret := func(name string, op ir.Op, force bool) {
		f := mod.LookupFunc(name)
		if f == nil || len(f.Params) != 1 {
			return
		}
		if !force && !emptyBody(f) {
			return
		}
		p := f.Params[0]
		f.Body = &ir.Seq{Stmts: []ir.Stmt{
			&ir.Return{Results: []ir.Expr{
				&ir.Instr{Op: op, Args: []ir.Expr{&ir.Name{Raw: p.Name, Var: p}}},
			}},
		}}
	}
	fillReinterpret("runtime.f32bits", ir.OpI32ReinterpretF32, false)
	fillReinterpret("runtime.f64bits", ir.OpI64ReinterpretF64, false)
	fillReinterpret("internal.strconv.float32bits", ir.OpI32ReinterpretF32, false)
	fillReinterpret("internal.strconv.float64bits", ir.OpI64ReinterpretF64, false)
	fillReinterpret("internal.strconv.float32frombits", ir.OpF32ReinterpretI32, false)
	fillReinterpret("internal.strconv.float64frombits", ir.OpF64ReinterpretI64, false)
	fillReinterpret("math.Float32bits", ir.OpI32ReinterpretF32, true)
	fillReinterpret("math.Float64bits", ir.OpI64ReinterpretF64, true)
	fillReinterpret("math.Float32frombits", ir.OpF32ReinterpretI32, true)
	fillReinterpret("math.Float64frombits", ir.OpF64ReinterpretI64, true)
}

// hashHelperFuncs are the per-type mapKeyHash branches (and their callees).
var hashHelperFuncs = []string{
	"runtime.hashBool",
	"runtime.hashInt",
	"runtime.hashInt64",
	"runtime.hashUint",
	"runtime.hashUint64",
	"runtime.hashFloat32",
	"runtime.hashFloat64",
	"runtime.hashComplex64",
	"runtime.hashComplex128",
	"runtime.hashString",
	"runtime.f32hash",
	"runtime.f64hash",
	"runtime.strhash",
	"runtime.f32bits",
	"runtime.f64bits",
}

// specializeMapHashes keeps only hash helpers for key types actually used in
// OpMap* (Instr.Str = desc.TypeKey). map[int] programs drop float/string/bool
// hashers that mapKeyHash would otherwise keep live for DCE.
func specializeMapHashes(mod *ir.Module) {
	if mod == nil || mod.LookupFunc("runtime.mapKeyHash") == nil {
		return
	}
	keep, ok := scanMapKeyHashKeep(mod)
	if !ok {
		return // unknown keys — leave the full cascade
	}
	rewriteMapKeyHash(mod, keep)
	drop := map[string]bool{"runtime.mapKeyHashFallthrough": true}
	for _, name := range hashHelperFuncs {
		if !keep[name] {
			drop[name] = true
		}
	}
	if len(drop) == 0 {
		return
	}
	out := mod.Funcs[:0]
	for _, f := range mod.Funcs {
		if drop[f.Name] {
			continue
		}
		out = append(out, f)
	}
	mod.Funcs = out
}

// scanMapKeyHashKeep returns the set of runtime.hash* (and callees) to keep.
// ok is false when no typed map op was found (keep everything).
func scanMapKeyHashKeep(mod *ir.Module) (keep map[string]bool, ok bool) {
	keys := map[string]bool{}
	found := false
	for _, f := range mod.Funcs {
		walkExprs(f.Body, func(e ir.Expr) {
			in, ok := e.(*ir.Instr)
			if !ok {
				return
			}
			switch in.Op {
			case ir.OpMapMake, ir.OpMapGet, ir.OpMapSet, ir.OpMapDelete,
				ir.OpMapLen, ir.OpMapIter, ir.OpMapNext:
				// Str holds desc.TypeKey(key) except comma-ok ("ok") and clear.
				if in.Str != "" && in.Str != "ok" && in.Str != "clear" {
					keys[in.Str] = true
					found = true
				}
			}
		})
	}
	if !found {
		return nil, false
	}
	keep = map[string]bool{}
	for k := range keys {
		helpers := hashHelpersForKey(k)
		if helpers == nil {
			// interface / struct / pointer / etc. need mapKeyHashFallthrough
			// for unhashable panics (recover2). Do not specialize.
			return nil, false
		}
		for _, h := range helpers {
			keep[h] = true
		}
	}
	return keep, true
}

func hashHelpersForKey(typeKey string) []string {
	switch typeKey {
	case "bool":
		return []string{"runtime.hashBool"}
	case "int", "int8", "int16", "int32":
		return []string{"runtime.hashInt"}
	case "int64":
		return []string{"runtime.hashInt64"}
	case "uint", "uint8", "uint16", "uint32", "uintptr", "byte", "rune":
		return []string{"runtime.hashUint"}
	case "uint64":
		return []string{"runtime.hashUint64"}
	case "float32":
		return []string{"runtime.hashFloat32", "runtime.f32hash", "runtime.f32bits"}
	case "float64":
		return []string{"runtime.hashFloat64", "runtime.f64hash", "runtime.f64bits"}
	case "complex64":
		return []string{"runtime.hashComplex64", "runtime.f32hash", "runtime.f32bits"}
	case "complex128":
		return []string{"runtime.hashComplex128", "runtime.f64hash", "runtime.f64bits"}
	case "string":
		return []string{"runtime.hashString", "runtime.strhash"}
	default:
		// Pointers, structs, arrays: mapKeyHash falls through to key==key only.
		return nil
	}
}

// rewriteMapKeyHash drops cascade arms whose helpers are not in keep.
func rewriteMapKeyHash(mod *ir.Module, keep map[string]bool) {
	f := mod.LookupFunc("runtime.mapKeyHash")
	if f == nil || f.Body == nil {
		return
	}
	seq, ok := f.Body.(*ir.Seq)
	if !ok {
		return
	}
	var out []ir.Stmt
	for i := 0; i < len(seq.Stmts); i++ {
		s := seq.Stmts[i]
		as, isAssign := s.(*ir.Assign)
		if !isAssign || len(as.Lhs) != 2 {
			out = append(out, s)
			continue
		}
		in, isCall := as.Rhs.(*ir.Instr)
		if !isCall || in.Op != ir.OpCall || !isHashHelperCall(in.Func) {
			out = append(out, s)
			continue
		}
		// Cascade arm: call hashX; h=; ok=; if ok { return h }
		arm := []ir.Stmt{s}
		j := i + 1
		for j < len(seq.Stmts) && j < i+4 {
			arm = append(arm, seq.Stmts[j])
			if _, isIf := seq.Stmts[j].(*ir.If); isIf {
				j++
				break
			}
			j++
		}
		if keep[in.Func] {
			out = append(out, arm...)
		}
		// Drop unused VarDefs later by rebuilding without them — keep
		// referenced locals only via a second pass on `out`.
		i = j - 1
	}
	out = replaceMapKeyHashFallthrough(out, f)
	f.Body = &ir.Seq{Stmts: stripUnusedHashLocals(out)}
}

// replaceMapKeyHashFallthrough swaps the Go `return mapKeyHashFallthrough(...)`
// lowering (assign from call + return name, or a direct return call) for
// `return seed ^ 0x9e3779b9` so int-only specialization does not keep the
// unhashable-path helper live.
func replaceMapKeyHashFallthrough(stmts []ir.Stmt, f *ir.Func) []ir.Stmt {
	var seed *ir.Var
	for _, p := range f.Params {
		if p != nil && p.Name == "seed" {
			seed = p
			break
		}
	}
	if seed == nil && len(f.Params) >= 2 {
		seed = f.Params[1]
	}
	if seed == nil {
		return stmts
	}
	xor := &ir.Instr{
		Op: ir.OpI32Xor,
		Args: []ir.Expr{
			&ir.Name{Raw: seed.Name, Var: seed},
			&ir.Const{Op: ir.OpI32Const, I64: 0x9e3779b9},
		},
	}
	out := make([]ir.Stmt, 0, len(stmts))
	for i := 0; i < len(stmts); i++ {
		s := stmts[i]
		// Direct return call.
		if ret, ok := s.(*ir.Return); ok && len(ret.Results) == 1 {
			if call, ok := ret.Results[0].(*ir.Instr); ok && call.Op == ir.OpCall && call.Func == "runtime.mapKeyHashFallthrough" {
				out = append(out, &ir.Return{Results: []ir.Expr{xor}})
				continue
			}
		}
		// assign h = mapKeyHashFallthrough(...); return h
		// (also skip temp-forwarding assigns: %t2 = %t1; return %t2)
		if as, ok := s.(*ir.Assign); ok && len(as.Lhs) >= 1 {
			if call, ok := as.Rhs.(*ir.Instr); ok && call.Op == ir.OpCall && call.Func == "runtime.mapKeyHashFallthrough" {
				out = append(out, &ir.Return{Results: []ir.Expr{xor}})
				live := map[string]bool{}
				if d := as.Lhs[0]; d.Temp != nil {
					live[d.Temp.Name] = true
				} else if d.Var != nil {
					live[d.Var.Name] = true
				}
				for i+1 < len(stmts) {
					next := stmts[i+1]
					if as2, ok := next.(*ir.Assign); ok && len(as2.Lhs) == 1 && as2.Rhs != nil {
						if n, ok := as2.Rhs.(*ir.Name); ok {
							src := ""
							if n.Temp != nil {
								src = n.Temp.Name
							} else if n.Var != nil {
								src = n.Var.Name
							} else {
								src = n.Raw
							}
							if live[src] {
								if d := as2.Lhs[0]; d.Temp != nil {
									live[d.Temp.Name] = true
								} else if d.Var != nil {
									live[d.Var.Name] = true
								}
								i++
								continue
							}
						}
					}
					if ret, ok := next.(*ir.Return); ok {
						if len(ret.Results) == 1 {
							if n, ok := ret.Results[0].(*ir.Name); ok {
								src := ""
								if n.Temp != nil {
									src = n.Temp.Name
								} else if n.Var != nil {
									src = n.Var.Name
								} else {
									src = n.Raw
								}
								if live[src] || len(live) > 0 {
									i++
								}
							} else if len(live) > 0 {
								i++
							}
						} else if len(live) > 0 {
							i++
						}
					}
					break
				}
				continue
			}
		}
		out = append(out, s)
	}
	return out
}

func isHashHelperCall(name string) bool {
	switch name {
	case "runtime.hashBool", "runtime.hashInt", "runtime.hashInt64",
		"runtime.hashUint", "runtime.hashUint64",
		"runtime.hashFloat32", "runtime.hashFloat64",
		"runtime.hashComplex64", "runtime.hashComplex128",
		"runtime.hashString":
		return true
	}
	return false
}

func stripUnusedHashLocals(stmts []ir.Stmt) []ir.Stmt {
	used := map[string]bool{}
	markExpr := func(e ir.Expr) {
		if e == nil {
			return
		}
		walkExprTree(e, func(x ir.Expr) {
			if n, ok := x.(*ir.Name); ok && n.Var != nil {
				used[n.Var.Name] = true
			}
		})
	}
	markDest := func(d ir.Dest) {
		if d.Var != nil {
			used[d.Var.Name] = true
		}
	}
	for _, s := range stmts {
		switch s := s.(type) {
		case *ir.Assign:
			for _, d := range s.Lhs {
				markDest(d)
			}
			markExpr(s.Rhs)
		case *ir.Return:
			for _, e := range s.Results {
				markExpr(e)
			}
		case *ir.ExprStmt:
			markExpr(s.X)
		case *ir.If:
			markExpr(s.Cond)
			walkExprs(s.Then, markExpr)
			walkExprs(s.Else, markExpr)
		}
	}
	out := stmts[:0]
	for _, s := range stmts {
		if vd, ok := s.(*ir.VarDef); ok {
			if vd.Var != nil && !used[vd.Var.Name] {
				continue
			}
		}
		out = append(out, s)
	}
	return out
}
