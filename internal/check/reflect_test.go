package check

import (
	"go/ast"
	"go/parser"
	"go/token"
	"go/types"
	"strings"
	"testing"
)

const reflectStub = `
package reflect

type Type interface {
	Elem() Type
	Key() Type
	Kind() int
	NumField() int
	Field(i int) StructField
}

type StructField struct {
	Name string
	Type Type
}

type Value struct{}

func (Value) Type() Type { return nil }

func TypeOf(i any) Type { return nil }
func TypeFor[T any]() Type { return nil }
func SliceOf(t Type) Type { return t }
func PointerTo(t Type) Type { return t }
func PtrTo(t Type) Type { return PointerTo(t) }
func MapOf(k, v Type) Type { return k }
func StructOf(fields []StructField) Type { return nil }
func ArrayOf(n int, t Type) Type { return t }
func ChanOf(dir int, t Type) Type { return t }
func FuncOf(in, out []Type, variadic bool) Type { return nil }
`

type reflectImporter struct {
	fset *token.FileSet
	pkg  *types.Package
}

func (imp *reflectImporter) Import(path string) (*types.Package, error) {
	if path == "unsafe" {
		return types.Unsafe, nil
	}
	if path != "reflect" {
		return nil, nil
	}
	if imp.pkg != nil {
		return imp.pkg, nil
	}
	f, err := parser.ParseFile(imp.fset, "reflect.go", reflectStub, 0)
	if err != nil {
		return nil, err
	}
	conf := types.Config{Sizes: StdSizes, Importer: imp}
	info := &types.Info{}
	pkg, err := conf.Check("reflect", imp.fset, []*ast.File{f}, info)
	if err != nil {
		return nil, err
	}
	imp.pkg = pkg
	return pkg, nil
}

func typeCheckReflect(t *testing.T, src string) (*token.FileSet, *types.Info, []*ast.File) {
	t.Helper()
	fset := token.NewFileSet()
	f, err := parser.ParseFile(fset, "foo.go", src, 0)
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
	imp := &reflectImporter{fset: fset}
	conf := types.Config{
		Sizes:    StdSizes,
		Importer: imp,
	}
	_, err = conf.Check("p", fset, []*ast.File{f}, info)
	if err != nil {
		t.Fatal(err)
	}
	return fset, info, []*ast.File{f}
}

func TestStructOfNonConstantRejected(t *testing.T) {
	src := `package p
import "reflect"
func f() {
	var fields []reflect.StructField
	_ = reflect.StructOf(fields)
}
`
	fset, info, files := typeCheckReflect(t, src)
	diags := CheckReflectConstructors(fset, info, files, nil)
	if len(diags) == 0 {
		t.Fatal("expected StructOf diagnostic")
	}
	if !strings.Contains(diags[0].Message, "reflect.StructOf result is not a Wasm type") {
		t.Fatalf("message: %q", diags[0].Message)
	}
	if !strings.Contains(diags[0].Message, "field list is not a constant") {
		t.Fatalf("message: %q", diags[0].Message)
	}
}

func TestSliceOfMapKeyAccepted(t *testing.T) {
	src := `package p
import "reflect"
func f() {
	m := map[int]string{1: "a"}
	t := reflect.TypeOf(m)
	_ = reflect.SliceOf(t.Key())
	_ = reflect.SliceOf(t.Elem())
	_ = reflect.PointerTo(t)
}
`
	fset, info, files := typeCheckReflect(t, src)
	diags := CheckReflectConstructors(fset, info, files, nil)
	if len(diags) != 0 {
		t.Fatalf("unexpected diagnostics: %v", diags)
	}
}

func TestMapOfEmptyStructAccepted(t *testing.T) {
	src := `package p
import "reflect"
func f() {
	m := map[int]string{1: "a"}
	t := reflect.TypeOf(m)
	empty := reflect.TypeFor[struct{}]()
	_ = reflect.MapOf(t.Key(), empty)
}
`
	fset, info, files := typeCheckReflect(t, src)
	diags := CheckReflectConstructors(fset, info, files, nil)
	if len(diags) != 0 {
		t.Fatalf("unexpected diagnostics: %v", diags)
	}
}

func TestSliceOfConstructedRejected(t *testing.T) {
	// SliceOf(SliceOf(t)) — [][]int is not in B when only map[int]string appears;
	// the inner SliceOf of Key() yields []int (constructed). Outer needs [][]int in U,
	// which requires []int in B — not present.
	src := `package p
import "reflect"
func f() {
	m := map[int]string{1: "a"}
	t := reflect.TypeOf(m)
	_ = reflect.SliceOf(reflect.SliceOf(t.Key()))
}
`
	fset, info, files := typeCheckReflect(t, src)
	diags := CheckReflectConstructors(fset, info, files, nil)
	if len(diags) == 0 {
		t.Fatal("expected SliceOf(SliceOf(...)) diagnostic")
	}
	found := false
	for _, d := range diags {
		if strings.Contains(d.Message, "reflect.SliceOf result is not a Wasm type") {
			found = true
		}
	}
	if !found {
		t.Fatalf("diagnostics: %v", diags)
	}
}

func TestDeadStructOfSkipped(t *testing.T) {
	src := `package p
import "reflect"
func dead() {
	var fields []reflect.StructField
	_ = reflect.StructOf(fields)
}
func main() {}
`
	fset, info, files := typeCheckReflect(t, src)
	reach := ReachableFuncs(nil, info, files)
	diags := CheckReflectConstructors(fset, info, files, reach)
	if len(diags) != 0 {
		t.Fatalf("dead StructOf should be skipped: %v", diags)
	}
}

func TestMapOfNonEmptyRejected(t *testing.T) {
	src := `package p
import "reflect"
func f() {
	k := reflect.TypeOf(0)
	v := reflect.TypeOf("")
	_ = reflect.MapOf(k, v)
}
`
	fset, info, files := typeCheckReflect(t, src)
	diags := CheckReflectConstructors(fset, info, files, nil)
	if len(diags) == 0 {
		t.Fatal("expected MapOf diagnostic for map[int]string mint")
	}
	if !strings.Contains(diags[0].Message, "reflect.MapOf result is not a Wasm type") {
		t.Fatalf("message: %q", diags[0].Message)
	}
}
