package desc

import (
	"go/types"
	"strings"
	"testing"

	"github.com/achille-roussel/wasmgc-go/internal/ir"
)

func TestSanitizeAnonIface(t *testing.T) {
	in := "iface.interface{Unwrap() error}"
	got := Sanitize(in)
	for _, bad := range []string{"{", "}", "(", ")", " ", "."} {
		if strings.Contains(got, bad) {
			t.Fatalf("Sanitize(%q)=%q still contains %q", in, got, bad)
		}
	}
	if !strings.Contains(got, "Unwrap") {
		t.Fatalf("Sanitize dropped method name: %q", got)
	}
}

func TestKindFromKeyMap(t *testing.T) {
	kind, name, comparable := kindFromKey("map[int]struct{}")
	if kind != KindMap {
		t.Fatalf("kind=%d, want KindMap=%d", kind, KindMap)
	}
	if name != "map[int]struct{}" {
		t.Fatalf("name=%q", name)
	}
	if comparable {
		t.Fatal("maps are not comparable")
	}
}

func TestTypeKeyMapEmptyStruct(t *testing.T) {
	empty := types.NewStruct(nil, nil)
	m := types.NewMap(types.Typ[types.Int], empty)
	got := TypeKey(m)
	if got != "map[int]struct{}" {
		t.Fatalf("TypeKey=%q, want map[int]struct{}", got)
	}
	if TypeKey(empty) != "struct{}" {
		t.Fatalf("empty struct key=%q", TypeKey(empty))
	}
}

func TestTypeKeySignatureIgnoresParamNames(t *testing.T) {
	intT := types.Typ[types.Int]
	named := types.NewSignatureType(nil, nil, nil,
		types.NewTuple(types.NewVar(0, nil, "x", intT)),
		types.NewTuple(types.NewVar(0, nil, "", intT)),
		false,
	)
	anon := types.NewSignatureType(nil, nil, nil,
		types.NewTuple(types.NewVar(0, nil, "", intT)),
		types.NewTuple(types.NewVar(0, nil, "", intT)),
		false,
	)
	gotNamed, gotAnon := TypeKey(named), TypeKey(anon)
	if gotNamed != gotAnon {
		t.Fatalf("named=%q anon=%q, want identical keys", gotNamed, gotAnon)
	}
	if gotAnon != "func(int)_int" {
		t.Fatalf("TypeKey=%q, want func(int)_int", gotAnon)
	}
}

func TestCloseUniverse(t *testing.T) {
	mod := &ir.Module{}
	g := &gen{
		mod:    mod,
		types:  map[string]*typeRec{},
		itabs:  map[string]*itabRec{},
		ifaces: map[string]bool{},
	}
	intT := types.Typ[types.Int]
	g.needTypeKey("int")
	g.types["int"].goType = intT
	g.closeUniverse()

	want := []string{"*int", "[]int", "map[int]struct{}"}
	for _, k := range want {
		tr := g.types[k]
		if tr == nil {
			t.Fatalf("missing closed type %q; have %#v", k, keysOf(g.types))
		}
		if tr.goType == nil {
			t.Fatalf("%s: nil goType", k)
		}
		if k == "map[int]struct{}" {
			if kindOf(tr.goType) != KindMap {
				t.Fatalf("map kind=%d", kindOf(tr.goType))
			}
		}
	}
}

func keysOf(m map[string]*typeRec) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	return out
}

func TestFieldTagEmission(t *testing.T) {
	mod := &ir.Module{}
	g := &gen{
		mod:    mod,
		types:  map[string]*typeRec{},
		itabs:  map[string]*itabRec{},
		ifaces: map[string]bool{},
	}
	g.ensurePreludeShapes()
	g.ensureExtraShapes()

	fields := []*types.Var{
		types.NewField(0, nil, "Name", types.Typ[types.String], false),
	}
	tags := []string{`json:"name"`}
	st := types.NewStruct(fields, tags)
	tr := &typeRec{key: "struct{Name string}", kind: KindStruct, goType: st, global: "$go.type.struct_Name_string"}
	g.types[tr.key] = tr
	g.needTypeKey("string")
	if g.types["string"] != nil {
		g.types["string"].goType = types.Typ[types.String]
	}

	fn := g.emitFieldTagFunc(tr, st, fields)
	f := mod.LookupFunc(fn)
	if f == nil {
		t.Fatalf("missing fieldTag func %s", fn)
	}
	// Body must mention the tag bytes somehow via data segment.
	found := false
	for _, d := range mod.Data {
		if string(d.Bytes) == `json:"name"` {
			found = true
			break
		}
	}
	if !found {
		t.Fatalf("tag string not in data segments; data=%v", mod.Data)
	}
}

func TestTypeKeyStructAliasFieldNames(t *testing.T) {
	pkg := types.NewPackage("main", "main")
	intAlias := types.NewAlias(types.NewTypeName(1, pkg, "Int", nil), types.Typ[types.Int])
	fInt := types.NewField(0, pkg, "int", types.Typ[types.Int], true)
	fIntAlias := types.NewField(0, pkg, "Int", intAlias, true)
	s1 := types.NewStruct([]*types.Var{fInt}, nil)
	s2 := types.NewStruct([]*types.Var{fIntAlias}, nil)
	k1, k2 := TypeKey(s1), TypeKey(s2)
	if k1 == k2 {
		t.Fatalf("struct{int} and struct{Int} keys collided: %q", k1)
	}
	if !strings.Contains(k1, "int:") || !strings.Contains(k2, "Int:") {
		t.Fatalf("keys=%q %q", k1, k2)
	}
}

func TestTypeKeyStructLocalAliasUnderlying(t *testing.T) {
	pkg := types.NewPackage("main", "main")
	c32 := types.NewAlias(types.NewTypeName(10, pkg, "C", nil), types.Typ[types.Int32])
	cU32 := types.NewAlias(types.NewTypeName(20, pkg, "C", nil), types.Typ[types.Uint32])
	s1 := types.NewStruct([]*types.Var{types.NewField(0, pkg, "C", c32, true)}, nil)
	s2 := types.NewStruct([]*types.Var{types.NewField(0, pkg, "C", cU32, true)}, nil)
	k1, k2 := TypeKey(s1), TypeKey(s2)
	if k1 == k2 {
		t.Fatalf("struct{C=int32} and struct{C=uint32} keys collided: %q", k1)
	}
}
