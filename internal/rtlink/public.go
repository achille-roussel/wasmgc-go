package rtlink

import (
	"fmt"
	"os"
	"path/filepath"

	"github.com/achille-roussel/wasmgc-go/internal/ir"
	"github.com/achille-roussel/wasmgc-go/internal/lower"
)

// Public runtime symbols that PackageHigh never lowers from GOROOT runtime.
// Linked from std/src/runtime/public_stubs.go when a module calls them.
var publicStubSyms = []string{
	"runtime.GC",
	"runtime.GOMAXPROCS",
	"runtime.KeepAlive",
	"runtime.SetFinalizer",
	"runtime.FuncForPC",
	"runtime.Caller",
	"runtime.Callers",
	"runtime.CallersFrames",
	"runtime.Stack",
	"runtime.NumCPU",
	"runtime.Breakpoint",
	"runtime.ReadMemStats",
	"runtime.MemProfile",
	"runtime.Goexit",
	"runtime.GOROOT",
	"runtime.Version",
	"runtime.(*Func).Name",
	"runtime.(*Func).Entry",
	"runtime.(*Func).FileLine",
	"runtime.(*Frames).Next",
	"runtime.(*MemProfileRecord).InUseBytes",
	"runtime.(*MemProfileRecord).InUseObjects",
	"runtime.(*MemProfileRecord).Stack",
}

// EnsurePublic links thin public runtime stubs (GC, Caller, Stack, …) when
// the module calls them. Bodies are no-ops / zeros so language tests that
// only need the symbols to resolve can compile and run.
func EnsurePublic(mod *ir.Module, rewriteDir string) error {
	if mod == nil {
		return nil
	}
	need := scanPublicCalls(mod)
	if len(need) == 0 {
		return nil
	}
	// Already linked if the primary entry points exist with bodies.
	if hasPrintBody(mod, "runtime.GC") || hasPrintBody(mod, "runtime.Caller") {
		return nil
	}
	if rewriteDir == "" {
		return fmt.Errorf("rtlink: rewrite dir unset; cannot link runtime public stubs")
	}
	path := filepath.Join(rewriteDir, "runtime", "public_stubs.go")
	src, err := os.ReadFile(path)
	if err != nil {
		return fmt.Errorf("rtlink: read %s: %w", path, err)
	}
	fset, files, pkg, info, err := lower.TypeCheckFiles([]string{path}, []string{string(src)})
	if err != nil {
		return fmt.Errorf("rtlink: typecheck public stubs: %w", err)
	}
	if err := lower.AppendFiles(mod, fset, files, pkg, info); err != nil {
		return fmt.Errorf("rtlink: lower public stubs: %w", err)
	}
	return nil
}

func scanPublicCalls(mod *ir.Module) map[string]bool {
	want := make(map[string]bool, len(publicStubSyms))
	for _, s := range publicStubSyms {
		want[s] = true
	}
	found := map[string]bool{}
	for _, f := range mod.Funcs {
		walkCalls(f.Body, func(name string) {
			if want[name] {
				found[name] = true
			}
		})
	}
	return found
}
