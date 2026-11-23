package pipeline

import (
	"fmt"
	"os"
	"path/filepath"

	"github.com/achille-roussel/wasmgc-go/internal/check"
	"github.com/achille-roussel/wasmgc-go/internal/ir"
	"github.com/achille-roussel/wasmgc-go/internal/load"
	"github.com/achille-roussel/wasmgc-go/internal/mono"
	"github.com/achille-roussel/wasmgc-go/internal/stdlib"
)

// TestOptions controls CompileTest.
type TestOptions struct {
	ImportPath  string
	Tags        []string
	Names       bool
	VetFail     bool
	GOROOT      string // materialized; empty → resolve via stdlib
	UpstreamSrc string // pin src/; empty → resolve via WASMGC_GOROOT / pin
	Skip        map[string]string
	ModuleRoot  string
	WorkDir     string
}

// CompileTest loads package tests and compiles a test main Wasm module.
func CompileTest(opts TestOptions) (*Result, *load.TestProgram, error) {
	b := resolveBackends(Backends{})
	goroot := opts.GOROOT
	var err error
	if goroot == "" {
		goroot, err = resolveGOROOT(Options{ModuleRoot: opts.ModuleRoot}, b)
		if err != nil {
			return nil, nil, fmt.Errorf("pipeline: stdlib GOROOT: %w", err)
		}
	}
	upstream := opts.UpstreamSrc
	if upstream == "" {
		upstream, err = ResolveUpstreamSrc(opts.ModuleRoot)
		if err != nil {
			return nil, nil, err
		}
	}
	tp, err := load.LoadTest(load.TestConfig{
		ImportPath:  opts.ImportPath,
		GOROOT:      goroot,
		UpstreamSrc: upstream,
		Skip:        opts.Skip,
		Tags:        opts.Tags,
		WorkDir:     opts.WorkDir,
	})
	if err != nil {
		return nil, nil, err
	}

	diags, err := check.Check(tp.Program, check.Options{VetFail: opts.VetFail})
	if err != nil {
		return nil, tp, err
	}
	var warnings []check.Diagnostic
	for _, d := range diags {
		if d.Severity == check.SeverityError {
			return nil, tp, d
		}
		warnings = append(warnings, d)
	}
	tab, err := mono.Program(tp.Program)
	if err != nil {
		return nil, tp, err
	}
	tp.Program.Mono = tab
	mod, err := lowerProgram(b, tp.Program, Options{
		ModuleRoot:        opts.ModuleRoot,
		Names:             opts.Names,
		SynthPrintRuntime: false,
	})
	if err != nil {
		return nil, tp, err
	}
	if err := ir.Verify(mod); err != nil {
		return nil, tp, fmt.Errorf("pipeline: IR verify: %w", err)
	}
	wasmBytes, err := b.Codegen.Module(mod, CodegenOptions{
		Names:             opts.Names,
		SynthPrintRuntime: false,
	})
	if err != nil {
		return nil, tp, err
	}
	return &Result{Program: tp.Program, IR: mod, Wasm: wasmBytes, Warnings: warnings}, tp, nil
}

// ResolveUpstreamSrc returns <pin-root>/src containing *_test.go files.
func ResolveUpstreamSrc(moduleRoot string) (string, error) {
	if v := os.Getenv("WASMGC_GOROOT"); v != "" {
		src := filepath.Join(v, "src")
		if st, err := os.Stat(src); err == nil && st.IsDir() {
			return src, nil
		}
	}
	root := moduleRoot
	if root == "" {
		var err error
		root, err = stdlib.CompilerModuleRoot()
		if err != nil {
			return "", err
		}
	}
	p, err := stdlib.DefaultPaths(root)
	if err != nil {
		return "", err
	}
	pin, err := stdlib.LoadPIN(p.PIN)
	if err != nil {
		return "", err
	}
	srcRoot, err := stdlib.EnsureUpstreamSrc(p, pin)
	if err != nil {
		return "", err
	}
	return filepath.Join(srcRoot, "src"), nil
}
