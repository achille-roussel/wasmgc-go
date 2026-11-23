package main

import (
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/achille-roussel/wasmgc-go/internal/pipeline"
)

// TestM1EmptyMainE2E is the M1 exit gate: wasmgc-go run of empty main exits 0,
// the module is under 2048 bytes with names off, and dump-ir matches the golden.
func TestM1EmptyMainE2E(t *testing.T) {
	if !pipeline.Default.Real() {
		t.Fatal("expected real lowerer wired in pipeline.Default")
	}
	runEmptyMainM1(t)
}

// TestM2PrintlnE2E is the M2 hello gate: println("hello, world") under node.
// Size is linked against std/src/runtime print (rtlink); budget stays 8192.
// Baseline with codegen SynthPrintRuntime alone was 378 bytes; with Go
// printint/printnl + intrinsic printstring expect under ~2 KiB.
func TestM2PrintlnE2E(t *testing.T) {
	if !pipeline.Default.Real() {
		t.Fatal("expected real lowerer wired in pipeline.Default")
	}
	runM2(t, "println", "hello, world\n", 8192)
}

// TestM2FibE2E prints fib(20) → 6765 with no extra NUL.
func TestM2FibE2E(t *testing.T) {
	if !pipeline.Default.Real() {
		t.Fatal("expected real lowerer wired in pipeline.Default")
	}
	runM2(t, "fib", "6765\n", 0)
}

func runM2(t *testing.T, name, wantStdout string, maxSize int) {
	t.Helper()
	dir := t.TempDir()
	srcDir := filepath.Join("testdata", name)
	for _, n := range []string{"main.go", "go.mod"} {
		data, err := os.ReadFile(filepath.Join(srcDir, n))
		if err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(dir, n), data, 0o644); err != nil {
			t.Fatal(err)
		}
	}
	exe := buildDriver(t)
	if maxSize > 0 {
		wasmPath := filepath.Join(dir, name+".wasm")
		cmd := exec.Command(exe, "build", "-skip-stdlib", "-o", wasmPath, ".")
		cmd.Dir = dir
		out, err := cmd.CombinedOutput()
		if err != nil {
			t.Fatalf("build: %v\n%s", err, out)
		}
		wasm, err := os.ReadFile(wasmPath)
		if err != nil {
			t.Fatal(err)
		}
		if len(wasm) > maxSize {
			t.Fatalf("wasm size %d > %d", len(wasm), maxSize)
		}
		t.Logf("%s.wasm size = %d", name, len(wasm))
	}
	cmd := exec.Command(exe, "run", "-skip-stdlib", ".")
	cmd.Dir = dir
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		t.Fatalf("run: %v\nstdout=%q stderr=%q", err, stdout.String(), stderr.String())
	}
	if stdout.String() != wantStdout {
		t.Fatalf("stdout %q, want %q", stdout.String(), wantStdout)
	}
}

func runEmptyMainM1(t *testing.T) {
	t.Helper()
	dir := t.TempDir()
	srcDir := filepath.Join("testdata", "empty_main")
	for _, name := range []string{"main.go", "go.mod"} {
		data, err := os.ReadFile(filepath.Join(srcDir, name))
		if err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(dir, name), data, 0o644); err != nil {
			t.Fatal(err)
		}
	}

	exe := buildDriver(t)

	cmd := exec.Command(exe, "dump-ir", "-skip-stdlib", ".")
	cmd.Dir = dir
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("dump-ir: %v\n%s", err, out)
	}
	got := string(out)
	wantBytes, err := os.ReadFile(filepath.Join(srcDir, "empty_main.ir"))
	if err != nil {
		t.Fatal(err)
	}
	want := string(wantBytes)
	if !strings.HasSuffix(want, "\n") {
		want += "\n"
	}
	if !strings.HasSuffix(got, "\n") {
		got += "\n"
	}
	if got != want {
		t.Fatalf("dump-ir mismatch\n--- got\n%s--- want\n%s", got, want)
	}

	wasmPath := filepath.Join(dir, "empty.wasm")
	cmd = exec.Command(exe, "build", "-skip-stdlib", "-o", wasmPath, ".")
	cmd.Dir = dir
	out, err = cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("build: %v\n%s", err, out)
	}
	wasm, err := os.ReadFile(wasmPath)
	if err != nil {
		t.Fatal(err)
	}
	if len(wasm) > 2048 {
		t.Fatalf("wasm size %d > 2048", len(wasm))
	}
	t.Logf("empty main wasm size = %d", len(wasm))

	cmd = exec.Command(exe, "run", "-skip-stdlib", ".")
	cmd.Dir = dir
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		t.Fatalf("run: %v\nstdout=%q stderr=%q", err, stdout.String(), stderr.String())
	}
	if stdout.Len() != 0 {
		t.Fatalf("run stdout not empty: %q", stdout.String())
	}
}

func buildDriver(t *testing.T) string {
	t.Helper()
	exe := filepath.Join(t.TempDir(), "wasmgc-go")
	cmd := exec.Command("go", "build", "-o", exe, ".")
	cmd.Dir = "."
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("go build driver: %v\n%s", err, out)
	}
	// Point the driver at the repo glue; the test package lives in a temp module.
	glue, err := filepath.Abs(filepath.Join("..", "..", "lib", "wasmgc.js"))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(glue); err != nil {
		t.Fatalf("lib/wasmgc.js: %v", err)
	}
	t.Setenv("WASMGC_JS", glue)
	return exe
}

func TestVersion(t *testing.T) {
	exe := buildDriver(t)
	out, err := exec.Command(exe, "version").CombinedOutput()
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(out), "wasmgc-go") {
		t.Fatalf("version: %s", out)
	}
}
