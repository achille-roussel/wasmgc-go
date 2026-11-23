package rtlink

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/achille-roussel/wasmgc-go/internal/ir"
	"github.com/achille-roussel/wasmgc-go/internal/lower"
)

// EnsureReflect links runtime/rtype.go with map.go (ReflectMap* needs hmap)
// when the module calls runtime.Reflect*. PackageHigh skips GOROOT runtime,
// so reflect's import "runtime" does not lower these helpers otherwise.
func EnsureReflect(mod *ir.Module, rewriteDir string) error {
	if mod == nil {
		return nil
	}
	if !scanReflectNeeds(mod) {
		return nil
	}
	if mod.LookupFunc("runtime.ReflectType") != nil {
		return nil
	}
	if rewriteDir == "" {
		return fmt.Errorf("rtlink: rewrite dir unset; cannot link runtime reflect helpers")
	}

	files := []string{"rtype.go", "map.go"}
	if mod.LookupFunc("runtime.fastrand") == nil {
		files = append(files, "rand.go")
	}
	needFatal := mod.LookupFunc("runtime.throw") == nil
	needPanic := mod.LookupFunc("runtime.panicUncomparable") == nil &&
		mod.LookupFunc("runtime.deferRun") == nil
	needPanicIndex := mod.LookupFunc("runtime.panicIndex") == nil
	needPrint := mod.LookupFunc("runtime.printstring") == nil
	needStreq := mod.LookupFunc("runtime.stringeq") == nil
	if needFatal {
		files = append(files, "fatal.go", "bridge.go")
	}
	if needPanic {
		files = append(files, "panic.go")
	}
	if needPanicIndex {
		files = append(files, "panic_index.go")
	}
	if needPrint || needFatal {
		files = append(files, "print.go")
	}
	if needStreq && !needPrint && !needFatal {
		files = append(files, "stringeq.go")
	}

	var filenames []string
	var srcs []string
	seen := map[string]bool{}
	for _, name := range files {
		if seen[name] {
			continue
		}
		seen[name] = true
		path := filepath.Join(rewriteDir, "runtime", name)
		src, err := os.ReadFile(path)
		if err != nil {
			return fmt.Errorf("rtlink: read %s: %w", path, err)
		}
		filenames = append(filenames, path)
		srcs = append(srcs, string(src))
	}
	fset, astFiles, pkg, info, err := lower.TypeCheckFiles(filenames, srcs)
	if err != nil {
		return fmt.Errorf("rtlink: typecheck reflect runtime: %w", err)
	}
	// Maps may already be linked by EnsureMaps; strip so we can re-append
	// with rtype.go in one package set.
	stripMapFuncs(mod)
	if needPrint || needFatal {
		stripPrintFuncs(mod)
	}
	if err := lower.AppendFiles(mod, fset, astFiles, pkg, info); err != nil {
		return fmt.Errorf("rtlink: lower reflect runtime: %w", err)
	}
	fillFloatBits(mod)
	specializeMapHashes(mod)
	return nil
}

func scanReflectNeeds(mod *ir.Module) bool {
	need := false
	for _, f := range mod.Funcs {
		walkCalls(f.Body, func(name string) {
			if strings.HasPrefix(name, "runtime.Reflect") || strings.HasPrefix(name, "runtime.(*Rtype).") {
				need = true
			}
		})
	}
	return need
}
