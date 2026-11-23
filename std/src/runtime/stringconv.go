package runtime

// StringToRunes implements []rune(s).
func StringToRunes(s string) []rune {
	n := 0
	for range s {
		n++
	}
	out := make([]rune, n)
	i := 0
	for _, r := range s {
		out[i] = r
		i++
	}
	return out
}

// RunesToString implements string(r) for []rune with inline UTF-8 encoding
// (no unicode/utf8 import — rtlink TypeCheckFiles only resolves unsafe).
func RunesToString(r []rune) string {
	n := 0
	for _, c := range r {
		switch {
		case c < 0 || c > 0x10FFFF || (0xD800 <= c && c <= 0xDFFF):
			n += 3 // utf8.RuneError
		case c <= 0x7F:
			n++
		case c <= 0x7FF:
			n += 2
		case c <= 0xFFFF:
			n += 3
		default:
			n += 4
		}
	}
	b := make([]byte, n)
	i := 0
	for _, c := range r {
		switch {
		case c < 0 || c > 0x10FFFF || (0xD800 <= c && c <= 0xDFFF):
			b[i] = 0xEF
			b[i+1] = 0xBF
			b[i+2] = 0xBD
			i += 3
		case c <= 0x7F:
			b[i] = byte(c)
			i++
		case c <= 0x7FF:
			b[i] = 0xC0 | byte(c>>6)
			b[i+1] = 0x80 | byte(c&0x3F)
			i += 2
		case c <= 0xFFFF:
			b[i] = 0xE0 | byte(c>>12)
			b[i+1] = 0x80 | byte((c>>6)&0x3F)
			b[i+2] = 0x80 | byte(c&0x3F)
			i += 3
		default:
			b[i] = 0xF0 | byte(c>>18)
			b[i+1] = 0x80 | byte((c>>12)&0x3F)
			b[i+2] = 0x80 | byte((c>>6)&0x3F)
			b[i+3] = 0x80 | byte(c&0x3F)
			i += 4
		}
	}
	return string(b)
}
