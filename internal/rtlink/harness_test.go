package rtlink

import (
	"os"
	"os/exec"
	"path/filepath"
	"testing"

	"github.com/achille-roussel/wasmgc-go/internal/stdlib"
)

func TestM5DeferPanicWAT(t *testing.T) {
	runRuntimeHarness(t, "run_m5.mjs")
}

func TestM6MapWAT(t *testing.T) {
	runRuntimeHarness(t, "run_m6.mjs")
}

func TestM7ChanSelectWAT(t *testing.T) {
	runRuntimeHarness(t, "run_m7.mjs")
}

func runRuntimeHarness(t *testing.T, script string) {
	t.Helper()
	if _, err := exec.LookPath("wasm-tools"); err != nil {
		t.Skip("wasm-tools not on PATH")
	}
	if _, err := exec.LookPath("node"); err != nil {
		t.Skip("node not on PATH")
	}
	root, err := stdlib.CompilerModuleRoot()
	if err != nil {
		t.Fatal(err)
	}
	dir := filepath.Join(root, "std", "src", "runtime", "testdata")
	cmd := exec.Command("node", script)
	cmd.Dir = dir
	cmd.Env = os.Environ()
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("%s: %v\n%s", script, err, out)
	}
	t.Log(string(out))
}
