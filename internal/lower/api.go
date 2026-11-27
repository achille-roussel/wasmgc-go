package lower

import (
	"fmt"
	"go/ast"
	"go/token"
	"go/types"

	"github.com/achille-roussel/wasmgc-go/internal/escape"
	"github.com/achille-roussel/wasmgc-go/internal/ir"
	"github.com/achille-roussel/wasmgc-go/internal/load"
	"github.com/achille-roussel/wasmgc-go/internal/mono"
)

// Package lowers a whole type-checked program to low IR ready for codegen.
//
// pkgs must be in dependency order (as from load.Program.Packages). main is
// the main package (load.Program.Main). The result has Module.Low set: string
// literals are expanded to data + array.new_data + struct.new, and
// export "run" main.run calls package inits then main.main.
//
// This is the driver contract (internal/pipeline.Lowerer):
//
//	mod, err := lower.Package(prog.Packages, prog.Main)
//
// Pipeline prefers PackageHigh + stream F's rtlink + RuntimeOps so rewrite
// runtime print can be linked before string expansion.
func Package(pkgs []*load.Package, main *load.Package) (*ir.Module, error) {
	mod, err := PackageHigh(pkgs, main)
	if err != nil {
		return nil, err
	}
	if err := RuntimeOps(mod); err != nil {
		return nil, err
	}
	return mod, nil
}

// PackageHigh is Package without RuntimeOps. Module.Low stays false so the
// pipeline can append rewrite-runtime funcs (stream F) before expanding
// string literals.
func PackageHigh(pkgs []*load.Package, main *load.Package) (*ir.Module, error) {
	return PackageHighMono(pkgs, main, nil)
}

// PackageHighMono is PackageHigh with a monomorphization table from mono.Program.
func PackageHighMono(pkgs []*load.Package, main *load.Package, tab *mono.Table) (*ir.Module, error) {
	if main == nil {
		return nil, fmt.Errorf("lower.Package: no main package")
	}
	mod := &ir.Module{}
	md := metaOf(mod)

	seen := map[*load.Package]bool{}
	for _, pkg := range pkgs {
		if pkg == nil || seen[pkg] {
			continue
		}
		seen[pkg] = true
		if err := lowerLoadPackage(mod, md, pkg, tab); err != nil {
			return nil, err
		}
	}
	if !seen[main] {
		if err := lowerLoadPackage(mod, md, main, tab); err != nil {
			return nil, err
		}
	}
	// Cross-package generics: EnsureFunc during later packages (e.g. slices
	// calling cmp.Less[int]) may register instances after the origin package
	// already finished emitMonoInstances. Drain remaining bodies now.
	if err := emitAllPendingMono(mod, md, tab); err != nil {
		return nil, err
	}
	if err := Finish(mod); err != nil {
		return nil, err
	}
	// Escape rewrites non-escaping addresses before the pipeline's rtlink
	// and RuntimeOps expand remaining high ops (docs/design/09 M3).
	if err := escape.Analyze(mod); err != nil {
		return nil, err
	}
	return mod, nil
}

// AppendFiles lowers already type-checked files into an existing module.
// Does not call Finish or RuntimeOps. Used by stream F to link std/src/runtime
// print without pulling the whole rewrite package.
func AppendFiles(mod *ir.Module, fset *token.FileSet, files []*ast.File, pkg *types.Package, info *types.Info) error {
	if mod == nil {
		return fmt.Errorf("lower.AppendFiles: nil module")
	}
	l := &pkgLower{
		fset:  fset,
		files: files,
		pkg:   pkg,
		info:  info,
		mod:   mod,
		meta:  metaOf(mod),
	}
	return l.lower()
}

func lowerLoadPackage(mod *ir.Module, md *meta, pkg *load.Package, tab *mono.Table) error {
	if pkg.Types == nil || pkg.Info == nil {
		return fmt.Errorf("lower.Package: package %s is not type-checked", pkg.PkgPath)
	}
	// Rewrite package runtime is linked on demand by rtlink.Ensure*
	// (EnsureSched / EnsureMaps / EnsurePanic / EnsurePrint), same as
	// -skip-stdlib. Lowering every GOROOT runtime file here (via sync's
	// import "runtime") would keep map/select/Frames in mutex programs.
	if pkg.PkgPath == "runtime" && pkg.Standard {
		return nil
	}
	l := &pkgLower{
		fset:  nil, // positions reported by package path
		files: pkg.Syntax,
		pkg:   pkg.Types,
		info:  pkg.Info,
		mod:   mod,
		meta:  md,
		mono:  tab,
	}
	return l.lower()
}

// Finish synthesizes the exported entry point:
//
//	func export "run" main.run() {
//	  call <pkg.init…>   // dependency order from Package calls
//	  call main.main()
//	  return
//	}
//
// When the program already defines func run (IR main.run), the wrapper is
// named $go.run instead so Export "run" is still emitted (issue18636).
//
// It does not set Module.Low; Package calls RuntimeOps after Finish.
// Inits linked later via AppendFiles (rtlink.EnsureSched, etc.) are wired by
// LinkPendingInits after those Ensure* calls.
func Finish(mod *ir.Module) error {
	if mod == nil {
		return fmt.Errorf("lower.Finish: nil module")
	}
	md := metaOf(mod)
	if md.finished {
		return nil
	}
	if mod.LookupFunc("main.main") == nil {
		return fmt.Errorf("lower.Finish: no main.main function")
	}
	if mod.LookupExport("run") != nil {
		md.finished = true
		return nil
	}
	body := &ir.Seq{}
	for _, name := range md.initOrder {
		body.Stmts = append(body.Stmts, &ir.ExprStmt{
			X: &ir.Instr{Op: ir.OpCall, Func: name},
		})
	}
	body.Stmts = append(body.Stmts,
		&ir.ExprStmt{X: &ir.Instr{Op: ir.OpCall, Func: "main.main"}},
		&ir.Return{},
	)
	entry := ir.RunEntryName
	if mod.LookupFunc(entry) != nil {
		entry = ir.AltRunEntryName
	}
	run := &ir.Func{
		Name:   entry,
		Export: "run",
		Body:   body,
	}
	mod.Funcs = append([]*ir.Func{run}, mod.Funcs...)
	md.finished = true
	return nil
}

// LinkPendingInits prepends any meta.initOrder entries not yet called from
// main.run. EnsureSched / EnsureMaps AppendFiles after Finish can register
// runtime.init (sets gp / ngo); without this hook the first park deadlocks
// ("all goroutines are asleep") and DCE drops the unreachable init.
func LinkPendingInits(mod *ir.Module) {
	if mod == nil {
		return
	}
	md := metaOf(mod)
	if md == nil || len(md.initOrder) == 0 {
		return
	}
	run := mod.LookupExport("run")
	if run == nil || run.Body == nil {
		return
	}
	called := map[string]bool{}
	walkInitCalls(run.Body, func(name string) { called[name] = true })
	var missing []string
	for _, name := range md.initOrder {
		if called[name] {
			continue
		}
		if mod.LookupFunc(name) == nil {
			continue
		}
		missing = append(missing, name)
	}
	if len(missing) == 0 {
		return
	}
	seq, ok := run.Body.(*ir.Seq)
	if !ok {
		return
	}
	// Insert before main.main (or at start if the body was EH-wrapped later).
	insert := make([]ir.Stmt, 0, len(missing))
	for _, name := range missing {
		insert = append(insert, &ir.ExprStmt{
			X: &ir.Instr{Op: ir.OpCall, Func: name},
		})
	}
	idx := 0
	for i, s := range seq.Stmts {
		if es, ok := s.(*ir.ExprStmt); ok {
			if in, ok := es.X.(*ir.Instr); ok && in.Op == ir.OpCall && in.Func == "main.main" {
				idx = i
				break
			}
		}
	}
	out := make([]ir.Stmt, 0, len(seq.Stmts)+len(insert))
	out = append(out, seq.Stmts[:idx]...)
	out = append(out, insert...)
	out = append(out, seq.Stmts[idx:]...)
	seq.Stmts = out
}

func walkInitCalls(r ir.Region, fn func(string)) {
	if r == nil {
		return
	}
	switch r := r.(type) {
	case *ir.Seq:
		for _, s := range r.Stmts {
			if es, ok := s.(*ir.ExprStmt); ok {
				if in, ok := es.X.(*ir.Instr); ok && in.Op == ir.OpCall {
					fn(in.Func)
				}
			}
			if nested, ok := s.(ir.Region); ok {
				walkInitCalls(nested, fn)
			}
		}
	case *ir.Block:
		walkInitCalls(r.Body, fn)
	case *ir.Try:
		walkInitCalls(r.Body, fn)
		walkInitCalls(r.Handler, fn)
	}
}

// meta holds whole-program state shared across package lowering.
type meta struct {
	initOrder []string
	hasMain   bool
	finished  bool
	closSeq   int
	cellVars  map[types.Object]bool
	needsEH    bool // set when any function uses defer/panic/recover
	needsSched bool // set when go / chan / select / park may be needed
	needsMaps  bool // set when map ops may be needed (M6)
}

var moduleMeta = map[*ir.Module]*meta{}

func metaOf(m *ir.Module) *meta {
	if m == nil {
		return nil
	}
	if md, ok := moduleMeta[m]; ok {
		return md
	}
	md := &meta{}
	moduleMeta[m] = md
	return md
}

// CompileHigh type-checks nothing; it lowers one already-checked package and
// Finishes, leaving high IR (Module.Low == false). Used by unit tests that
// compare against internal/ir/testdata goldens before RuntimeOps.
func CompileHigh(fset *token.FileSet, files []*ast.File, pkg *types.Package, info *types.Info) (*ir.Module, error) {
	mod := &ir.Module{}
	md := metaOf(mod)
	l := &pkgLower{
		fset:  fset,
		files: files,
		pkg:   pkg,
		info:  info,
		mod:   mod,
		meta:  md,
	}
	if err := l.lower(); err != nil {
		return nil, err
	}
	if err := Finish(mod); err != nil {
		return nil, err
	}
	return mod, nil
}

// Compile is a test helper: type-check src, lower to high IR, Finish.
func Compile(filename, src string) (*ir.Module, error) {
	fset, files, pkg, info, err := TypeCheck(filename, src)
	if err != nil {
		return nil, err
	}
	return CompileHigh(fset, files, pkg, info)
}
