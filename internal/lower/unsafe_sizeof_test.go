package lower_test

import (
	"strings"
	"testing"
)

// Assignment of unsafe.Sizeof/Alignof used to miss the builtin path and hit
// resolveCallee ("not a function: unsafe.Sizeof obj=*types.Builtin").
func TestUnsafeSizeofAssign(t *testing.T) {
	m := mustCompile(t, `package main
import "unsafe"
func main() {
	n := unsafe.Sizeof(0)
	a := unsafe.Alignof(0)
	_ = n
	_ = a
}
`)
	got := formatFunc(m, "main.main")
	if !strings.Contains(got, "n = 4") || !strings.Contains(got, "a = 4") {
		t.Fatalf("expected Sizeof/Alignof constants in IR:\n%s", got)
	}
}

func TestUnsafeOffsetofEmbedded(t *testing.T) {
	m := mustCompile(t, `package main
import "unsafe"
type Embedded struct { B int }
type S struct {
	A int
	Embedded
}
func main() {
	var d S
	o1 := unsafe.Offsetof(d.B)
	o2 := unsafe.Offsetof(d.Embedded)
	if o1 != o2 {
		panic("offset mismatch")
	}
}
`)
	got := formatFunc(m, "main.main")
	// WordSize 4: A at 0, Embedded/B at 4.
	if !strings.Contains(got, "o1 = 4") || !strings.Contains(got, "o2 = 4") {
		t.Fatalf("expected Offsetof(d.B) and Offsetof(d.Embedded) both 4:\n%s", got)
	}
}
