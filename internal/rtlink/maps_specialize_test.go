package rtlink

import (
	"path/filepath"
	"testing"

	"github.com/achille-roussel/wasmgc-go/internal/ir"
	"github.com/achille-roussel/wasmgc-go/internal/stdlib"
)

func TestSpecializeMapHashesIntOnly(t *testing.T) {
	root, err := stdlib.CompilerModuleRoot()
	if err != nil {
		t.Fatal(err)
	}
	mod := &ir.Module{}
	mod.Funcs = []*ir.Func{{
		Name:   "main.main",
		Export: "run",
		Body: &ir.Seq{Stmts: []ir.Stmt{&ir.ExprStmt{X: &ir.Instr{
			Op: ir.OpMapSet, Str: "int",
		}}}},
	}}
	rd := filepath.Join(root, "std", "src")
	if err := EnsureMaps(mod, rd); err != nil {
		t.Fatal(err)
	}
	if mod.LookupFunc("runtime.hashInt") == nil {
		t.Fatal("expected hashInt kept")
	}
	for _, name := range []string{
		"runtime.hashBool", "runtime.hashString", "runtime.hashFloat64",
		"runtime.strhash", "runtime.f32hash",
	} {
		if mod.LookupFunc(name) != nil {
			t.Fatalf("expected %s dropped for int-only map", name)
		}
	}
	f := mod.LookupFunc("runtime.mapKeyHash")
	if f == nil {
		t.Fatal("missing mapKeyHash")
	}
	var calls []string
	walkCalls(f.Body, func(name string) { calls = append(calls, name) })
	for _, c := range calls {
		if c != "runtime.hashInt" {
			t.Fatalf("mapKeyHash still calls %s: %v", c, calls)
		}
	}
}
