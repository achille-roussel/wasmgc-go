// overlay of go1.27.4 strings/builder.go
package strings

import (
	"unicode/utf8"
)

// A Builder is used to efficiently build a string using Write methods.
type Builder struct {
	addr *Builder
	buf  []byte
}

func (b *Builder) copyCheck() {
	if b.addr == nil {
		b.addr = b
	} else if b.addr != b {
		panic("strings: illegal use of non-zero Builder copied by value")
	}
}

func (b *Builder) String() string {
	return string(b.buf)
}

func (b *Builder) Len() int { return len(b.buf) }

func (b *Builder) Cap() int { return cap(b.buf) }

func (b *Builder) Reset() {
	b.addr = nil
	b.buf = nil
}

func (b *Builder) grow(n int) {
	buf := make([]byte, len(b.buf), 2*cap(b.buf)+n)
	copy(buf, b.buf)
	b.buf = buf
}

func (b *Builder) Grow(n int) {
	b.copyCheck()
	if n < 0 {
		panic("strings.Builder.Grow: negative count")
	}
	if cap(b.buf)-len(b.buf) < n {
		b.grow(n)
	}
}

func (b *Builder) Write(p []byte) (int, error) {
	b.copyCheck()
	b.buf = append(b.buf, p...)
	return len(p), nil
}

func (b *Builder) WriteByte(c byte) error {
	b.copyCheck()
	b.buf = append(b.buf, c)
	return nil
}

func (b *Builder) WriteRune(r rune) (int, error) {
	b.copyCheck()
	n := len(b.buf)
	b.buf = utf8.AppendRune(b.buf, r)
	return len(b.buf) - n, nil
}

func (b *Builder) WriteString(s string) (int, error) {
	b.copyCheck()
	b.buf = append(b.buf, s...)
	return len(s), nil
}
