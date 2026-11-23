package pipeline

import (
	"fmt"
	"go/ast"

	"github.com/achille-roussel/wasmgc-go/internal/ir"
	"github.com/achille-roussel/wasmgc-go/internal/load"
)

// stubLowerer implements Lowerer for the M1 empty-main subset until
// internal/lower lands. It emits IR matching internal/ir/testdata/empty_main.ir.
type stubLowerer struct{}

func isStubLowerer(l Lowerer) bool {
	_, ok := l.(*stubLowerer)
	return ok
}

func (s *stubLowerer) Package(pkgs []*load.Package, main *load.Package) (*ir.Module, error) {
	_ = pkgs
	if main == nil {
		return nil, fmt.Errorf("pipeline: stub lowerer: no main package")
	}
	if !isEmptyMainPackage(main) {
		return nil, fmt.Errorf("pipeline: stub lowerer: only empty main is supported until internal/lower lands (got %s)", main.PkgPath)
	}
	const src = `func export "run" main.run() {
  call main.main()
  return
}

func main.main() {
  return
}
`
	m, err := ir.Parse("empty_main.ir", src)
	if err != nil {
		return nil, fmt.Errorf("pipeline: stub lowerer: parse empty main IR: %w", err)
	}
	m.Low = true
	if err := ir.Verify(m); err != nil {
		return nil, fmt.Errorf("pipeline: stub lowerer: verify: %w", err)
	}
	return m, nil
}

func isEmptyMainPackage(p *load.Package) bool {
	if p == nil || p.Name != "main" {
		return false
	}
	if p.Types != nil {
		for _, imp := range p.Types.Imports() {
			if imp.Path() != "unsafe" {
				return false
			}
		}
	}
	sawMain := false
	for _, f := range p.Syntax {
		for _, d := range f.Decls {
			switch d := d.(type) {
			case *ast.FuncDecl:
				if d.Recv != nil {
					return false
				}
				if d.Name == nil || d.Name.Name != "main" {
					return false
				}
				if d.Type.Params != nil && len(d.Type.Params.List) > 0 {
					return false
				}
				if d.Type.Results != nil && len(d.Type.Results.List) > 0 {
					return false
				}
				if d.Body == nil || len(d.Body.List) != 0 {
					return false
				}
				sawMain = true
			case *ast.GenDecl:
				return false
			}
		}
	}
	return sawMain
}
