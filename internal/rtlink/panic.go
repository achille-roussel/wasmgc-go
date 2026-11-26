package rtlink

import (
	"fmt"
	"os"
	"path/filepath"

	"github.com/achille-roussel/wasmgc-go/internal/ir"
	"github.com/achille-roussel/wasmgc-go/internal/lower"
)

// EnsurePanic links body-less runtime panic/defer symbols from
// std/src/runtime/panic.go when the module needs them. Codegen fills EH bodies.
// panicIndex + itoa live in panic_index.go and are linked only when needed so
// closure/nil-check programs do not pull digit tables (M5 size).
//
// When package sync (or anything else) imports runtime, PackageHigh already
// lowers panic.go with opaque *byte params; we still must rewrite those to
// $go.defer / $go.panicObj even if no files are linked here.
func EnsurePanic(mod *ir.Module, rewriteDir string) error {
	if mod == nil {
		return nil
	}
	need := scanPanicNeeds(mod)
	missing := panicSymsMissing(mod)
	needIndex := scanPanicIndex(mod)
	// Also fix signatures when runtime was loaded as a package dependency
	// (opaque *byte params) even if scanPanicNeeds is currently false.
	hasPanicSyms := mod.LookupFunc("runtime.unrecoveredPanic") != nil ||
		mod.LookupFunc("runtime.deferRun") != nil
	if need && missing {
		if rewriteDir == "" {
			return fmt.Errorf("rtlink: rewrite dir unset; cannot link runtime panic")
		}
		path := filepath.Join(rewriteDir, "runtime", "panic.go")
		src, err := os.ReadFile(path)
		if err != nil {
			return fmt.Errorf("rtlink: read %s: %w", path, err)
		}
		fset, files, pkg, info, err := lower.TypeCheck(path, string(src))
		if err != nil {
			return fmt.Errorf("rtlink: typecheck panic.go: %w", err)
		}
		stripPanicFuncs(mod)
		if err := lower.AppendFiles(mod, fset, files, pkg, info); err != nil {
			return fmt.Errorf("rtlink: lower panic.go: %w", err)
		}
		hasPanicSyms = true
	}
	needSliceToArr := mod.LookupFunc("runtime.panicSliceToArray") == nil && scanCallsName(mod, "runtime.panicSliceToArray")
	if (needIndex && mod.LookupFunc("runtime.panicIndex") == nil) || needSliceToArr {
		if rewriteDir == "" {
			return fmt.Errorf("rtlink: rewrite dir unset; cannot link runtime panic_index")
		}
		path := filepath.Join(rewriteDir, "runtime", "panic_index.go")
		src, err := os.ReadFile(path)
		if err != nil {
			return fmt.Errorf("rtlink: read %s: %w", path, err)
		}
		// Type-check with panic.go so throwPanic is in scope.
		core := filepath.Join(rewriteDir, "runtime", "panic.go")
		coreSrc, err := os.ReadFile(core)
		if err != nil {
			return fmt.Errorf("rtlink: read %s: %w", core, err)
		}
		fset, files, pkg, info, err := lower.TypeCheckFiles(
			[]string{core, path},
			[]string{string(coreSrc), string(src)},
		)
		if err != nil {
			return fmt.Errorf("rtlink: typecheck panic_index.go: %w", err)
		}
		// Only append the index file's funcs: strip index syms, lower both,
		// then drop duplicate core symbols that AppendFiles would re-add.
		stripPanicIndexFuncs(mod)
		prior := append([]*ir.Func(nil), mod.Funcs...)
		if err := lower.AppendFiles(mod, fset, files, pkg, info); err != nil {
			return fmt.Errorf("rtlink: lower panic_index.go: %w", err)
		}
		// Keep prior funcs; add panicIndex* / panicBounds / itoa helpers.
		index := map[string]*ir.Func{}
		for _, f := range mod.Funcs {
			switch f.Name {
			case "runtime.panicIndex", "runtime.panicIndexU", "runtime.panicBounds",
				"runtime.panicSliceToArray", "runtime.itoa", "runtime.itoa64",
				"runtime.uitoa", "runtime.digit":
				index[f.Name] = f
			}
		}
		mod.Funcs = prior
		for _, name := range []string{
			"runtime.panicIndex", "runtime.panicIndexU", "runtime.panicBounds",
			"runtime.panicSliceToArray", "runtime.itoa", "runtime.itoa64",
			"runtime.uitoa", "runtime.digit",
		} {
			if f := index[name]; f != nil && mod.LookupFunc(name) == nil {
				mod.Funcs = append(mod.Funcs, f)
			}
		}
		hasPanicSyms = true
	}
	if hasPanicSyms {
		fixPanicSignatures(mod)
	}
	// Drop unused static panic helpers (divide/uncomparable/…) when unreferenced.
	stripUnreferencedPanicHelpers(mod)
	return nil
}

func panicSymsMissing(mod *ir.Module) bool {
	refs := map[string]bool{}
	for _, f := range mod.Funcs {
		walkCalls(f.Body, func(name string) {
			for _, s := range panicSyms {
				if name == s {
					refs[s] = true
				}
			}
		})
	}
	for s := range refs {
		if mod.LookupFunc(s) == nil {
			return true
		}
	}
	// Core suite (without panicIndex — that is optional / separate file).
	for _, s := range []string{
		"runtime.throwPanic", "runtime.deferRun", "runtime.panicNil",
	} {
		if mod.LookupFunc(s) == nil {
			return true
		}
	}
	return false
}

func scanPanicIndex(mod *ir.Module) bool {
	need := false
	for _, f := range mod.Funcs {
		walkCalls(f.Body, func(name string) {
			if name == "runtime.panicIndex" || name == "runtime.panicIndexU" ||
				name == "runtime.panicBounds" || name == "runtime.panicSliceToArray" {
				need = true
			}
		})
		walkHighOps(f.Body, func(op ir.Op) {
			switch op {
			case ir.OpSliceGet, ir.OpSliceSet, ir.OpStringGet,
				ir.OpSliceAppend, ir.OpSliceSlice, ir.OpStringSlice:
				need = true
			}
		})
	}
	return need
}

func fixPanicSignatures(mod *ir.Module) {
	// Ensure prelude-shaped types exist for the rewritten params.
	mod.InternType(&ir.TypeDef{
		Name:  "$go.defer",
		Kind:  ir.TypeStruct,
		Super: "$go.object",
		Fields: []ir.Field{
			{Name: "fn", Type: &ir.Ref{Named: "$go.clos"}},
			{Name: "link", Type: &ir.Ref{Null: true, Named: "$go.defer"}, Mut: true},
		},
	})
	mod.InternType(&ir.TypeDef{
		Name:  "$go.clos",
		Kind:  ir.TypeStruct,
		Super: "$go.object",
		Fields: []ir.Field{
			{Name: "fn", Type: &ir.Ref{Null: true, Heap: ir.HeapFunc}},
		},
	})
	if f := mod.LookupFunc("runtime.deferRun"); f != nil && len(f.Params) == 1 {
		f.Params[0].Typ = &ir.Ref{Null: true, Named: "$go.defer"}
	}
	if f := mod.LookupFunc("runtime.unrecoveredPanic"); f != nil && len(f.Params) == 1 {
		f.Params[0].Typ = &ir.Ref{Named: "$go.panicObj"}
	}
}

var panicSyms = []string{
	"runtime.deferRun",
	"runtime.throwPanic",
	"runtime.gorecover",
	"runtime.goexit",
	"runtime.unrecoveredPanic",
	"runtime.panicIndex",
	"runtime.panicIndexU",
	"runtime.panicBounds",
	"runtime.panicSliceToArray",
	"runtime.panicSliceB",
	"runtime.panicNil",
	"runtime.panicDivide",
	"runtime.panicNegativeShift",
	"runtime.panicUncomparable",
	"runtime.panicTypeAssert",
	"runtime.panicTypeAssertTypes",
	"runtime.panicTypeAssertNil",
	"runtime.panicTypeAssertMissing",
	"runtime.panicmakeslicelen",
	"runtime.panicmakeslicecap",
	"runtime.panicrangestate",
}

func scanPanicNeeds(mod *ir.Module) bool {
	need := false
	for _, f := range mod.Funcs {
		walkCalls(f.Body, func(name string) {
			for _, s := range panicSyms {
				if name == s {
					need = true
				}
			}
		})
		walkHighOps(f.Body, func(op ir.Op) {
			switch op {
			case ir.OpPanic, ir.OpRecover, ir.OpDefer,
				ir.OpSliceGet, ir.OpSliceSet, ir.OpStringGet,
				ir.OpSliceAppend, ir.OpSliceSlice, ir.OpStringSlice,
				ir.OpMakeSlice:
				need = true
			}
		})
		if regionHasTry(f.Body) {
			need = true
		}
	}
	return need
}

func stripPanicFuncs(mod *ir.Module) {
	names := map[string]bool{}
	for _, s := range panicSyms {
		names[s] = true
	}
	names["runtime.itoa"] = true
	names["runtime.itoa64"] = true
	names["runtime.uitoa"] = true
	names["runtime.digit"] = true
	// plainError methods + ctor ship in panic.go; drop before re-AppendFiles
	// so PackageHigh + EnsurePanic do not duplicate.
	names["runtime.plainError.Error"] = true
	names["runtime.plainError.RuntimeError"] = true
	names["runtime.panicErr"] = true
	out := mod.Funcs[:0]
	for _, f := range mod.Funcs {
		if names[f.Name] {
			continue
		}
		out = append(out, f)
	}
	mod.Funcs = out
}

func scanCallsName(mod *ir.Module, want string) bool {
	found := false
	for _, f := range mod.Funcs {
		walkCalls(f.Body, func(name string) {
			if name == want {
				found = true
			}
		})
	}
	return found
}

func stripPanicIndexFuncs(mod *ir.Module) {
	names := map[string]bool{
		"runtime.panicIndex":        true,
		"runtime.panicIndexU":       true,
		"runtime.panicBounds":       true,
		"runtime.panicSliceToArray": true,
		"runtime.itoa":              true,
		"runtime.itoa64":            true,
		"runtime.uitoa":             true,
		"runtime.digit":             true,
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

// stripUnreferencedPanicHelpers removes static panic* message funcs that nothing
// calls. Keeps EH body-less symbols and anything still referenced.
func stripUnreferencedPanicHelpers(mod *ir.Module) {
	optional := []string{
		"runtime.panicSliceB",
		"runtime.panicDivide",
		"runtime.panicNegativeShift",
		"runtime.panicUncomparable",
		"runtime.panicTypeAssert",
		"runtime.panicmakeslicelen",
		"runtime.panicmakeslicecap",
		"runtime.panicNil",
		"runtime.panicrangestate",
		"runtime.goexit",
	}
	refs := map[string]bool{}
	for _, f := range mod.Funcs {
		walkCalls(f.Body, func(name string) { refs[name] = true })
	}
	drop := map[string]bool{}
	for _, s := range optional {
		if !refs[s] {
			drop[s] = true
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

func regionHasTry(r ir.Region) bool {
	if r == nil {
		return false
	}
	switch r := r.(type) {
	case *ir.Seq:
		for _, s := range r.Stmts {
			if stmtHasTry(s) {
				return true
			}
		}
	case *ir.Block:
		return regionHasTry(r.Body)
	case *ir.Loop:
		return regionHasTry(r.Body)
	case *ir.If:
		return regionHasTry(r.Then) || regionHasTry(r.Else)
	case *ir.Switch:
		for _, c := range r.Cases {
			if regionHasTry(c.Body) {
				return true
			}
		}
		return regionHasTry(r.Default)
	case *ir.Try:
		return true
	case *ir.Dispatch:
		for _, a := range r.Arms {
			if regionHasTry(a) {
				return true
			}
		}
	}
	return false
}

func stmtHasTry(s ir.Stmt) bool {
	if r, ok := s.(ir.Region); ok {
		return regionHasTry(r)
	}
	return false
}
