package pipeline

import (
	"context"
	"fmt"
	"path/filepath"

	"github.com/achille-roussel/wasmgc-go/internal/check"
	"github.com/achille-roussel/wasmgc-go/internal/desc"
	"github.com/achille-roussel/wasmgc-go/internal/ir"
	"github.com/achille-roussel/wasmgc-go/internal/load"
	"github.com/achille-roussel/wasmgc-go/internal/lower"
	"github.com/achille-roussel/wasmgc-go/internal/mono"
	"github.com/achille-roussel/wasmgc-go/internal/rtlink"
	"github.com/achille-roussel/wasmgc-go/internal/stdlib"
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
	// Files, if non-empty, compiles these .go files as package main (single-file
	// / $GOROOT/test mode) instead of resolving Package via go list.
	Files []string
	// SkipStdlib skips materializing GOROOT when the program needs no std.
	SkipStdlib bool
	// SynthPrintRuntime synthesizes print* when rtlink did not link them
	// (unit tests). Drivers default this off; rtlink supplies std/src/runtime.
	SynthPrintRuntime bool
	// ModuleRoot is the wasmgc-go repo root (for std/src rewrites). Empty =
	// discover via FindModuleRoot.
	ModuleRoot string
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
		Files:   opts.Files,
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
	tab, err := mono.Program(prog)
	if err != nil {
		return nil, err
	}
	prog.Mono = tab
	mod, err := lowerProgram(b, prog, opts)
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
		Files:   opts.Files,
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
	tab, err := mono.Program(prog)
	if err != nil {
		return "", nil, err
	}
	prog.Mono = tab
	mod, err := lowerProgram(b, prog, opts)
	if err != nil {
		return "", nil, err
	}
	if err := ir.Verify(mod); err != nil {
		// Still return IR so callers can dump on failure.
		return ir.Format(mod), &Result{Program: prog, IR: mod, Warnings: warnings}, fmt.Errorf("pipeline: IR verify: %w", err)
	}
	return ir.Format(mod), &Result{Program: prog, IR: mod, Warnings: warnings}, nil
}

// lowerProgram runs PackageHigh, links rewrite runtime print/slice panics
// (stream F), then RuntimeOps. Stub lowerers skip rtlink.
func lowerProgram(b Backends, prog *load.Program, opts Options) (*ir.Module, error) {
	if isStubLowerer(b.Lower) {
		return b.Lower.Package(prog.Packages, prog.Main)
	}
	var tab *mono.Table
	if prog.Mono != nil {
		tab, _ = prog.Mono.(*mono.Table)
	}
	mod, err := lower.PackageHighMono(prog.Packages, prog.Main, tab)
	if err != nil {
		return nil, err
	}
	rewriteDir, err := rewriteDir(opts)
	if err != nil {
		return nil, err
	}
	if err := rtlink.EnsurePrint(mod, rewriteDir); err != nil {
		return nil, err
	}
	if err := rtlink.EnsureSlice(mod, rewriteDir); err != nil {
		return nil, err
	}
	if err := rtlink.EnsureStringConv(mod, rewriteDir); err != nil {
		return nil, err
	}
	if err := rtlink.EnsureUTF8(mod, rewriteDir); err != nil {
		return nil, err
	}
	if err := rtlink.EnsurePanic(mod, rewriteDir); err != nil {
		return nil, err
	}
	if err := rtlink.EnsureSched(mod, rewriteDir); err != nil {
		return nil, err
	}
	if err := rtlink.EnsureArgs(mod, rewriteDir); err != nil {
		return nil, err
	}
	if err := rtlink.EnsureMaps(mod, rewriteDir); err != nil {
		return nil, err
	}
	rtlink.EnsurePool(mod)
	if err := rtlink.EnsurePublic(mod, rewriteDir); err != nil {
		return nil, err
	}
	// Reflect helpers (runtime.Reflect*) before desc fills their body-less IR.
	if err := rtlink.EnsureReflect(mod, rewriteDir); err != nil {
		return nil, err
	}
	// Sched/maps may introduce printstring / printnl via fatal; link if still missing.
	if err := rtlink.EnsurePrint(mod, rewriteDir); err != nil {
		return nil, err
	}
	// Stream G: descriptors and itables before RuntimeOps expands iface ops.
	if err := desc.Populate(mod, prog); err != nil {
		return nil, err
	}
	if err := lower.RuntimeOps(mod); err != nil {
		return nil, err
	}
	// RuntimeOps may introduce panicTypeAssert / panicNil via iface asserts
	// in linked chan ops closures; link any newly referenced panic/print
	// symbols and expand again.
	if err := rtlink.EnsurePanic(mod, rewriteDir); err != nil {
		return nil, err
	}
	if err := rtlink.EnsurePrint(mod, rewriteDir); err != nil {
		return nil, err
	}
	if err := lower.RuntimeOps(mod); err != nil {
		return nil, err
	}
	// AppendFiles from Ensure* may have registered package inits (e.g.
	// runtime.init for gp/ngo) after Finish already built main.run.
	lower.LinkPendingInits(mod)
	// Drop RegisterPoolCleanup calls (no-op) so sync.poolCleanup can die.
	ir.PruneNoopCalls(mod)
	// Blank fmt type-switch arms for types never boxed as any in this
	// program so float/complex strconv paths die before DCE (M8 size).
	ir.PruneDeadFmtTypeCases(mod)
	// Blank fmt verb arms unused by Print/Println-only programs.
	ir.PruneDeadFmtVerbs(mod)
	// Remove write-only package globals (pow10Tab, math tables) and their
	// init stores, then drop empty inits / unused data.
	ir.PruneDeadInits(mod)
	// Drop funcs not reachable from exports / ref.func sites.
	ir.PruneUnreachable(mod)
	// Second scrub: DCE may have removed the only global.get of a table.
	ir.PruneDeadInits(mod)
	ir.PruneUnreachable(mod)
	// Pack remaining live const struct tables (e.g. pow10Tab when floats are
	// printed) after dead-init scrub so we do not keep side-effecting fill
	// loops for write-only tables.
	ir.PackConstStructArrays(mod)
	ir.PruneUnusedData(mod)
	return mod, nil
}

func rewriteDir(opts Options) (string, error) {
	root := opts.ModuleRoot
	if root == "" {
		var err error
		root, err = stdlib.CompilerModuleRoot()
		if err != nil {
			return "", fmt.Errorf("pipeline: compiler module root: %w", err)
		}
	}
	return filepath.Join(root, "std", "src"), nil
}
