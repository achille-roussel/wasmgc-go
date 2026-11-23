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

// printF64bits is body-less; EnsurePrint fills i64.reinterpret_f64
// (same pattern as runtime.f64bits in map.go).
func printF64bits(f float64) uint64

// printfloat32 formats v like strconv.AppendFloat(_, v, 'g', -1, 32).
func printfloat32(v float32) {
	printfloat64(float64(v))
}

// printfloat64 formats v like strconv.AppendFloat(_, v, 'g', -1, 64).
// Exact integers and specials match Go; other values use a best-effort
// digit extraction sufficient for $GOROOT/test println paths.
func printfloat64(v float64) {
	bits := printF64bits(v)
	sign := bits >> 63
	exp := int((bits >> 52) & 0x7FF)
	frac := bits & 0xFFFFFFFFFFFFF

	if exp == 0x7FF {
		if frac != 0 {
			printstring("NaN")
			return
		}
		if sign != 0 {
			printstring("-Inf")
		} else {
			printstring("+Inf")
		}
		return
	}
	if exp == 0 && frac == 0 {
		if sign != 0 {
			printstring("-0")
		} else {
			printstring("0")
		}
		return
	}

	neg := sign != 0
	abs := v
	if neg {
		abs = -v
	}

	// Exact integers: digit-format then apply 'g' shortest eprec=6 rule.
	if abs < 9e15 {
		i := int64(abs)
		if float64(i) == abs {
			printFloatUint(neg, uint64(i))
			return
		}
	}

	// Best-effort general path via repeated ×/÷ 10 on float64.
	exp10 := 0
	scaled := abs
	for scaled >= 10 {
		scaled /= 10
		exp10++
	}
	for scaled > 0 && scaled < 1 {
		scaled *= 10
		exp10--
	}
	var digs [18]byte
	n := 0
	t := scaled
	for n < 17 {
		d := int(t)
		if d > 9 {
			d = 9
		}
		if d < 0 {
			d = 0
		}
		digs[n] = byte(d)
		n++
		t = (t - float64(d)) * 10
		if t < 1e-8 {
			break
		}
	}
	if t >= 5 {
		i := n - 1
		for i >= 0 {
			digs[i]++
			if digs[i] <= 9 {
				break
			}
			digs[i] = 0
			i--
		}
		if i < 0 {
			digs[0] = 1
			n = 1
			exp10++
		}
	}
	for n > 1 && digs[n-1] == 0 {
		n--
	}
	if n > 0 && digs[0] == 0 {
		for n > 1 && digs[0] == 0 {
			for j := 0; j < n-1; j++ {
				digs[j] = digs[j+1]
			}
			n--
			exp10--
		}
	}
	printFloatDigits(neg, digs[:n], exp10)
}

func printFloatUint(neg bool, u uint64) {
	if u == 0 {
		if neg {
			printstring("-0")
		} else {
			printstring("0")
		}
		return
	}
	var digs [20]byte
	n := 0
	for u > 0 {
		digs[n] = byte(u % 10)
		u /= 10
		n++
	}
	var out [20]byte
	for i := 0; i < n; i++ {
		out[i] = digs[n-1-i]
	}
	printFloatDigits(neg, out[:n], n-1)
}

func printFloatDigits(neg bool, digs []byte, exp10 int) {
	n := len(digs)
	if n == 0 {
		printstring("0")
		return
	}
	// strconv fmtEFG shortest: eprec=6; use %e if exp < -4 || exp >= 6.
	useE := exp10 < -4 || exp10 >= 6
	if neg {
		printstring("-")
	}
	if useE {
		printDigitByte(digs[0])
		if n > 1 {
			printstring(".")
			for i := 1; i < n; i++ {
				printDigitByte(digs[i])
			}
		}
		printstring("e")
		e := exp10
		if e >= 0 {
			printstring("+")
		} else {
			printstring("-")
			e = -e
		}
		if e >= 100 {
			printDigitByte(byte(e / 100))
			e %= 100
		}
		printDigitByte(byte(e / 10))
		printDigitByte(byte(e % 10))
		return
	}
	if exp10 < 0 {
		printstring("0.")
		for i := -1; i > exp10; i-- {
			printstring("0")
		}
		for i := 0; i < n; i++ {
			printDigitByte(digs[i])
		}
		return
	}
	before := exp10 + 1
	for i := 0; i < n || i < before; i++ {
		if i == before && n > before {
			printstring(".")
		}
		if i < n {
			printDigitByte(digs[i])
		} else if i < before {
			printstring("0")
		}
	}
}

func printDigitByte(d byte) {
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

func printcomplex128(c complex128) {
	printstring("(")
	printfloat64(real(c))
	im := imag(c)
	if printF64bits(im)>>63 == 0 {
		printstring("+")
	}
	printfloat64(im)
	printstring("i)")
}

func printcomplex64(c complex64) {
	printcomplex128(complex128(c))
}

// printbool matches cmd/compile's printbool (true/false, not 0/1).
func printbool(v bool) {
	if v {
		printstring("true")
	} else {
		printstring("false")
	}
}

// printpointer formats a pointer-like value. Wasm GC refs have no stable
// uintptr identity; nil is "0x0", non-nil is opaque "0x1".
func printpointer(isNil bool) {
	if isNil {
		printstring("0x0")
	} else {
		printstring("0x1")
	}
}

// printiface formats an interface as (type,data) hex pairs. Nil is (0x0,0x0);
// non-nil type word is opaque 0x1 (data stays 0x0 — no linear address).
func printiface(isNil bool) {
	printstring("(")
	printpointer(isNil)
	printstring(",")
	printstring("0x0")
	printstring(")")
}

// printslice formats [len/cap]ptr like cmd/compile's printslice.
func printslice(length, capacity int, isNil bool) {
	printstring("[")
	printint(length)
	printstring("/")
	printint(capacity)
	printstring("]")
	printpointer(isNil)
}
