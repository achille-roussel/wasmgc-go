// Package pipeline owns compile orchestration for wasmgc-go and the narrow
// interfaces that connect stream E (driver/loader) to stream D (lower).
//
// Stream A' (internal/codegen), stream H (internal/stdlib), and stream D
// (internal/lower) are wired in wire.go.
//
// D's API:
//
//	func Package(pkgs []*load.Package, main *load.Package) (*ir.Module, error)
//
// The returned module is Low (runtime ops applied) and has export "run".
package pipeline
