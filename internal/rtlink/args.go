package rtlink

import (
	"fmt"
	"os"
	"path/filepath"

	"github.com/achille-roussel/wasmgc-go/internal/ir"
	"github.com/achille-roussel/wasmgc-go/internal/lower"
)

// EnsureArgs links runtime/args.go when the module calls GetArgc / GetArg /
// getArgString. PackageHigh skips GOROOT runtime, so os.init's import
// "runtime" does not lower these otherwise.
func EnsureArgs(mod *ir.Module, rewriteDir string) error {
	if mod == nil {
		return nil
	}
	if !scanArgCalls(mod) {
		return nil
	}
	if mod.LookupFunc("runtime.GetArgc") != nil {
		return nil
	}
	if rewriteDir == "" {
		return fmt.Errorf("rtlink: rewrite dir unset; cannot link runtime args")
	}
	path := filepath.Join(rewriteDir, "runtime", "args.go")
	src, err := os.ReadFile(path)
	if err != nil {
		return fmt.Errorf("rtlink: read %s: %w", path, err)
	}
	fset, files, pkg, info, err := lower.TypeCheckFiles([]string{path}, []string{string(src)})
	if err != nil {
		return fmt.Errorf("rtlink: typecheck args: %w", err)
	}
	if err := lower.AppendFiles(mod, fset, files, pkg, info); err != nil {
		return fmt.Errorf("rtlink: lower args: %w", err)
	}
	return nil
}

func scanArgCalls(mod *ir.Module) bool {
	need := false
	for _, f := range mod.Funcs {
		walkCalls(f.Body, func(name string) {
			switch name {
			case "runtime.GetArgc", "runtime.GetArg",
				"runtime.getArgc", "runtime.getArg", "runtime.getArgString":
				need = true
			}
		})
	}
	return need
}
