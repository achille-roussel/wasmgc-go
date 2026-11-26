package runtime

// panicIndex and its itoa/digit helpers. Linked only when bounds checks need
// the formatted "index out of range [x] with length y" message.
//
// x is int64 so GOROOT issue30116 can print the full Go index (not an i32 wrap).
// Negative indexes omit "with length" (matching gc boundsError).

func panicIndex(x int64, y int) {
	msg := "runtime error: index out of range [" + itoa64(x) + "]"
	if x >= 0 {
		msg += " with length " + itoa(y)
	}
	throwPanic(panicErr(msg))
}

// panicIndexU formats an unsigned index (issue30116u).
func panicIndexU(x uint64, y int) {
	throwPanic(panicErr("runtime error: index out of range [" + uitoa(x) + "] with length " + itoa(y)))
}

// panicSliceToArray reports a failed []T → [N]T / *[N]T conversion (Go 1.17+).
func panicSliceToArray(slen, alen int) {
	throwPanic(panicErr("runtime error: cannot convert slice with length " + itoa(slen) +
		" to array or pointer to array with length " + itoa(alen)))
}

// Bounds error codes — subset of internal/abi.BoundsErrorCode used by
// formatted slice panics (issue30116).
const (
	boundsIndex = iota
	boundsSliceAlen
	boundsSliceAcap
	boundsSliceB
	boundsSlice3Alen
	boundsSlice3Acap
	boundsSlice3B
	boundsSlice3C
)

// panicBounds reports a slice/index bounds failure with gc-shaped text.
// signed selects whether x is shown as signed (negative → omit y / use neg fmts).
func panicBounds(code int, x int64, y int, signed bool) {
	neg := signed && x < 0
	var fmt string
	switch code {
	case boundsIndex:
		if neg {
			fmt = "index out of range [%x]"
		} else {
			fmt = "index out of range [%x] with length %y"
		}
	case boundsSliceAlen:
		if neg {
			fmt = "slice bounds out of range [:%x]"
		} else {
			fmt = "slice bounds out of range [:%x] with length %y"
		}
	case boundsSliceAcap:
		if neg {
			fmt = "slice bounds out of range [:%x]"
		} else {
			fmt = "slice bounds out of range [:%x] with capacity %y"
		}
	case boundsSliceB:
		if neg {
			fmt = "slice bounds out of range [%x:]"
		} else {
			fmt = "slice bounds out of range [%x:%y]"
		}
	case boundsSlice3Alen:
		if neg {
			fmt = "slice bounds out of range [::%x]"
		} else {
			fmt = "slice bounds out of range [::%x] with length %y"
		}
	case boundsSlice3Acap:
		if neg {
			fmt = "slice bounds out of range [::%x]"
		} else {
			fmt = "slice bounds out of range [::%x] with capacity %y"
		}
	case boundsSlice3B:
		if neg {
			fmt = "slice bounds out of range [:%x:]"
		} else {
			fmt = "slice bounds out of range [:%x:%y]"
		}
	case boundsSlice3C:
		if neg {
			fmt = "slice bounds out of range [%x::]"
		} else {
			fmt = "slice bounds out of range [%x:%y:]"
		}
	default:
		fmt = "slice bounds out of range"
	}
	msg := "runtime error: "
	for i := 0; i < len(fmt); i++ {
		c := fmt[i]
		if c != '%' {
			// Avoid string(byte) → AppendRune (needs utf8 link in tiny tests).
			msg += string([]byte{c})
			continue
		}
		i++
		if i >= len(fmt) {
			break
		}
		switch fmt[i] {
		case 'x':
			if signed {
				msg += itoa64(x)
			} else {
				msg += uitoa(uint64(x))
			}
		case 'y':
			msg += itoa(y)
		}
	}
	throwPanic(panicErr(msg))
}

func itoa(v int) string {
	return itoa64(int64(v))
}

func itoa64(v int64) string {
	if v == 0 {
		return "0"
	}
	neg := false
	u := uint64(v)
	if v < 0 {
		neg = true
		u = uint64(-v)
	}
	s := uitoa(u)
	if neg {
		return "-" + s
	}
	return s
}

func uitoa(v uint64) string {
	if v == 0 {
		return "0"
	}
	s := ""
	for v > 0 {
		s = digit(int(v%10)) + s
		v /= 10
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
	if d == 9 {
		return "9"
	}
	return "?"
}
