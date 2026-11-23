package pipeline

import (
	"github.com/achille-roussel/wasmgc-go/internal/ir"
	"github.com/achille-roussel/wasmgc-go/internal/load"
	"github.com/achille-roussel/wasmgc-go/internal/lower"
)

// Default is the backends used by cmd/wasmgc-go.
//
// Codegen (A'), stdlib (H), and lower (D) are real.
var Default = Backends{
	Lower:   lowerAdapter{},
	Codegen: codegenBackend{},
	Stdlib:  &stdlibBackend{},
}

// lowerAdapter connects stream D to the pipeline Lowerer interface.
type lowerAdapter struct{}

func (lowerAdapter) Package(pkgs []*load.Package, main *load.Package) (*ir.Module, error) {
	return lower.Package(pkgs, main)
}
