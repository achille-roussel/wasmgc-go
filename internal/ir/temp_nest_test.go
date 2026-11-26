package ir_test

import (
	"testing"

	"github.com/achille-roussel/wasmgc-go/internal/ir"
)

func TestOuterTempReadableInNestedRegions(t *testing.T) {
	cases := []string{
		`func f() {
  %0 = i32.add 1, 2
  block $b {
    %1 = i32.add %0, 1
  }
  return
}`,
		`func f() {
  %0 = i32.add 1, 2
  if i32.eqz %0 {
    %1 = i32.add %0, 1
  } else {
    %2 = i32.add %0, 2
  }
  return
}`,
		`func f() {
  %0 = i32.add 1, 2
  loop $L {
    br_if $L i32.eqz %0
  }
  return
}`,
	}
	for i, src := range cases {
		m, err := ir.Parse("t.ir", src)
		if err != nil {
			t.Errorf("case %d: parse: %v", i, err)
			continue
		}
		if err := ir.Verify(m); err != nil {
			t.Errorf("case %d: verify: %v\n%s", i, err, src)
		}
	}
}
