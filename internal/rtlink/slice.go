package rtlink

import (
	"fmt"
	"os"
	"path/filepath"

	"github.com/achille-roussel/wasmgc-go/internal/ir"
	"github.com/achille-roussel/wasmgc-go/internal/lower"
)

// EnsureSlice links runtime.concatstrings from std/src/runtime/slice.go when
// needed. Bounds panics are linked by EnsurePanic.
func EnsureSlice(mod *ir.Module, rewriteDir string) error {
	if mod == nil {
		return nil
	}
	needConcat := scanConcat(mod)
	if !needConcat {
		return nil
	}
	if mod.LookupFunc("runtime.concatstrings") != nil {
		return nil
	}
	if rewriteDir == "" {
		return fmt.Errorf("rtlink: rewrite dir unset; cannot link runtime slice")
	}
	path := filepath.Join(rewriteDir, "runtime", "slice.go")
	src, err := os.ReadFile(path)
	if err != nil {
		return fmt.Errorf("rtlink: read %s: %w", path, err)
	}
	fset, files, pkg, info, err := lower.TypeCheck(path, string(src))
	if err != nil {
		return fmt.Errorf("rtlink: typecheck slice.go: %w", err)
	}
	stripConcat(mod)
	if err := lower.AppendFiles(mod, fset, files, pkg, info); err != nil {
		return fmt.Errorf("rtlink: lower slice.go: %w", err)
	}
	return nil
}

func scanConcat(mod *ir.Module) bool {
	need := false
	for _, f := range mod.Funcs {
		walkCalls(f.Body, func(name string) {
			if name == "runtime.concatstrings" {
				need = true
			}
		})
	}
	return need
}

func stripConcat(mod *ir.Module) {
	out := mod.Funcs[:0]
	for _, f := range mod.Funcs {
		if f.Name == "runtime.concatstrings" {
			continue
		}
		out = append(out, f)
	}
	mod.Funcs = out
}
