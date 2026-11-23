package pipeline

import (
	"context"

	"github.com/achille-roussel/wasmgc-go/internal/ir"
	"github.com/achille-roussel/wasmgc-go/internal/load"
)

// Lowerer turns a type-checked program into IR.
// Assumed real API (stream D): lower.Package(pkgs, main) (*ir.Module, error).
type Lowerer interface {
	Package(pkgs []*load.Package, main *load.Package) (*ir.Module, error)
}

// CodegenOptions mirrors codegen.Options for the driver.
type CodegenOptions struct {
	Names             bool
	SynthPrintRuntime bool
}

// Codegen turns a verified IR module into Wasm bytes.
// Wired to internal/codegen.Module.
type Codegen interface {
	Module(m *ir.Module, opts CodegenOptions) ([]byte, error)
}

// Stdlib materializes the pinned GOROOT and runs std check.
// Wired to internal/stdlib (stream H).
type Stdlib interface {
	GOROOT(ctx context.Context) (string, error)
	Check(ctx context.Context) error
}

// Backends holds the pluggable stages.
type Backends struct {
	Lower   Lowerer
	Codegen Codegen
	Stdlib  Stdlib
}

// Real reports whether the lowerer is the real stream D package rather than
// the M1 stub. The end-to-end test skips until this is true. Codegen (A') and
// stdlib (H) are already real.
func (b Backends) Real() bool {
	return b.Lower != nil && !isStubLowerer(b.Lower)
}
