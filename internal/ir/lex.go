package ir

import (
	"fmt"
	"strings"
	"unicode"
	"unicode/utf8"
)

// token kinds
type tokenKind int

const (
	tokEOF tokenKind = iota
	tokError
	tokIdent  // name, opcode, type name (may contain '.')
	tokTemp   // %digits or %ident
	tokInt    // integer literal
	tokFloat  // float literal
	tokString // "…"
	tokLBrace // {
	tokRBrace // }
	tokLParen // (
	tokRParen // )
	tokComma  // ,
	tokColon  // :
	tokAssign // =
)

type token struct {
	kind tokenKind
	lit  string
	pos  Pos
}

func (t token) String() string {
	switch t.kind {
	case tokEOF:
		return "EOF"
	case tokError:
		return "error:" + t.lit
	case tokString:
		return strconvQuote(t.lit)
	default:
		if t.lit != "" {
			return t.lit
		}
		return tokName(t.kind)
	}
}

func tokName(k tokenKind) string {
	switch k {
	case tokEOF:
		return "EOF"
	case tokIdent:
		return "identifier"
	case tokTemp:
		return "temp"
	case tokInt:
		return "integer"
	case tokFloat:
		return "float"
	case tokString:
		return "string"
	case tokLBrace:
		return "'{'"
	case tokRBrace:
		return "'}'"
	case tokLParen:
		return "'('"
	case tokRParen:
		return "')'"
	case tokComma:
		return "','"
	case tokColon:
		return "':'"
	case tokAssign:
		return "'='"
	default:
		return "token"
	}
}

func strconvQuote(s string) string {
	var b strings.Builder
	b.WriteByte('"')
	for _, r := range s {
		switch r {
		case '"', '\\':
			b.WriteByte('\\')
			b.WriteRune(r)
		case '\n':
			b.WriteString(`\n`)
		case '\t':
			b.WriteString(`\t`)
		case '\r':
			b.WriteString(`\r`)
		default:
			if r < 0x20 {
				fmt.Fprintf(&b, `\x%02x`, r)
			} else {
				b.WriteRune(r)
			}
		}
	}
	b.WriteByte('"')
	return b.String()
}

type lexer struct {
	file string
	src  string
	off  int
	line int
	col  int
	err  error
}

func newLexer(file, src string) *lexer {
	return &lexer{file: file, src: src, line: 1, col: 1}
}

func (l *lexer) pos() Pos {
	return Pos{File: l.file, Off: l.off, Line: l.line, Col: l.col}
}

func (l *lexer) peek() rune {
	if l.off >= len(l.src) {
		return -1
	}
	r, _ := utf8.DecodeRuneInString(l.src[l.off:])
	return r
}

func (l *lexer) next() rune {
	if l.off >= len(l.src) {
		return -1
	}
	r, size := utf8.DecodeRuneInString(l.src[l.off:])
	l.off += size
	if r == '\n' {
		l.line++
		l.col = 1
	} else {
		l.col++
	}
	return r
}

func (l *lexer) skipSpace() {
	for {
		r := l.peek()
		if r == -1 {
			return
		}
		if r == '/' && l.off+1 < len(l.src) && l.src[l.off+1] == '/' {
			// line comment
			for r != '\n' && r != -1 {
				r = l.next()
			}
			continue
		}
		if !unicode.IsSpace(r) {
			return
		}
		l.next()
	}
}

func (l *lexer) lex() token {
	l.skipSpace()
	p := l.pos()
	r := l.peek()
	if r == -1 {
		return token{kind: tokEOF, pos: p}
	}
	switch r {
	case '{':
		l.next()
		return token{kind: tokLBrace, lit: "{", pos: p}
	case '}':
		l.next()
		return token{kind: tokRBrace, lit: "}", pos: p}
	case '(':
		l.next()
		return token{kind: tokLParen, lit: "(", pos: p}
	case ')':
		l.next()
		return token{kind: tokRParen, lit: ")", pos: p}
	case ',':
		l.next()
		return token{kind: tokComma, lit: ",", pos: p}
	case ':':
		l.next()
		return token{kind: tokColon, lit: ":", pos: p}
	case '=':
		l.next()
		return token{kind: tokAssign, lit: "=", pos: p}
	case '"':
		return l.lexString(p)
	case '%':
		return l.lexTemp(p)
	case '-', '+':
		return l.lexNumber(p)
	default:
		if unicode.IsDigit(r) {
			return l.lexNumber(p)
		}
		if isIdentStart(r) {
			return l.lexIdent(p)
		}
		l.next()
		l.err = fmt.Errorf("%s: unexpected character %q", p, r)
		return token{kind: tokError, lit: fmt.Sprintf("unexpected character %q", r), pos: p}
	}
}

func isIdentStart(r rune) bool {
	return r == '_' || r == '$' || r == '.' || unicode.IsLetter(r)
}

func isIdentCont(r rune) bool {
	return isIdentStart(r) || unicode.IsDigit(r) || r == '.' || r == '/' || r == '*' || r == '-'
}

func (l *lexer) lexIdent(p Pos) token {
	start := l.off
	l.next()
	for {
		r := l.peek()
		if !isIdentCont(r) {
			break
		}
		// Stop before a second '.' that would be weird? No — opcodes like
		// i32.add and names like main.fib use one or more dots.
		l.next()
	}
	lit := l.src[start:l.off]
	return token{kind: tokIdent, lit: lit, pos: p}
}

func (l *lexer) lexTemp(p Pos) token {
	start := l.off
	l.next() // %
	r := l.peek()
	if r == -1 || (!unicode.IsDigit(r) && !isIdentStart(r)) {
		l.err = fmt.Errorf("%s: expected temp name after %%", p)
		return token{kind: tokError, lit: "bad temp", pos: p}
	}
	for {
		r := l.peek()
		if !unicode.IsDigit(r) && !isIdentCont(r) {
			break
		}
		l.next()
	}
	return token{kind: tokTemp, lit: l.src[start:l.off], pos: p}
}

func (l *lexer) lexNumber(p Pos) token {
	start := l.off
	r := l.peek()
	if r == '+' || r == '-' {
		l.next()
		r = l.peek()
	}
	if r == -1 || (!unicode.IsDigit(r) && r != '.') {
		l.err = fmt.Errorf("%s: bad number", p)
		return token{kind: tokError, lit: "bad number", pos: p}
	}
	isFloat := false
	for {
		r := l.peek()
		if unicode.IsDigit(r) {
			l.next()
			continue
		}
		if r == '.' && !isFloat {
			isFloat = true
			l.next()
			continue
		}
		if (r == 'e' || r == 'E') && !strings.ContainsAny(l.src[start:l.off], "eE") {
			isFloat = true
			l.next()
			r = l.peek()
			if r == '+' || r == '-' {
				l.next()
			}
			continue
		}
		break
	}
	lit := l.src[start:l.off]
	if isFloat {
		return token{kind: tokFloat, lit: lit, pos: p}
	}
	return token{kind: tokInt, lit: lit, pos: p}
}

func (l *lexer) lexString(p Pos) token {
	l.next() // "
	var b strings.Builder
	for {
		r := l.peek()
		if r == -1 {
			l.err = fmt.Errorf("%s: unterminated string", p)
			return token{kind: tokError, lit: "unterminated string", pos: p}
		}
		if r == '"' {
			l.next()
			return token{kind: tokString, lit: b.String(), pos: p}
		}
		if r == '\\' {
			l.next()
			esc := l.next()
			switch esc {
			case 'n':
				b.WriteByte('\n')
			case 't':
				b.WriteByte('\t')
			case 'r':
				b.WriteByte('\r')
			case '\\', '"':
				b.WriteRune(esc)
			case 'x':
				h1, h2 := l.next(), l.next()
				v := unhex(h1)*16 + unhex(h2)
				if v < 0 {
					l.err = fmt.Errorf("%s: bad hex escape", p)
					return token{kind: tokError, lit: "bad hex escape", pos: p}
				}
				b.WriteByte(byte(v))
			default:
				l.err = fmt.Errorf("%s: bad escape \\%c", p, esc)
				return token{kind: tokError, lit: "bad escape", pos: p}
			}
			continue
		}
		if r == '\n' {
			l.err = fmt.Errorf("%s: newline in string", p)
			return token{kind: tokError, lit: "newline in string", pos: p}
		}
		b.WriteRune(l.next())
	}
}

func unhex(r rune) int {
	switch {
	case r >= '0' && r <= '9':
		return int(r - '0')
	case r >= 'a' && r <= 'f':
		return int(r - 'a' + 10)
	case r >= 'A' && r <= 'F':
		return int(r - 'A' + 10)
	default:
		return -1
	}
}

// Keywords that start statements / decls. An identifier that is also an
// opcode (i32.add) is not a keyword; LookupOp handles that.
var keywords = map[string]bool{
	"type": true, "func": true, "global": true, "data": true, "low": true,
	"var": true, "return": true, "br": true, "br_if": true, "unreachable": true,
	"if": true, "else": true, "loop": true, "block": true, "switch": true,
	"case": true, "default": true, "try": true, "catch": true,
	"dispatch": true, "arm": true, "export": true, "mut": true, "sub": true,
	"struct": true, "array": true, "call": true, "call_ref": true,
}
