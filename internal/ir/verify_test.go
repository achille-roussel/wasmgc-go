package ir_test

import (
	"strings"
	"testing"

	"github.com/achille-roussel/wasmgc-go/internal/ir"
)

func TestVerifyOK(t *testing.T) {
	cases := []string{
		`func f() { return }`,
		`func f(x i32) i32 { return x }`,
		`func f(n i32) i32 {
  var r i32
  if i32.lt_u n, 2 {
    r = n
  } else {
    r = i32.sub n, 1
  }
  return r
}`,
		`func f(n i32) {
  block $done {
    loop $L {
      br_if $done i32.eqz n
      n = i32.sub n, 1
      br $L
    }
  }
  return
}`,
		`func f() {
  %0 = i32.add 1, 2
  return
}`,
	}
	for i, src := range cases {
		m, err := ir.Parse("ok.ir", src)
		if err != nil {
			t.Errorf("case %d: parse: %v", i, err)
			continue
		}
		if err := ir.Verify(m); err != nil {
			t.Errorf("case %d: verify: %v\n%s", i, err, src)
		}
	}
}

func TestVerifyErrors(t *testing.T) {
	cases := []struct {
		src string
		sub string
	}{
		{
			`func f() {
  br $nope
}`,
			"not in scope",
		},
		{
			// Temps defined inside a nested region do not escape it.
			`func f() {
  block $b {
    %0 = i32.add 1, 2
  }
  %1 = i32.add %0, 1
  return
}`,
			"undefined temp",
		},
		{
			`func f() i32 {
  return
}`,
			"return has 0 values, function wants 1",
		},
		{
			`func f(x i32) {
  x = i64.const 1
  return
}`,
			"cannot assign",
		},
		{
			`low

func f() {
  %0 = string "x"
  return
}`,
			"high-level",
		},
		{
			`func f() {
  var x i8
  return
}`,
			"packed",
		},
		{
			`func f() {
  if 1 {
    return
  }
  return
}`,
			"", // 1 is i32, OK
		},
	}
	for i, tc := range cases {
		m, err := ir.Parse("bad.ir", tc.src)
		if err == nil {
			err = ir.Verify(m)
		}
		if tc.sub == "" {
			if err != nil {
				t.Errorf("case %d: unexpected error: %v", i, err)
			}
			continue
		}
		if err == nil {
			t.Errorf("case %d: expected error containing %q", i, tc.sub)
			continue
		}
		if !strings.Contains(err.Error(), tc.sub) {
			t.Errorf("case %d: error %q does not contain %q", i, err, tc.sub)
		}
	}
}

func TestVerifyTempSameRegion(t *testing.T) {
	src := `func f() i32 {
  %0 = i32.add 1, 2
  %1 = i32.add %0, 3
  return %1
}`
	m, err := ir.Parse("t.ir", src)
	if err != nil {
		t.Fatal(err)
	}
	if err := ir.Verify(m); err != nil {
		t.Fatal(err)
	}
}

func TestVerifyLowRejectsHigh(t *testing.T) {
	src := `low

func f() {
  call runtime.printnl()
  return
}`
	m, err := ir.Parse("t.ir", src)
	if err != nil {
		t.Fatal(err)
	}
	// call is low; should be fine
	if err := ir.Verify(m); err != nil {
		t.Fatal(err)
	}

	src2 := `low

func f() {
  panic 1
  return
}`
	m2, err := ir.Parse("t.ir", src2)
	if err != nil {
		t.Fatal(err)
	}
	err = ir.Verify(m2)
	if err == nil || !strings.Contains(err.Error(), "high-level") {
		t.Fatalf("expected high-level error, got %v", err)
	}
}
