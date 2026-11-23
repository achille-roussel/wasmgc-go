package pipeline

import (
	"context"
	"fmt"

	"github.com/achille-roussel/wasmgc-go/internal/check"
	"github.com/achille-roussel/wasmgc-go/internal/ir"
	"github.com/achille-roussel/wasmgc-go/internal/load"
)

// Options controls a compile.
type Options struct {
	Package  string
	Tags     []string
	Names    bool
	VetFail  bool
	GOROOT   string
	Dir      string
	Backends Backends
	// SkipStdlib skips materializing GOROOT when the program needs no std.
	SkipStdlib bool
	// SynthPrintRuntime forwards to codegen until stream F lands the runtime.
	// Default true for run; size builds may leave it true (empty main ignores it).
	SynthPrintRuntime bool
}

// Result is the output of a compile.
type Result struct {
	Program  *load.Program
	IR       *ir.Module
	Wasm     []byte
	Warnings []check.Diagnostic
}

func resolveBackends(b Backends) Backends {
	if b.Lower == nil {
		b.Lower = Default.Lower
	}
	if b.Codegen == nil {
		b.Codegen = Default.Codegen
	}
	if b.Stdlib == nil {
		b.Stdlib = Default.Stdlib
	}
	return b
}

func resolveGOROOT(opts Options, b Backends) (string, error) {
	if opts.GOROOT != "" {
		return opts.GOROOT, nil
	}
	if opts.SkipStdlib {
		return "", nil
	}
	if b.Stdlib == nil {
		return "", nil
	}
	return b.Stdlib.GOROOT(context.Background())
}

// Compile loads, checks, lowers, and codegens a package.
func Compile(opts Options) (*Result, error) {
	b := resolveBackends(opts.Backends)
	pkg := opts.Package
	if pkg == "" {
		pkg = "."
	}
	goroot, err := resolveGOROOT(opts, b)
	if err != nil {
		return nil, fmt.Errorf("pipeline: stdlib GOROOT: %w", err)
	}
	prog, err := load.Load(load.Config{
		Dir:     opts.Dir,
		Pattern: pkg,
		Tags:    opts.Tags,
		GOROOT:  goroot,
	})
	if err != nil {
		return nil, err
	}
	diags, err := check.Check(prog, check.Options{VetFail: opts.VetFail})
	if err != nil {
		return nil, err
	}
	var warnings []check.Diagnostic
	for _, d := range diags {
		if d.Severity == check.SeverityError {
			return nil, d
		}
		warnings = append(warnings, d)
	}
	mod, err := b.Lower.Package(prog.Packages, prog.Main)
	if err != nil {
		return nil, err
	}
	if err := ir.Verify(mod); err != nil {
		return nil, fmt.Errorf("pipeline: IR verify: %w", err)
	}
	wasmBytes, err := b.Codegen.Module(mod, CodegenOptions{
		Names:             opts.Names,
		SynthPrintRuntime: opts.SynthPrintRuntime,
	})
	if err != nil {
		return nil, err
	}
	return &Result{Program: prog, IR: mod, Wasm: wasmBytes, Warnings: warnings}, nil
}

// DumpIR loads, checks, and lowers a package, returning the IR text.
func DumpIR(opts Options) (string, *Result, error) {
	b := resolveBackends(opts.Backends)
	pkg := opts.Package
	if pkg == "" {
		pkg = "."
	}
	goroot, err := resolveGOROOT(opts, b)
	if err != nil {
		return "", nil, fmt.Errorf("pipeline: stdlib GOROOT: %w", err)
	}
	prog, err := load.Load(load.Config{
		Dir:     opts.Dir,
		Pattern: pkg,
		Tags:    opts.Tags,
		GOROOT:  goroot,
	})
	if err != nil {
		return "", nil, err
	}
	diags, err := check.Check(prog, check.Options{VetFail: opts.VetFail})
	if err != nil {
		return "", nil, err
	}
	var warnings []check.Diagnostic
	for _, d := range diags {
		if d.Severity == check.SeverityError {
			return "", nil, d
		}
		warnings = append(warnings, d)
	}
	mod, err := b.Lower.Package(prog.Packages, prog.Main)
	if err != nil {
		return "", nil, err
	}
	if err := ir.Verify(mod); err != nil {
		return "", nil, fmt.Errorf("pipeline: IR verify: %w", err)
	}
	return ir.Format(mod), &Result{Program: prog, IR: mod, Warnings: warnings}, nil
}
