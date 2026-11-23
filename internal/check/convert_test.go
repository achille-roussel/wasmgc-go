package check

import (
	"strings"
	"testing"
)

func TestIntConvertWarning(t *testing.T) {
	src := `package p
func f(x int64) int {
	return int(x)
}
`
	fset, info, files := typeCheck(t, src)
	diags := CheckIntConvert(fset, info, files)
	if len(diags) != 1 {
		t.Fatalf("got %v", diags)
	}
	if diags[0].Severity != SeverityWarning {
		t.Fatalf("severity: %v", diags[0].Severity)
	}
	if !strings.Contains(diags[0].Message, "may overflow") {
		t.Fatalf("message: %q", diags[0].Message)
	}
	if !strings.Contains(diags[0].Message, "int is 32 bits") {
		t.Fatalf("message: %q", diags[0].Message)
	}
}

func TestIntConvertConstantSilent(t *testing.T) {
	src := `package p
func f() int {
	return int(int64(3))
}
`
	fset, info, files := typeCheck(t, src)
	diags := CheckIntConvert(fset, info, files)
	if len(diags) != 0 {
		t.Fatalf("unexpected: %v", diags)
	}
}
