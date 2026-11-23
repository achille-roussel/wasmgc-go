// Package ir defines the structured intermediate representation for wasmgc-go.
//
// The IR is a tree of regions (seq, block, loop, if, switch, try, dispatch),
// not a CFG. Values are mutable vars (Wasm locals) and single-assignment
// temps that do not cross region boundaries. High-level ops (map.get, panic,
// …) are rewritten by the runtime-lowering pass; a module marked Low must
// contain only low ops that codegen can emit.
//
// The text format grammar is published in docs/design/02-ir.md. The round-trip
// guarantee is parse(print(x)) == x. Stream D (internal/lower) consumes this
// format; stream A' (internal/codegen) consumes verified Low modules.
//
// This package imports nothing from internal/lower, internal/codegen, or go/ast.
package ir
