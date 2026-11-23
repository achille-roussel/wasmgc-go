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


// TestM3AppendE2E: slice lit + append → 4\n4\n.
func TestM3AppendE2E(t *testing.T) {
	if !pipeline.Default.Real() {
		t.Fatal("expected real lowerer wired in pipeline.Default")
	}
	runM2(t, "append", "4\n4\n", 16384)
}

// TestM3StructValueE2E: callee field write does not change caller.
func TestM3StructValueE2E(t *testing.T) {
	if !pipeline.Default.Real() {
		t.Fatal("expected real lowerer wired in pipeline.Default")
	}
	runM2(t, "struct_value", "1\n", 16384)
}

// TestM3FieldPtrE2E: global *int = &s.X; write through; print s.X.
func TestM3FieldPtrE2E(t *testing.T) {
	if !pipeline.Default.Real() {
		t.Fatal("expected real lowerer wired in pipeline.Default")
	}
	runM2(t, "field_ptr", "7\n", 16384)
}

// TestM3NewIntE2E: new(int) written through.
func TestM3NewIntE2E(t *testing.T) {
	if !pipeline.Default.Real() {
		t.Fatal("expected real lowerer wired in pipeline.Default")
	}
	runM2(t, "new_int", "3\n", 16384)
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

// --- M5: closures, defer, panic, recover (docs/design/08, 09) ---

func TestM5DeferPrintE2E(t *testing.T) {
	if !pipeline.Default.Real() {
		t.Fatal("expected real lowerer")
	}
	runM2(t, "defer_print", "hi\nbye\n", 8192)
}

func TestM5RecoverPanicE2E(t *testing.T) {
	if !pipeline.Default.Real() {
		t.Fatal("expected real lowerer")
	}
	runM2(t, "recover_panic", "boom\n", 8192)
}

func TestM5RePanicE2E(t *testing.T) {
	if !pipeline.Default.Real() {
		t.Fatal("expected real lowerer")
	}
	runM2(t, "re_panic", "again\n", 8192)
}

func TestM5IndexRecoverE2E(t *testing.T) {
	if !pipeline.Default.Real() {
		t.Fatal("expected real lowerer")
	}
	runM2(t, "index_recover", "runtime error: index out of range [5] with length 3\n", 8192)
}

func TestM5UnrecoveredPanicE2E(t *testing.T) {
	if !pipeline.Default.Real() {
		t.Fatal("expected real lowerer")
	}
	runM5Exit(t, "unrecovered_panic", 2, "", "panic: x\n\n", 8192)
}

func TestM5ClosureMutE2E(t *testing.T) {
	if !pipeline.Default.Real() {
		t.Fatal("expected real lowerer")
	}
	runM2(t, "closure_mut", "2\n", 8192)
}

func TestM5StrConcatE2E(t *testing.T) {
	if !pipeline.Default.Real() {
		t.Fatal("expected real lowerer")
	}
	runM2(t, "strcat", "ab\n", 8192)
}

func TestM5MethodValE2E(t *testing.T) {
	if !pipeline.Default.Real() {
		t.Fatal("expected real lowerer")
	}
	runM2(t, "method_val", "7\n", 8192)
}

// --- M7: goroutines, channels, select (docs/design/04, 07, 08) ---

func jspiAvailable(t *testing.T) bool {
	t.Helper()
	out, err := exec.Command("node", "-e",
		`try{new WebAssembly.Suspending(()=>{});process.exit(0)}catch(e){process.exit(1)}`).CombinedOutput()
	if err != nil {
		t.Logf("JSPI unavailable: %v %s", err, out)
		return false
	}
	return true
}

func materializeGOROOT(t *testing.T, exe string) string {
	t.Helper()
	cmd := exec.Command(exe, "std", "materialize")
	cmd.Dir = "."
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("std materialize: %v\n%s", err, out)
	}
	// "materialized GOROOT <path> (pin ...)"
	line := strings.TrimSpace(string(out))
	const prefix = "materialized GOROOT "
	if !strings.HasPrefix(line, prefix) {
		t.Fatalf("unexpected materialize output: %q", line)
	}
	rest := strings.TrimPrefix(line, prefix)
	if i := strings.Index(rest, " "); i >= 0 {
		rest = rest[:i]
	}
	return rest
}

func runM7(t *testing.T, name, wantStdout, wantStderrSubstr string, wantExit int) {
	t.Helper()
	if !jspiAvailable(t) {
		t.Skip("JSPI (WebAssembly.Suspending) required for M7 parking tests")
	}
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
	goroot := materializeGOROOT(t, exe)

	wasmPath := filepath.Join(dir, name+".wasm")
	cmd := exec.Command(exe, "build", "-goroot", goroot, "-o", wasmPath, ".")
	cmd.Dir = dir
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("build: %v\n%s", err, out)
	}
	wasm, err := os.ReadFile(wasmPath)
	if err != nil {
		t.Fatal(err)
	}
	t.Logf("%s.wasm size = %d", name, len(wasm))

	cmd = exec.Command(exe, "run", "-goroot", goroot, ".")
	cmd.Dir = dir
	// Prefer a Node with JSPI when available (e.g. /usr/local/bin/node 26+).
	env := os.Environ()
	if p := os.Getenv("PATH"); p != "" && !strings.Contains(p, "/usr/local/bin") {
		env = append(env, "PATH=/usr/local/bin:"+p)
	}
	cmd.Env = env
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	err = cmd.Run()
	ec := 0
	if err != nil {
		if ee, ok := err.(*exec.ExitError); ok {
			ec = ee.ExitCode()
		} else {
			t.Fatalf("run: %v\nstdout=%q stderr=%q", err, stdout.String(), stderr.String())
		}
	}
	if ec != wantExit {
		t.Fatalf("exit %d, want %d\nstdout=%q stderr=%q", ec, wantExit, stdout.String(), stderr.String())
	}
	if stdout.String() != wantStdout {
		t.Fatalf("stdout %q, want %q", stdout.String(), wantStdout)
	}
	if wantStderrSubstr != "" && !strings.Contains(stderr.String(), wantStderrSubstr) {
		t.Fatalf("stderr %q, want substring %q", stderr.String(), wantStderrSubstr)
	}
}

func TestM7ChanBufE2E(t *testing.T) {
	if !pipeline.Default.Real() {
		t.Fatal("expected real lowerer")
	}
	runM7(t, "m7_chan_buf", "7\n", "", 0)
}

func TestM7ChanUnbufE2E(t *testing.T) {
	if !pipeline.Default.Real() {
		t.Fatal("expected real lowerer")
	}
	runM7(t, "m7_chan_unbuf", "7\n", "", 0)
}

func TestM7TwoWaitersE2E(t *testing.T) {
	if !pipeline.Default.Real() {
		t.Fatal("expected real lowerer")
	}
	runM7(t, "m7_two_waiters", "30\n", "", 0)
}

func TestM7SelectDefaultE2E(t *testing.T) {
	if !pipeline.Default.Real() {
		t.Fatal("expected real lowerer")
	}
	runM7(t, "m7_select_default", "default\n", "", 0)
}

func TestM7SelectParkE2E(t *testing.T) {
	if !pipeline.Default.Real() {
		t.Fatal("expected real lowerer")
	}
	runM7(t, "m7_select_park", "99\n", "", 0)
}

func TestM7SelectTimeoutE2E(t *testing.T) {
	if !pipeline.Default.Real() {
		t.Fatal("expected real lowerer")
	}
	runM7(t, "m7_select_timeout", "timeout\n", "", 0)
}

func TestM7WaitGroupE2E(t *testing.T) {
	if !pipeline.Default.Real() {
		t.Fatal("expected real lowerer")
	}
	runM7(t, "m7_waitgroup", "3\n", "", 0)
}

func TestM7MutexE2E(t *testing.T) {
	if !pipeline.Default.Real() {
		t.Fatal("expected real lowerer")
	}
	runM7(t, "m7_mutex", "1\n", "", 0)
}

func TestM7DeadlockE2E(t *testing.T) {
	if !pipeline.Default.Real() {
		t.Fatal("expected real lowerer")
	}
	runM7(t, "m7_deadlock", "fatal error: all goroutines are asleep - deadlock!\n", "", 2)
}

func TestM7MainReturnE2E(t *testing.T) {
	if !pipeline.Default.Real() {
		t.Fatal("expected real lowerer")
	}
	// Queued goroutine must NOT run (macrotask after main microtask exit).
	runM7(t, "m7_main_return", "main\n", "", 0)
}

func TestM7MainParkE2E(t *testing.T) {
	if !pipeline.Default.Real() {
		t.Fatal("expected real lowerer")
	}
	runM7(t, "m7_main_park", "9\n", "", 0)
}

func TestM7SizeBudgets(t *testing.T) {
	if !pipeline.Default.Real() {
		t.Fatal("expected real lowerer")
	}
	exe := buildDriver(t)
	for _, tc := range []struct {
		name string
		want int
	}{
		{"empty_main", 140},
		{"println", 970},
	} {
		dir := t.TempDir()
		srcDir := filepath.Join("testdata", tc.name)
		for _, n := range []string{"main.go", "go.mod"} {
			data, err := os.ReadFile(filepath.Join(srcDir, n))
			if err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(filepath.Join(dir, n), data, 0o644); err != nil {
				t.Fatal(err)
			}
		}
		wasmPath := filepath.Join(dir, tc.name+".wasm")
		cmd := exec.Command(exe, "build", "-skip-stdlib", "-o", wasmPath, ".")
		cmd.Dir = dir
		out, err := cmd.CombinedOutput()
		if err != nil {
			t.Fatalf("%s build: %v\n%s", tc.name, err, out)
		}
		wasm, err := os.ReadFile(wasmPath)
		if err != nil {
			t.Fatal(err)
		}
		t.Logf("%s.wasm size = %d (want %d)", tc.name, len(wasm), tc.want)
		if len(wasm) != tc.want {
			t.Fatalf("%s.wasm size %d, want %d", tc.name, len(wasm), tc.want)
		}
	}
}

func runM5Exit(t *testing.T, name string, wantExit int, wantStdout, wantStderr string, maxSize int) {
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
	err := cmd.Run()
	ec := 0
	if err != nil {
		if ee, ok := err.(*exec.ExitError); ok {
			ec = ee.ExitCode()
		} else {
			t.Fatalf("run: %v\nstdout=%q stderr=%q", err, stdout.String(), stderr.String())
		}
	}
	if ec != wantExit {
		t.Fatalf("exit %d, want %d\nstdout=%q stderr=%q", ec, wantExit, stdout.String(), stderr.String())
	}
	if stdout.String() != wantStdout {
		t.Fatalf("stdout %q, want %q", stdout.String(), wantStdout)
	}
	if stderr.String() != wantStderr {
		t.Fatalf("stderr %q, want %q", stderr.String(), wantStderr)
	}
}
