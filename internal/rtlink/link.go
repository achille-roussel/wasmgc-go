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

// EnsurePrint links runtime.printstring / printint / printnl / printfloat*
// / printcomplex* / printbool / printpointer / printiface / printslice from
// std/src/runtime/print.go when the module calls them and they are missing.
// printstring stays body-less (codegen intrinsic until WAT); the rest are
// ordinary Go.
func EnsurePrint(mod *ir.Module, rewriteDir string) error {
	if mod == nil {
		return nil
	}
	needString, needInt, needNL, needFloat, needComplex, needBool, needPtr, needStreq, needCmpstr := scanPrintCalls(mod)
	if !needString && !needInt && !needNL && !needFloat && !needComplex && !needBool && !needPtr && !needStreq && !needCmpstr {
		return nil
	}
	needPrintGo := needString || needInt || needNL || needFloat || needComplex || needBool || needPtr
	// Linked when decls exist: printstring may be body-less; others need bodies.
	if (!needString || mod.LookupFunc("runtime.printstring") != nil) &&
		(!needInt || hasPrintBody(mod, "runtime.printint")) &&
		(!needNL || hasPrintBody(mod, "runtime.printnl")) &&
		(!needFloat || hasPrintBody(mod, "runtime.printfloat64")) &&
		(!needComplex || hasPrintBody(mod, "runtime.printcomplex128")) &&
		(!needBool || hasPrintBody(mod, "runtime.printbool")) &&
		(!needPtr || hasPrintBody(mod, "runtime.printpointer")) &&
		(!needStreq || hasPrintBody(mod, "runtime.stringeq")) &&
		(!needCmpstr || hasPrintBody(mod, "runtime.cmpstring")) {
		return nil
	}
	if rewriteDir == "" {
		return fmt.Errorf("rtlink: rewrite dir unset; cannot link runtime print")
	}
	var filenames []string
	var srcs []string
	add := func(name string) error {
		path := filepath.Join(rewriteDir, "runtime", name)
		src, err := os.ReadFile(path)
		if err != nil {
			return fmt.Errorf("rtlink: read %s: %w", path, err)
		}
		filenames = append(filenames, path)
		srcs = append(srcs, string(src))
		return nil
	}
	if needPrintGo {
		if err := add("print.go"); err != nil {
			return err
		}
	}
	if needStreq || needCmpstr {
		if err := add("stringeq.go"); err != nil {
			return err
		}
	}
	fset, files, pkg, info, err := lower.TypeCheckFiles(filenames, srcs)
	if err != nil {
		return fmt.Errorf("rtlink: typecheck print: %w", err)
	}

	stripPrintFuncs(mod)
	if err := lower.AppendFiles(mod, fset, files, pkg, info); err != nil {
		return fmt.Errorf("rtlink: lower print: %w", err)
	}
	fillPrintF64bits(mod)
	return nil
}

func scanPrintCalls(mod *ir.Module) (needString, needInt, needNL, needFloat, needComplex, needBool, needPtr, needStreq, needCmpstr bool) {
	for _, f := range mod.Funcs {
		walkCalls(f.Body, func(name string) {
			switch name {
			case "runtime.printstring":
				needString = true
			case "runtime.printint", "runtime.printint64", "runtime.printuint", "runtime.printuint64":
				needInt = true
			case "runtime.printnl":
				needNL = true
			case "runtime.printfloat32", "runtime.printfloat64":
				needFloat = true
			case "runtime.printcomplex64", "runtime.printcomplex128":
				needComplex = true
			case "runtime.printbool":
				needBool = true
			case "runtime.printpointer", "runtime.printiface", "runtime.printslice":
				needPtr = true
			case "runtime.stringeq":
				needStreq = true
			case "runtime.cmpstring":
				needCmpstr = true
			}
		})
	}
	return
}

// fillPrintF64bits replaces empty printF64bits with i64.reinterpret_f64.
func fillPrintF64bits(mod *ir.Module) {
	f := mod.LookupFunc("runtime.printF64bits")
	if f == nil || len(f.Params) != 1 || !emptyBody(f) {
		return
	}
	p := f.Params[0]
	f.Body = &ir.Seq{Stmts: []ir.Stmt{
		&ir.Return{Results: []ir.Expr{
			&ir.Instr{Op: ir.OpI64ReinterpretF64, Args: []ir.Expr{&ir.Name{Raw: p.Name, Var: p}}},
		}},
	}}
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
		"runtime.printstring":      true,
		"runtime.printint":         true,
		"runtime.printint64":       true,
		"runtime.printnl":          true,
		"runtime.printuint":        true,
		"runtime.printuint64":      true,
		"runtime.printfloat32":     true,
		"runtime.printfloat64":     true,
		"runtime.printcomplex64":   true,
		"runtime.printcomplex128":  true,
		"runtime.printbool":        true,
		"runtime.printpointer":     true,
		"runtime.printiface":       true,
		"runtime.printslice":       true,
		"runtime.printF64bits":     true,
		"runtime.printFloatUint":   true,
		"runtime.printFloatDigits": true,
		"runtime.printDigitByte":   true,
		"runtime.stringeq":         true,
		"runtime.cmpstring":        true,
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
	case *ir.Try:
		walkCalls(r.Body, fn)
		walkCalls(r.Handler, fn)
	case *ir.Switch:
		for _, c := range r.Cases {
			walkCalls(c.Body, fn)
		}
		walkCalls(r.Default, fn)
	}
}

func walkHighOps(r ir.Region, fn func(ir.Op)) {
	if r == nil {
		return
	}
	switch r := r.(type) {
	case *ir.Seq:
		for _, s := range r.Stmts {
			walkStmtOps(s, fn)
		}
	case *ir.Block:
		walkHighOps(r.Body, fn)
	case *ir.Loop:
		walkHighOps(r.Body, fn)
	case *ir.If:
		walkExprOps(r.Cond, fn)
		walkHighOps(r.Then, fn)
		walkHighOps(r.Else, fn)
	case *ir.Switch:
		walkExprOps(r.Tag, fn)
		for _, c := range r.Cases {
			walkHighOps(c.Body, fn)
		}
		walkHighOps(r.Default, fn)
	case *ir.Try:
		walkHighOps(r.Body, fn)
		walkHighOps(r.Handler, fn)
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
		walkHighOps(s, fn)
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
