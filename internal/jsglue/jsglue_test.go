package jsglue_test

import (
	"os"
	"path/filepath"
	"runtime"
	"testing"

	"github.com/achille-roussel/wasmgc-go/internal/jsglue"
)

func TestMatchesLib(t *testing.T) {
	_, thisFile, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("runtime.Caller failed")
	}
	lib := filepath.Join(filepath.Dir(thisFile), "..", "..", "lib", "wasmgc.js")
	want, err := os.ReadFile(lib)
	if err != nil {
		t.Fatal(err)
	}
	if string(want) != jsglue.Source {
		t.Fatalf("embedded wasmgc.js diverged from lib/wasmgc.js (%d vs %d bytes)", len(jsglue.Source), len(want))
	}
}

func TestPath(t *testing.T) {
	t.Setenv("WASMGC_JS", "")
	p, err := jsglue.Path()
	if err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(p)
	if err != nil {
		t.Fatal(err)
	}
	if string(data) != jsglue.Source {
		t.Fatal("materialized file does not match embed")
	}
}
