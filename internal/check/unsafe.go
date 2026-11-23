package check

import (
	"fmt"
	"go/ast"
	"go/token"
	"go/types"
	"strings"
)

// Severity classifies a diagnostic.
type Severity int

const (
	// SeverityError fails the build.
	SeverityError Severity = iota
	// SeverityWarning is reported but does not fail unless VetFail is set.
	SeverityWarning
)

// Kind classifies the diagnostic family for formatting.
type Kind string

const (
	KindUnsafe     Kind = "unsafe"
	KindIntConvert Kind = "intconvert"
)

// Diagnostic is a pre-IR check finding.
type Diagnostic struct {
	Pos        token.Position
	Severity   Severity
	Kind       Kind
	Message    string
	Expression string
}

func (d Diagnostic) Error() string { return d.String() }

func (d Diagnostic) String() string {
	var b strings.Builder
	if d.Pos.IsValid() {
		fmt.Fprintf(&b, "%s: ", d.Pos)
	}
	b.WriteString(d.Message)
	if d.Kind == KindIntConvert {
		return b.String()
	}
	// Default / unsafe format from docs/design/05-unsafe-and-reflect.md.
	if d.Expression != "" {
		fmt.Fprintf(&b, "\n    expression: %s", d.Expression)
	}
	b.WriteString("\n    Wasm GC has no integer address for a GC object.\n")
	b.WriteString("    supported: unsafe.Slice, unsafe.String, unsafe.SliceData, unsafe.StringData,\n")
	b.WriteString("    and field or element access on a typed pointer.")
	return b.String()
}

// CheckUnsafe walks type-checked files and reports rejected unsafe operations
// from docs/design/05-unsafe-and-reflect.md.
func CheckUnsafe(fset *token.FileSet, info *types.Info, files []*ast.File) []Diagnostic {
	var diags []Diagnostic
	for _, f := range files {
		ast.Inspect(f, func(n ast.Node) bool {
			switch e := n.(type) {
			case *ast.CallExpr:
				diags = append(diags, checkCall(fset, info, e)...)
			case *ast.BinaryExpr:
				diags = append(diags, checkBinary(fset, info, e)...)
			case *ast.UnaryExpr:
				// nothing special
			}
			return true
		})
		// Conversions are *ast.CallExpr for unsafe.Pointer(x) style and
		// *ast.ParenExpr / conversion via CallExpr when Fun is a type.
		ast.Inspect(f, func(n ast.Node) bool {
			ce, ok := n.(*ast.CallExpr)
			if !ok || len(ce.Args) != 1 {
				return true
			}
			if d := checkConversion(fset, info, ce); d != nil {
				diags = append(diags, *d)
			}
			return true
		})
	}
	return dedupe(diags)
}

func checkCall(fset *token.FileSet, info *types.Info, ce *ast.CallExpr) []Diagnostic {
	var diags []Diagnostic
	sel, ok := ce.Fun.(*ast.SelectorExpr)
	if !ok {
		return nil
	}
	pkgIdent, ok := sel.X.(*ast.Ident)
	if !ok {
		return nil
	}
	obj := info.Uses[pkgIdent]
	pkgName, ok := obj.(*types.PkgName)
	if !ok || pkgName.Imported().Path() != "unsafe" {
		return nil
	}
	switch sel.Sel.Name {
	case "Add":
		diags = append(diags, Diagnostic{
			Pos:        fset.Position(ce.Pos()),
			Message:    "unsupported unsafe operation",
			Expression: exprString(fset, ce),
		})
	case "Slice", "String":
		// Accepted when the pointer is an element pointer; full proof is
		// deferred to lowering. For std check we only reject when the first
		// argument's type is clearly not *T (e.g. already caught elsewhere).
		// Sizeof/Alignof/Offsetof/SliceData/StringData are supported.
	}
	return diags
}

func checkConversion(fset *token.FileSet, info *types.Info, ce *ast.CallExpr) *Diagnostic {
	tv, ok := info.Types[ce.Fun]
	if !ok || !tv.IsType() {
		return nil
	}
	dst := tv.Type
	argTV, ok := info.Types[ce.Args[0]]
	if !ok {
		return nil
	}
	src := argTV.Type
	if convertsUintptrUnsafe(src, dst) || convertsUintptrUnsafe(dst, src) {
		// Only when the conversion is between uintptr and unsafe.Pointer
		// (either direction), including named types with those underlyings.
		if isUintptr(src) && isUnsafePointer(dst) || isUnsafePointer(src) && isUintptr(dst) {
			return &Diagnostic{
				Pos:        fset.Position(ce.Pos()),
				Message:    "unsupported unsafe operation",
				Expression: exprString(fset, ce),
			}
		}
	}
	// Also catch uintptr(unsafe.Pointer(...)) when Fun type is uintptr.
	if isUintptr(dst) && isUnsafePointer(src) {
		return &Diagnostic{
			Pos:        fset.Position(ce.Pos()),
			Message:    "unsupported unsafe operation",
			Expression: exprString(fset, ce),
		}
	}
	if isUnsafePointer(dst) && isUintptr(src) {
		return &Diagnostic{
			Pos:        fset.Position(ce.Pos()),
			Message:    "unsupported unsafe operation",
			Expression: exprString(fset, ce),
		}
	}
	return nil
}

func convertsUintptrUnsafe(a, b types.Type) bool {
	return (isUintptr(a) && isUnsafePointer(b)) || (isUnsafePointer(a) && isUintptr(b))
}

func checkBinary(fset *token.FileSet, info *types.Info, e *ast.BinaryExpr) []Diagnostic {
	switch e.Op {
	case token.LSS, token.GTR, token.LEQ, token.GEQ:
		if isPointer(info, e.X) && isPointer(info, e.Y) {
			// Ordering of pointers rejected unless both are element pointers
			// sharing a base — points-to is not implemented; reject ordering.
			return []Diagnostic{{
				Pos:        fset.Position(e.Pos()),
				Message:    "unsupported unsafe operation",
				Expression: exprString(fset, e),
			}}
		}
	case token.SUB:
		if isPointer(info, e.X) && isPointer(info, e.Y) {
			return []Diagnostic{{
				Pos:        fset.Position(e.Pos()),
				Message:    "unsupported unsafe operation",
				Expression: exprString(fset, e),
			}}
		}
	}
	return nil
}

func isPointer(info *types.Info, e ast.Expr) bool {
	tv, ok := info.Types[e]
	if !ok {
		return false
	}
	_, ok = tv.Type.Underlying().(*types.Pointer)
	return ok
}

func isUintptr(t types.Type) bool {
	b, ok := t.Underlying().(*types.Basic)
	return ok && b.Kind() == types.Uintptr
}

func isUnsafePointer(t types.Type) bool {
	b, ok := t.Underlying().(*types.Basic)
	return ok && b.Kind() == types.UnsafePointer
}

func exprString(fset *token.FileSet, e ast.Expr) string {
	return types.ExprString(e)
}

func dedupe(in []Diagnostic) []Diagnostic {
	seen := map[string]bool{}
	var out []Diagnostic
	for _, d := range in {
		key := d.Pos.String() + "|" + d.Expression
		if seen[key] {
			continue
		}
		seen[key] = true
		out = append(out, d)
	}
	return out
}
