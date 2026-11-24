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
	return TypeCheckFiles([]string{filename}, []string{src})
}

// TypeCheckFiles parses and type-checks a multi-file package (same package
// name across files). Used by rtlink when linking several runtime sources.
// The types.Package path is the Go package clause name (e.g. "utf8").
func TypeCheckFiles(filenames []string, srcs []string) (*token.FileSet, []*ast.File, *types.Package, *types.Info, error) {
	return TypeCheckPath("", filenames, srcs)
}

// TypeCheckPath is TypeCheckFiles with an explicit import path (e.g.
// "unicode/utf8") so IR names use the sanitized path prefix.
func TypeCheckPath(path string, filenames []string, srcs []string) (*token.FileSet, []*ast.File, *types.Package, *types.Info, error) {
	if len(filenames) != len(srcs) {
		return nil, nil, nil, nil, fmt.Errorf("typecheck: %d filenames vs %d srcs", len(filenames), len(srcs))
	}
	if len(filenames) == 0 {
		return nil, nil, nil, nil, fmt.Errorf("typecheck: no files")
	}
	fset := token.NewFileSet()
	var files []*ast.File
	pkgName := ""
	for i, src := range srcs {
		f, err := parser.ParseFile(fset, filenames[i], src, parser.ParseComments)
		if err != nil {
			return nil, nil, nil, nil, err
		}
		if pkgName == "" {
			pkgName = f.Name.Name
		} else if f.Name.Name != pkgName {
			return nil, nil, nil, nil, fmt.Errorf("typecheck: package name mismatch %q vs %q", pkgName, f.Name.Name)
		}
		files = append(files, f)
	}
	info := &types.Info{
		Types:      make(map[ast.Expr]types.TypeAndValue),
		Defs:       make(map[*ast.Ident]types.Object),
		Uses:       make(map[*ast.Ident]types.Object),
		Implicits:  make(map[ast.Node]types.Object),
		Selections: make(map[*ast.SelectorExpr]*types.Selection),
		Scopes:     make(map[ast.Node]*types.Scope),
	}
	conf := types.Config{
		Sizes:    Sizes,
		Importer: unsafeOnlyImporter{},
	}
	checkPath := path
	if checkPath == "" {
		checkPath = pkgName
	}
	pkg, err := conf.Check(checkPath, fset, files, info)
	if err != nil {
		return nil, nil, nil, nil, fmt.Errorf("typecheck: %w", err)
	}
	return fset, files, pkg, info, nil
}

// unsafeOnlyImporter resolves the built-in "unsafe" package for rtlink
// typechecks of rewrite-tier runtime fragments that import unsafe.
type unsafeOnlyImporter struct{}

func (unsafeOnlyImporter) Import(path string) (*types.Package, error) {
	if path == "unsafe" {
		return types.Unsafe, nil
	}
	return nil, fmt.Errorf("import %q not available in TypeCheckFiles", path)
}
