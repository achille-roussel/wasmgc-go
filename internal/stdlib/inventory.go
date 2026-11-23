package stdlib

import (
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/achille-roussel/wasmgc-go/internal/load"
)

// InventoryReport lists every stdlib package with tier and notable sites.
type InventoryReport struct {
	Pin      string            `json:"pin"`
	Packages []InventoryPackage `json:"packages"`
}

// InventoryPackage is one package's classification and findings.
type InventoryPackage struct {
	Path              string   `json:"path"`
	Tier              Tier     `json:"tier"`
	UnsafeSites       []Site   `json:"unsafe_sites,omitempty"`
	FinalizerSites    []Site   `json:"finalizer_sites,omitempty"`
	DynamicReflect    []Site   `json:"dynamic_reflect_sites,omitempty"`
	Notes             string   `json:"notes,omitempty"`
}

// Site is a source location of interest.
type Site struct {
	File string `json:"file"`
	Line int    `json:"line"`
	Text string `json:"text"`
}

// Inventory scans the upstream (and rewrite) trees for unsafe, finalizer,
// and dynamic reflect constructor sites, combining with the manifest tiers.
func Inventory(mat *Materialized, p Paths) (*InventoryReport, error) {
	filter := load.DefaultFilter()
	srcRoot := filepath.Join(mat.GOROOT, "src")
	paths, err := listPackageDirs(srcRoot)
	if err != nil {
		return nil, err
	}
	rep := &InventoryReport{Pin: mat.Pin.Version}
	for path := range paths {
		entry := mat.Manifest.Lookup(path)
		pkg := InventoryPackage{
			Path:  path,
			Tier:  entry.Tier,
			Notes: entry.Notes,
		}
		dir := filepath.Join(srcRoot, filepath.FromSlash(path))
		files, err := filter.ListGoFiles(dir)
		if err != nil {
			continue
		}
		fset := token.NewFileSet()
		for _, f := range files {
			af, err := parser.ParseFile(fset, f, nil, 0)
			if err != nil {
				continue
			}
			base := filepath.Base(f)
			ast.Inspect(af, func(n ast.Node) bool {
				switch e := n.(type) {
				case *ast.CallExpr:
					text := exprText(fset, e)
					pos := fset.Position(e.Pos())
					site := Site{File: base, Line: pos.Line, Text: text}
					if looksUnsafeSite(text) {
						pkg.UnsafeSites = append(pkg.UnsafeSites, site)
					}
					if looksFinalizer(text) {
						pkg.FinalizerSites = append(pkg.FinalizerSites, site)
					}
					if looksDynamicReflect(text) {
						pkg.DynamicReflect = append(pkg.DynamicReflect, site)
					}
				}
				return true
			})
		}
		rep.Packages = append(rep.Packages, pkg)
	}
	sort.Slice(rep.Packages, func(i, j int) bool {
		return rep.Packages[i].Path < rep.Packages[j].Path
	})
	return rep, nil
}

func looksUnsafeSite(text string) bool {
	return strings.Contains(text, "uintptr(unsafe.Pointer") ||
		strings.Contains(text, "unsafe.Pointer(uintptr") ||
		strings.Contains(text, "unsafe.Add(")
}

func looksFinalizer(text string) bool {
	return strings.Contains(text, "SetFinalizer(") ||
		strings.Contains(text, "AddCleanup(")
}

func looksDynamicReflect(text string) bool {
	for _, name := range []string{
		"reflect.StructOf(", "reflect.FuncOf(", "reflect.ArrayOf(",
		"reflect.ChanOf(", "reflect.MapOf(", "reflect.SliceOf(",
		"reflect.PointerTo(", "reflect.PtrTo(",
	} {
		if strings.Contains(text, name) {
			return true
		}
	}
	return false
}

func exprText(fset *token.FileSet, e ast.Expr) string {
	start := fset.Position(e.Pos())
	end := fset.Position(e.End())
	if start.Filename == "" || start.Filename != end.Filename {
		return ""
	}
	data, err := os.ReadFile(start.Filename)
	if err != nil {
		return ""
	}
	lines := strings.Split(string(data), "\n")
	if start.Line <= 0 || start.Line > len(lines) {
		return ""
	}
	line := lines[start.Line-1]
	if start.Column > 0 && start.Column <= len(line) {
		line = line[start.Column-1:]
	}
	line = strings.TrimSpace(line)
	if len(line) > 120 {
		line = line[:117] + "..."
	}
	return line
}
