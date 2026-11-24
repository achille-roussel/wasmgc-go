package mono

import (
	"go/ast"
	"go/parser"
	"go/token"
	"go/types"
	"strings"
	"testing"
)

func TestLocalTypeSuffixDisambiguatesPackageAndLocal(t *testing.T) {
	src := `package p
type A struct{ X int }
func f() {
	type A struct{}
	var _ A
}
`
	fset := token.NewFileSet()
	file, err := parser.ParseFile(fset, "p.go", src, 0)
	if err != nil {
		t.Fatal(err)
	}
	cfg := &types.Config{}
	info := &types.Info{Defs: map[*ast.Ident]types.Object{}}
	pkg, err := cfg.Check("p", fset, []*ast.File{file}, info)
	if err != nil {
		t.Fatal(err)
	}
	pkgA := pkg.Scope().Lookup("A").(*types.TypeName)
	if s := LocalTypeSuffix(pkgA); s != "" {
		t.Fatalf("package A suffix=%q, want empty", s)
	}
	var localA *types.TypeName
	for _, obj := range info.Defs {
		tn, ok := obj.(*types.TypeName)
		if !ok || tn == pkgA {
			continue
		}
		if tn.Name() == "A" {
			localA = tn
			break
		}
	}
	if localA == nil {
		t.Fatal("local A not found")
	}
	suf := LocalTypeSuffix(localA)
	if !strings.HasPrefix(suf, "_p") {
		t.Fatalf("local A suffix=%q, want _pN", suf)
	}
	if NamedTypeKey(pkgA.Type().(*types.Named)) == NamedTypeKey(localA.Type().(*types.Named)) {
		t.Fatal("package and local A share NamedTypeKey")
	}
}
