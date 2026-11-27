package load

import (
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"go/types"
	"os"
	"path/filepath"
	"strings"
)

// Config controls Load.
type Config struct {
	// Dir is the working directory for package patterns. Empty means cwd.
	Dir string
	// Pattern is a package pattern (".", import path, or directory). Default ".".
	Pattern string
	// Tags are extra build tags; wasmgc is always included via DefaultFilter.
	Tags []string
	// GOROOT is the materialized wasmgc GOROOT used to load standard library
	// sources. When empty, standard-library imports fail unless the program
	// imports nothing from std (empty main).
	GOROOT string
	// GoVersion is passed to types.Config (default "go1.27").
	GoVersion string
	// Files, if non-empty, skips go list and type-checks these .go files as
	// package main in Dir (test helper / single-file mode).
	Files []string
	// Overlay, if set, replaces the source file list for the given import
	// paths (used by LoadTest to inject *_test.go and synthetic xtest packages).
	Overlay map[string][]string
}

// Package is one type-checked package in a Program.
type Package struct {
	PkgPath  string
	Name     string
	Dir      string
	GoFiles  []string
	Syntax   []*ast.File
	Types    *types.Package
	Info     *types.Info
	Standard bool
}

// Program is a whole-program load result.
type Program struct {
	Fset     *token.FileSet
	Packages []*Package // dependency-first order from go list -deps
	Main     *Package
	Sizes    types.Sizes
	// Mono holds monomorphization results after pipeline runs mono.Program.
	// Typed as any to avoid an import cycle (mono imports load); concrete
	// type is *mono.Table.
	Mono any
}

// Load resolves, re-filters, parses, and type-checks a package and its deps.
func Load(cfg Config) (*Program, error) {
	dir := cfg.Dir
	if dir == "" {
		var err error
		dir, err = os.Getwd()
		if err != nil {
			return nil, err
		}
	}
	dir, err := filepath.Abs(dir)
	if err != nil {
		return nil, err
	}
	pattern := cfg.Pattern
	if pattern == "" {
		pattern = "."
	}
	goVersion := cfg.GoVersion
	if goVersion == "" {
		goVersion = "go1.27"
	}
	filter := DefaultFilter()
	if len(cfg.Tags) > 0 {
		filter.BuildTags = append(append([]string{}, filter.BuildTags...), cfg.Tags...)
	}

	fset := token.NewFileSet()
	prog := &Program{Fset: fset, Sizes: &types.StdSizes{WordSize: 4, MaxAlign: 4}}

	if len(cfg.Files) > 0 {
		pkg, err := loadFiles(fset, dir, "main", "command-line-arguments", cfg.Files, filter, cfg.GOROOT, goVersion, prog)
		if err != nil {
			return nil, err
		}
		// loadFiles / importer already appended deps and pkg to prog.Packages.
		prog.Main = pkg
		return prog, nil
	}

	listed, err := goList(dir, pattern, filter.BuildTags)
	if err != nil {
		return nil, err
	}
	if len(listed) == 0 {
		return nil, fmt.Errorf("load: no packages matched %q", pattern)
	}

	// Index by import path; go list -deps is dependency-first.
	byPath := make(map[string]*listPackage, len(listed))
	for i := range listed {
		lp := &listed[i]
		byPath[lp.ImportPath] = lp
	}

	imp := &programImporter{
		goroot:    cfg.GOROOT,
		filter:    filter,
		fset:      fset,
		goVersion: goVersion,
		sizes:     prog.Sizes,
		byPath:    byPath,
		loaded:    map[string]*Package{},
		typesPkgs: map[string]*types.Package{},
		prog:      prog,
	}

	// Eagerly load only the roots (DepOnly=false). go list -deps also reports
	// link-time deps such as runtime for every package main, even when the
	// source imports nothing; those are loaded on demand if go/types sees an
	// import. Standard-library bodies always come from the materialized GOROOT.
	var mainLP *listPackage
	for i := range listed {
		lp := &listed[i]
		if lp.Error != nil && lp.Error.Err != "" && !lp.DepOnly {
			return nil, fmt.Errorf("load: %s: %s", lp.ImportPath, lp.Error.Err)
		}
		if lp.DepOnly {
			continue
		}
		mainLP = lp
		if _, err := imp.loadOne(lp); err != nil {
			return nil, err
		}
	}
	if mainLP == nil {
		return nil, fmt.Errorf("load: no root package in go list for %q", pattern)
	}
	mainPkg := imp.loaded[mainLP.ImportPath]
	if mainPkg == nil {
		return nil, fmt.Errorf("load: main package %q not loaded", mainLP.ImportPath)
	}
	if mainPkg.Name != "main" {
		return nil, fmt.Errorf("load: package %s is not package main (got %q)", mainPkg.PkgPath, mainPkg.Name)
	}
	prog.Main = mainPkg
	return prog, nil
}

func loadFiles(fset *token.FileSet, dir, name, pkgPath string, files []string, filter FilterConfig, goroot, goVersion string, prog *Program) (*Package, error) {
	abs := make([]string, len(files))
	for i, f := range files {
		if filepath.IsAbs(f) {
			abs[i] = f
		} else {
			abs[i] = filepath.Join(dir, f)
		}
	}
	imp := &programImporter{
		goroot:    goroot,
		filter:    filter,
		fset:      fset,
		goVersion: goVersion,
		sizes:     prog.Sizes,
		byPath:    map[string]*listPackage{},
		loaded:    map[string]*Package{},
		typesPkgs: map[string]*types.Package{},
		prog:      prog,
	}
	pkg := &Package{
		PkgPath: pkgPath,
		Name:    name,
		Dir:     dir,
		GoFiles: abs,
		Info: &types.Info{
			Types:      make(map[ast.Expr]types.TypeAndValue),
			Defs:       make(map[*ast.Ident]types.Object),
			Uses:       make(map[*ast.Ident]types.Object),
			Selections: make(map[*ast.SelectorExpr]*types.Selection),
			Implicits:  make(map[ast.Node]types.Object),
			Scopes:     make(map[ast.Node]*types.Scope),
			Instances:  make(map[*ast.Ident]types.Instance),
		},
	}
	var err error
	pkg.Syntax, err = parseFiles(fset, abs)
	if err != nil {
		return nil, err
	}
	if err := typeCheckPackage(pkg, imp, goVersion, prog.Sizes); err != nil {
		return nil, err
	}
	imp.loaded[pkgPath] = pkg
	prog.Packages = append(prog.Packages, pkg)
	return pkg, nil
}

func parseFiles(fset *token.FileSet, files []string) ([]*ast.File, error) {
	var out []*ast.File
	for _, f := range files {
		af, err := parser.ParseFile(fset, f, nil, parser.ParseComments)
		if err != nil {
			return nil, err
		}
		out = append(out, af)
	}
	return out, nil
}

func typeCheckPackage(pkg *Package, imp *programImporter, goVersion string, sizes types.Sizes) error {
	var typeErrs []error
	conf := types.Config{
		GoVersion: goVersion,
		Sizes:     sizes,
		Importer:  imp,
		Error: func(err error) {
			// WordSize 4 makes int 32-bit, so go/types rejects untyped constants
			// like 0xffffffff as println args (same as GOARCH=386). GOROOT tests
			// assume amd64-sized int for those println failure messages. Soft-ignore
			// so the arg stays untyped int; the lowerer prints it via int64.
			if isPrintUntypedIntOverflow(err) {
				return
			}
			typeErrs = append(typeErrs, err)
		},
	}
	tpkg, err := conf.Check(pkg.PkgPath, imp.fset, pkg.Syntax, pkg.Info)
	if err != nil && tpkg == nil {
		return err
	}
	pkg.Types = tpkg
	if len(typeErrs) > 0 {
		var b strings.Builder
		for i, e := range typeErrs {
			if i > 0 {
				b.WriteByte('\n')
			}
			b.WriteString(e.Error())
		}
		return fmt.Errorf("load: typecheck %s:\n%s", pkg.PkgPath, b.String())
	}
	return nil
}

// isPrintUntypedIntOverflow reports go/types errors for untyped int constants
// that overflow 32-bit int when passed to built-in print/println.
func isPrintUntypedIntOverflow(err error) bool {
	msg := err.Error()
	if !strings.Contains(msg, "(overflows)") {
		return false
	}
	if !strings.Contains(msg, "untyped int constant") {
		return false
	}
	return strings.Contains(msg, "argument to built-in println") ||
		strings.Contains(msg, "argument to built-in print")
}

// programImporter resolves imports from the go list inventory and materialized GOROOT.
type programImporter struct {
	goroot    string
	filter    FilterConfig
	fset      *token.FileSet
	goVersion string
	sizes     types.Sizes
	byPath    map[string]*listPackage
	loaded    map[string]*Package
	typesPkgs map[string]*types.Package
	stack     map[string]bool
	prog      *Program
	overlay   map[string][]string
}

func (imp *programImporter) Import(path string) (*types.Package, error) {
	return imp.ImportFrom(path, "", 0)
}

func (imp *programImporter) ImportFrom(path, dir string, mode types.ImportMode) (*types.Package, error) {
	_ = dir
	_ = mode
	if path == "unsafe" {
		return types.Unsafe, nil
	}
	if pkg, ok := imp.typesPkgs[path]; ok && pkg.Complete() {
		return pkg, nil
	}
	if imp.stack[path] {
		if pkg := imp.typesPkgs[path]; pkg != nil {
			return pkg, nil
		}
		return nil, fmt.Errorf("import cycle: %s", path)
	}
	if imp.stack == nil {
		imp.stack = map[string]bool{}
	}
	imp.stack[path] = true
	defer delete(imp.stack, path)

	lp := imp.byPath[path]
	pkg, err := imp.loadOne(lpOrStd(lp, path, imp.goroot))
	if err != nil {
		return nil, err
	}
	return pkg.Types, nil
}

func lpOrStd(lp *listPackage, path, goroot string) *listPackage {
	if lp != nil {
		return lp
	}
	// Synthetic std entry when go list did not mention it (shouldn't happen for -deps)
	// or when loading Files mode that imports std.
	return &listPackage{
		ImportPath: path,
		Dir:        filepath.Join(goroot, "src", filepath.FromSlash(path)),
		Goroot:     true,
		Standard:   true,
	}
}

func (imp *programImporter) loadOne(lp *listPackage) (*Package, error) {
	if lp == nil {
		return nil, fmt.Errorf("load: nil package")
	}
	if pkg, ok := imp.loaded[lp.ImportPath]; ok {
		return pkg, nil
	}

	// Trust go list's Goroot/Standard flags. Do not use the "no dot in the
	// first path element" heuristic here: a main module path without a dot
	// (e.g. module hello) is not a standard package, and cmd/go would not
	// treat it as one. isStdImportPath remains for synthetic std entries
	// when go list did not return a package (lpOrStd).
	standard := lp.Goroot || lp.Standard
	dir := lp.Dir
	var goFiles []string
	var err error

	if files, ok := imp.overlay[lp.ImportPath]; ok && len(files) > 0 {
		goFiles = append([]string{}, files...)
		if dir == "" {
			dir = filepath.Dir(files[0])
		}
		// Overlay packages under test are treated as complete replacements;
		// keep Standard true when the path looks like std so init order matches.
		if isStdImportPath(lp.ImportPath) && !strings.HasSuffix(lp.ImportPath, "_test") {
			standard = true
		}
	} else if standard {
		if imp.goroot == "" {
			return nil, fmt.Errorf("load: standard package %q required but GOROOT is unset (materialize via wasmgc-go std materialize)", lp.ImportPath)
		}
		dir = filepath.Join(imp.goroot, "src", filepath.FromSlash(lp.ImportPath))
		goFiles, err = imp.filter.ListGoFiles(dir)
		if err != nil {
			if os.IsNotExist(err) {
				return nil, fmt.Errorf("load: package %q not found in GOROOT %s", lp.ImportPath, imp.goroot)
			}
			return nil, err
		}
		if len(goFiles) == 0 {
			return nil, fmt.Errorf("load: package %q has no Go files in materialized GOROOT (tier out?)", lp.ImportPath)
		}
	} else {
		selected, badS, err := refilterList(lp, imp.filter)
		if err != nil {
			return nil, err
		}
		if len(badS) > 0 {
			return nil, fmt.Errorf("load: assembly not supported: %s (package %s)", strings.Join(badS, ", "), lp.ImportPath)
		}
		if len(lp.CgoFiles) > 0 {
			// Cgo selected by go list under js/wasm — reject if any remain after our filter.
			for _, c := range lp.CgoFiles {
				base := filepath.Base(c)
				ok, err := imp.filter.MatchGoFile(lp.Dir, base)
				if err != nil {
					return nil, err
				}
				if ok {
					return nil, fmt.Errorf("load: cgo not supported: %s", c)
				}
			}
		}
		goFiles = selected
		if len(goFiles) == 0 {
			return nil, fmt.Errorf("load: package %q has no Go files after wasmgc re-filter", lp.ImportPath)
		}
		dir = lp.Dir
	}

	pkg := &Package{
		PkgPath:  lp.ImportPath,
		Name:     lp.Name,
		Dir:      dir,
		GoFiles:  goFiles,
		Standard: standard,
		Info: &types.Info{
			Types:      make(map[ast.Expr]types.TypeAndValue),
			Defs:       make(map[*ast.Ident]types.Object),
			Uses:       make(map[*ast.Ident]types.Object),
			Selections: make(map[*ast.SelectorExpr]*types.Selection),
			Implicits:  make(map[ast.Node]types.Object),
			Scopes:     make(map[ast.Node]*types.Scope),
			Instances:  make(map[*ast.Ident]types.Instance),
		},
	}
	pkg.Syntax, err = parseFiles(imp.fset, goFiles)
	if err != nil {
		return nil, err
	}
	if pkg.Name == "" && len(pkg.Syntax) > 0 {
		pkg.Name = pkg.Syntax[0].Name.Name
	}

	// Reserve incomplete package for cycles.
	tplaceholder := types.NewPackage(pkg.PkgPath, pkg.Name)
	imp.typesPkgs[pkg.PkgPath] = tplaceholder
	imp.loaded[pkg.PkgPath] = pkg

	if err := typeCheckPackage(pkg, imp, imp.goVersion, imp.sizes); err != nil {
		return nil, err
	}
	imp.typesPkgs[pkg.PkgPath] = pkg.Types
	imp.prog.Packages = append(imp.prog.Packages, pkg)
	return pkg, nil
}

func refilterList(lp *listPackage, filter FilterConfig) (selected []string, badS []string, err error) {
	seen := map[string]bool{}
	add := func(names []string) error {
		for _, n := range names {
			base := filepath.Base(n)
			if seen[base] {
				continue
			}
			seen[base] = true
			ok, err := filter.MatchGoFile(lp.Dir, base)
			if err != nil {
				return err
			}
			if !ok {
				continue
			}
			path := n
			if !filepath.IsAbs(path) {
				path = filepath.Join(lp.Dir, base)
			}
			selected = append(selected, path)
		}
		return nil
	}
	if err := add(lp.GoFiles); err != nil {
		return nil, nil, err
	}
	if err := add(lp.IgnoredGoFiles); err != nil {
		return nil, nil, err
	}
	for _, n := range lp.SFiles {
		base := filepath.Base(n)
		// Assembly is never accepted for wasmgc; if the OS/Arch suffix matches
		// the target, report it so Load can error. Other-arch files are ignored.
		if goodOSArchSuffix(filter.GOOS, filter.GOARCH, base) {
			path := n
			if !filepath.IsAbs(path) {
				path = filepath.Join(lp.Dir, base)
			}
			badS = append(badS, path)
		}
	}
	return selected, badS, nil
}

func goodOSArchSuffix(goos, goarch, name string) bool {
	// Strip extension.
	base := name
	if i := strings.LastIndex(name, "."); i >= 0 {
		base = name[:i]
	}
	parts := strings.Split(base, "_")
	if len(parts) < 2 {
		return true // name.s with no suffix — would be selected; reject
	}
	n := parts[len(parts)-1]
	if knownArch(n) {
		if n != goarch {
			return false
		}
		if len(parts) >= 3 {
			osName := parts[len(parts)-2]
			if knownOS(osName) && osName != goos {
				return false
			}
		}
		return true
	}
	if knownOS(n) {
		return n == goos
	}
	return true
}

func knownOS(name string) bool {
	switch name {
	case "aix", "android", "darwin", "dragonfly", "freebsd", "hurd", "illumos",
		"ios", "js", "linux", "nacl", "netbsd", "openbsd", "plan9", "solaris",
		"wasip1", "windows", "zos":
		return true
	}
	return false
}

func knownArch(name string) bool {
	switch name {
	case "386", "amd64", "amd64p32", "arm", "armbe", "arm64", "arm64be",
		"loong64", "mips", "mipsle", "mips64", "mips64le", "mips64p32",
		"mips64p32le", "ppc", "ppc64", "ppc64le", "riscv", "riscv64",
		"s390", "s390x", "sparc", "sparc64", "wasm", "wasmgc":
		return true
	}
	return false
}

func isStdImportPath(path string) bool {
	if path == "C" {
		return true
	}
	i := strings.Index(path, "/")
	first := path
	if i >= 0 {
		first = path[:i]
	}
	// Module paths have a dot in the first element; std does not.
	return !strings.Contains(first, ".")
}
