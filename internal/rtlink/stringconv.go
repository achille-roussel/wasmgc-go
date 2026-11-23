package rtlink

import (
	"fmt"
	"os"
	"path/filepath"

	"github.com/achille-roussel/wasmgc-go/internal/ir"
	"github.com/achille-roussel/wasmgc-go/internal/lower"
)

// EnsureStringConv links runtime.StringToRunes / RunesToString when the module
// converts between string and []rune.
func EnsureStringConv(mod *ir.Module, rewriteDir string) error {
	if mod == nil {
		return nil
	}
	need := scanStringConv(mod)
	if !need {
		return nil
	}
	if mod.LookupFunc("runtime.StringToRunes") != nil &&
		mod.LookupFunc("runtime.RunesToString") != nil {
		return nil
	}
	if rewriteDir == "" {
		return fmt.Errorf("rtlink: rewrite dir unset; cannot link runtime stringconv")
	}
	path := filepath.Join(rewriteDir, "runtime", "stringconv.go")
	src, err := os.ReadFile(path)
	if err != nil {
		return fmt.Errorf("rtlink: read %s: %w", path, err)
	}
	fset, files, pkg, info, err := lower.TypeCheck(path, string(src))
	if err != nil {
		return fmt.Errorf("rtlink: typecheck stringconv.go: %w", err)
	}
	stripStringConv(mod)
	if err := lower.AppendFiles(mod, fset, files, pkg, info); err != nil {
		return fmt.Errorf("rtlink: lower stringconv.go: %w", err)
	}
	return nil
}

func scanStringConv(mod *ir.Module) bool {
	need := false
	for _, f := range mod.Funcs {
		walkCalls(f.Body, func(name string) {
			if name == "runtime.StringToRunes" || name == "runtime.RunesToString" {
				need = true
			}
		})
	}
	return need
}

func stripStringConv(mod *ir.Module) {
	out := mod.Funcs[:0]
	for _, f := range mod.Funcs {
		if f.Name == "runtime.StringToRunes" || f.Name == "runtime.RunesToString" {
			continue
		}
		out = append(out, f)
	}
	mod.Funcs = out
}
