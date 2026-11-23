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
//
// Skip until stream D (internal/lower) is wired in internal/pipeline/wire.go.
// Codegen (A') and stdlib (H) are already real. When D lands, replace
// stubLowerer with a lower.Package adapter so pipeline.Default.Real() becomes
// true; this test then runs without further changes.
func TestM1EmptyMainE2E(t *testing.T) {
	if !pipeline.Default.Real() {
		t.Skip("M1 e2e gated on real lowerer: update internal/pipeline/wire.go when stream D lands (see pipeline/doc.go)")
	}
	runEmptyMainM1(t)
}

// TestM1EmptyMainStubLower exercises the driver with the stub lowerer so CI
// covers load → check → (stub)lower → codegen → node while D is absent.
func TestM1EmptyMainStubLower(t *testing.T) {
	if pipeline.Default.Real() {
		t.Skip("real lowerer wired; covered by TestM1EmptyMainE2E")
	}
	runEmptyMainM1(t)
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
