package check

import (
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"go/types"
	"os"
	"path/filepath"
	"strings"

	"github.com/achille-roussel/wasmgc-go/internal/load"
)

// StdSizes is the 32-bit size fiction: WordSize 4, MaxAlign 4.
var StdSizes = &types.StdSizes{WordSize: 4, MaxAlign: 4}

// Result is the outcome of checking one package.
type Result struct {
	ImportPath string
	Files      []string
	TypeErrors []error
	Unsafe     []Diagnostic
	OK         bool
}

// Config for checking packages from a materialized GOROOT.
type Config struct {
	GOROOT string
	Filter load.FilterConfig
	// GoVersion passed to types.Config, e.g. "go1.27".
	GoVersion string
}

// CheckPackage type-checks importPath from GOROOT/src and runs unsafe checks.
func (c *Config) CheckPackage(importPath string) Result {
	res := Result{ImportPath: importPath}
	dir := filepath.Join(c.GOROOT, "src", filepath.FromSlash(importPath))
	filter := c.Filter
	if filter.GOOS == "" {
		filter = load.DefaultFilter()
	}
	files, err := filter.ListGoFiles(dir)
	if err != nil {
		res.TypeErrors = []error{err}
		return res
	}
	if len(files) == 0 {
		res.TypeErrors = []error{fmt.Errorf("no Go files in %s", dir)}
		return res
	}
	res.Files = files

	fset := token.NewFileSet()
	var astFiles []*ast.File
	for _, f := range files {
		af, err := parser.ParseFile(fset, f, nil, parser.ParseComments)
		if err != nil {
			res.TypeErrors = append(res.TypeErrors, err)
			continue
		}
		astFiles = append(astFiles, af)
	}
	if len(res.TypeErrors) > 0 || len(astFiles) == 0 {
		return res
	}

	imp := &sourceImporter{
		goroot:    c.GOROOT,
		filter:    filter,
		fset:      fset,
		packages:  map[string]*types.Package{},
		goVersion: c.goVersion(),
	}
	conf := types.Config{
		GoVersion: c.goVersion(),
		Sizes:     StdSizes,
		Importer:  imp,
		Error: func(err error) {
			res.TypeErrors = append(res.TypeErrors, err)
		},
	}
	info := &types.Info{
		Types:      make(map[ast.Expr]types.TypeAndValue),
		Defs:       make(map[*ast.Ident]types.Object),
		Uses:       make(map[*ast.Ident]types.Object),
		Selections: make(map[*ast.SelectorExpr]*types.Selection),
	}
	func() {
		defer func() {
			if r := recover(); r != nil {
				res.TypeErrors = append(res.TypeErrors, fmt.Errorf("go/types panic: %v", r))
			}
		}()
		_, _ = conf.Check(importPath, fset, astFiles, info)
	}()
	if len(res.TypeErrors) == 0 {
		res.Unsafe = CheckUnsafe(fset, info, astFiles)
	}
	res.OK = len(res.TypeErrors) == 0 && len(res.Unsafe) == 0
	return res
}

func (c *Config) goVersion() string {
	if c.GoVersion != "" {
		return c.GoVersion
	}
	return "go1.27"
}

// sourceImporter loads packages from GOROOT/src with the wasmgc filter.
type sourceImporter struct {
	goroot    string
	filter    load.FilterConfig
	fset      *token.FileSet
	packages  map[string]*types.Package
	goVersion string
	stack     map[string]bool
}

func (imp *sourceImporter) Import(path string) (*types.Package, error) {
	return imp.ImportFrom(path, "", 0)
}

func (imp *sourceImporter) ImportFrom(path, dir string, mode types.ImportMode) (*types.Package, error) {
	if path == "unsafe" {
		return types.Unsafe, nil
	}
	if pkg, ok := imp.packages[path]; ok && pkg.Complete() {
		return pkg, nil
	}
	if imp.stack[path] {
		// Incomplete package for import cycles (shouldn't happen in std).
		if pkg := imp.packages[path]; pkg != nil {
			return pkg, nil
		}
		return nil, fmt.Errorf("import cycle: %s", path)
	}
	if imp.stack == nil {
		imp.stack = map[string]bool{}
	}
	imp.stack[path] = true
	defer delete(imp.stack, path)

	pkgDir := filepath.Join(imp.goroot, "src", filepath.FromSlash(path))
	files, err := imp.filter.ListGoFiles(pkgDir)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, fmt.Errorf("package %q not found in materialized GOROOT", path)
		}
		return nil, err
	}
	if len(files) == 0 {
		// Empty / out package.
		return nil, fmt.Errorf("package %q has no Go files (tier out or missing rewrite)", path)
	}
	var astFiles []*ast.File
	for _, f := range files {
		af, err := parser.ParseFile(imp.fset, f, nil, 0)
		if err != nil {
			return nil, err
		}
		astFiles = append(astFiles, af)
	}
	conf := types.Config{
		GoVersion: imp.goVersion,
		Sizes:     StdSizes,
		Importer:  imp,
		FakeImportC: true,
	}
	info := &types.Info{}
	pkg, err := conf.Check(path, imp.fset, astFiles, info)
	if err != nil {
		// Still cache incomplete package to break cycles / soft failures.
		if pkg != nil {
			imp.packages[path] = pkg
		}
		return pkg, err
	}
	imp.packages[path] = pkg
	return pkg, nil
}

// PackageExists reports whether the materialized tree has any filtered Go files.
func PackageExists(goroot, importPath string) bool {
	dir := filepath.Join(goroot, "src", filepath.FromSlash(importPath))
	files, err := load.DefaultFilter().ListGoFiles(dir)
	return err == nil && len(files) > 0
}

// ListStdPackages walks GOROOT/src for package import paths (excluding cmd).
func ListStdPackages(goroot string) ([]string, error) {
	src := filepath.Join(goroot, "src")
	var out []string
	err := filepath.Walk(src, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}
		if !info.IsDir() {
			return nil
		}
		rel, _ := filepath.Rel(src, path)
		if rel == "cmd" || strings.HasPrefix(rel, "cmd"+string(os.PathSeparator)) {
			return filepath.SkipDir
		}
		if info.Name() == "testdata" {
			return filepath.SkipDir
		}
		if rel == "." {
			return nil
		}
		entries, err := os.ReadDir(path)
		if err != nil {
			return err
		}
		for _, e := range entries {
			if !e.IsDir() && strings.HasSuffix(e.Name(), ".go") && !strings.HasSuffix(e.Name(), "_test.go") {
				out = append(out, filepath.ToSlash(rel))
				break
			}
		}
		return nil
	})
	return out, err
}
