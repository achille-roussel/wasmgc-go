package runtime

// Linear bridge and host imports (docs/design/04, 07).
//
// The host import is go.runtime.write(fd, ptr, n) -> n. Bytes are copied from
// a GC string/[]byte into linear memory at the bump pointer, then the import
// is called. Host cannot read Wasm GC arrays.
//
// //go:wasmimport is not wired in the lowerer yet. Codegen currently emits the
// write import when filling printstring. When wasmimport lands, declare:
//
//	//go:wasmimport go runtime.write
//	func write(fd, ptr, n int32) int32
//
//	//go:wasmimport go runtime.exit
//	func exit(code int32)
//
// WAT candidates (docs/design/12, nosuspend default in runtime):
//   - printstring / bridge copy loop (hot, multi-block)
//   - memequal / memmove over (array i8)
//   - map hashing of byte keys (M6+)

// exit terminates the process with the given status.
// Body filled when wasmimport / unrecovered-panic wiring lands (M5).
func exit(code int32) {
	_ = code
	throw("runtime.exit")
}
