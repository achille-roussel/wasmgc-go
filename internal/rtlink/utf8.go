package rtlink

import (
	"fmt"
	"os"
	"path/filepath"

	"github.com/achille-roussel/wasmgc-go/internal/ir"
	"github.com/achille-roussel/wasmgc-go/internal/lower"
)

// EnsureUTF8 links unicode.utf8.AppendRune (and helpers) from
// std/src/unicode/utf8 when the module converts int/rune → string via
// intToString. Minimal programs never import unicode/utf8 otherwise.
func EnsureUTF8(mod *ir.Module, rewriteDir string) error {
	if mod == nil {
		return nil
	}
	if !scanUTF8Calls(mod) {
		return nil
	}
	if mod.LookupFunc("unicode.utf8.AppendRune") != nil {
		return nil
	}
	if rewriteDir == "" {
		return fmt.Errorf("rtlink: rewrite dir unset; cannot link unicode/utf8")
	}
	path := filepath.Join(rewriteDir, "unicode", "utf8", "append_rune.go")
	src, err := os.ReadFile(path)
	if err != nil {
		return fmt.Errorf("rtlink: read %s: %w", path, err)
	}
	fset, files, pkg, info, err := lower.TypeCheckPath("unicode/utf8", []string{path}, []string{string(src)})
	if err != nil {
		return fmt.Errorf("rtlink: typecheck unicode/utf8: %w", err)
	}
	if err := lower.AppendFiles(mod, fset, files, pkg, info); err != nil {
		return fmt.Errorf("rtlink: lower unicode/utf8: %w", err)
	}
	return nil
}

func scanUTF8Calls(mod *ir.Module) bool {
	need := false
	for _, f := range mod.Funcs {
		walkCalls(f.Body, func(name string) {
			if name == "unicode.utf8.AppendRune" {
				need = true
			}
		})
	}
	return need
}
