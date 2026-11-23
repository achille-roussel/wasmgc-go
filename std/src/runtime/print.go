package runtime

// Print builtins the lowerer emits for print/println (docs/design/04).
//
// printstring needs the linear-memory bridge (array.get_u → i32.store8 →
// go.runtime.write). That is not expressible in the M1/M2 Go subset, so the
// declaration is body-less: codegen fills the body until printstring_wasmgc.wat
// lands (docs/design/12). printnl and printint are ordinary Go.

// printstring writes the bytes of s to fd 1 through the linear bridge.
func printstring(s string)

// printnl writes a single newline.
func printnl() {
	printstring("\n")
}

// printint writes the decimal representation of v (exactly the digits, no NUL).
func printint(v int) {
	if v == 0 {
		printstring("0")
		return
	}
	if v < 0 {
		printstring("-")
		// Two's-complement abs so MinInt does not overflow.
		printuint(^uint(v) + 1)
		return
	}
	printuint(uint(v))
}

func printuint(v uint) {
	if v >= 10 {
		printuint(v / 10)
	}
	// if/else — not switch: M2 switch codegen falls through cases.
	d := v % 10
	if d == 0 {
		printstring("0")
	} else if d == 1 {
		printstring("1")
	} else if d == 2 {
		printstring("2")
	} else if d == 3 {
		printstring("3")
	} else if d == 4 {
		printstring("4")
	} else if d == 5 {
		printstring("5")
	} else if d == 6 {
		printstring("6")
	} else if d == 7 {
		printstring("7")
	} else if d == 8 {
		printstring("8")
	} else {
		printstring("9")
	}
}
