package ir

import "fmt"

// Pos is a byte offset in a source file, used for diagnostics.
// Line and column are 1-based when known; zero means unknown.
type Pos struct {
	File string
	Off  int // byte offset, 0-based
	Line int // 1-based; 0 = unknown
	Col  int // 1-based; 0 = unknown
}

// NoPos is the zero position.
var NoPos Pos

func (p Pos) IsValid() bool { return p.Line > 0 || p.Off > 0 || p.File != "" }

func (p Pos) String() string {
	if p.File == "" && p.Line == 0 {
		return ""
	}
	if p.File == "" {
		return fmt.Sprintf("%d:%d", p.Line, p.Col)
	}
	if p.Line == 0 {
		return p.File
	}
	return fmt.Sprintf("%s:%d:%d", p.File, p.Line, p.Col)
}
