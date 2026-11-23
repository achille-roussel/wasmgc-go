package rtlink

import (
	"path/filepath"
	"testing"

	"github.com/achille-roussel/wasmgc-go/internal/lower"
	"github.com/achille-roussel/wasmgc-go/internal/stdlib"
)

func TestEnsurePrintLinksPrintint(t *testing.T) {
	mod, err := lower.Compile("main.go", `package main
func main() {
	println(42)
}
`)
	if err != nil {
		t.Fatal(err)
	}
	if mod.LookupFunc("runtime.printint") != nil {
		t.Fatal("expected no runtime.printint before rtlink")
	}
	root, err := stdlib.CompilerModuleRoot()
	if err != nil {
		t.Fatal(err)
	}
	rewrite := filepath.Join(root, "std", "src")
	if err := EnsurePrint(mod, rewrite); err != nil {
		t.Fatal(err)
	}
	if err := lower.RuntimeOps(mod); err != nil {
		t.Fatal(err)
	}
	if mod.LookupFunc("runtime.printstring") == nil {
		t.Fatal("missing printstring")
	}
	if f := mod.LookupFunc("runtime.printint"); f == nil || emptyBody(f) {
		t.Fatal("expected Go printint body")
	}
	if f := mod.LookupFunc("runtime.printnl"); f == nil || emptyBody(f) {
		t.Fatal("expected Go printnl body")
	}
}
