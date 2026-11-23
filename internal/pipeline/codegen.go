package pipeline

import (
	"github.com/achille-roussel/wasmgc-go/internal/codegen"
	"github.com/achille-roussel/wasmgc-go/internal/ir"
)

// codegenBackend adapts internal/codegen to the Codegen interface.
type codegenBackend struct{}

func (codegenBackend) Module(m *ir.Module, opts CodegenOptions) ([]byte, error) {
	return codegen.Module(m, codegen.Options{
		Names:             opts.Names,
		SynthPrintRuntime: opts.SynthPrintRuntime,
	})
}
