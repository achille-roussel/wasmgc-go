package lower

import (
	"testing"

	"github.com/achille-roussel/wasmgc-go/internal/ir"
)

func TestLinkPendingInitsPrepends(t *testing.T) {
	mod := &ir.Module{}
	md := metaOf(mod)
	md.initOrder = []string{"runtime.init.0", "sync.init.0"}
	mod.Funcs = []*ir.Func{
		{
			Name:   "main.run",
			Export: "run",
			Body: &ir.Seq{Stmts: []ir.Stmt{
				&ir.ExprStmt{X: &ir.Instr{Op: ir.OpCall, Func: "sync.init.0"}},
				&ir.ExprStmt{X: &ir.Instr{Op: ir.OpCall, Func: "main.main"}},
				&ir.Return{},
			}},
		},
		{Name: "main.main", Body: &ir.Seq{}},
		{Name: "runtime.init.0", Body: &ir.Seq{}},
		{Name: "sync.init.0", Body: &ir.Seq{}},
	}
	LinkPendingInits(mod)
	seq := mod.LookupFunc("main.run").Body.(*ir.Seq)
	var calls []string
	for _, s := range seq.Stmts {
		es, ok := s.(*ir.ExprStmt)
		if !ok {
			continue
		}
		in, ok := es.X.(*ir.Instr)
		if !ok || in.Op != ir.OpCall {
			continue
		}
		calls = append(calls, in.Func)
	}
	want := []string{"sync.init.0", "runtime.init.0", "main.main"}
	if len(calls) != len(want) {
		t.Fatalf("calls=%v want %v", calls, want)
	}
	for i := range want {
		if calls[i] != want[i] {
			t.Fatalf("calls=%v want %v", calls, want)
		}
	}
}
