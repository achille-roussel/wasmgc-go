package mono

import (
	"fmt"
	"go/ast"
	"go/token"
	"go/types"

	"github.com/achille-roussel/wasmgc-go/internal/load"
)

// Instance is one concrete specialization of a generic function or method.
type Instance struct {
	Origin *types.Func
	Args   []types.Type // concrete type arguments
	Recv   types.Type   // concrete receiver for methods; nil for funcs
	IRName string
	Sig    *types.Signature // concrete signature
	Subst  Subst
}

// Table indexes reachable generic instantiations for the lowerer.
type Table struct {
	ByName  map[string]*Instance
	Decl    map[*types.Func]*ast.FuncDecl
	InfoFor map[*types.Func]*types.Info
	PkgFor  map[*types.Func]*types.Package
	Fset    *token.FileSet

	ctxt *types.Context
}

// Program collects every reachable generic instantiation from info.Instances
// and method selections. It does not rewrite ASTs; the lowerer emits one IR
// function per Instance using Origin's body and Subst.
func Program(prog *load.Program) (*Table, error) {
	if prog == nil {
		return &Table{ByName: map[string]*Instance{}}, nil
	}
	tab := &Table{
		ByName:  map[string]*Instance{},
		Decl:    map[*types.Func]*ast.FuncDecl{},
		InfoFor: map[*types.Func]*types.Info{},
		PkgFor:  map[*types.Func]*types.Package{},
		Fset:    prog.Fset,
		ctxt:    types.NewContext(),
	}
	for _, pkg := range prog.Packages {
		if pkg == nil || pkg.Info == nil || pkg.Types == nil {
			continue
		}
		tab.IndexDecls(pkg)
		if err := tab.collectPackage(pkg); err != nil {
			return nil, err
		}
	}
	return tab, nil
}

// IndexDecls records FuncDecl → origin Func for specialization.
func (tab *Table) IndexDecls(pkg *load.Package) {
	if pkg == nil || pkg.Info == nil {
		return
	}
	for _, f := range pkg.Syntax {
		for _, d := range f.Decls {
			fd, ok := d.(*ast.FuncDecl)
			if !ok || fd.Body == nil {
				continue
			}
			obj, _ := pkg.Info.Defs[fd.Name].(*types.Func)
			if obj == nil {
				continue
			}
			tab.Decl[obj] = fd
			tab.InfoFor[obj] = pkg.Info
			tab.PkgFor[obj] = pkg.Types
		}
	}
}

func (tab *Table) collectPackage(pkg *load.Package) error {
	info := pkg.Info
	for id, inst := range info.Instances {
		obj := info.Uses[id]
		if obj == nil {
			obj = info.Defs[id]
		}
		fn, ok := obj.(*types.Func)
		if !ok {
			continue
		}
		origin := fn.Origin()
		if origin == nil {
			origin = fn
		}
		sig, _ := origin.Type().(*types.Signature)
		if sig == nil || sig.TypeParams().Len() == 0 {
			continue
		}
		args := typeListSlice(inst.TypeArgs)
		if len(args) == 0 || argsContainTypeParam(args) {
			continue
		}
		if _, err := tab.EnsureFunc(origin, args); err != nil {
			return err
		}
	}
	for _, f := range pkg.Syntax {
		var walkErr error
		ast.Inspect(f, func(n ast.Node) bool {
			if walkErr != nil {
				return false
			}
			sel, ok := n.(*ast.SelectorExpr)
			if !ok {
				return true
			}
			s := info.Selections[sel]
			if s == nil || s.Kind() != types.MethodVal {
				return true
			}
			fn, ok := s.Obj().(*types.Func)
			if !ok {
				return true
			}
			origin := fn.Origin()
			if origin == nil {
				return true
			}
			recv := fn.Type().(*types.Signature).Recv().Type()
			named := namedOf(recv)
			if named == nil {
				return true
			}
			args := typeListSlice(named.TypeArgs())
			if len(args) == 0 || argsContainTypeParam(args) {
				return true
			}
			if !methodNeedsMono(origin) {
				return true
			}
			if _, err := tab.EnsureMethod(origin, recv); err != nil {
				walkErr = err
				return false
			}
			return true
		})
		if walkErr != nil {
			return walkErr
		}
	}
	return nil
}

func methodNeedsMono(origin *types.Func) bool {
	origSig := origin.Type().(*types.Signature)
	if origSig.TypeParams() != nil && origSig.TypeParams().Len() > 0 {
		return true
	}
	if r := origSig.Recv(); r != nil {
		rt := types.Unalias(r.Type())
		if _, ok := rt.Underlying().(*types.Interface); ok {
			return false
		}
		if on := namedOf(r.Type()); on != nil && on.Origin().TypeParams().Len() > 0 {
			return true
		}
	}
	return false
}

// EnsureFunc records (or returns) the specialization of a generic function.
func (tab *Table) EnsureFunc(origin *types.Func, args []types.Type) (*Instance, error) {
	if tab.ByName == nil {
		tab.ByName = map[string]*Instance{}
	}
	name := FuncIRName(origin, args)
	if inst, ok := tab.ByName[name]; ok {
		return inst, nil
	}
	subst, err := SubstForFunc(origin, args)
	if err != nil {
		return nil, err
	}
	concrete, err := types.Instantiate(tab.ctxt, origin.Type(), args, false)
	if err != nil {
		return nil, fmt.Errorf("mono: instantiate %s: %w", origin.FullName(), err)
	}
	inst := &Instance{
		Origin: origin,
		Args:   args,
		IRName: name,
		Sig:    concrete.(*types.Signature),
		Subst:  subst,
	}
	tab.ByName[name] = inst
	return inst, nil
}

// EnsureMethod records (or returns) the specialization of a method whose
// receiver is an instantiated generic named type.
func (tab *Table) EnsureMethod(origin *types.Func, concreteRecv types.Type) (*Instance, error) {
	if tab.ByName == nil {
		tab.ByName = map[string]*Instance{}
	}
	named := namedOf(concreteRecv)
	if named == nil {
		return nil, fmt.Errorf("mono: method receiver not named: %s", concreteRecv)
	}
	args := typeListSlice(named.TypeArgs())
	name := FuncIRName(origin, args)
	if inst, ok := tab.ByName[name]; ok {
		return inst, nil
	}
	subst, err := SubstForMethod(origin, concreteRecv)
	if err != nil {
		return nil, err
	}
	concSig := subst.Apply(origin.Type()).(*types.Signature)
	inst := &Instance{
		Origin: origin,
		Args:   args,
		Recv:   concreteRecv,
		IRName: name,
		Sig:    concSig,
		Subst:  subst,
	}
	tab.ByName[name] = inst
	return inst, nil
}

// Lookup returns the instance for an IR name, if any.
func (tab *Table) Lookup(irName string) *Instance {
	if tab == nil {
		return nil
	}
	return tab.ByName[irName]
}

// Package is retained for single-package checks. Prefer Program.
func Package(_ *token.FileSet, _ []*ast.File, pkg *types.Package, info *types.Info) error {
	if pkg == nil || info == nil {
		return nil
	}
	if info.Instances == nil {
		scope := pkg.Scope()
		for _, name := range scope.Names() {
			obj := scope.Lookup(name)
			if fn, ok := obj.(*types.Func); ok {
				if sig, ok := fn.Type().(*types.Signature); ok && sig.TypeParams().Len() > 0 {
					return fmt.Errorf("mono: generic function %s (enable types.Info.Instances)", fn.FullName())
				}
			}
		}
	}
	return nil
}

func namedOf(t types.Type) *types.Named {
	t = types.Unalias(t)
	if p, ok := t.(*types.Pointer); ok {
		t = types.Unalias(p.Elem())
	}
	n, _ := t.(*types.Named)
	return n
}

func typeListSlice(t *types.TypeList) []types.Type {
	if t == nil {
		return nil
	}
	out := make([]types.Type, t.Len())
	for i := 0; i < t.Len(); i++ {
		out[i] = t.At(i)
	}
	return out
}

func argsContainTypeParam(args []types.Type) bool {
	for _, a := range args {
		if ContainsTypeParam(a) {
			return true
		}
	}
	return false
}
