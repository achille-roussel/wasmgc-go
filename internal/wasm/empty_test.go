package wasm_test

import (
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"testing"

	"github.com/achille-roussel/wasmgc-go/internal/wasm"
)

func repoRoot(t *testing.T) string {
	t.Helper()
	_, file, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("no caller")
	}
	// internal/wasm/empty_test.go -> repo root
	return filepath.Clean(filepath.Join(filepath.Dir(file), "..", ".."))
}

func TestEmptyRunModule(t *testing.T) {
	m := wasm.EmptyRunModule()
	b, err := m.Encode()
	if err != nil {
		t.Fatal(err)
	}
	if len(b) < 8 || !bytes.Equal(b[:4], []byte{0x00, 0x61, 0x73, 0x6d}) {
		t.Fatalf("bad magic: %x", b[:min(8, len(b))])
	}
	if !bytes.Contains(b, []byte("run")) {
		t.Fatal("module missing export name run")
	}

	dir := t.TempDir()
	path := filepath.Join(dir, "empty.wasm")
	if err := os.WriteFile(path, b, 0o644); err != nil {
		t.Fatal(err)
	}
	validateWithWasmTools(t, path)

	golden := filepath.Join(repoRoot(t), "testdata", "m0", "empty.wasm")
	if os.Getenv("UPDATE_GOLDEN") == "1" {
		if err := os.MkdirAll(filepath.Dir(golden), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(golden, b, 0o644); err != nil {
			t.Fatal(err)
		}
		t.Logf("wrote %s (%d bytes)", golden, len(b))
		return
	}
	want, err := os.ReadFile(golden)
	if err != nil {
		// First run without a golden: write it so CI and the JS glue have a file.
		if err := os.MkdirAll(filepath.Dir(golden), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(golden, b, 0o644); err != nil {
			t.Fatal(err)
		}
		t.Logf("created missing golden %s", golden)
		return
	}
	if !bytes.Equal(b, want) {
		t.Fatalf("empty.wasm golden mismatch: got %d bytes, want %d; re-run with UPDATE_GOLDEN=1", len(b), len(want))
	}
}

func TestEmptyRunModulePrint(t *testing.T) {
	if _, err := exec.LookPath("wasm-tools"); err != nil {
		t.Skip("wasm-tools not on PATH")
	}
	m := wasm.EmptyRunModule()
	b, err := m.Encode()
	if err != nil {
		t.Fatal(err)
	}
	dir := t.TempDir()
	path := filepath.Join(dir, "empty.wasm")
	if err := os.WriteFile(path, b, 0o644); err != nil {
		t.Fatal(err)
	}
	out, err := exec.Command("wasm-tools", "print", path).CombinedOutput()
	if err != nil {
		t.Fatalf("wasm-tools print: %v\n%s", err, out)
	}
	if !bytes.Contains(out, []byte("\"run\"")) {
		t.Fatalf("printed module missing run export:\n%s", out)
	}
}

func validateWithWasmTools(t *testing.T, path string) {
	t.Helper()
	if _, err := exec.LookPath("wasm-tools"); err != nil {
		t.Skip("wasm-tools not on PATH")
	}
	out, err := exec.Command("wasm-tools", "validate", path).CombinedOutput()
	if err != nil {
		t.Fatalf("wasm-tools validate %s: %v\n%s", path, err, out)
	}
}
