// Package testmain scans package tests and generates a synthetic main for
// wasmgc-go test (M10).
package testmain

import (
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"path"
	"path/filepath"
	"strings"
	"unicode"
	"unicode/utf8"
)

// TestFunc is one TestXxx (or TestMain) discovered in a test file.
type TestFunc struct {
	Name    string // TestFoo or TestMain
	PkgName string // package clause name (errors or errors_test)
	File    string // absolute path
	External bool  // package P_test
}

// FileInfo is a parsed test file after skip stripping.
type FileInfo struct {
	Path     string
	PkgName  string
	External bool
	Syntax   *ast.File
	Fset     *token.FileSet
}

// ScanResult holds discovered tests and processed files.
type ScanResult struct {
	ImportPath string
	PkgName    string // base package name (errors, utf8, …)
	Internal   []FileInfo
	External   []FileInfo
	Tests      []TestFunc // InternalTest candidates (not TestMain)
	XTests     []TestFunc
	TestMain   *TestFunc
	Skipped    map[string]string // name → reason (from skip list)
}

// Scan parses test files, applies skip lists (omit from tables + strip funcs),
// and strips Benchmark/Example/Fuzz decls so they need not compile in v1.
func Scan(importPath string, testFiles []string, skip map[string]string) (*ScanResult, error) {
	base := path.Base(importPath)
	res := &ScanResult{
		ImportPath: importPath,
		PkgName:    base,
		Skipped:    skip,
	}
	if skip == nil {
		res.Skipped = map[string]string{}
	}
	fset := token.NewFileSet()
	for _, f := range testFiles {
		base := filepath.Base(f)
		// example*_test.go is examples-only; strip entirely for v1.
		if strings.HasPrefix(base, "example") && strings.HasSuffix(base, "_test.go") {
			continue
		}
		af, err := parser.ParseFile(fset, f, nil, parser.ParseComments)
		if err != nil {
			return nil, fmt.Errorf("testmain: parse %s: %w", f, err)
		}
		pkgName := af.Name.Name
		ext := strings.HasSuffix(pkgName, "_test")
		// Strip benchmarks/examples/fuzz and skipped tests before typecheck.
		stripFuncs(af, func(name string) bool {
			if isBenchmarkOrExampleOrFuzz(name) {
				return true
			}
			if _, ok := res.Skipped[name]; ok {
				return true
			}
			return false
		})
		pruneUnusedImports(af)
		baseName := filepath.Base(f)
		keepExport := !ext && (baseName == "export_test.go" || strings.HasPrefix(baseName, "export_"))
		keepImportHelper := ext && (baseName == "import_test.go" || strings.HasPrefix(baseName, "import_"))
		// Drop emptied example/bench files, but keep export_test / import_test.
		if !fileHasFuncs(af) && !keepExport && !keepImportHelper {
			continue
		}
		// Drop package-level var/init baggage that remaining tests do not
		// reference. Large composite literals (fmtTests, etc.) often fail
		// wasm codegen (array.new_fixed) when left in init for skipped-only
		// tables. used-analysis keeps vars still referenced by live tests.
		if countTestFuncs(af) >= 1 {
			stripVarAndInit(af)
			pruneUnusedImports(af)
		}
		// Collect remaining tests in this file first; helpers-only leftovers from
		// skipped tests are dropped so they cannot break lowering/codegen.
		var fileTests []TestFunc
		var fileMain *TestFunc
		for _, d := range af.Decls {
			fd, ok := d.(*ast.FuncDecl)
			if !ok || fd.Recv != nil || fd.Name == nil {
				continue
			}
			name := fd.Name.Name
			if name == "TestMain" {
				tm := TestFunc{Name: name, PkgName: pkgName, File: f, External: ext}
				fileMain = &tm
				continue
			}
			if !isTestFunc(name) {
				continue
			}
			if _, skipped := res.Skipped[name]; skipped {
				continue
			}
			fileTests = append(fileTests, TestFunc{Name: name, PkgName: pkgName, File: f, External: ext})
		}
		if fileMain == nil && len(fileTests) == 0 && !keepExport && !keepImportHelper {
			continue
		}
		fi := FileInfo{
			Path:     f,
			PkgName:  pkgName,
			External: ext,
			Syntax:   af,
			Fset:     fset,
		}
		if ext {
			res.External = append(res.External, fi)
		} else {
			res.Internal = append(res.Internal, fi)
		}
		if fileMain != nil {
			res.TestMain = fileMain
		}
		for _, tf := range fileTests {
			if tf.External {
				res.XTests = append(res.XTests, tf)
			} else {
				res.Tests = append(res.Tests, tf)
			}
		}
	}
	// Drop export_test.go when no external tests remain (avoids rewrite packages
	// whose upstream export_test references symbols we do not provide).
	if len(res.XTests) == 0 {
		var kept []FileInfo
		for _, fi := range res.Internal {
			base := filepath.Base(fi.Path)
			if base == "export_test.go" || strings.HasPrefix(base, "export_") {
				continue
			}
			kept = append(kept, fi)
		}
		res.Internal = kept
	}
	// Drop helper-only external files when no XTests remain.
	if len(res.XTests) == 0 {
		var kept []FileInfo
		for _, fi := range res.External {
			if fileHasTestOrMain(fi.Syntax) {
				kept = append(kept, fi)
			}
			// else drop helper-only
		}
		res.External = kept
	}
	return res, nil
}

func fileHasTestOrMain(f *ast.File) bool {
	for _, d := range f.Decls {
		fd, ok := d.(*ast.FuncDecl)
		if !ok || fd.Recv != nil || fd.Name == nil {
			continue
		}
		if fd.Name.Name == "TestMain" || isTestFunc(fd.Name.Name) {
			return true
		}
	}
	return false
}

func isTestFunc(name string) bool {
	if !strings.HasPrefix(name, "Test") {
		return false
	}
	if name == "Test" {
		return false
	}
	r, _ := utf8.DecodeRuneInString(name[len("Test"):])
	return unicode.IsUpper(r)
}

func isBenchmarkOrExampleOrFuzz(name string) bool {
	for _, p := range []string{"Benchmark", "Example", "Fuzz"} {
		if strings.HasPrefix(name, p) {
			if name == p {
				return true
			}
			rest := name[len(p):]
			r, _ := utf8.DecodeRuneInString(rest)
			if unicode.IsUpper(r) {
				return true
			}
			// Example_xxx / ExampleXxx variants.
			if p == "Example" {
				return true
			}
		}
	}
	return false
}

func stripFuncs(f *ast.File, drop func(name string) bool) {
	var keep []ast.Decl
	for _, d := range f.Decls {
		fd, ok := d.(*ast.FuncDecl)
		if ok && fd.Recv == nil && fd.Name != nil && drop(fd.Name.Name) {
			continue
		}
		keep = append(keep, d)
	}
	f.Decls = keep
}

func fileHasFuncs(f *ast.File) bool {
	for _, d := range f.Decls {
		if _, ok := d.(*ast.FuncDecl); ok {
			return true
		}
	}
	return false
}

func fileHasDecls(f *ast.File) bool {
	return len(f.Decls) > 0
}

func countTestFuncs(f *ast.File) int {
	n := 0
	for _, d := range f.Decls {
		fd, ok := d.(*ast.FuncDecl)
		if !ok || fd.Recv != nil || fd.Name == nil {
			continue
		}
		if isTestFunc(fd.Name.Name) || fd.Name.Name == "TestMain" {
			n++
		}
	}
	return n
}

func stripVarAndInit(f *ast.File) {
	// Names still referenced by remaining funcs/methods/types must be kept
	// (e.g. poserPathErr used by (*poser).As after TestAs is skipped).
	// init bodies are handled separately: keep an init when it mutates a
	// still-used table (strconv NumError wrapping), drop it when it only
	// touches unused vars (fmtTests-sized baggage for skipped printers).
	used := map[string]bool{}
	ast.Inspect(f, func(n ast.Node) bool {
		switch n := n.(type) {
		case *ast.GenDecl:
			if n.Tok == token.VAR || n.Tok == token.IMPORT {
				return false // do not mark LHS idents of vars as uses
			}
		case *ast.FuncDecl:
			if n.Recv == nil && n.Name != nil && n.Name.Name == "init" {
				return false // defer init refs until we know which vars stay
			}
		case *ast.Ident:
			used[n.Name] = true
		}
		return true
	})

	pkgVars := map[string]bool{}
	type varSpec struct {
		names []string
		rhs   []ast.Expr
		decl  *ast.GenDecl
		spec  *ast.ValueSpec
	}
	var specs []varSpec
	for _, d := range f.Decls {
		gd, ok := d.(*ast.GenDecl)
		if !ok || gd.Tok != token.VAR {
			continue
		}
		for _, s := range gd.Specs {
			vs := s.(*ast.ValueSpec)
			names := make([]string, len(vs.Names))
			for i, n := range vs.Names {
				names[i] = n.Name
				pkgVars[n.Name] = true
			}
			specs = append(specs, varSpec{names: names, rhs: vs.Values, decl: gd, spec: vs})
		}
	}

	expandUsedVars := func() {
		for {
			added := false
			for _, vs := range specs {
				needed := false
				for _, n := range vs.names {
					if used[n] {
						needed = true
						break
					}
				}
				if !needed {
					continue
				}
				for _, rhs := range vs.rhs {
					ast.Inspect(rhs, func(n ast.Node) bool {
						if id, ok := n.(*ast.Ident); ok && !used[id.Name] {
							used[id.Name] = true
							added = true
						}
						return true
					})
				}
			}
			if !added {
				return
			}
		}
	}
	expandUsedVars()

	// Keep inits that touch any still-used package var; pulling those inits
	// in also keeps every package var they reference (and their RHS deps).
	keepInit := map[*ast.FuncDecl]bool{}
	for {
		added := false
		for _, d := range f.Decls {
			fd, ok := d.(*ast.FuncDecl)
			if !ok || fd.Recv != nil || fd.Name == nil || fd.Name.Name != "init" || keepInit[fd] {
				continue
			}
			varRefs := map[string]bool{}
			touchesUsed := false
			ast.Inspect(fd, func(n ast.Node) bool {
				id, ok := n.(*ast.Ident)
				if !ok || !pkgVars[id.Name] {
					return true
				}
				varRefs[id.Name] = true
				if used[id.Name] {
					touchesUsed = true
				}
				return true
			})
			if !touchesUsed {
				continue
			}
			keepInit[fd] = true
			for name := range varRefs {
				if !used[name] {
					used[name] = true
					added = true
				}
			}
		}
		if !added {
			break
		}
		expandUsedVars()
	}

	var keep []ast.Decl
	for _, d := range f.Decls {
		switch d := d.(type) {
		case *ast.GenDecl:
			if d.Tok == token.VAR {
				// Keep the whole var block if any name is used. Specs in one
				// GenDecl often reference each other (slice = array[:]).
				needed := false
				for _, s := range d.Specs {
					vs := s.(*ast.ValueSpec)
					for _, name := range vs.Names {
						if used[name.Name] {
							needed = true
							break
						}
					}
					if needed {
						break
					}
				}
				if !needed {
					continue
				}
				keep = append(keep, d)
				continue
			}
			keep = append(keep, d)
		case *ast.FuncDecl:
			if d.Recv == nil && d.Name != nil && d.Name.Name == "init" {
				if keepInit[d] {
					keep = append(keep, d)
				}
				continue
			}
			keep = append(keep, d)
		default:
			keep = append(keep, d)
		}
	}
	f.Decls = keep
}

// pruneUnusedImports drops import specs whose local name is never referenced.
// Conservative: keeps blank/dot imports.
func pruneUnusedImports(f *ast.File) {
	used := map[string]bool{}
	ast.Inspect(f, func(n ast.Node) bool {
		switch n := n.(type) {
		case *ast.GenDecl:
			if n.Tok == token.IMPORT {
				return false // do not walk import idents
			}
		case *ast.SelectorExpr:
			if id, ok := n.X.(*ast.Ident); ok {
				used[id.Name] = true
			}
		case *ast.Ident:
			// Bare idents (e.g. type switches) — keep if exported pkg use is rare.
			used[n.Name] = true
		}
		return true
	})
	keepSet := map[*ast.ImportSpec]bool{}
	var keepImports []*ast.ImportSpec
	for _, is := range f.Imports {
		name := importLocalName(is)
		if name == "_" || name == "." || used[name] {
			keepSet[is] = true
			keepImports = append(keepImports, is)
		}
	}
	if len(keepImports) == len(f.Imports) {
		return
	}
	var newDecls []ast.Decl
	for _, d := range f.Decls {
		gd, ok := d.(*ast.GenDecl)
		if !ok || gd.Tok != token.IMPORT {
			newDecls = append(newDecls, d)
			continue
		}
		var specs []ast.Spec
		for _, s := range gd.Specs {
			is := s.(*ast.ImportSpec)
			if keepSet[is] {
				specs = append(specs, is)
			}
		}
		if len(specs) > 0 {
			gd.Specs = specs
			newDecls = append(newDecls, gd)
		}
	}
	f.Decls = newDecls
	f.Imports = keepImports
}

func importLocalName(is *ast.ImportSpec) string {
	if is.Name != nil {
		return is.Name.Name
	}
	path := strings.Trim(is.Path.Value, `"`)
	if i := strings.LastIndex(path, "/"); i >= 0 {
		return path[i+1:]
	}
	return path
}

// WriteStripped writes a FileInfo's AST to destPath.
func WriteStripped(fi FileInfo, destPath string) error {
	var b strings.Builder
	if err := formatFile(&b, fi.Fset, fi.Syntax); err != nil {
		return err
	}
	return writeFile(destPath, []byte(b.String()))
}

// DestBase returns a stable basename for a stripped test file in a temp dir.
func DestBase(srcPath string, i int) string {
	base := filepath.Base(srcPath)
	if base == "" {
		return fmt.Sprintf("test_%d.go", i)
	}
	return base
}
