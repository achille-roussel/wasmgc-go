// overlay of go1.27.4 internal/cpu/cpu_wasmgc.go
// Counterpart to cpu_wasm.go for GOARCH=wasmgc (the *_wasm.go file is dropped
// by the re-filter; see docs/design/01-architecture.md).
package cpu

const CacheLinePadSize = 64

func doinit() {
}
