package lower_test

import (
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"testing"

	"github.com/achille-roussel/wasmgc-go/internal/codegen"
	"github.com/achille-roussel/wasmgc-go/internal/ir"
	"github.com/achille-roussel/wasmgc-go/internal/lower"
)

func repoRoot(t *testing.T) string {
	t.Helper()
	_, file, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("no caller")
	}
	return filepath.Clean(filepath.Join(filepath.Dir(file), "..", ".."))
}

func lowerToWasm(t *testing.T, src string) []byte {
	t.Helper()
	m, err := lower.Compile("e2e.go", src)
	if err != nil {
		t.Fatal(err)
	}
	if err := lower.RuntimeOps(m); err != nil {
		t.Fatal(err)
	}
	if !m.Low {
		t.Fatal("expected Low module")
	}
	if err := ir.Verify(m); err != nil {
		t.Fatalf("verify: %v\n%s", err, ir.Format(m))
	}
	b, err := codegen.Module(m, codegen.Options{SynthPrintRuntime: true})
	if err != nil {
		t.Fatalf("codegen: %v\n%s", err, ir.Format(m))
	}
	return b
}

func runNode(t *testing.T, wasm []byte, wantStdout string) {
	t.Helper()
	if _, err := exec.LookPath("node"); err != nil {
		t.Skip("node not installed")
	}
	dir := t.TempDir()
	wasmPath := filepath.Join(dir, "prog.wasm")
	if err := os.WriteFile(wasmPath, wasm, 0o644); err != nil {
		t.Fatal(err)
	}
	glue := filepath.Join(repoRoot(t), "lib", "wasmgc.js")
	cmd := exec.Command("node", glue, wasmPath)
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		t.Fatalf("node: %v\nstderr: %s\nstdout: %s", err, stderr.String(), stdout.String())
	}
	if stdout.String() != wantStdout {
		t.Fatalf("stdout %q, want %q", stdout.String(), wantStdout)
	}
}

func TestE2EEmptyMain(t *testing.T) {
	b := lowerToWasm(t, `package main
func main() {}
`)
	runNode(t, b, "")
}

func TestE2EPrintln(t *testing.T) {
	b := lowerToWasm(t, `package main
func main() {
	println("hello, world")
}
`)
	runNode(t, b, "hello, world\n")
}

func TestE2EFib(t *testing.T) {
	b := lowerToWasm(t, `package main
func fib(n int) int {
	if n < 2 {
		return n
	}
	return fib(n-1) + fib(n-2)
}
func main() {
	println(fib(20))
}
`)
	runNode(t, b, "6765\n")
}
