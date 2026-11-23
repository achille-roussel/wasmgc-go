// Package rtlink links rewrite-tier runtime symbols into an IR module.
// Stream F owns this; it reads std/src/runtime without requiring a full
// GOROOT materialize (docs/design/06).
package rtlink

import (
	"fmt"
	"os"
	"path/filepath"

	"github.com/achille-roussel/wasmgc-go/internal/ir"
	"github.com/achille-roussel/wasmgc-go/internal/lower"
)

// EnsurePrint links runtime.printstring / printint / printnl from
// std/src/runtime/print.go when the module calls them and they are missing.
// printstring stays body-less (codegen intrinsic until WAT); printnl and
// printint are ordinary Go.
func EnsurePrint(mod *ir.Module, rewriteDir string) error {
	if mod == nil {
		return nil
	}
	needString, needInt, needNL := scanPrintCalls(mod)
	if !needString && !needInt && !needNL {
		return nil
	}
	// Linked when decls exist: printstring may be body-less; int/nl need bodies.
	if (!needString || mod.LookupFunc("runtime.printstring") != nil) &&
		(!needInt || hasPrintBody(mod, "runtime.printint")) &&
		(!needNL || hasPrintBody(mod, "runtime.printnl")) {
		return nil
	}
	if rewriteDir == "" {
		return fmt.Errorf("rtlink: rewrite dir unset; cannot link runtime print")
	}
	path := filepath.Join(rewriteDir, "runtime", "print.go")
	src, err := os.ReadFile(path)
	if err != nil {
		return fmt.Errorf("rtlink: read %s: %w", path, err)
	}
	fset, files, pkg, info, err := lower.TypeCheck(path, string(src))
	if err != nil {
		return fmt.Errorf("rtlink: typecheck print.go: %w", err)
	}

	stripPrintFuncs(mod)
	if err := lower.AppendFiles(mod, fset, files, pkg, info); err != nil {
		return fmt.Errorf("rtlink: lower print.go: %w", err)
	}
	return nil
}

func scanPrintCalls(mod *ir.Module) (needString, needInt, needNL bool) {
	for _, f := range mod.Funcs {
		walkCalls(f.Body, func(name string) {
			switch name {
			case "runtime.printstring":
				needString = true
			case "runtime.printint":
				needInt = true
			case "runtime.printnl":
				needNL = true
			}
		})
	}
	return
}

func hasPrintBody(mod *ir.Module, name string) bool {
	f := mod.LookupFunc(name)
	if f == nil {
		return false
	}
	return !emptyBody(f)
}

func emptyBody(f *ir.Func) bool {
	if f == nil || f.Body == nil {
		return true
	}
	seq, ok := f.Body.(*ir.Seq)
	if !ok {
		return false
	}
	return len(seq.Stmts) == 0
}

func stripPrintFuncs(mod *ir.Module) {
	names := map[string]bool{
		"runtime.printstring": true,
		"runtime.printint":    true,
		"runtime.printnl":     true,
		"runtime.printuint":   true,
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

func walkCalls(r ir.Region, fn func(string)) {
	if r == nil {
		return
	}
	switch r := r.(type) {
	case *ir.Seq:
		for _, s := range r.Stmts {
			walkStmtCalls(s, fn)
		}
	case *ir.Block:
		walkCalls(r.Body, fn)
	case *ir.Loop:
		walkCalls(r.Body, fn)
	case *ir.If:
		walkCalls(r.Then, fn)
		walkCalls(r.Else, fn)
	}
}

func walkStmtCalls(s ir.Stmt, fn func(string)) {
	switch s := s.(type) {
	case *ir.ExprStmt:
		walkExprCalls(s.X, fn)
	case *ir.Assign:
		walkExprCalls(s.Rhs, fn)
	case *ir.Return:
		for _, e := range s.Results {
			walkExprCalls(e, fn)
		}
	case *ir.If:
		walkExprCalls(s.Cond, fn)
		walkCalls(s.Then, fn)
		walkCalls(s.Else, fn)
	case *ir.Block:
		walkCalls(s.Body, fn)
	case *ir.Loop:
		walkCalls(s.Body, fn)
	case *ir.Seq:
		walkCalls(s, fn)
	case ir.Region:
		walkCalls(s, fn)
	}
}

func walkExprCalls(e ir.Expr, fn func(string)) {
	if e == nil {
		return
	}
	switch e := e.(type) {
	case *ir.Instr:
		if e.Op == ir.OpCall {
			fn(e.Func)
		}
		for _, a := range e.Args {
			walkExprCalls(a, fn)
		}
	}
}
