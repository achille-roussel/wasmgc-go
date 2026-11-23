package lower_test

import (
	"strings"
	"testing"
)

// Local print/println must not be treated as the builtin. bug148.go and
// issue6866.go both define such shadows and call them as statements; the
// buggy builtinName path misclassified them and reported "print used as value".
func TestUserPrintShadowCall(t *testing.T) {
	m := mustCompile(t, `package main
func println(x, y int) {}
func print() {}
func main() {
	println(1, 2)
	print()
}
`)
	got := formatFunc(m, "main.main")
	if strings.Contains(got, "runtime.print") {
		t.Fatalf("user println/print lowered via runtime.print*:\n%s", got)
	}
	if !strings.Contains(got, "call main.println") {
		t.Fatalf("expected call main.println:\n%s", got)
	}
	if !strings.Contains(got, "call main.print") {
		t.Fatalf("expected call main.print:\n%s", got)
	}
}

func TestUserPrintAsValue(t *testing.T) {
	// Assigning a user-defined print is legal Go; the builtin is not.
	m := mustCompile(t, `package main
func print() {}
func main() {
	f := print
	f()
}
`)
	got := formatFunc(m, "main.main")
	if !strings.Contains(got, "main.print") {
		t.Fatalf("expected ref to main.print:\n%s", got)
	}
}

func TestBuiltinPrintlnStillWorks(t *testing.T) {
	m := mustCompile(t, `package main
func main() {
	println("ok")
}
`)
	got := formatFunc(m, "main.main")
	if !strings.Contains(got, "runtime.printstring") {
		t.Fatalf("expected builtin println → runtime.printstring:\n%s", got)
	}
}
