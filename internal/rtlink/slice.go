package rtlink

import (
	"fmt"
	"os"
	"path/filepath"

	"github.com/achille-roussel/wasmgc-go/internal/ir"
	"github.com/achille-roussel/wasmgc-go/internal/lower"
)

// EnsureSlice links runtime.panicIndex / panicSliceB from
// std/src/runtime/slice.go when the module will need them (slice/index
// high ops, or existing calls) and they are missing. Bodies stay empty;
// codegen fills unreachable until M5 EH.
func EnsureSlice(mod *ir.Module, rewriteDir string) error {
	if mod == nil {
		return nil
	}
	needIndex, needSliceB := scanSlicePanics(mod)
	if !needIndex && !needSliceB {
		return nil
	}
	if (!needIndex || mod.LookupFunc("runtime.panicIndex") != nil) &&
		(!needSliceB || mod.LookupFunc("runtime.panicSliceB") != nil) {
		return nil
	}
	if rewriteDir == "" {
		return fmt.Errorf("rtlink: rewrite dir unset; cannot link runtime slice panics")
	}
	path := filepath.Join(rewriteDir, "runtime", "slice.go")
	src, err := os.ReadFile(path)
	if err != nil {
		return fmt.Errorf("rtlink: read %s: %w", path, err)
	}
	fset, files, pkg, info, err := lower.TypeCheck(path, string(src))
	if err != nil {
		return fmt.Errorf("rtlink: typecheck slice.go: %w", err)
	}
	stripSliceFuncs(mod)
	if err := lower.AppendFiles(mod, fset, files, pkg, info); err != nil {
		return fmt.Errorf("rtlink: lower slice.go: %w", err)
	}
	return nil
}

func scanSlicePanics(mod *ir.Module) (needIndex, needSliceB bool) {
	for _, f := range mod.Funcs {
		walkCalls(f.Body, func(name string) {
			switch name {
			case "runtime.panicIndex":
				needIndex = true
			case "runtime.panicSliceB":
				needSliceB = true
			}
		})
		walkHighOps(f.Body, func(op ir.Op) {
			switch op {
			case ir.OpSliceGet, ir.OpSliceSet, ir.OpStringGet:
				needIndex = true
			case ir.OpSliceAppend, ir.OpSliceSlice, ir.OpStringSlice:
				needSliceB = true
			}
		})
	}
	return
}

func stripSliceFuncs(mod *ir.Module) {
	names := map[string]bool{
		"runtime.panicIndex":  true,
		"runtime.panicSliceB": true,
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

func walkHighOps(r ir.Region, fn func(ir.Op)) {
	if r == nil {
		return
	}
	walkStmtsOps(r, fn)
}

func walkStmtsOps(r ir.Region, fn func(ir.Op)) {
	switch r := r.(type) {
	case *ir.Seq:
		for _, s := range r.Stmts {
			walkStmtOps(s, fn)
		}
	case *ir.Block:
		walkStmtsOps(r.Body, fn)
	case *ir.Loop:
		walkStmtsOps(r.Body, fn)
	case *ir.If:
		walkExprOps(r.Cond, fn)
		walkStmtsOps(r.Then, fn)
		walkStmtsOps(r.Else, fn)
	case *ir.Switch:
		walkExprOps(r.Tag, fn)
		for _, c := range r.Cases {
			walkStmtsOps(c.Body, fn)
		}
		if r.Default != nil {
			walkStmtsOps(r.Default, fn)
		}
	}
}

func walkStmtOps(s ir.Stmt, fn func(ir.Op)) {
	switch s := s.(type) {
	case *ir.ExprStmt:
		walkExprOps(s.X, fn)
	case *ir.Assign:
		walkExprOps(s.Rhs, fn)
	case *ir.Return:
		for _, e := range s.Results {
			walkExprOps(e, fn)
		}
	case *ir.Br:
		if s.Cond != nil {
			walkExprOps(s.Cond, fn)
		}
	case ir.Region:
		walkStmtsOps(s, fn)
	}
}

func walkExprOps(e ir.Expr, fn func(ir.Op)) {
	if e == nil {
		return
	}
	in, ok := e.(*ir.Instr)
	if !ok {
		return
	}
	fn(in.Op)
	for _, a := range in.Args {
		walkExprOps(a, fn)
	}
}
