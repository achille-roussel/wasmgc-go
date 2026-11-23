package check

import (
	"go/ast"
	"go/token"
	"go/types"
)

// CheckIntConvert reports vet-style warnings for conversions from int64/uint64
// to int/uint that the checker cannot bound. See docs/design/00-overview.md.
func CheckIntConvert(fset *token.FileSet, info *types.Info, files []*ast.File) []Diagnostic {
	var diags []Diagnostic
	for _, f := range files {
		ast.Inspect(f, func(n ast.Node) bool {
			ce, ok := n.(*ast.CallExpr)
			if !ok || len(ce.Args) != 1 {
				return true
			}
			tv, ok := info.Types[ce.Fun]
			if !ok || !tv.IsType() {
				return true
			}
			dst := tv.Type.Underlying()
			argTV, ok := info.Types[ce.Args[0]]
			if !ok {
				return true
			}
			src := argTV.Type.Underlying()
			if !isIntOrUint(dst) || !isInt64OrUint64(src) {
				return true
			}
			// Constant sources that fit are already rejected or accepted by go/types;
			// a constant that typed-checks needs no warning.
			if argTV.Value != nil {
				return true
			}
			dstName := basicName(dst)
			srcName := basicName(src)
			diags = append(diags, Diagnostic{
				Pos:        fset.Position(ce.Pos()),
				Severity:   SeverityWarning,
				Kind:       KindIntConvert,
				Message:    "conversion from " + srcName + " to " + dstName + " may overflow; " + dstName + " is 32 bits",
				Expression: exprString(fset, ce),
			})
			return true
		})
	}
	return diags
}

func isIntOrUint(t types.Type) bool {
	b, ok := t.(*types.Basic)
	if !ok {
		return false
	}
	return b.Kind() == types.Int || b.Kind() == types.Uint
}

func isInt64OrUint64(t types.Type) bool {
	b, ok := t.(*types.Basic)
	if !ok {
		return false
	}
	return b.Kind() == types.Int64 || b.Kind() == types.Uint64
}

func basicName(t types.Type) string {
	b, ok := t.(*types.Basic)
	if !ok {
		return t.String()
	}
	return b.Name()
}
