package load

import (
	"fmt"
	"go/ast"
	"go/token"
	"go/types"
	"os"
	"path/filepath"
	"strings"

	"github.com/achille-roussel/wasmgc-go/internal/testmain"
)

// TestConfig controls LoadTest.
type TestConfig struct {
	// ImportPath is the stdlib package under test (e.g. "errors").
	ImportPath string
	// GOROOT is the materialized wasmgc GOROOT (production sources).
	GOROOT string
	// UpstreamSrc is the pin's src directory (contains *_test.go).
	UpstreamSrc string
	// Skip maps TestName → reason; those tests are omitted and stripped.
	Skip map[string]string
	// Tags are extra build tags.
	Tags []string
	GoVersion string
	// WorkDir holds generated/stripped files; created if empty.
	WorkDir string
	// ModuleRoot is the wasmgc-go module (for std/overlay test file overrides).
	ModuleRoot string
}

// TestProgram extends Program with suite metadata.
type TestProgram struct {
	*Program
	Scan    *testmain.ScanResult
	WorkDir string
	MainSrc string
}

// LoadTest builds a package-main program that runs the package's tests.
// Production sources come from GOROOT; *_test.go come from UpstreamSrc.
func LoadTest(cfg TestConfig) (*TestProgram, error) {
	if cfg.ImportPath == "" {
		return nil, fmt.Errorf("load: LoadTest: empty ImportPath")
	}
	if cfg.GOROOT == "" {
		return nil, fmt.Errorf("load: LoadTest: GOROOT unset")
	}
	if cfg.UpstreamSrc == "" {
		return nil, fmt.Errorf("load: LoadTest: UpstreamSrc unset")
	}
	goVersion := cfg.GoVersion
	if goVersion == "" {
		goVersion = "go1.27"
	}
	filter := DefaultFilter()
	if len(cfg.Tags) > 0 {
		filter.BuildTags = append(append([]string{}, filter.BuildTags...), cfg.Tags...)
	}

	work := cfg.WorkDir
	if work == "" {
		var err error
		work, err = os.MkdirTemp("", "wasmgc-loadtest-*")
		if err != nil {
			return nil, err
		}
	} else if err := os.MkdirAll(work, 0o755); err != nil {
		return nil, err
	}

	prodDir := filepath.Join(cfg.GOROOT, "src", filepath.FromSlash(cfg.ImportPath))
	prodFiles, err := filter.ListGoFiles(prodDir)
	if err != nil {
		return nil, fmt.Errorf("load: LoadTest: production %s: %w", cfg.ImportPath, err)
	}
	upDir := filepath.Join(cfg.UpstreamSrc, filepath.FromSlash(cfg.ImportPath))
	testFiles, err := filter.ListTestGoFiles(upDir)
	if err != nil {
		return nil, fmt.Errorf("load: LoadTest: tests %s: %w", cfg.ImportPath, err)
	}
	if len(testFiles) == 0 {
		return nil, fmt.Errorf("load: LoadTest: no *_test.go for %s in %s", cfg.ImportPath, upDir)
	}
	if cfg.ModuleRoot != "" {
		ovlDir := filepath.Join(cfg.ModuleRoot, "std", "overlay", filepath.FromSlash(cfg.ImportPath))
		for i, tf := range testFiles {
			base := filepath.Base(tf)
			ovl := filepath.Join(ovlDir, base)
			if _, err := os.Stat(ovl); err == nil {
				testFiles[i] = ovl
			}
		}
	}

	scan, err := testmain.Scan(cfg.ImportPath, testFiles, cfg.Skip)
	if err != nil {
		return nil, err
	}
	// Thin rewrite packages (e.g. time) ship far fewer production .go files than
	// upstream. Their export_test.go references unexported symbols we do not
	// provide; keeping it blocks every external test. Drop it so public-API
	// tests can compile; tests that need export helpers fail typecheck and stay
	// skipped.
	if thinRewritePackage(prodDir, upDir, filter) {
		scan.Internal = dropExportTestFiles(scan.Internal)
	}

	// Materialize stripped test sources into work dir.
	var internalPaths, externalPaths []string
	for i, fi := range scan.Internal {
		dest := filepath.Join(work, "internal", testmain.DestBase(fi.Path, i))
		if err := testmain.WriteStripped(fi, dest); err != nil {
			return nil, err
		}
		internalPaths = append(internalPaths, dest)
	}
	for i, fi := range scan.External {
		dest := filepath.Join(work, "external", testmain.DestBase(fi.Path, i))
		if err := testmain.WriteStripped(fi, dest); err != nil {
			return nil, err
		}
		externalPaths = append(externalPaths, dest)
	}

	overlay := map[string][]string{
		cfg.ImportPath: append(append([]string{}, prodFiles...), internalPaths...),
	}
	xtestPath := cfg.ImportPath + "_test"
	if len(externalPaths) > 0 {
		overlay[xtestPath] = externalPaths
	}

	mainSrc, err := testmain.GenerateMain(scan)
	if err != nil {
		return nil, err
	}
	mainPath := filepath.Join(work, "main.go")
	if err := os.WriteFile(mainPath, []byte(mainSrc), 0o644); err != nil {
		return nil, err
	}

	fset := token.NewFileSet()
	prog := &Program{Fset: fset, Sizes: &types.StdSizes{WordSize: 4, MaxAlign: 4}}
	imp := &programImporter{
		goroot:    cfg.GOROOT,
		filter:    filter,
		fset:      fset,
		goVersion: goVersion,
		sizes:     prog.Sizes,
		byPath:    map[string]*listPackage{},
		loaded:    map[string]*Package{},
		typesPkgs: map[string]*types.Package{},
		prog:      prog,
		overlay:   overlay,
	}

	// Prefetch the package under test (and xtest) so imports resolve to overlays.
	if _, err := imp.loadOne(lpOrStd(nil, cfg.ImportPath, cfg.GOROOT)); err != nil {
		return nil, err
	}
	if len(externalPaths) > 0 {
		lp := &listPackage{
			ImportPath: xtestPath,
			Dir:        filepath.Join(work, "external"),
			Name:       scan.PkgName + "_test",
			Goroot:     false,
			Standard:   false,
		}
		if _, err := imp.loadOne(lp); err != nil {
			return nil, err
		}
	}

	mainPkg := &Package{
		PkgPath: "command-line-arguments",
		Name:    "main",
		Dir:     work,
		GoFiles: []string{mainPath},
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
	mainPkg.Syntax, err = parseFiles(fset, mainPkg.GoFiles)
	if err != nil {
		return nil, err
	}
	if err := typeCheckPackage(mainPkg, imp, goVersion, prog.Sizes); err != nil {
		return nil, err
	}
	imp.loaded[mainPkg.PkgPath] = mainPkg
	prog.Packages = append(prog.Packages, mainPkg)
	prog.Main = mainPkg

	return &TestProgram{
		Program: prog,
		Scan:    scan,
		WorkDir: work,
		MainSrc: mainSrc,
	}, nil
}

// thinRewritePackage reports whether the materialized production package is a
// thin rewrite of upstream (far fewer .go files), so export_test cannot work.
func thinRewritePackage(prodDir, upDir string, filter FilterConfig) bool {
	prod, err1 := filter.ListGoFiles(prodDir)
	up, err2 := filter.ListGoFiles(upDir)
	if err1 != nil || err2 != nil || len(up) == 0 {
		return false
	}
	// Rewrite packages typically keep a handful of files; upstream time has ~20+.
	return len(prod)*2 < len(up)
}

func dropExportTestFiles(files []testmain.FileInfo) []testmain.FileInfo {
	var kept []testmain.FileInfo
	for _, fi := range files {
		base := filepath.Base(fi.Path)
		if base == "export_test.go" || strings.HasPrefix(base, "export_") {
			continue
		}
		kept = append(kept, fi)
	}
	return kept
}

// ListTestGoFiles returns *_test.go files in dir that pass the wasmgc filter.
func (c FilterConfig) ListTestGoFiles(dir string) ([]string, error) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil, err
	}
	var out []string
	for _, e := range entries {
		if e.IsDir() {
			continue
		}
		name := e.Name()
		if !strings.HasSuffix(name, "_test.go") {
			continue
		}
		ok, err := c.MatchGoFile(dir, name)
		if err != nil {
			return nil, err
		}
		if ok {
			out = append(out, filepath.Join(dir, name))
		}
	}
	return out, nil
}
