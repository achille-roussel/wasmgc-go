// Package wasm encodes WebAssembly modules with the GC and exception-handling
// proposals. It knows only Wasm binary formats: no Go types, no IR.
//
// Heap-type immediates (ref.null, ref.cast, ref.test, br_on_cast, and the
// heap type inside a reference value type) are signed LEB128. Encoding a
// type index of 64 as the single unsigned byte 0x40 is the wasm3 bug: the
// validator reads it as −64. See docs/design/01-architecture.md and
// docs/wasm3-study/02-runtime-and-abi.md.
package wasm
