package rtlink

import "github.com/achille-roussel/wasmgc-go/internal/ir"

// EnsurePool stubs runtime.RegisterPoolCleanup when sync.Pool's init calls it.
// PackageHigh skips GOROOT runtime, so the real no-op in runtime.go is never
// lowered; GC-cycle pool cleanup is not needed until a GC hook exists.
func EnsurePool(mod *ir.Module) {
	if mod == nil || mod.LookupFunc("runtime.RegisterPoolCleanup") != nil {
		return
	}
	need := false
	for _, f := range mod.Funcs {
		walkCalls(f.Body, func(name string) {
			if name == "runtime.RegisterPoolCleanup" {
				need = true
			}
		})
	}
	if !need {
		return
	}
	// cleanup is a closure ref (any); ignore it.
	mod.Funcs = append(mod.Funcs, &ir.Func{
		Name:   "runtime.RegisterPoolCleanup",
		Params: []*ir.Var{{Name: "cleanup", Typ: &ir.Ref{Null: true, Heap: ir.HeapAny}}},
		Body:   &ir.Seq{Stmts: []ir.Stmt{&ir.Return{}}},
	})
}
