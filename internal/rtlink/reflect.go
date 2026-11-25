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

	// Source set must typecheck as a fresh package: map.go calls fastrand/throw,
	// fatal calls printstring/exit. Include those even if already linked in IR.
	files := []string{
		"rtype.go", "map.go", "rand.go",
		"fatal.go", "bridge.go", "print.go", "stringeq.go", "panic.go",
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
	// Strip anything we are about to re-append (maps/print/rand/fatal may already
	// be present from EnsureMaps / EnsurePrint).
	stripMapFuncs(mod)
	stripPrintFuncs(mod)
	stripReflectSupport(mod)
	stripReflectDecls(mod)
	stripPanicFuncs(mod)
	if err := lower.AppendFiles(mod, fset, astFiles, pkg, info); err != nil {
		return fmt.Errorf("rtlink: lower reflect runtime: %w", err)
	}
	fillFloatBits(mod)
	specializeMapHashes(mod)
	return nil
}

// stripReflectSupport removes rand/fatal/exit symbols and fastrandState so
// AppendFiles can re-emit them without duplicate global/func errors.
func stripReflectSupport(mod *ir.Module) {
	names := map[string]bool{
		"runtime.fastrand":  true,
		"runtime.fastrandn": true,
		"runtime.throw":     true,
		"runtime.fatal":     true,
		"runtime.exit":      true,
	}
	out := mod.Funcs[:0]
	for _, f := range mod.Funcs {
		if names[f.Name] || strings.HasPrefix(f.Name, "runtime.init.") {
			continue
		}
		out = append(out, f)
	}
	mod.Funcs = out
	gout := mod.Globals[:0]
	for _, g := range mod.Globals {
		if g.Name == "runtime.fastrandState" {
			continue
		}
		gout = append(gout, g)
	}
	mod.Globals = gout
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
