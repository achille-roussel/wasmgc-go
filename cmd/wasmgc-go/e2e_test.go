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

// TestGenericMaxE2E: monomorphized Max/Identity and Pair[T].First.
func TestGenericMaxE2E(t *testing.T) {
	if !pipeline.Default.Real() {
		t.Fatal("expected real lowerer wired in pipeline.Default")
	}
	runM2(t, "generic_max", "7\n2\n9\n5\n", 16384)
}

// TestPrintlnFloatE2E: println of float32/64 and complex128.
func TestPrintlnFloatE2E(t *testing.T) {
	if !pipeline.Default.Real() {
		t.Fatal("expected real lowerer wired in pipeline.Default")
	}
	runM2(t, "println_float", "3.14\n1.5\n7\n1 2 3 4\n(5+6i)\n", 65536)
}

// TestAppendNilE2E: append(slice, nil) with pointer and interface elems.
func TestAppendNilE2E(t *testing.T) {
	if !pipeline.Default.Real() {
		t.Fatal("expected real lowerer wired in pipeline.Default")
	}
	runM2(t, "append_nil", "1\n7\n1\n", 16384)
}

// TestMethodExprNilE2E: T.M(recv, nil) method expression with untyped nil.
func TestMethodExprNilE2E(t *testing.T) {
	if !pipeline.Default.Real() {
		t.Fatal("expected real lowerer wired in pipeline.Default")
	}
	runM2(t, "method_expr_nil", "5\n3\n5\n0\n", 16384)
}

// TestIfaceMethodExprE2E: I.m MethodExpr + defer of iface method via OpIfaceCall.
func TestIfaceMethodExprE2E(t *testing.T) {
	if !pipeline.Default.Real() {
		t.Fatal("expected real lowerer wired in pipeline.Default")
	}
	runM2(t, "iface_method_expr", "7\n7\nclosed\n", 16384)
}

// TestStringRuneE2E: string(rune) links unicode.utf8.AppendRune without import.
func TestStringRuneE2E(t *testing.T) {
	if !pipeline.Default.Real() {
		t.Fatal("expected real lowerer wired in pipeline.Default")
	}
	runM2(t, "string_rune", "A\n世\n", 16384)
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

// TestIfaceUnwrapAssertE2E: error → .(interface{Unwrap() error}) and the
// matching type-switch arm need assert-site itabs (desc.synthesizeAssertItabs).
func TestIfaceUnwrapAssertE2E(t *testing.T) {
	if !pipeline.Default.Real() {
		t.Fatal("expected real lowerer wired in pipeline.Default")
	}
	runM2(t, "iface_unwrap_assert", "inner\ninner\n", 32768)
}

// TestTypeAssertCalleeE2E: f.(func(T))(x) — TypeAssertExpr as call Fun.
func TestTypeAssertCalleeE2E(t *testing.T) {
	if !pipeline.Default.Real() {
		t.Fatal("expected real lowerer wired in pipeline.Default")
	}
	runM2(t, "typeassert_callee", "42\n", 32768)
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
	// panicBounds / deferStartDepth grow EH + deferRun (~10KiB names-off).
	runM2(t, "recover_panic", "boom\n", 12288)
}

func TestM5RePanicE2E(t *testing.T) {
	if !pipeline.Default.Real() {
		t.Fatal("expected real lowerer")
	}
	runM2(t, "re_panic", "again\n", 12288)
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

// --- M6: maps (docs/design/03, 04, 08, 09) ---
// Concurrent map write detection (go vet) is out of scope for M6.

func TestM6MapIntE2E(t *testing.T) {
	if !pipeline.Default.Real() {
		t.Fatal("expected real lowerer")
	}
	runM6(t, "m6_map_int", "2\n20\n30\n")
}

func TestM6MapStringE2E(t *testing.T) {
	if !pipeline.Default.Real() {
		t.Fatal("expected real lowerer")
	}
	runM6(t, "m6_map_string", "1\n2\n2\n")
}

func TestM6MapLiteralE2E(t *testing.T) {
	if !pipeline.Default.Real() {
		t.Fatal("expected real lowerer")
	}
	runM6(t, "m6_map_literal", "2\n10\n20\n")
}

func TestM6MapCommaOkE2E(t *testing.T) {
	if !pipeline.Default.Real() {
		t.Fatal("expected real lowerer")
	}
	runM6(t, "m6_map_commaok", "7\n1\n0\n0\n")
}

func TestM6MapClearE2E(t *testing.T) {
	if !pipeline.Default.Real() {
		t.Fatal("expected real lowerer")
	}
	runM6(t, "m6_map_clear", "0\n0\n")
}

func TestM6MapNilE2E(t *testing.T) {
	if !pipeline.Default.Real() {
		t.Fatal("expected real lowerer")
	}
	runM6Exit(t, "m6_map_nil", 2, "0\n0\n", "assignment to entry in nil map")
}

func TestM6MapNaNE2E(t *testing.T) {
	if !pipeline.Default.Real() {
		t.Fatal("expected real lowerer")
	}
	runM6(t, "m6_map_nan", "notfound\n2\n")
}

func TestM6MapRangeSeedE2E(t *testing.T) {
	if !pipeline.Default.Real() {
		t.Fatal("expected real lowerer")
	}
	runM6(t, "m6_map_range", "ok\n")
}

func TestM6MapDeleteRangeE2E(t *testing.T) {
	if !pipeline.Default.Real() {
		t.Fatal("expected real lowerer")
	}
	// 5 entries visited, sum 0+10+20+30+40, map empty after deleting each current key.
	runM6(t, "m6_map_delete_range", "5\n100\n0\n")
}

func TestM6MapOpAssignE2E(t *testing.T) {
	if !pipeline.Default.Real() {
		t.Fatal("expected real lowerer")
	}
	runM6(t, "m6_map_opassign", "16\n3\n")
}

func TestM6MapKeysE2E(t *testing.T) {
	if !pipeline.Default.Real() {
		t.Fatal("expected real lowerer")
	}
	runM6(t, "m6_map_keys", "1\n2\n1\n8\n1\n9\n3\n4\n1\n11\n12\n")
}

func runM6(t *testing.T, name, wantStdout string) {
	t.Helper()
	runM6Exit(t, name, 0, wantStdout, "")
}

func runM6Exit(t *testing.T, name string, wantExit int, wantStdout, wantStderrSubstr string) {
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
	t.Logf("%s.wasm size = %d (names off)", name, len(wasm))

	cmd = exec.Command(exe, "run", "-skip-stdlib", ".")
	cmd.Dir = dir
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

// --- M7: goroutines, channels, select (docs/design/04, 07, 08) ---

func jspiAvailable(t *testing.T) bool {
	t.Helper()
	for _, node := range jspiNodeCandidates() {
		out, err := exec.Command(node, "-e",
			`try{new WebAssembly.Suspending(()=>{});process.exit(0)}catch(e){process.exit(1)}`).CombinedOutput()
		if err == nil {
			t.Logf("JSPI available via %s", node)
			return true
		}
		t.Logf("JSPI unavailable via %s: %v %s", node, err, out)
	}
	return false
}

// jspiNodeCandidates lists node binaries that may expose WebAssembly.Suspending
// (Node 25+ / 26.13.1 in CI). Prefer PATH "node", then common install locations.
func jspiNodeCandidates() []string {
	cands := []string{"node"}
	if home, err := os.UserHomeDir(); err == nil {
		cands = append(cands,
			filepath.Join(home, ".nvm/versions/node/v26.13.1/bin/node"),
			filepath.Join(home, ".nvm/versions/node/v26.13.0/bin/node"),
		)
	}
	cands = append(cands, "/usr/local/bin/node")
	return cands
}

// jspiNodePATH prepends a JSPI-capable node directory to PATH when found.
func jspiNodePATH() string {
	p := os.Getenv("PATH")
	for _, node := range jspiNodeCandidates() {
		if node == "node" {
			continue
		}
		if _, err := os.Stat(node); err != nil {
			continue
		}
		out, err := exec.Command(node, "-e",
			`try{new WebAssembly.Suspending(()=>{});process.exit(0)}catch(e){process.exit(1)}`).CombinedOutput()
		if err == nil {
			dir := filepath.Dir(node)
			if p == "" {
				return dir
			}
			return dir + string(os.PathListSeparator) + p
		}
		_ = out
	}
	return p
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
		t.Fatal("JSPI (WebAssembly.Suspending) required; need Node.js 25+")
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
	env := os.Environ()
	env = append(env, "PATH="+jspiNodePATH())
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

func TestM7SelectCommaOkE2E(t *testing.T) {
	if !pipeline.Default.Real() {
		t.Fatal("expected real lowerer")
	}
	runM7(t, "m7_select_commaok", "7\n", "", 0)
}

func TestM7ChanRangeE2E(t *testing.T) {
	if !pipeline.Default.Real() {
		t.Fatal("expected real lowerer")
	}
	runM7(t, "m7_chan_range", "6\n", "", 0)
}

func TestM7ChanCloseE2E(t *testing.T) {
	if !pipeline.Default.Real() {
		t.Fatal("expected real lowerer")
	}
	runM7(t, "m7_chan_close", "1\n", "", 0)
}

func TestM7ChanLenCapE2E(t *testing.T) {
	if !pipeline.Default.Real() {
		t.Fatal("expected real lowerer")
	}
	runM7(t, "m7_chan_lencap", "2\n4\n", "", 0)
}

func TestM7OnceE2E(t *testing.T) {
	if !pipeline.Default.Real() {
		t.Fatal("expected real lowerer")
	}
	runM7(t, "m7_once", "1\n", "", 0)
}

func TestM7PanicGoroutineE2E(t *testing.T) {
	if !pipeline.Default.Real() {
		t.Fatal("expected real lowerer")
	}
	runM7(t, "m7_panic_g", "", "panic: boom", 2)
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

// --- M8: fmt (docs/design/03, 08, 09) ---

func TestM8FmtHelloE2E(t *testing.T) {
	if !pipeline.Default.Real() {
		t.Fatal("expected real lowerer")
	}
	// ~248 KiB after AssertItabs filter + no bulk TypeName registration
	// (cuts dead time.Ticker / Stringer leaks from #66–#71). Same-env #65 is
	// ~175 KiB; remaining gap is fmt/reflect/map/panic-assert.
	runM8(t, "m8_fmt_hello", "hello\n", 270*1024)
}

func TestM8FmtIntE2E(t *testing.T) {
	if !pipeline.Default.Real() {
		t.Fatal("expected real lowerer")
	}
	runM8(t, "m8_fmt_int", "42\n", 0)
}

func TestM8FmtStructE2E(t *testing.T) {
	if !pipeline.Default.Real() {
		t.Fatal("expected real lowerer")
	}
	runM8(t, "m8_fmt_struct", "{3}\n", 0)
}

func TestM8FmtMapE2E(t *testing.T) {
	if !pipeline.Default.Real() {
		t.Fatal("expected real lowerer")
	}
	// Single-key map so %v text is deterministic without sorting in the test.
	runM8(t, "m8_fmt_map", "map[b:2]\n", 0)
}

func TestM8FmtFloatE2E(t *testing.T) {
	if !pipeline.Default.Real() {
		t.Fatal("expected real lowerer")
	}
	runM8Host(t, "m8_fmt_float")
}

func TestM8FmtQuoteE2E(t *testing.T) {
	if !pipeline.Default.Real() {
		t.Fatal("expected real lowerer")
	}
	runM8Host(t, "m8_fmt_quote")
}

// runM8Host builds/runs a fmt program and requires stdout byte-identical to host go run.
func runM8Host(t *testing.T, name string) {
	t.Helper()
	if !jspiAvailable(t) {
		t.Fatal("JSPI (WebAssembly.Suspending) required; need Node.js 25+")
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

	host := exec.Command("go", "run", ".")
	host.Dir = dir
	hostOut, err := host.CombinedOutput()
	if err != nil {
		t.Fatalf("host go run: %v\n%s", err, hostOut)
	}

	exe := buildDriver(t)
	goroot := materializeGOROOT(t, exe)
	cmd := exec.Command(exe, "run", "-goroot", goroot, ".")
	cmd.Dir = dir
	env := os.Environ()
	env = append(env, "PATH="+jspiNodePATH())
	cmd.Env = env
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		t.Fatalf("wasm run: %v\nstdout=%q stderr=%q", err, stdout.String(), stderr.String())
	}
	if stdout.String() != string(hostOut) {
		t.Fatalf("stdout not byte-identical to host go run\ngot:\n%s\nwant:\n%s", stdout.String(), hostOut)
	}
}

// runM8 builds/runs a fmt program against a materialized GOROOT.
// maxSize > 0 enforces a names-off Wasm size budget (bytes).
// JSPI is always required (no opt-out).
func runM8(t *testing.T, name, wantStdout string, maxSize int) {
	t.Helper()
	runM8Args(t, name, wantStdout, maxSize, nil)
}

// runM8Args is runM8 with optional program args after "--".
func runM8Args(t *testing.T, name, wantStdout string, maxSize int, progArgs []string) {
	t.Helper()
	if !jspiAvailable(t) {
		t.Fatal("JSPI (WebAssembly.Suspending) required; need Node.js 25+")
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
	t.Logf("%s.wasm size = %d (names off)", name, len(wasm))
	if maxSize > 0 && len(wasm) > maxSize {
		t.Fatalf("%s.wasm size %d exceeds budget %d", name, len(wasm), maxSize)
	}

	runArgs := []string{"run", "-goroot", goroot, "."}
	if len(progArgs) > 0 {
		runArgs = append(runArgs, "--")
		runArgs = append(runArgs, progArgs...)
	}
	cmd = exec.Command(exe, runArgs...)
	cmd.Dir = dir
	env := os.Environ()
	env = append(env, "PATH="+jspiNodePATH())
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
	if ec != 0 {
		t.Fatalf("exit %d, want 0\nstdout=%q stderr=%q", ec, stdout.String(), stderr.String())
	}
	if stdout.String() != wantStdout {
		t.Fatalf("stdout %q, want %q", stdout.String(), wantStdout)
	}
}

// --- M9: time.Now, os.Args, os.Write (docs/design/07, 09) ---

func TestM9TimeNowE2E(t *testing.T) {
	if !pipeline.Default.Real() {
		t.Fatal("expected real lowerer")
	}
	runM8(t, "m9_time_now", "1\n", 0)
}

func TestM9OsArgsE2E(t *testing.T) {
	if !pipeline.Default.Real() {
		t.Fatal("expected real lowerer")
	}
	runM8Args(t, "m9_os_args", "hello\n", 0, []string{"hello", "world"})
}

func TestM9OsWriteE2E(t *testing.T) {
	if !pipeline.Default.Real() {
		t.Fatal("expected real lowerer")
	}
	runM8(t, "m9_os_write", "1\n", 0)
}

func TestM9ReflectTagE2E(t *testing.T) {
	if !pipeline.Default.Real() {
		t.Fatal("expected real lowerer")
	}
	runM8(t, "m9_reflect_tag", "name\nok\n", 0)
}

func TestM9ReflectMakeE2E(t *testing.T) {
	if !pipeline.Default.Real() {
		t.Fatal("expected real lowerer")
	}
	runM8(t, "m9_reflect_make", "7\n3\n1\n", 0)
}

// --- M9: encoding/json marshal/unmarshal (docs/design/06, 09) ---

func TestM9JsonStructE2E(t *testing.T) {
	if !pipeline.Default.Real() {
		t.Fatal("expected real lowerer")
	}
	runM8(t, "m9_json_struct", "{\"name\":\"alice\",\"age\":30}\nalice\n30\n", 0)
}

func TestM9JsonMapStringE2E(t *testing.T) {
	if !pipeline.Default.Real() {
		t.Fatal("expected real lowerer")
	}
	runM8(t, "m9_json_map_string", "1\n2\n2\n", 0)
}

func TestM9JsonMapIntE2E(t *testing.T) {
	if !pipeline.Default.Real() {
		t.Fatal("expected real lowerer")
	}
	// Exercises SliceOf/MapOf in encoding/json/v2/arshal_default.go.
	runM8(t, "m9_json_map_int", "one\ntwo\n2\n", 0)
}

// --- M10: range-over-func + maps.Keys/Clone ---

func TestM10RangeFuncE2E(t *testing.T) {
	if !pipeline.Default.Real() {
		t.Fatal("expected real lowerer")
	}
	runM2(t, "m10_rangefunc", "1\n2\n", 0)
}

func TestM10MapsKeysE2E(t *testing.T) {
	if !pipeline.Default.Real() {
		t.Fatal("expected real lowerer")
	}
	runM8(t, "m10_maps_keys", "2\n2\n3\n", 0)
}

// TestPkgStringBytesE2E: package-level string([]byte{…}) must validate under
// V8 (synthetic init zero-inits non-defaultable $go.string temps).
func TestPkgStringBytesE2E(t *testing.T) {
	if !pipeline.Default.Real() {
		t.Fatal("expected real lowerer")
	}
	// bool prints via printbool → true.
	runM2(t, "pkg_string_bytes", "2\ntrue\n", 0)
}

func TestSwitchFallthroughReturnE2E(t *testing.T) {
	if !pipeline.Default.Real() {
		t.Fatal("expected real lowerer")
	}
	runM2(t, "switch_fallthrough_return", "z\no\nother\n", 0)
}

func TestCopyNilSliceE2E(t *testing.T) {
	if !pipeline.Default.Real() {
		t.Fatal("expected real lowerer")
	}
	runM2(t, "copy_nil_slice", "0\n2\n", 0)
}

func TestRangeUTF8E2E(t *testing.T) {
	if !pipeline.Default.Real() {
		t.Fatal("expected real lowerer")
	}
	runM2(t, "range_utf8", "97\n9786\n65533\n3\n", 0)
}

// TestM9HTTPGetE2E: http.Get of a local Node http server returns the body
// via fetch (docs/design/09). Passes the server URL as os.Args[0].
func TestM9HTTPGetE2E(t *testing.T) {
	if !pipeline.Default.Real() {
		t.Fatal("expected real lowerer")
	}
	if !jspiAvailable(t) {
		t.Fatal("JSPI (WebAssembly.Suspending) required; need Node.js 25+")
	}

	node := "node"
	for _, c := range jspiNodeCandidates() {
		if c == "node" {
			continue
		}
		if _, err := os.Stat(c); err == nil {
			out, err := exec.Command(c, "-e",
				`try{new WebAssembly.Suspending(()=>{});process.exit(0)}catch(e){process.exit(1)}`).CombinedOutput()
			if err == nil {
				node = c
				break
			}
			_ = out
		}
	}

	srvScript := `
const http = require("http");
const srv = http.createServer((req, res) => {
  res.writeHead(200, {"Content-Type": "text/plain"});
  res.end("hello-fetch");
});
srv.listen(0, "127.0.0.1", () => {
  const { port } = srv.address();
  process.stdout.write("http://127.0.0.1:" + port + "\n");
});
`
	srvCmd := exec.Command(node, "-e", srvScript)
	stdoutPipe, err := srvCmd.StdoutPipe()
	if err != nil {
		t.Fatal(err)
	}
	srvCmd.Stderr = os.Stderr
	if err := srvCmd.Start(); err != nil {
		t.Fatal(err)
	}
	defer func() {
		_ = srvCmd.Process.Kill()
		_, _ = srvCmd.Process.Wait()
	}()

	urlBuf := make([]byte, 256)
	n, err := stdoutPipe.Read(urlBuf)
	if err != nil {
		t.Fatalf("read server url: %v", err)
	}
	url := strings.TrimSpace(string(urlBuf[:n]))
	if !strings.HasPrefix(url, "http://") {
		t.Fatalf("bad server url %q", url)
	}
	t.Logf("test server at %s", url)

	runM8Args(t, "m9_http_get", "hello-fetch\n", 0, []string{url})
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
		{"empty_main", 371}, // +panicObj.depth / g.deferDepth fields
		{"println", 1402},   // float/complex print + fat-ptr scalar types
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
