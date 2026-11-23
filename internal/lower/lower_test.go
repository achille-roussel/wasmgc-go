package lower_test

import (
	"flag"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/achille-roussel/wasmgc-go/internal/ir"
	"github.com/achille-roussel/wasmgc-go/internal/lower"
)

var update = flag.Bool("update", false, "update golden .ir files")

func mustCompile(t *testing.T, src string) *ir.Module {
	t.Helper()
	m, err := lower.Compile("test.go", src)
	if err != nil {
		t.Fatalf("compile: %v", err)
	}
	if err := ir.Verify(m); err != nil {
		t.Fatalf("verify: %v\n%s", err, ir.Format(m))
	}
	return m
}

func assertRoundTrip(t *testing.T, m *ir.Module) {
	t.Helper()
	text := ir.Format(m)
	m2, err := ir.Parse("roundtrip.ir", text)
	if err != nil {
		t.Fatalf("re-parse: %v\n%s", err, text)
	}
	if err := ir.Verify(m2); err != nil {
		t.Fatalf("re-verify: %v", err)
	}
	if !ir.Equal(m, m2) {
		t.Fatalf("parse(print(x)) != x\n%s", text)
	}
}

func formatFunc(m *ir.Module, name string) string {
	fn := m.LookupFunc(name)
	if fn == nil {
		return ""
	}
	return strings.TrimSpace(ir.Format(&ir.Module{Funcs: []*ir.Func{fn}})) + "\n"
}

func TestEmptyMain(t *testing.T) {
	m := mustCompile(t, `package main
func main() {}
`)
	assertRoundTrip(t, m)
	got := ir.Format(m)
	wantPath := filepath.Join("..", "ir", "testdata", "empty_main.ir")
	want, err := os.ReadFile(wantPath)
	if err != nil {
		t.Fatal(err)
	}
	if normalize(got) != normalize(string(want)) {
		t.Fatalf("empty_main mismatch\n--- got\n%s--- want\n%s", got, want)
	}
}

func TestPrintln(t *testing.T) {
	m := mustCompile(t, `package main
func main() {
	println("hello, world")
}
`)
	assertRoundTrip(t, m)
	got := formatFunc(m, "main.main")
	wantPath := filepath.Join("..", "ir", "testdata", "println.ir")
	want, err := os.ReadFile(wantPath)
	if err != nil {
		t.Fatal(err)
	}
	if normalize(got) != normalize(string(want)) {
		t.Fatalf("println mismatch\n--- got\n%s--- want\n%s", got, want)
	}
}

func TestFib(t *testing.T) {
	m := mustCompile(t, `package main
func fib(n int) int {
	var r int
	if n < 2 {
		r = n
	} else {
		r = fib(n-1) + fib(n-2)
	}
	return r
}
func main() {}
`)
	assertRoundTrip(t, m)
	got := formatFunc(m, "main.fib")
	compareGolden(t, "fib.ir", got)
}

func TestRuntimeOpsPrintln(t *testing.T) {
	m := mustCompile(t, `package main
func main() { println("hi") }
`)
	if err := lower.RuntimeOps(m); err != nil {
		t.Fatal(err)
	}
	if !m.Low {
		t.Fatal("not low")
	}
	if err := ir.Verify(m); err != nil {
		t.Fatalf("verify: %v\n%s", err, ir.Format(m))
	}
	text := ir.Format(m)
	if strings.Contains(text, `string "`) {
		t.Fatalf("high string lit remains:\n%s", text)
	}
	if !strings.Contains(text, "array.new_data") {
		t.Fatalf("expected array.new_data:\n%s", text)
	}
	assertRoundTrip(t, m)
}

func TestScalarsAndControl(t *testing.T) {
	src := `package main
func add(a, b int) int { return a + b }
func abs(x int) int {
	if x < 0 {
		return -x
	}
	return x
}
func sum(n int) int {
	s := 0
	for i := 0; i < n; i++ {
		s += i
	}
	return s
}
func forever() {
	for {
		break
	}
}
func classified(x int) int {
	switch x {
	case 0:
		return 1
	case 1, 2:
		return 2
	default:
		return 3
	}
}
func multi() (int, int) { return 1, 2 }
func useMulti() int {
	a, b := multi()
	return a + b
}
func main() {
	println(add(1, 2))
	println(sum(5))
}
`
	m := mustCompile(t, src)
	assertRoundTrip(t, m)
	compareGolden(t, "scalars.ir", ir.Format(m))
	if err := lower.RuntimeOps(m); err != nil {
		t.Fatal(err)
	}
	if err := ir.Verify(m); err != nil {
		t.Fatalf("verify low: %v\n%s", err, ir.Format(m))
	}
	assertRoundTrip(t, m)
}

func TestConstReturn(t *testing.T) {
	m := mustCompile(t, `package main
func answer() int { return 42 }
func main() {}
`)
	got := formatFunc(m, "main.answer")
	want := "func main.answer() i32 {\n  return 42\n}\n"
	if normalize(got) != normalize(want) {
		t.Fatalf("got %q want %q", got, want)
	}
}

func TestIntIsI32(t *testing.T) {
	m := mustCompile(t, `package main
func f(x int, y uint, z uintptr) int64 {
	return int64(x) + int64(y) + int64(z)
}
func main() {}
`)
	fn := m.LookupFunc("main.f")
	if fn == nil {
		t.Fatal("missing main.f")
	}
	if len(fn.Params) != 3 {
		t.Fatalf("params: %d", len(fn.Params))
	}
	for i, p := range fn.Params {
		if !ir.TypesEqual(p.Typ, ir.TypeI32) {
			t.Errorf("param %d type %s, want i32", i, p.Typ)
		}
	}
	if len(fn.Results) != 1 || !ir.TypesEqual(fn.Results[0], ir.TypeI64) {
		t.Errorf("result %v, want i64", fn.Results)
	}
}

func TestGoldenGoFiles(t *testing.T) {
	dir := "testdata"
	entries, err := os.ReadDir(dir)
	if err != nil {
		if os.IsNotExist(err) {
			t.Skip("no testdata yet")
		}
		t.Fatal(err)
	}
	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".go") {
			continue
		}
		name := e.Name()
		t.Run(name, func(t *testing.T) {
			src, err := os.ReadFile(filepath.Join(dir, name))
			if err != nil {
				t.Fatal(err)
			}
			m := mustCompile(t, string(src))
			assertRoundTrip(t, m)
			got := ir.Format(m)
			golden := strings.TrimSuffix(name, ".go") + ".ir"
			compareGolden(t, golden, got)
			if err := lower.RuntimeOps(m); err != nil {
				t.Fatalf("RuntimeOps: %v", err)
			}
			if err := ir.Verify(m); err != nil {
				t.Fatalf("verify low: %v\n%s", err, ir.Format(m))
			}
			assertRoundTrip(t, m)
		})
	}
}

func compareGolden(t *testing.T, name, got string) {
	t.Helper()
	path := filepath.Join("testdata", name)
	if *update {
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte(normalize(got)), 0o644); err != nil {
			t.Fatal(err)
		}
		return
	}
	want, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read golden %s (re-run with -update): %v\n--- got\n%s", name, err, got)
	}
	if normalize(got) != normalize(string(want)) {
		t.Fatalf("%s mismatch\n--- got\n%s--- want\n%s", name, got, want)
	}
}

func normalize(s string) string {
	if !strings.HasSuffix(s, "\n") {
		s += "\n"
	}
	return s
}
