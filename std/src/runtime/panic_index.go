package runtime

// panicIndex and its itoa/digit helpers. Linked only when bounds checks need
// the formatted "index out of range [x] with length y" message.

func panicIndex(x, y int) {
	throwPanic(panicErr("runtime error: index out of range [" + itoa(x) + "] with length " + itoa(y)))
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
