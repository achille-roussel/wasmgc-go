package ir_test

import (
	"strings"
	"testing"

	"github.com/achille-roussel/wasmgc-go/internal/ir"
)

func TestParseRoundTrip(t *testing.T) {
	cases := []string{
		`func f() {
  return
}
`,
		`func f(x i32) i32 {
  return x
}
`,
		`func main.fib(n i32) i32 {
  var r i32
  if i32.lt_u n, 2 {
    r = n
  } else {
    %0 = call main.fib(i32.sub n, 1)
    %1 = call main.fib(i32.sub n, 2)
    r = i32.add %0, %1
  }
  return r
}
`,
		`type $Point struct {
  mut X i32
  mut Y i32
}

func f(p ref $Point) i32 {
  %0 = struct.get $Point X p
  return %0
}
`,
		`func f(n i32) {
  block $done {
    loop $L {
      br_if $done i32.eqz n
      n = i32.sub n, 1
      br $L
    }
  }
  return
}
`,
		`low

func f() {
  return
}
`,
		`func main.main() {
  %0 = string "hello"
  call runtime.printstring(%0)
  return
}
`,
		`data $lit "hi"

func f() {
  return
}
`,
		`global mut $g i32 = 0

func f() {
  global.set $g 1
  return
}
`,
		`func f(x i32) {
  switch x {
  case 0: {
    return
  }
  case 1: {
    return
  }
  default: {
    unreachable
  }
  }
}
`,
		`func f() {
  try {
    call maypanic()
  } catch $go.panic (obj ref $go.panicObj, exn exn) {
    throw_ref exn
  }
  return
}
`,
		`func f(state i32) {
  dispatch $d (state) {
  arm 0: {
    state = 1
    br $d
  }
  arm 1: {
    return
  }
  }
}
`,
	}
	for i, src := range cases {
		m, err := ir.Parse("input.ir", src)
		if err != nil {
			t.Errorf("case %d: parse: %v\n%s", i, err, src)
			continue
		}
		out := ir.Format(m)
		m2, err := ir.Parse("input.ir", out)
		if err != nil {
			t.Errorf("case %d: re-parse: %v\nprinted:\n%s", i, err, out)
			continue
		}
		if !ir.Equal(m, m2) {
			t.Errorf("case %d: round-trip mismatch\n--- first print\n%s--- second print\n%s", i, out, ir.Format(m2))
		}
	}
}

func TestParseErrors(t *testing.T) {
	cases := []struct {
		src string
		sub string
	}{
		{`func {`, "expected name"},
		{`func f( {`, "expected"},
		{`xyz`, "expected declaration"},
		{`func f() { br $missing }`, ""}, // parse OK; verify catches
		{`"unterminated`, "unterminated"},
	}
	for _, tc := range cases {
		_, err := ir.Parse("t.ir", tc.src)
		if tc.sub == "" {
			continue
		}
		if err == nil {
			t.Errorf("expected error containing %q for %q", tc.sub, tc.src)
			continue
		}
		if !strings.Contains(err.Error(), tc.sub) {
			t.Errorf("error %q does not contain %q", err, tc.sub)
		}
	}
}

func TestParsePositions(t *testing.T) {
	src := "func f() {\n  return\n}\n"
	m, err := ir.Parse("pos.ir", src)
	if err != nil {
		t.Fatal(err)
	}
	if m.Funcs[0].Pos.Line != 1 {
		t.Errorf("func pos line = %d, want 1", m.Funcs[0].Pos.Line)
	}
}
