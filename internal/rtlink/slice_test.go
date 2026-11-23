package rtlink

import (
	"path/filepath"
	"testing"

	"github.com/achille-roussel/wasmgc-go/internal/lower"
	"github.com/achille-roussel/wasmgc-go/internal/stdlib"
)

func TestEnsureSliceLinksPanicIndex(t *testing.T) {
	mod, err := lower.Compile("main.go", `package main
func main() {
	s := []int{1, 2, 3}
	println(s[1])
}
`)
	if err != nil {
		t.Fatal(err)
	}
	if mod.LookupFunc("runtime.panicIndex") != nil {
		t.Fatal("expected no panicIndex before rtlink")
	}
	root, err := stdlib.CompilerModuleRoot()
	if err != nil {
		t.Fatal(err)
	}
	rewrite := filepath.Join(root, "std", "src")
	if err := EnsureSlice(mod, rewrite); err != nil {
		t.Fatal(err)
	}
	if f := mod.LookupFunc("runtime.panicIndex"); f == nil || !emptyBody(f) {
		t.Fatal("expected body-less runtime.panicIndex")
	}
}
