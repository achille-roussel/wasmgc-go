package lower_test

import (
	"os"
	"strings"
	"testing"

	"github.com/achille-roussel/wasmgc-go/internal/codegen"
	"github.com/achille-roussel/wasmgc-go/internal/escape"
	"github.com/achille-roussel/wasmgc-go/internal/ir"
	"github.com/achille-roussel/wasmgc-go/internal/lower"
)

func compileM3(t *testing.T, src string) *ir.Module {
	t.Helper()
	m, err := lower.Compile("t.go", src)
	if err != nil {
		t.Fatal(err)
	}
	if err := escape.Analyze(m); err != nil {
		t.Fatal(err)
	}
	if err := lower.RuntimeOps(m); err != nil {
		t.Fatal(err)
	}
	if err := ir.Verify(m); err != nil {
		t.Fatalf("verify: %v\n%s", err, ir.Format(m))
	}
	return m
}

func runM3(t *testing.T, src, want string) {
	t.Helper()
	m := compileM3(t, src)
	b, err := codegen.Module(m, codegen.Options{SynthPrintRuntime: true})
	if err != nil {
		t.Fatalf("codegen: %v\n%s", err, ir.Format(m))
	}
	t.Logf("wasm size = %d", len(b))
	runNode(t, b, want)
}

func TestM3AppendSmoke(t *testing.T) {
	runM3(t, `package main
func main() {
	s := []int{1, 2, 3}
	s = append(s, 4)
	println(len(s))
	println(s[3])
}
`, "4\n4\n")
}

func TestM3StructValueSmoke(t *testing.T) {
	runM3(t, `package main
type S struct { X int }
func set(s S) { s.X = 99 }
func main() {
	s := S{X: 1}
	set(s)
	println(s.X)
}
`, "1\n")
}

func TestM3FieldPtrSmoke(t *testing.T) {
	runM3(t, `package main
type S struct { X int }
var p *int
func main() {
	s := S{X: 0}
	p = &s.X
	*p = 7
	println(s.X)
}
`, "7\n")
}

func TestM3NewIntSmoke(t *testing.T) {
	runM3(t, `package main
func main() {
	p := new(int)
	*p = 3
	println(*p)
}
`, "3\n")
}

func TestM3EscapeFieldIR(t *testing.T) {
	src := `package main
type S struct { X int }
func main() {
	s := S{X: 3}
	p := &s.X
	println(*p)
}
`
	m, err := lower.Compile("t.go", src)
	if err != nil {
		t.Fatal(err)
	}
	if err := escape.Analyze(m); err != nil {
		t.Fatal(err)
	}
	text := ir.Format(m)
	t.Log(text)
	if !strings.Contains(text, "struct.get") {
		t.Fatal("expected struct.get after escape")
	}
	if strings.Contains(text, "addr X") {
		t.Fatal("addr X should have been rewritten")
	}
	main := ""
	for _, f := range m.Funcs {
		if f.Name == "main.main" {
			main = ir.Format(&ir.Module{Funcs: []*ir.Func{f}})
		}
	}
	wantPath := "testdata/escape_field.ir"
	want, err := os.ReadFile(wantPath)
	if err != nil {
		t.Fatal(err)
	}
	if normalize(main) != normalize(string(want)) {
		t.Fatalf("escape_field golden mismatch\n--- got\n%s--- want\n%s", main, want)
	}
	if err := lower.RuntimeOps(m); err != nil {
		t.Fatal(err)
	}
	low := ir.Format(m)
	if strings.Contains(low, "struct.new $go.ptr") {
		t.Fatalf("fat pointer struct.new after RuntimeOps:\n%s", low)
	}
}
