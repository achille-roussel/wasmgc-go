package rtlink

import (
	"fmt"
	"os"
	"path/filepath"

	"github.com/achille-roussel/wasmgc-go/internal/ir"
	"github.com/achille-roussel/wasmgc-go/internal/lower"
)

// EnsureMaps links runtime map.go (+ rand for seeds) when the module uses
// map ops or calls runtime.map*. Fills body-less f32bits/f64bits with
// reinterpret IR (design 12 nosuspend intrinsic stand-in).
func EnsureMaps(mod *ir.Module, rewriteDir string) error {
	if mod == nil {
		return nil
	}
	if !scanMapNeeds(mod) {
		return nil
	}
	if mod.LookupFunc("runtime.makemap") != nil && mod.LookupFunc("runtime.mapAccess") != nil {
		fillFloatBits(mod)
		return nil
	}
	if rewriteDir == "" {
		return fmt.Errorf("rtlink: rewrite dir unset; cannot link runtime maps")
	}
	files := []string{"map.go"}
	if mod.LookupFunc("runtime.fastrand") == nil {
		files = append(files, "rand.go")
	}
	// throw / panicUncomparable / print for map nil-assign and uncomparable keys.
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
		return fmt.Errorf("rtlink: typecheck map runtime: %w", err)
	}
	stripMapFuncs(mod)
	if needPrint || needFatal {
		stripPrintFuncs(mod)
	}
	if err := lower.AppendFiles(mod, fset, astFiles, pkg, info); err != nil {
		return fmt.Errorf("rtlink: lower map runtime: %w", err)
	}
	fillFloatBits(mod)
	return nil
}

var mapSyms = []string{
	"runtime.makemap", "runtime.mapAccess", "runtime.mapAssign", "runtime.mapDelete",
	"runtime.mapClear", "runtime.mapLen", "runtime.mapIterStart", "runtime.mapIterNext",
	"runtime.mapKeyHash", "runtime.strhash", "runtime.f32hash", "runtime.f64hash",
	"runtime.f32bits", "runtime.f64bits",
	"runtime.hashBool", "runtime.hashInt", "runtime.hashInt64", "runtime.hashUint",
	"runtime.hashUint64", "runtime.hashFloat32", "runtime.hashFloat64", "runtime.hashString",
}

func scanMapNeeds(mod *ir.Module) bool {
	for _, f := range mod.Funcs {
		need := false
		walkCalls(f.Body, func(name string) {
			switch name {
			case "runtime.makemap", "runtime.mapAccess", "runtime.mapAssign",
				"runtime.mapDelete", "runtime.mapClear", "runtime.mapLen",
				"runtime.mapIterStart", "runtime.mapIterNext", "runtime.mapKeyHash":
				need = true
			}
		})
		walkHighOps(f.Body, func(op ir.Op) {
			switch op {
			case ir.OpMapMake, ir.OpMapGet, ir.OpMapSet, ir.OpMapDelete,
				ir.OpMapLen, ir.OpMapIter, ir.OpMapNext:
				need = true
			}
		})
		if need {
			return true
		}
	}
	// Also detect $go.hmap types in the module (make already emits OpMapMake).
	return false
}

func stripMapFuncs(mod *ir.Module) {
	names := map[string]bool{}
	for _, s := range mapSyms {
		names[s] = true
	}
	out := mod.Funcs[:0]
	for _, f := range mod.Funcs {
		if names[f.Name] {
			continue
		}
		out = append(out, f)
	}
	mod.Funcs = out
}

// fillFloatBits replaces empty f32bits/f64bits bodies with reinterpret.
func fillFloatBits(mod *ir.Module) {
	if f := mod.LookupFunc("runtime.f32bits"); f != nil && emptyBody(f) {
		p := f.Params[0]
		f.Body = &ir.Seq{Stmts: []ir.Stmt{
			&ir.Return{Results: []ir.Expr{
				&ir.Instr{Op: ir.OpI32ReinterpretF32, Args: []ir.Expr{&ir.Name{Raw: p.Name, Var: p}}},
			}},
		}}
	}
	if f := mod.LookupFunc("runtime.f64bits"); f != nil && emptyBody(f) {
		p := f.Params[0]
		f.Body = &ir.Seq{Stmts: []ir.Stmt{
			&ir.Return{Results: []ir.Expr{
				&ir.Instr{Op: ir.OpI64ReinterpretF64, Args: []ir.Expr{&ir.Name{Raw: p.Name, Var: p}}},
			}},
		}}
	}
}

