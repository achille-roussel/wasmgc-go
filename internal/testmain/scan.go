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
	// Helper-only files (all Test* stripped) are stashed. After live tests are
	// known, reattach only the helper decls that live tests actually reference
	// (e.g. encoding/json indentNewlines). Blindly keeping every leftover
	// helper (initBig/genValue/…) overflows the host typechecker.
	var helperInternal, helperExternal []FileInfo
	// Phase 1: parse, strip skipped tests, classify live vs helper — but do
	// not stripVarAndInit yet. Types like fmt.renamedBool / json.Loop are
	// defined in one file and referenced from another; stripping per-file
	// would drop them before package-wide uses are known.
	type pendingFile struct {
		fi           FileInfo
		live         bool
		keepExport   bool
		keepImport   bool
		fileTests    []TestFunc
		fileMain     *TestFunc
	}
	var pending []pendingFile
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
		// Also drop helpers whose first param is *testing.B (e.g. lowercase
		// benchmarkFoo) so the thin testing.B stub need not implement the
		// full harness API for packages that still have live Test* funcs.
		stripFuncs(af, func(name string) bool {
			if isBenchmarkOrExampleOrFuzz(name) {
				return true
			}
			if _, ok := res.Skipped[name]; ok {
				return true
			}
			return false
		})
		stripTestingBHelpers(af)
		pruneUnusedImports(af)
		baseName := filepath.Base(f)
		keepExport := !ext && (baseName == "export_test.go" || strings.HasPrefix(baseName, "export_"))
		keepImportHelper := ext && (baseName == "import_test.go" || strings.HasPrefix(baseName, "import_"))
		// Drop emptied example/bench files, but keep export_test / import_test.
		if !fileHasFuncs(af) && !keepExport && !keepImportHelper {
			continue
		}
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
		fi := FileInfo{
			Path:     f,
			PkgName:  pkgName,
			External: ext,
			Syntax:   af,
			Fset:     fset,
		}
		live := fileMain != nil || len(fileTests) > 0 || keepExport || keepImportHelper
		if !live {
			if !fileHasFuncs(af) && !fileHasTypeOrValueDecls(af) {
				continue
			}
		}
		pending = append(pending, pendingFile{
			fi:         fi,
			live:       live,
			keepExport: keepExport,
			keepImport: keepImportHelper,
			fileTests:  fileTests,
			fileMain:   fileMain,
		})
	}
	// Package-wide uses from live files (both sides), so stripVarAndInit keeps
	// cross-file type/var refs (renamedBool, Loop, …). Seed from live funcs,
	// then expand through var/type decls across all live files on that side
	// (scan_test tables reference renamedBool defined in fmt_test.go).
	pkgUsedInternal := map[string]bool{}
	pkgUsedExternal := map[string]bool{}
	var liveInternal, liveExternal []*ast.File
	for i := range pending {
		p := &pending[i]
		if !p.live {
			continue
		}
		if p.fi.External {
			collectLiveFuncIdents(p.fi.Syntax, pkgUsedExternal)
			liveExternal = append(liveExternal, p.fi.Syntax)
		} else {
			collectLiveFuncIdents(p.fi.Syntax, pkgUsedInternal)
			liveInternal = append(liveInternal, p.fi.Syntax)
		}
	}
	expandPkgUsed(pkgUsedInternal, liveInternal)
	expandPkgUsed(pkgUsedExternal, liveExternal)
	// Phase 2: stripVarAndInit on live files with package-wide seeds; stash helpers.
	for _, p := range pending {
		af := p.fi.Syntax
		ext := p.fi.External
		if p.live && countTestFuncs(af) >= 1 {
			extra := pkgUsedInternal
			if ext {
				extra = pkgUsedExternal
			}
			stripVarAndInit(af, extra)
			pruneUnusedImports(af)
		}
		if !p.live {
			// Do NOT stripVarAndInit: helpers like CaseName/Name are only
			// referenced from other live files; filterReferencedHelpers picks
			// them up later.
			if ext {
				helperExternal = append(helperExternal, p.fi)
			} else {
				helperInternal = append(helperInternal, p.fi)
			}
			continue
		}
		if ext {
			res.External = append(res.External, p.fi)
		} else {
			res.Internal = append(res.Internal, p.fi)
		}
		if p.fileMain != nil {
			res.TestMain = p.fileMain
		}
		for _, tf := range p.fileTests {
			if tf.External {
				res.XTests = append(res.XTests, tf)
			} else {
				res.Tests = append(res.Tests, tf)
			}
		}
	}
	// Reattach helpers referenced by live tests on the same package side.
	if len(res.Tests) > 0 || (res.TestMain != nil && !res.TestMain.External) {
		res.Internal = append(res.Internal, filterReferencedHelpers(res.Internal, helperInternal)...)
	}
	if len(res.XTests) > 0 || (res.TestMain != nil && res.TestMain.External) {
		res.External = append(res.External, filterReferencedHelpers(res.External, helperExternal)...)
	}
	// Live files (not only helper-only ones) may keep or strip jsontest
	// CaseName/Name aliases depending on which tests survived. Normalize:
	// strip jsontest-backed decls everywhere, then ensure local stubs exist
	// when any live file still references CaseName/Name.
	ensureLocalCaseNameHelpers(res)
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

// filterReferencedHelpers keeps decls from helper-only files that are
// referenced (transitively) by live test files. Unreferenced leftovers such
// as initBig/genValue are dropped so they cannot blow up typecheck/codegen.
func filterReferencedHelpers(live, helpers []FileInfo) []FileInfo {
	if len(helpers) == 0 || len(live) == 0 {
		return nil
	}
	used := map[string]bool{}
	for _, fi := range live {
		collectIdents(fi.Syntax, used)
	}
	// Package-level defs available from helpers.
	type defKind int
	const (
		defFunc defKind = iota
		defOther
	)
	type def struct {
		kind defKind
		fi   int
		decl ast.Decl
		name string
	}
	var defs []def
	for i, fi := range helpers {
		for _, d := range fi.Syntax.Decls {
			switch d := d.(type) {
			case *ast.FuncDecl:
				if d.Name == nil {
					continue
				}
				if d.Recv != nil {
					// Methods: keep keyed by receiver type name when possible.
					continue
				}
				defs = append(defs, def{kind: defFunc, fi: i, decl: d, name: d.Name.Name})
			case *ast.GenDecl:
				for _, s := range d.Specs {
					switch s := s.(type) {
					case *ast.TypeSpec:
						if s.Name != nil {
							defs = append(defs, def{kind: defOther, fi: i, decl: d, name: s.Name.Name})
						}
					case *ast.ValueSpec:
						for _, n := range s.Names {
							defs = append(defs, def{kind: defOther, fi: i, decl: d, name: n.Name})
						}
					}
				}
			}
		}
	}
	// Expand used set through helper bodies that are themselves used.
	for {
		added := false
		for _, d := range defs {
			if !used[d.name] {
				continue
			}
			before := len(used)
			collectIdents(d.decl, used)
			if len(used) > before {
				added = true
			}
		}
		if !added {
			break
		}
	}
	keptByFile := map[int][]ast.Decl{}
	seenDecl := map[ast.Decl]bool{}
	for _, d := range defs {
		if !used[d.name] || d.name == "init" {
			continue
		}
		if seenDecl[d.decl] {
			continue
		}
		seenDecl[d.decl] = true
		keptByFile[d.fi] = append(keptByFile[d.fi], d.decl)
	}
	var out []FileInfo
	for i, fi := range helpers {
		decls := keptByFile[i]
		if len(decls) == 0 {
			continue
		}
		// Preserve imports; prune after rewrite.
		var newDecls []ast.Decl
		for _, d := range fi.Syntax.Decls {
			if gd, ok := d.(*ast.GenDecl); ok && gd.Tok == token.IMPORT {
				newDecls = append(newDecls, d)
			}
		}
		newDecls = append(newDecls, decls...)
		fi.Syntax.Decls = newDecls
		pruneUnusedImports(fi.Syntax)
		// Helper files may contribute only types/vars (e.g. CaseName alias +
		// Name var) with no funcs — still needed for typecheck.
		if !fileHasFuncs(fi.Syntax) && !fileHasTypeOrValueDecls(fi.Syntax) {
			continue
		}
		out = append(out, fi)
	}
	return out
}

// ensureLocalCaseNameHelpers strips jsontest-backed CaseName/CasePos/Name
// decls from every file and, when live tests still reference those names,
// injects thin local stubs. The real jsontest package embeds large .json.zst
// fixtures (internal/zstd) and panics under wasmgc; we only need Name() for
// table-driven case labels.
func ensureLocalCaseNameHelpers(res *ScanResult) {
	if res == nil {
		return
	}
	stripJsontestCaseDecls := func(files []FileInfo) {
		for i := range files {
			stripJsontestCaseDeclsFromFile(files[i].Syntax)
			pruneUnusedImports(files[i].Syntax)
		}
	}
	stripJsontestCaseDecls(res.Internal)
	stripJsontestCaseDecls(res.External)

	need := false
	defined := false
	check := func(files []FileInfo) {
		for _, fi := range files {
			if fi.Syntax == nil {
				continue
			}
			ast.Inspect(fi.Syntax, func(n ast.Node) bool {
				switch x := n.(type) {
				case *ast.Ident:
					if x.Name == "CaseName" || x.Name == "Name" || x.Name == "CasePos" {
						need = true
					}
				case *ast.FuncDecl:
					if x.Name != nil && x.Name.Name == "Name" && x.Recv == nil {
						defined = true
					}
				case *ast.TypeSpec:
					if x.Name != nil && (x.Name.Name == "CaseName" || x.Name.Name == "CasePos") {
						defined = true
					}
				case *ast.ValueSpec:
					for _, n := range x.Names {
						if n.Name == "Name" {
							defined = true
						}
					}
				}
				return true
			})
		}
	}
	check(res.Internal)
	check(res.External)
	if !need || defined {
		return
	}
	stubs := jsontestCaseHelperStubs()
	if len(stubs) == 0 {
		return
	}
	// Prefer attaching to an existing internal file; else synthesize one.
	if len(res.Internal) > 0 {
		fi := &res.Internal[0]
		fi.Syntax.Decls = append(fi.Syntax.Decls, stubs...)
		return
	}
	fset := token.NewFileSet()
	af, err := parser.ParseFile(fset, "case_name_stubs.go", "package "+res.PkgName+"\n", 0)
	if err != nil {
		return
	}
	af.Decls = append(af.Decls, stubs...)
	res.Internal = append(res.Internal, FileInfo{
		Path:    "case_name_stubs.go",
		PkgName: res.PkgName,
		Syntax:  af,
		Fset:    fset,
	})
}

func stripJsontestCaseDeclsFromFile(f *ast.File) {
	if f == nil {
		return
	}
	var keep []ast.Decl
	for _, d := range f.Decls {
		gd, ok := d.(*ast.GenDecl)
		if !ok || (gd.Tok != token.TYPE && gd.Tok != token.VAR) {
			keep = append(keep, d)
			continue
		}
		var specs []ast.Spec
		for _, s := range gd.Specs {
			if declRefsJsontest(s) {
				continue
			}
			// Also drop leftover local CaseName/Name/CasePos so we can
			// re-inject a single consistent stub set.
			switch s := s.(type) {
			case *ast.TypeSpec:
				if s.Name != nil && (s.Name.Name == "CaseName" || s.Name.Name == "CasePos") {
					continue
				}
			case *ast.ValueSpec:
				drop := false
				for _, n := range s.Names {
					if n.Name == "Name" {
						drop = true
						break
					}
				}
				if drop {
					continue
				}
			}
			specs = append(specs, s)
		}
		if len(specs) == 0 {
			continue
		}
		if len(specs) == len(gd.Specs) {
			keep = append(keep, d)
			continue
		}
		cp := *gd
		cp.Specs = specs
		keep = append(keep, &cp)
	}
	// Also drop func Name if it was a prior stub inject or jsontest wrapper.
	var keep2 []ast.Decl
	for _, d := range keep {
		fd, ok := d.(*ast.FuncDecl)
		if ok && fd.Recv == nil && fd.Name != nil && fd.Name.Name == "Name" {
			continue
		}
		if ok && fd.Recv != nil && fd.Name != nil && fd.Name.Name == "String" {
			// Drop CasePos.String from prior stubs; re-inject with stubs.
			if recvIsCasePos(fd) {
				continue
			}
		}
		keep2 = append(keep2, d)
	}
	f.Decls = keep2
}

func recvIsCasePos(fd *ast.FuncDecl) bool {
	if fd.Recv == nil || len(fd.Recv.List) == 0 {
		return false
	}
	t := fd.Recv.List[0].Type
	if st, ok := t.(*ast.StarExpr); ok {
		t = st.X
	}
	id, ok := t.(*ast.Ident)
	return ok && id.Name == "CasePos"
}

func declRefsJsontest(s ast.Spec) bool {
	refs := false
	ast.Inspect(s, func(n ast.Node) bool {
		sel, ok := n.(*ast.SelectorExpr)
		if !ok {
			return true
		}
		pkg, ok := sel.X.(*ast.Ident)
		if ok && pkg.Name == "jsontest" {
			refs = true
			return false
		}
		return true
	})
	return refs
}

func jsontestCaseHelperStubs() []ast.Decl {
	// CasePos must be non-zero-sized: wasmgc struct descriptors omit
	// zero-size fields, so an empty CasePos would make Where invisible
	// ("type CaseName has no field Where") on tt.Where after embedding.
	src := `package p
type CaseName struct {
	Name  string
	Where CasePos
}
type CasePos struct{ _ byte }
func (CasePos) String() string { return "?" }
func Name(s string) CaseName { return CaseName{Name: s} }
`
	fset := token.NewFileSet()
	af, err := parser.ParseFile(fset, "jsontest_stubs.go", src, 0)
	if err != nil {
		return nil
	}
	var out []ast.Decl
	for _, d := range af.Decls {
		out = append(out, d)
	}
	return out
}

func fileHasTypeOrValueDecls(f *ast.File) bool {
	for _, d := range f.Decls {
		gd, ok := d.(*ast.GenDecl)
		if !ok {
			continue
		}
		if gd.Tok == token.TYPE || gd.Tok == token.VAR || gd.Tok == token.CONST {
			return true
		}
	}
	return false
}

func collectIdents(n ast.Node, used map[string]bool) {
	if n == nil {
		return
	}
	ast.Inspect(n, func(n ast.Node) bool {
		id, ok := n.(*ast.Ident)
		if ok && id.Name != "" {
			used[id.Name] = true
		}
		return true
	})
}

// collectLiveFuncIdents seeds used with idents from non-init funcs/methods
// (same seed stripVarAndInit uses per file).
func collectLiveFuncIdents(f *ast.File, used map[string]bool) {
	if f == nil {
		return
	}
	for _, d := range f.Decls {
		fd, ok := d.(*ast.FuncDecl)
		if !ok {
			continue
		}
		if fd.Recv == nil && fd.Name != nil && fd.Name.Name == "init" {
			continue
		}
		collectIdents(fd, used)
	}
}

// expandPkgUsed grows used by walking var/type decls across files until
// fixed point — so a live scan_test table typed as renamedBool pulls that
// type name even when it lives in fmt_test.go.
func expandPkgUsed(used map[string]bool, files []*ast.File) {
	type varGroup struct {
		names []string
		nodes []ast.Node // types + RHSs of every spec in the GenDecl
	}
	type typeSpec struct {
		name string
		rhs  ast.Expr
	}
	var groups []varGroup
	var types []typeSpec
	for _, f := range files {
		if f == nil {
			continue
		}
		for _, d := range f.Decls {
			gd, ok := d.(*ast.GenDecl)
			if !ok {
				continue
			}
			switch gd.Tok {
			case token.VAR:
				// stripVarAndInit keeps a whole GenDecl when any name is used,
				// so expand the same way: one used name pulls every spec's type.
				g := varGroup{}
				for _, s := range gd.Specs {
					vs := s.(*ast.ValueSpec)
					for _, n := range vs.Names {
						g.names = append(g.names, n.Name)
					}
					if vs.Type != nil {
						g.nodes = append(g.nodes, vs.Type)
					}
					for _, rhs := range vs.Values {
						g.nodes = append(g.nodes, rhs)
					}
				}
				groups = append(groups, g)
			case token.TYPE:
				for _, s := range gd.Specs {
					ts := s.(*ast.TypeSpec)
					if ts.Name != nil {
						types = append(types, typeSpec{name: ts.Name.Name, rhs: ts.Type})
					}
				}
			}
		}
	}
	mark := func(node ast.Node) bool {
		if node == nil {
			return false
		}
		added := false
		ast.Inspect(node, func(n ast.Node) bool {
			id, ok := n.(*ast.Ident)
			if ok && id.Name != "" && !used[id.Name] {
				used[id.Name] = true
				added = true
			}
			return true
		})
		return added
	}
	for {
		added := false
		for _, g := range groups {
			needed := false
			for _, n := range g.names {
				if used[n] {
					needed = true
					break
				}
			}
			if !needed {
				continue
			}
			for _, n := range g.names {
				if !used[n] {
					used[n] = true
					added = true
				}
			}
			for _, node := range g.nodes {
				if mark(node) {
					added = true
				}
			}
		}
		for _, ts := range types {
			if !used[ts.name] {
				continue
			}
			if mark(ts.rhs) {
				added = true
			}
		}
		if !added {
			return
		}
	}
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

// stripTestingBHelpers drops package-level funcs whose first parameter is
// *testing.B (or testing.B). Named Benchmark* are already removed by
// stripFuncs; this catches unexported helpers like benchmarkCompareBytesBig.
func stripTestingBHelpers(f *ast.File) {
	var keep []ast.Decl
	for _, d := range f.Decls {
		fd, ok := d.(*ast.FuncDecl)
		if ok && fd.Recv == nil && funcFirstParamIsTestingB(fd) {
			continue
		}
		keep = append(keep, d)
	}
	f.Decls = keep
}

func funcFirstParamIsTestingB(fd *ast.FuncDecl) bool {
	if fd.Type == nil || fd.Type.Params == nil || len(fd.Type.Params.List) == 0 {
		return false
	}
	p := fd.Type.Params.List[0].Type
	return isTestingBType(p)
}

func isTestingBType(expr ast.Expr) bool {
	switch t := expr.(type) {
	case *ast.StarExpr:
		return isTestingBType(t.X)
	case *ast.SelectorExpr:
		pkg, ok := t.X.(*ast.Ident)
		return ok && pkg.Name == "testing" && t.Sel != nil && t.Sel.Name == "B"
	default:
		return false
	}
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

func stripVarAndInit(f *ast.File, extraUsed map[string]bool) {
	// Names still referenced by remaining funcs/methods/types must be kept
	// (e.g. poserPathErr used by (*poser).As after TestAs is skipped).
	// init bodies are handled separately: keep an init when it mutates a
	// still-used table (strconv NumError wrapping), drop it when it only
	// touches unused vars (fmtTests-sized baggage for skipped printers).
	// Unused type decls (e.g. RecursiveSlice only used by skipped tests) are
	// dropped too — cyclic named types blow up Wasm global init otherwise.
	used := map[string]bool{}
	for name := range extraUsed {
		used[name] = true
	}
	// Seed uses from live funcs/methods only. Package-level var/type decls
	// are pulled in via expandUsedVars so unused RecursiveSlice-style cycles
	// (and their vars) disappear with the skipped tests that referenced them.
	for _, d := range f.Decls {
		fd, ok := d.(*ast.FuncDecl)
		if !ok {
			continue
		}
		if fd.Recv == nil && fd.Name != nil && fd.Name.Name == "init" {
			continue
		}
		ast.Inspect(fd, func(n ast.Node) bool {
			if id, ok := n.(*ast.Ident); ok && id.Name != "" {
				used[id.Name] = true
			}
			return true
		})
	}

	pkgVars := map[string]bool{}
	type varSpec struct {
		names []string
		typ   ast.Expr
		rhs   []ast.Expr
		decl  *ast.GenDecl
		spec  *ast.ValueSpec
	}
	var specs []varSpec
	type typeSpec struct {
		name string
		rhs  ast.Expr
		decl *ast.GenDecl
		spec *ast.TypeSpec
	}
	var typeSpecs []typeSpec
	for _, d := range f.Decls {
		gd, ok := d.(*ast.GenDecl)
		if !ok {
			continue
		}
		switch gd.Tok {
		case token.VAR:
			for _, s := range gd.Specs {
				vs := s.(*ast.ValueSpec)
				names := make([]string, len(vs.Names))
				for i, n := range vs.Names {
					names[i] = n.Name
					pkgVars[n.Name] = true
				}
				specs = append(specs, varSpec{names: names, typ: vs.Type, rhs: vs.Values, decl: gd, spec: vs})
			}
		case token.TYPE:
			for _, s := range gd.Specs {
				ts := s.(*ast.TypeSpec)
				if ts.Name != nil {
					typeSpecs = append(typeSpecs, typeSpec{name: ts.Name.Name, rhs: ts.Type, decl: gd, spec: ts})
				}
			}
		}
	}

	markIdents := func(node ast.Node) bool {
		added := false
		if node == nil {
			return false
		}
		ast.Inspect(node, func(n ast.Node) bool {
			if id, ok := n.(*ast.Ident); ok && id.Name != "" && !used[id.Name] {
				used[id.Name] = true
				added = true
			}
			return true
		})
		return added
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
				if markIdents(vs.typ) {
					added = true
				}
				for _, rhs := range vs.rhs {
					if markIdents(rhs) {
						added = true
					}
				}
			}
			for _, ts := range typeSpecs {
				if !used[ts.name] {
					continue
				}
				if markIdents(ts.rhs) {
					added = true
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
			if d.Tok == token.TYPE {
				var keptSpecs []ast.Spec
				for _, s := range d.Specs {
					ts := s.(*ast.TypeSpec)
					if ts.Name != nil && used[ts.Name.Name] {
						keptSpecs = append(keptSpecs, s)
					}
				}
				if len(keptSpecs) == 0 {
					continue
				}
				if len(keptSpecs) == len(d.Specs) {
					keep = append(keep, d)
					continue
				}
				nd := *d
				nd.Specs = keptSpecs
				keep = append(keep, &nd)
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
			// Drop methods whose receiver type was stripped.
			if d.Recv != nil && len(d.Recv.List) > 0 {
				recvName := recvTypeName(d.Recv.List[0].Type)
				if recvName != "" && !used[recvName] {
					// Still keep if the name was never a package type decl
					// (builtin/qualified) — only drop when we know the type
					// was defined here and unused.
					isPkgType := false
					for _, ts := range typeSpecs {
						if ts.name == recvName {
							isPkgType = true
							break
						}
					}
					if isPkgType {
						continue
					}
				}
			}
			keep = append(keep, d)
		default:
			keep = append(keep, d)
		}
	}
	f.Decls = keep
}

func recvTypeName(expr ast.Expr) string {
	switch t := expr.(type) {
	case *ast.Ident:
		return t.Name
	case *ast.StarExpr:
		return recvTypeName(t.X)
	case *ast.IndexExpr:
		return recvTypeName(t.X)
	case *ast.IndexListExpr:
		return recvTypeName(t.X)
	default:
		return ""
	}
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
	base := path
	if i := strings.LastIndex(path, "/"); i >= 0 {
		base = path[i+1:]
	}
	// Major-version path suffix (/v2, /v3, …): default name is the prior
	// element (math/rand/v2 → rand), matching cmd/go — not the "v2" segment.
	if isMajorVersionSuffix(base) {
		parent := path[:len(path)-len(base)-1] // trim "/vN"
		if j := strings.LastIndex(parent, "/"); j >= 0 {
			return parent[j+1:]
		}
		return parent
	}
	return base
}

func isMajorVersionSuffix(s string) bool {
	if len(s) < 2 || s[0] != 'v' {
		return false
	}
	for i := 1; i < len(s); i++ {
		if s[i] < '0' || s[i] > '9' {
			return false
		}
	}
	// v1 is not a major-version import path suffix in modules.
	return s != "v1"
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
