package rtlink

import (
	"path/filepath"
	"testing"

	"github.com/achille-roussel/wasmgc-go/internal/ir"
	"github.com/achille-roussel/wasmgc-go/internal/lower"
	"github.com/achille-roussel/wasmgc-go/internal/stdlib"
)

// Regression: Finish runs before EnsureSched AppendFiles. Without
// LinkPendingInits, runtime.init (gp/ngo=1) never runs and the first park
// hits checkDeadlock ("all goroutines are asleep"). DCE then drops the
// unreachable init — mutex Unlock cannot wake a waiter that never parked
// correctly either. Guard: after EnsureSched + LinkPendingInits + prune,
// main.run must call a live runtime.init.*.
func TestSchedInitLinkedAndKept(t *testing.T) {
	root, err := stdlib.CompilerModuleRoot()
	if err != nil {
		t.Fatal(err)
	}
	mod := &ir.Module{}
	// Finish-shaped entry (no inits yet), plus a go/sema use so EnsureSched links.
	mod.Funcs = []*ir.Func{
		{
			Name:   "main.run",
			Export: "run",
			Body: &ir.Seq{Stmts: []ir.Stmt{
				&ir.ExprStmt{X: &ir.Instr{Op: ir.OpCall, Func: "main.main"}},
				&ir.Return{},
			}},
		},
		{
			Name: "main.main",
			Body: &ir.Seq{Stmts: []ir.Stmt{
				&ir.ExprStmt{X: &ir.Instr{Op: ir.OpCall, Func: "runtime.Semacquire"}},
				&ir.ExprStmt{X: &ir.Instr{Op: ir.OpCall, Func: "runtime.go_"}},
				&ir.Return{},
			}},
		},
	}
	rd := filepath.Join(root, "std", "src")
	if err := EnsureSched(mod, rd); err != nil {
		t.Fatal(err)
	}
	var initBefore string
	for _, f := range mod.Funcs {
		if len(f.Name) > 13 && f.Name[:13] == "runtime.init." {
			initBefore = f.Name
			break
		}
	}
	if initBefore == "" {
		t.Fatal("EnsureSched did not lower runtime.init")
	}
	lower.LinkPendingInits(mod)
	ir.PruneUnreachable(mod)

	if mod.LookupFunc(initBefore) == nil {
		t.Fatalf("PruneUnreachable dropped %s (must stay reachable from main.run)", initBefore)
	}
	run := mod.LookupFunc("main.run")
	if run == nil {
		t.Fatal("missing main.run")
	}
	found := false
	var calls []string
	walkCalls(run.Body, func(name string) {
		calls = append(calls, name)
		if name == initBefore {
			found = true
		}
	})
	if !found {
		t.Fatalf("main.run does not call %s; calls=%v", initBefore, calls)
	}
}
