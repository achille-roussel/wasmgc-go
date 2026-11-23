package desc

import (
	"go/types"
	"testing"

	"github.com/achille-roussel/wasmgc-go/internal/ir"
)

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
