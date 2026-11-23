package codegen_test

import (
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/achille-roussel/wasmgc-go/internal/codegen"
	"github.com/achille-roussel/wasmgc-go/internal/ir"
)

func testdataIR(t *testing.T, name string) *ir.Module {
	t.Helper()
	path := filepath.Join(repoRoot(t), "internal", "ir", "testdata", name)
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	m, err := ir.Parse(name, string(data))
	if err != nil {
		t.Fatal(err)
	}
	return m
}

func repoRoot(t *testing.T) string {
	t.Helper()
	_, file, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("no caller")
	}
	// internal/codegen/codegen_test.go → repo root
	return filepath.Clean(filepath.Join(filepath.Dir(file), "..", ".."))
}

func compileGolden(t *testing.T, name string, opts codegen.Options) []byte {
	t.Helper()
	m := testdataIR(t, name)
	if err := codegen.PrepareForCodegen(m); err != nil {
		t.Fatal(err)
	}
	b, err := codegen.Module(m, opts)
	if err != nil {
		t.Fatal(err)
	}
	return b
}

func TestEmptyMain(t *testing.T) {
	b := compileGolden(t, "empty_main.ir", codegen.Options{})
	validateWasm(t, b)
	if len(b) >= 2048 {
		t.Fatalf("empty main size %d >= 2048", len(b))
	}
	t.Logf("empty_main.wasm size = %d", len(b))
	runNode(t, b, "", 0)
}

func TestPrintln(t *testing.T) {
	m := testdataIR(t, "println.ir")
	if err := codegen.PrepareForCodegen(m); err != nil {
		t.Fatal(err)
	}
	b, err := codegen.Module(m, codegen.Options{SynthPrintRuntime: true})
	if err != nil {
		t.Fatal(err)
	}
	validateWasm(t, b)
	if len(b) >= 8192 {
		t.Fatalf("hello size %d >= 8192", len(b))
	}
	t.Logf("println.wasm size = %d", len(b))
	runNode(t, b, "hello, world\n", 0)
}

func TestFib(t *testing.T) {
	// Extend the fib golden with a main that prints fib(20).
	path := filepath.Join(repoRoot(t), "internal", "ir", "testdata", "fib.ir")
	src, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	wrapped := string(src) + `
func main.main() {
  %0 = call main.fib(20)
  call runtime.printint(%0)
  call runtime.printnl()
  return
}
`
	m, err := ir.Parse("fib_wrap.ir", wrapped)
	if err != nil {
		t.Fatal(err)
	}
	if err := codegen.PrepareForCodegen(m); err != nil {
		t.Fatal(err)
	}
	b, err := codegen.Module(m, codegen.Options{SynthPrintRuntime: true})
	if err != nil {
		t.Fatal(err)
	}
	validateWasm(t, b)
	runNode(t, b, "6765\n", 0)
}

func TestRejectsNonLow(t *testing.T) {
	m := &ir.Module{Low: false}
	_, err := codegen.Module(m, codegen.Options{})
	if err == nil || !strings.Contains(err.Error(), "not low") {
		t.Fatalf("expected not-low error, got %v", err)
	}
}

func TestNamesOption(t *testing.T) {
	off := compileGolden(t, "empty_main.ir", codegen.Options{Names: false})
	on := compileGolden(t, "empty_main.ir", codegen.Options{Names: true})
	if len(on) <= len(off) {
		t.Fatalf("names on (%d) should be larger than off (%d)", len(on), len(off))
	}
	if !bytes.Contains(on, []byte("main.main")) {
		t.Fatal("name section should contain main.main")
	}
}

func TestTypeInternDeterministic(t *testing.T) {
	a := compileGolden(t, "empty_main.ir", codegen.Options{})
	b := compileGolden(t, "empty_main.ir", codegen.Options{})
	if !bytes.Equal(a, b) {
		t.Fatal("codegen not deterministic")
	}
}

func validateWasm(t *testing.T, b []byte) {
	t.Helper()
	dir := t.TempDir()
	path := filepath.Join(dir, "t.wasm")
	if err := os.WriteFile(path, b, 0o644); err != nil {
		t.Fatal(err)
	}
	cmd := exec.Command("wasm-tools", "validate", path)
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("wasm-tools validate: %v\n%s", err, out)
	}
}

func runNode(t *testing.T, wasmBytes []byte, wantStdout string, wantCode int) {
	t.Helper()
	dir := t.TempDir()
	wasmPath := filepath.Join(dir, "prog.wasm")
	if err := os.WriteFile(wasmPath, wasmBytes, 0o644); err != nil {
		t.Fatal(err)
	}
	glue := filepath.Join(repoRoot(t), "lib", "wasmgc.js")
	nodes := nodeBinaries(t)
	for _, node := range nodes {
		cmd := exec.Command(node, glue, wasmPath)
		var stdout, stderr bytes.Buffer
		cmd.Stdout = &stdout
		cmd.Stderr = &stderr
		err := cmd.Run()
		code := 0
		if err != nil {
			if ee, ok := err.(*exec.ExitError); ok {
				code = ee.ExitCode()
			} else {
				t.Fatalf("node %s: %v\n%s", node, err, stderr.Bytes())
			}
		}
		if code != wantCode {
			t.Fatalf("node %s: exit %d want %d\nstderr=%s\nstdout=%q", node, code, wantCode, stderr.Bytes(), stdout.String())
		}
		if stdout.String() != wantStdout {
			t.Fatalf("node %s: stdout %q want %q\nstderr=%s", node, stdout.String(), wantStdout, stderr.Bytes())
		}
	}
}

func nodeBinaries(t *testing.T) []string {
	t.Helper()
	var out []string
	// JSPI required (decision 13): only Node 25+ / 26.13.1.
	home, _ := os.UserHomeDir()
	for _, ver := range []string{"v26.13.1", "v26.13.0", "v25.9.0"} {
		p := filepath.Join(home, ".nvm", "versions", "node", ver, "bin", "node")
		if st, err := os.Stat(p); err == nil && !st.IsDir() {
			out = append(out, p)
		}
	}
	if len(out) == 0 {
		if p, err := exec.LookPath("node"); err == nil {
			out = append(out, p)
		}
	}
	if len(out) == 0 {
		t.Skip("no node binary found")
	}
	// Drop hosts without WebAssembly.Suspending.
	var jspi []string
	for _, node := range out {
		cmd := exec.Command(node, "-e",
			`try{new WebAssembly.Suspending(()=>{});process.exit(0)}catch(e){process.exit(1)}`)
		if err := cmd.Run(); err == nil {
			jspi = append(jspi, node)
		}
	}
	if len(jspi) == 0 {
		t.Fatal("JSPI (WebAssembly.Suspending) required; need Node.js 25+")
	}
	return jspi
}
