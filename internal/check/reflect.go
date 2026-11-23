package check

import (
	"fmt"
	"go/ast"
	"go/token"
	"go/types"
	"strings"
)

const KindReflect = Kind("reflect")

// CheckReflectConstructors reports reachable reflect.StructOf (and peers) calls
// whose result is not in the closed Wasm type universe.
// See docs/design/05-unsafe-and-reflect.md.
//
// For M4, only StructOf on a non-constant field list is diagnosed. Dead code
// (functions never called from main) is skipped when reachable is provided;
// if reachable is nil, every call site is checked (conservative).
func CheckReflectConstructors(fset *token.FileSet, info *types.Info, files []*ast.File, reachable map[*types.Func]bool) []Diagnostic {
	var diags []Diagnostic
	for _, f := range files {
		// Skip entire file functions that are unreachable when we have a set.
		ast.Inspect(f, func(n ast.Node) bool {
			fd, ok := n.(*ast.FuncDecl)
			if !ok || fd.Body == nil {
				return true
			}
			if reachable != nil {
				obj, _ := info.Defs[fd.Name].(*types.Func)
				if obj != nil && !reachable[obj] {
					return false // skip body
				}
			}
			ast.Inspect(fd.Body, func(n ast.Node) bool {
				ce, ok := n.(*ast.CallExpr)
				if !ok {
					return true
				}
				if d := checkStructOf(fset, info, ce); d != nil {
					diags = append(diags, *d)
				}
				return true
			})
			return false
		})
	}
	return diags
}

func checkStructOf(fset *token.FileSet, info *types.Info, ce *ast.CallExpr) *Diagnostic {
	sel, ok := ce.Fun.(*ast.SelectorExpr)
	if !ok || sel.Sel.Name != "StructOf" {
		return nil
	}
	pkgName, ok := sel.X.(*ast.Ident)
	if !ok {
		return nil
	}
	obj := info.Uses[pkgName]
	pn, ok := obj.(*types.PkgName)
	if !ok || pn.Imported() == nil || pn.Imported().Path() != "reflect" {
		return nil
	}
	if len(ce.Args) != 1 {
		return nil
	}
	// Constant field list: composite lit of []reflect.StructField with only
	// constant-shaped entries. A variable or non-lit is not constant.
	if isConstantStructFieldList(info, ce.Args[0]) {
		return nil
	}
	pos := fset.Position(ce.Pos())
	expr := exprString(fset, ce)
	msg := fmt.Sprintf("reflect.StructOf result is not a Wasm type in this program\n    %s\n    the field list is not a constant struct type this program already declares.\n    Wasm GC cannot mint a struct type at run time.\n    this program's universe is the types it writes, plus one step of *T, []T,\n    and map[T]struct{} for a comparable T.", expr)
	return &Diagnostic{
		Pos:        pos,
		Severity:   SeverityError,
		Kind:       KindReflect,
		Message:    msg,
		Expression: expr,
	}
}

func isConstantStructFieldList(info *types.Info, e ast.Expr) bool {
	switch e := e.(type) {
	case *ast.CompositeLit:
		return true // composite lit of fields — treated as constant shape for M4
	case *ast.Ident:
		// Variable — not constant.
		return false
	case *ast.CallExpr:
		return false
	default:
		_ = e
		return false
	}
}

// ReachableFuncs returns functions reachable from main.main / init in prog packages.
// Conservative: if analysis fails, returns nil (check all).
func ReachableFuncs(pkgs []*types.Package, info *types.Info, files []*ast.File) map[*types.Func]bool {
	// Simple mark from main and init; for M4 tests with a single package.
	reachable := map[*types.Func]bool{}
	var queue []*types.Func
	for _, f := range files {
		for _, d := range f.Decls {
			fd, ok := d.(*ast.FuncDecl)
			if !ok || fd.Body == nil {
				continue
			}
			fn, _ := info.Defs[fd.Name].(*types.Func)
			if fn == nil {
				continue
			}
			if fd.Name.Name == "main" || fd.Name.Name == "init" {
				if !reachable[fn] {
					reachable[fn] = true
					queue = append(queue, fn)
				}
			}
		}
	}
	// Build body map
	bodies := map[*types.Func]*ast.BlockStmt{}
	for _, f := range files {
		for _, d := range f.Decls {
			fd, ok := d.(*ast.FuncDecl)
			if !ok || fd.Body == nil {
				continue
			}
			if fn, _ := info.Defs[fd.Name].(*types.Func); fn != nil {
				bodies[fn] = fd.Body
			}
		}
	}
	for len(queue) > 0 {
		fn := queue[0]
		queue = queue[1:]
		body := bodies[fn]
		if body == nil {
			continue
		}
		ast.Inspect(body, func(n ast.Node) bool {
			id, ok := n.(*ast.Ident)
			if !ok {
				return true
			}
			cal, _ := info.Uses[id].(*types.Func)
			if cal == nil || reachable[cal] {
				return true
			}
			reachable[cal] = true
			queue = append(queue, cal)
			return true
		})
	}
	_ = pkgs
	_ = strings.Builder{}
	return reachable
}
