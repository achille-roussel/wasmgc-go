package load_test

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/achille-roussel/wasmgc-go/internal/load"
)

func TestLoadEmptyMain(t *testing.T) {
	dir := t.TempDir()
	src := "package main\n\nfunc main() {}\n"
	if err := os.WriteFile(filepath.Join(dir, "main.go"), []byte(src), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "go.mod"), []byte("module example.com/empty\n\ngo 1.27\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	prog, err := load.Load(load.Config{Dir: dir, Pattern: "."})
	if err != nil {
		t.Fatal(err)
	}
	if prog.Main == nil || prog.Main.Name != "main" {
		t.Fatalf("main: %+v", prog.Main)
	}
	if len(prog.Main.GoFiles) != 1 {
		t.Fatalf("GoFiles: %v", prog.Main.GoFiles)
	}
	if prog.Sizes == nil {
		t.Fatal("Sizes unset")
	}
}

func TestLoadEmptyMainFilesMode(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "main.go")
	if err := os.WriteFile(path, []byte("package main\nfunc main() {}\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	prog, err := load.Load(load.Config{Dir: dir, Files: []string{path}})
	if err != nil {
		t.Fatal(err)
	}
	if prog.Main.Name != "main" {
		t.Fatalf("got %q", prog.Main.Name)
	}
}

func TestLoadRefilterDropsWasmKeepsWasmgc(t *testing.T) {
	dir := t.TempDir()
	files := map[string]string{
		"go.mod":        "module example.com/tags\n\ngo 1.27\n",
		"main.go":       "package main\nfunc main() { _ = Y }\n",
		"helper_wasm.go": "//go:build wasm\n\npackage main\nvar X = 1\n",
		"helper_wasmgc.go": "package main\nvar Y = 2\n",
	}
	for name, body := range files {
		if err := os.WriteFile(filepath.Join(dir, name), []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	prog, err := load.Load(load.Config{Dir: dir, Pattern: "."})
	if err != nil {
		t.Fatal(err)
	}
	var bases []string
	for _, f := range prog.Main.GoFiles {
		bases = append(bases, filepath.Base(f))
	}
	joined := filepath.Join(bases...)
	for _, b := range bases {
		if b == "helper_wasm.go" {
			t.Fatalf("wasm file survived re-filter: %v", bases)
		}
	}
	found := false
	for _, b := range bases {
		if b == "helper_wasmgc.go" {
			found = true
		}
	}
	if !found {
		t.Fatalf("wasmgc file missing: %v (%s)", bases, joined)
	}
}

func TestLoadWithFakeRuntime(t *testing.T) {
	// Files mode avoids go list pulling the full real-GOROOT runtime dep graph;
	// only package runtime is resolved from the fake tree.
	goroot, err := load.WriteFakeGOROOT(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	dir := t.TempDir()
	path := filepath.Join(dir, "main.go")
	src := "package main\nimport _ \"runtime\"\nfunc main() {}\n"
	if err := os.WriteFile(path, []byte(src), 0o644); err != nil {
		t.Fatal(err)
	}
	prog, err := load.Load(load.Config{Dir: dir, Files: []string{path}, GOROOT: goroot})
	if err != nil {
		t.Fatal(err)
	}
	sawRuntime := false
	for _, p := range prog.Packages {
		if p.PkgPath == "runtime" {
			sawRuntime = true
			if !p.Standard {
				t.Fatal("runtime should be standard")
			}
		}
	}
	if !sawRuntime {
		t.Fatal("runtime not loaded")
	}
}

func TestWriteFakeGOROOT(t *testing.T) {
	root, err := load.WriteFakeGOROOT(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(root, "src", "runtime", "runtime.go")); err != nil {
		t.Fatal(err)
	}
}

func TestLoadUndottedModule(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "main.go"), []byte("package main\nfunc main() {}\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "go.mod"), []byte("module hello\n\ngo 1.22\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	prog, err := load.Load(load.Config{Dir: dir, Pattern: "."})
	if err != nil {
		t.Fatal(err)
	}
	if prog.Main == nil || prog.Main.Name != "main" {
		t.Fatalf("main: %+v", prog.Main)
	}
}
