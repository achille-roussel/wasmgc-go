// overlay of go1.27.4 internal/goarch/goarch_wasmgc.go
// Counterpart to goarch_wasm.go for GOARCH=wasmgc.
package goarch

const (
	_ArchFamily          = WASM
	_DefaultPhysPageSize = 65536
	_PCQuantum           = 1
	_MinFrameSize        = 0
	_StackAlign          = PtrSize
)
