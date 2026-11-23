package lower_test

import (
	"strings"
	"testing"

	"github.com/achille-roussel/wasmgc-go/internal/ir"
	"github.com/achille-roussel/wasmgc-go/internal/lower"
)

func TestEmbeddedComplitKeys(t *testing.T) {
	src := `package main
type A struct {
	a int
	B
}
type B struct {
	b int
	C
}
type C struct {
	c int
}
func main() {
	_ = A{a: 1, b: 2}
	_ = A{a: 1, c: 3}
	_ = A{a: 1, b: 2, c: 3}
}
`
	m, err := lower.Compile("test.go", src)
	if err != nil {
		t.Fatalf("compile: %v", err)
	}
	if err := ir.Verify(m); err != nil {
		t.Fatalf("verify: %v\n%s", err, ir.Format(m))
	}
	text := ir.Format(m)
	t.Log(text)
	if strings.Contains(text, "literal i32") {
		t.Fatal("unexpected i32 literal")
	}
}
