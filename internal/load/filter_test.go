package load

import (
	"os"
	"path/filepath"
	"testing"
)

func TestEvalTagsWasmgc(t *testing.T) {
	c := DefaultFilter()
	cases := []struct {
		line string
		want bool
	}{
		{"//go:build !wasm", true},
		{"//go:build wasm", false},
		{"//go:build js && wasm", false},
		{"//go:build js && wasmgc", true},
		{"//go:build ignore", false},
		{"//go:build !amd64 && !wasm", true},
		{"//go:build 386 || amd64 || wasm", false},
		{"//go:build goexperiment.jsonv2", true},
	}
	for _, tc := range cases {
		got, err := c.EvalTags(tc.line)
		if err != nil {
			t.Fatalf("%s: %v", tc.line, err)
		}
		if got != tc.want {
			t.Errorf("%s: got %v want %v", tc.line, got, tc.want)
		}
	}
}

func TestMatchGoFileSuffixes(t *testing.T) {
	dir := t.TempDir()
	files := map[string]string{
		"plain.go":         "package p\n",
		"foo_js.go":        "package p\n",
		"foo_wasm.go":      "package p\n",
		"foo_wasmgc.go":    "package p\n",
		"foo_amd64.go":     "package p\n",
		"tagged_wasm.go":   "//go:build wasm\n\npackage p\n",
		"tagged_notwasm.go": "//go:build !wasm\n\npackage p\n",
		"ignored.go":       "//go:build ignore\n\npackage p\n",
		"plain_test.go":    "package p\n",
	}
	for name, body := range files {
		if err := os.WriteFile(filepath.Join(dir, name), []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	c := DefaultFilter()
	want := map[string]bool{
		"plain.go":          true,
		"foo_js.go":         true,
		"foo_wasm.go":       false,
		"foo_wasmgc.go":     true,
		"foo_amd64.go":      false,
		"tagged_wasm.go":    false,
		"tagged_notwasm.go": true,
		"ignored.go":        false,
	}
	for name, keep := range want {
		ok, err := c.MatchGoFile(dir, name)
		if err != nil {
			t.Fatalf("%s: %v", name, err)
		}
		if ok != keep {
			t.Errorf("%s: got %v want %v", name, ok, keep)
		}
	}
	list, err := c.ListGoFiles(dir)
	if err != nil {
		t.Fatal(err)
	}
	for _, f := range list {
		base := filepath.Base(f)
		if base == "plain_test.go" {
			t.Errorf("ListGoFiles included test file")
		}
		if !want[base] {
			t.Errorf("ListGoFiles kept %s", base)
		}
	}
}
