// Rewrite-tier unicode/utf8 fragment linked on demand by rtlink.EnsureUTF8
// when string(rune) lowering emits unicode.utf8.AppendRune.
package utf8

// Numbers fundamental to the encoding (subset needed by AppendRune).
const (
	RuneError = '\uFFFD'
	MaxRune   = '\U0010FFFF'
)

const (
	tx = 0b10000000
	t2 = 0b11000000
	t3 = 0b11100000
	t4 = 0b11110000

	maskx = 0b00111111

	rune1Max = 1<<7 - 1
	rune2Max = 1<<11 - 1
	rune3Max = 1<<16 - 1

	surrogateMin = 0xD800
	surrogateMax = 0xDFFF

	runeErrorByte0 = t3 | (RuneError >> 12)
	runeErrorByte1 = tx | (RuneError>>6)&maskx
	runeErrorByte2 = tx | RuneError&maskx
)

// AppendRune appends the UTF-8 encoding of r to the end of p and
// returns the extended buffer. If the rune is out of range,
// it appends the encoding of RuneError.
func AppendRune(p []byte, r rune) []byte {
	if uint32(r) <= rune1Max {
		return append(p, byte(r))
	}
	return appendRuneNonASCII(p, r)
}

func appendRuneNonASCII(p []byte, r rune) []byte {
	switch i := uint32(r); {
	case i <= rune2Max:
		return append(p, t2|byte(r>>6), tx|byte(r)&maskx)
	case i < surrogateMin, surrogateMax < i && i <= rune3Max:
		return append(p, t3|byte(r>>12), tx|byte(r>>6)&maskx, tx|byte(r)&maskx)
	case i > rune3Max && i <= MaxRune:
		return append(p, t4|byte(r>>18), tx|byte(r>>12)&maskx, tx|byte(r>>6)&maskx, tx|byte(r)&maskx)
	default:
		return append(p, runeErrorByte0, runeErrorByte1, runeErrorByte2)
	}
}
