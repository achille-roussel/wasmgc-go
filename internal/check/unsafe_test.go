package check

import (
	"go/ast"
	"go/parser"
	"go/token"
	"go/types"
	"strings"
	"testing"
)

type simpleImporter struct{}

func (simpleImporter) Import(path string) (*types.Package, error) {
	if path == "unsafe" {
		return types.Unsafe, nil
	}
	return nil, nil
}

func typeCheck(t *testing.T, src string) (*token.FileSet, *types.Info, []*ast.File) {
	t.Helper()
	fset := token.NewFileSet()
	f, err := parser.ParseFile(fset, "foo.go", src, 0)
	if err != nil {
		t.Fatal(err)
	}
	info := &types.Info{
		Types: make(map[ast.Expr]types.TypeAndValue),
		Defs:  make(map[*ast.Ident]types.Object),
		Uses:  make(map[*ast.Ident]types.Object),
	}
	conf := types.Config{
		Sizes: StdSizes,
		Importer: simpleImporter{},
	}
	_, err = conf.Check("p", fset, []*ast.File{f}, info)
	if err != nil {
		t.Fatal(err)
	}
	return fset, info, []*ast.File{f}
}

func TestRejectUintptrUnsafePointer(t *testing.T) {
	src := `package p
import "unsafe"
func f(p *int) {
	_ = uintptr(unsafe.Pointer(p))
}
`
	fset, info, files := typeCheck(t, src)
	diags := CheckUnsafe(fset, info, files)
	if len(diags) == 0 {
		t.Fatal("expected diagnostic")
	}
	if !strings.Contains(diags[0].Message, "unsupported unsafe operation") {
		t.Fatalf("message: %q", diags[0].Message)
	}
	if !strings.Contains(diags[0].Expression, "uintptr(unsafe.Pointer(p))") {
		t.Fatalf("expression: %q", diags[0].Expression)
	}
	s := diags[0].String()
	if !strings.Contains(s, "expression:") {
		t.Fatalf("diagnostic format: %s", s)
	}
}

func TestRejectUnsafeAdd(t *testing.T) {
	src := `package p
import "unsafe"
func f(p unsafe.Pointer) {
	_ = unsafe.Add(p, 4)
}
`
	fset, info, files := typeCheck(t, src)
	diags := CheckUnsafe(fset, info, files)
	if len(diags) == 0 {
		t.Fatal("expected diagnostic")
	}
	if !strings.Contains(diags[0].Expression, "unsafe.Add") {
		t.Fatalf("expression: %q", diags[0].Expression)
	}
}

func TestAllowSizeofAndSliceData(t *testing.T) {
	src := `package p
import "unsafe"
func f(s []byte) {
	_ = unsafe.Sizeof(0)
	_ = unsafe.SliceData(s)
	_ = unsafe.String(unsafe.SliceData(s), len(s))
}
`
	fset, info, files := typeCheck(t, src)
	diags := CheckUnsafe(fset, info, files)
	if len(diags) != 0 {
		t.Fatalf("unexpected: %v", diags)
	}
}

func TestRejectUintptrPointerOrder(t *testing.T) {
	src := `package p
import "unsafe"
func f(a, b *int) bool {
	return uintptr(unsafe.Pointer(a)) < uintptr(unsafe.Pointer(b))
}
`
	fset, info, files := typeCheck(t, src)
	diags := CheckUnsafe(fset, info, files)
	if len(diags) < 2 {
		t.Fatalf("expected uintptr conversions rejected, got %v", diags)
	}
}
