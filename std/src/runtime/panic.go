package runtime

// Panic / defer surface (docs/design/04). throwPanic / deferRun / gorecover /
// unrecoveredPanic are body-less: codegen emits EH opcodes. Message builders
// below are ordinary Go so the recoverable index text matches gc.
//
// deferRun / unrecoveredPanic take opaque pointers; rtlink rewrites their
// IR param types to $go.defer / $go.panicObj after linking.

func deferRun(d *byte)
func throwPanic(v interface{})
func gorecover(allow int32) interface{}
func goexit()
func unrecoveredPanic(p *byte)

func panicIndex(x, y int) {
	throwPanic("runtime error: index out of range [" + itoa(x) + "] with length " + itoa(y))
}

func panicSliceB() {
	throwPanic("runtime error: slice bounds out of range")
}

func panicNil() {
	throwPanic("runtime error: invalid memory address or nil pointer dereference")
}

func panicDivide() {
	throwPanic("runtime error: integer divide by zero")
}

func panicUncomparable() {
	throwPanic("runtime error: comparing uncomparable type")
}

func panicTypeAssert() {
	throwPanic("interface conversion: type assertion failed")
}

func itoa(v int) string {
	if v == 0 {
		return "0"
	}
	neg := false
	if v < 0 {
		neg = true
		v = -v
	}
	s := ""
	for v > 0 {
		s = digit(v%10) + s
		v /= 10
	}
	if neg {
		s = "-" + s
	}
	return s
}

func digit(d int) string {
	if d == 0 {
		return "0"
	}
	if d == 1 {
		return "1"
	}
	if d == 2 {
		return "2"
	}
	if d == 3 {
		return "3"
	}
	if d == 4 {
		return "4"
	}
	if d == 5 {
		return "5"
	}
	if d == 6 {
		return "6"
	}
	if d == 7 {
		return "7"
	}
	if d == 8 {
		return "8"
	}
	return "9"
}
