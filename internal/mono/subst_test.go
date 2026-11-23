package mono_test

import (
	"go/ast"
	"go/parser"
	"go/token"
	"go/types"
	"testing"

	"github.com/achille-roussel/wasmgc-go/internal/mono"
)

func TestSubstForMethodPair(t *testing.T) {
	fset := token.NewFileSet()
	src := `package main
type Pair[T any] struct{ X, Y T }
func (p Pair[T]) First() T { return p.X }
func main() { p := Pair[int]{X: 9}; _ = p.First() }
`
	f, err := parser.ParseFile(fset, "main.go", src, 0)
	if err != nil {
		t.Fatal(err)
	}
	info := &types.Info{
		Types:      make(map[ast.Expr]types.TypeAndValue),
		Defs:       make(map[*ast.Ident]types.Object),
		Uses:       make(map[*ast.Ident]types.Object),
		Selections: make(map[*ast.SelectorExpr]*types.Selection),
		Instances:  make(map[*ast.Ident]types.Instance),
	}
	cfg := &types.Config{Sizes: &types.StdSizes{WordSize: 4, MaxAlign: 4}}
	if _, err := cfg.Check("main", fset, []*ast.File{f}, info); err != nil {
		t.Fatal(err)
	}
	var origin *types.Func
	for id, obj := range info.Defs {
		if fn, ok := obj.(*types.Func); ok && id.Name == "First" {
			origin = fn
		}
	}
	if origin == nil {
		t.Fatal("no First")
	}
	var concRecv types.Type
	ast.Inspect(f, func(n ast.Node) bool {
		sel, ok := n.(*ast.SelectorExpr)
		if !ok || sel.Sel.Name != "First" {
			return true
		}
		fn := info.Selections[sel].Obj().(*types.Func)
		concRecv = fn.Type().(*types.Signature).Recv().Type()
		return true
	})
	subst, err := mono.SubstForMethod(origin, concRecv)
	if err != nil {
		t.Fatal(err)
	}
	t.Logf("subst len=%d", len(subst))
	for tp, ty := range subst {
		t.Logf("  %s -> %s", tp, ty)
	}
	recv := origin.Type().(*types.Signature).Recv().Type()
	applied := subst.Apply(recv)
	t.Logf("recv %s -> %s", recv, applied)
	named, ok := applied.(*types.Named)
	if !ok {
		t.Fatalf("applied not named: %T", applied)
	}
	st := named.Underlying().(*types.Struct)
	ft := st.Field(0).Type()
	t.Logf("field X: %s containTP=%v", ft, mono.ContainsTypeParam(ft))
	if mono.ContainsTypeParam(ft) {
		t.Fatalf("field still has type param")
	}
	if ft != types.Typ[types.Int] && types.Identical(ft, types.Typ[types.Int]) == false {
		// allow identical int
		if !types.Identical(ft, types.Typ[types.Int]) {
			t.Fatalf("want int, got %s", ft)
		}
	}
}
