package rtlink

import (
	"fmt"
	"os"
	"path/filepath"

	"github.com/achille-roussel/wasmgc-go/internal/ir"
	"github.com/achille-roussel/wasmgc-go/internal/lower"
)

// EnsureComplexDiv links runtime.complex128div when the module calls it.
func EnsureComplexDiv(mod *ir.Module, rewriteDir string) error {
	if mod == nil || !callsFunc(mod, "runtime.complex128div") {
		return nil
	}
	if hasPrintBody(mod, "runtime.complex128div") {
		return nil
	}
	if rewriteDir == "" {
		return fmt.Errorf("rtlink: rewrite dir unset; cannot link runtime complex128div")
	}
	path := filepath.Join(rewriteDir, "runtime", "complex.go")
	src, err := os.ReadFile(path)
	if err != nil {
		return fmt.Errorf("rtlink: read %s: %w", path, err)
	}
	fset, files, pkg, info, err := lower.TypeCheckFiles([]string{path}, []string{string(src)})
	if err != nil {
		return fmt.Errorf("rtlink: typecheck complex: %w", err)
	}
	if err := lower.AppendFiles(mod, fset, files, pkg, info); err != nil {
		return fmt.Errorf("rtlink: lower complex: %w", err)
	}
	return nil
}

func callsFunc(mod *ir.Module, name string) bool {
	found := false
	for _, f := range mod.Funcs {
		walkCalls(f.Body, func(n string) {
			if n == name {
				found = true
			}
		})
	}
	return found
}
