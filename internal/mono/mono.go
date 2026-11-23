package mono

import (
	"fmt"
	"go/ast"
	"go/token"
	"go/types"
)

// Package is a no-op for M1/M2. It returns an error if the package declares
// generic functions or types that would need monomorphization.
func Package(_ *token.FileSet, _ []*ast.File, pkg *types.Package, _ *types.Info) error {
	if pkg == nil {
		return nil
	}
	scope := pkg.Scope()
	for _, name := range scope.Names() {
		obj := scope.Lookup(name)
		switch obj := obj.(type) {
		case *types.Func:
			if sig, ok := obj.Type().(*types.Signature); ok && sig.TypeParams().Len() > 0 {
				return fmt.Errorf("mono: generic function %s (monomorphization not yet implemented)", obj.FullName())
			}
		case *types.TypeName:
			if named, ok := obj.Type().(*types.Named); ok && named.TypeParams().Len() > 0 {
				return fmt.Errorf("mono: generic type %s (monomorphization not yet implemented)", obj.Name())
			}
		}
	}
	return nil
}
