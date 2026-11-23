package ir_test

import (
	"testing"

	"github.com/achille-roussel/wasmgc-go/internal/ir"
)

func TestParseArrayNewDataAssign(t *testing.T) {
	src := `low
data $lit0 "hi"
func main.main() {
  %strdata1 = array.new_data $go.bytes $lit0
  %0 = struct.new $go.string %strdata1, 0, 2
  return
}
`
	m, err := ir.Parse("t.ir", src)
	if err != nil {
		t.Fatal(err)
	}
	got := ir.Format(m)
	m2, err := ir.Parse("t2.ir", got)
	if err != nil {
		t.Fatalf("reparse: %v\n%s", err, got)
	}
	if !ir.Equal(m, m2) {
		t.Fatal("roundtrip")
	}
}
