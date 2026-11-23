// Package escape will rewrite non-escaping addresses and spill escaping
// scalar locals (docs/design/01-architecture.md). Correctness does not
// depend on this pass: spill lowering is valid for every &.
//
// For M1/M2 this package is a stub: those milestones have no pointers.
package escape

import "github.com/achille-roussel/wasmgc-go/internal/ir"

// Analyze is a no-op for M1/M2.
func Analyze(_ *ir.Module) error {
	return nil
}
