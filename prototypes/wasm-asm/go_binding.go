//go:build ignore

// Package wasmasm documents the Go binding shape for hand-written WAT.
//
// This file is not part of a build. It shows the declaration model from
// docs/design/12-wasm-assembly.md: body-less funcs that a *_wasmgc.wat
// file implements, with directives from cmd/compile.
package wasmasm

import _ "unsafe"

// MemEqual reports whether the UTF-8 bytes of a and b are equal.
// Implemented in asm/memequal.wat (export "memequal").
//
//go:noescape
func MemEqual(a, b string) bool

// IndexByte returns the index of c in s, or -1.
// Implemented in asm/indexbyte.wat.
//
//go:noescape
func IndexByte(s string, c byte) int

// Example push linkname so runtime can share the same body.
//
//go:linkname memequal runtime.memequal
func memequal(a, b string) bool
