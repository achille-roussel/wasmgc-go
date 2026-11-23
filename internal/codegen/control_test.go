package codegen_test

import (
	"testing"

	"github.com/achille-roussel/wasmgc-go/internal/codegen"
	"github.com/achille-roussel/wasmgc-go/internal/ir"
)

func TestLoopBr(t *testing.T) {
	src := `low
func export "run" main.run() {
  call main.main()
  return
}
func main.main() {
  var i i32
  var sum i32
  i = 0
  sum = 0
  block $done {
    loop $L {
      br_if $done i32.ge_u i, 5
      sum = i32.add sum, i
      i = i32.add i, 1
      br $L
    }
  }
  call runtime.printint(sum)
  call runtime.printnl()
  return
}
`
	m, err := ir.Parse("loop.ir", src)
	if err != nil {
		t.Fatal(err)
	}
	b, err := codegen.Module(m, codegen.Options{SynthPrintRuntime: true})
	if err != nil {
		t.Fatal(err)
	}
	validateWasm(t, b)
	// 0+1+2+3+4 = 10
	runNode(t, b, "10\n", 0)
}

func TestIfElse(t *testing.T) {
	src := `low
func export "run" main.run() {
  %0 = call main.pick(1)
  call runtime.printint(%0)
  call runtime.printnl()
  return
}
func main.pick(x i32) i32 {
  var r i32
  if x {
    r = 42
  } else {
    r = 7
  }
  return r
}
`
	m, err := ir.Parse("if.ir", src)
	if err != nil {
		t.Fatal(err)
	}
	b, err := codegen.Module(m, codegen.Options{SynthPrintRuntime: true})
	if err != nil {
		t.Fatal(err)
	}
	validateWasm(t, b)
	runNode(t, b, "42\n", 0)
}

func TestMultiValue(t *testing.T) {
	src := `low
func export "run" main.run() {
  %a, %b = call main.pair()
  call runtime.printint(%a)
  call runtime.printnl()
  call runtime.printint(%b)
  call runtime.printnl()
  return
}
func main.pair() i32, i32 {
  return 3, 4
}
`
	m, err := ir.Parse("mv.ir", src)
	if err != nil {
		t.Fatal(err)
	}
	b, err := codegen.Module(m, codegen.Options{SynthPrintRuntime: true})
	if err != nil {
		t.Fatal(err)
	}
	validateWasm(t, b)
	runNode(t, b, "3\n4\n", 0)
}
