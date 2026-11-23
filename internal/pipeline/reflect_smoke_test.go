package pipeline_test

import (
	"path/filepath"
	"testing"

	"github.com/achille-roussel/wasmgc-go/internal/pipeline"
	"github.com/achille-roussel/wasmgc-go/internal/stdlib"
)

func TestReflectSmokeLower(t *testing.T) {
	root, err := stdlib.CompilerModuleRoot()
	if err != nil {
		t.Fatal(err)
	}
	p, err := stdlib.DefaultPaths(root)
	if err != nil {
		t.Fatal(err)
	}
	mat, err := stdlib.Materialize(p)
	if err != nil {
		t.Fatal(err)
	}
	dir := filepath.Join(root, "cmd/wasmgc-go/testdata/m8_reflect_basic")
	_, _, err = pipeline.DumpIR(pipeline.Options{Dir: dir, GOROOT: mat.GOROOT})
	if err != nil {
		t.Fatal(err)
	}
}
