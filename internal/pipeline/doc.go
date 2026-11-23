// Package pipeline owns compile orchestration for wasmgc-go and the narrow
// interfaces that connect stream E (driver/loader) to stream D (lower).
//
// Stream A' (internal/codegen) and stream H (internal/stdlib) are wired
// directly. Stream D is still stubbed for the M1 empty-main subset.
//
// Assumed API for D when it lands (swap in wire.go):
//
//	func Package(pkgs []*load.Package, main *load.Package) (*ir.Module, error)
//
// Integrating the lowerer PR should be a one-file change to wire.go.
package pipeline
