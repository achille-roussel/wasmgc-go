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
	if hasPanicSyms {
		fixPanicSignatures(mod)
	}
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
	// High ops / try also need the core panic suite even without direct calls.
	for _, s := range []string{
		"runtime.throwPanic", "runtime.deferRun", "runtime.panicIndex",
		"runtime.panicTypeAssert", "runtime.panicNil",
	} {
		if mod.LookupFunc(s) == nil {
			// Only require if scanPanicNeeds already said we need panics;
			// missing any core symbol means we should (re)link.
			return true
		}
	}
	return false
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
	"runtime.panicSliceB",
	"runtime.panicNil",
	"runtime.panicDivide",
	"runtime.panicUncomparable",
	"runtime.panicTypeAssert",
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
				ir.OpSliceAppend, ir.OpSliceSlice, ir.OpStringSlice:
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
	out := mod.Funcs[:0]
	for _, f := range mod.Funcs {
		if names[f.Name] {
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
