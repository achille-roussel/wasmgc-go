package lower

import (
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"go/types"
)

// Sizes is the go/types size table for wasmgc (docs/design/03, 10).
var Sizes = &types.StdSizes{WordSize: 4, MaxAlign: 4}

// TypeCheck parses and type-checks src as a single-file package.
func TypeCheck(filename, src string) (*token.FileSet, []*ast.File, *types.Package, *types.Info, error) {
	fset := token.NewFileSet()
	f, err := parser.ParseFile(fset, filename, src, parser.ParseComments)
	if err != nil {
		return nil, nil, nil, nil, err
	}
	info := &types.Info{
		Types:      make(map[ast.Expr]types.TypeAndValue),
		Defs:       make(map[*ast.Ident]types.Object),
		Uses:       make(map[*ast.Ident]types.Object),
		Implicits:  make(map[ast.Node]types.Object),
		Selections: make(map[*ast.SelectorExpr]*types.Selection),
		Scopes:     make(map[ast.Node]*types.Scope),
	}
	conf := types.Config{Sizes: Sizes}
	pkg, err := conf.Check(f.Name.Name, fset, []*ast.File{f}, info)
	if err != nil {
		return nil, nil, nil, nil, fmt.Errorf("typecheck: %w", err)
	}
	return fset, []*ast.File{f}, pkg, info, nil
}
