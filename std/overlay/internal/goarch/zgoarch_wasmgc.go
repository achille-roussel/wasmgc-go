// overlay of go1.27.4 internal/goarch/zgoarch_wasmgc.go
// Counterpart to zgoarch_wasm.go for GOARCH=wasmgc.
// IsWasm is 0: wasmgc is not the upstream wasm arch (no *_wasm.go / wasm tags).
package goarch

const GOARCH = `wasmgc`

const Is386 = 0
const IsAmd64 = 0
const IsAmd64p32 = 0
const IsArm = 0
const IsArmbe = 0
const IsArm64 = 0
const IsArm64be = 0
const IsLoong64 = 0
const IsMips = 0
const IsMipsle = 0
const IsMips64 = 0
const IsMips64le = 0
const IsMips64p32 = 0
const IsMips64p32le = 0
const IsPpc = 0
const IsPpc64 = 0
const IsPpc64le = 0
const IsRiscv = 0
const IsRiscv64 = 0
const IsS390 = 0
const IsS390x = 0
const IsSparc = 0
const IsSparc64 = 0
const IsWasm = 0
