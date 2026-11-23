package codegen

import (
	"fmt"
	"strings"

	"github.com/achille-roussel/wasmgc-go/internal/ir"
	"github.com/achille-roussel/wasmgc-go/internal/wasm"
)

// emitPrelude writes the fixed prelude from 03-type-mapping.md.
// Indices are the IdxGo* constants. Always emitted so the intern table
// is deterministic for stream G.
func (g *gen) emitPrelude() {
	// 0: $go.object (sub (struct)) — open, empty
	obj := g.wm.AddType(wasm.SubOpen(nil, wasm.StructType()))
	g.types.ByName["$go.object"] = obj

	// 1: $go.bytes (array (mut i8))
	bytes := g.wm.AddArrayType(wasm.Field(wasm.StorageI8(), true))
	g.types.ByName["$go.bytes"] = bytes

	// 2: $go.i16s (array (mut i16))
	i16s := g.wm.AddArrayType(wasm.Field(wasm.StorageI16(), true))
	g.types.ByName["$go.i16s"] = i16s

	// 3: $go.string (sub $go.object (struct (field $data (ref $go.bytes)) (field $off i32) (field $len i32)))
	str := g.wm.AddType(wasm.SubFinal([]uint32{obj}, wasm.StructType(
		wasm.Field(wasm.StorageVal(wasm.Ref(wasm.TypeIndexHeap(bytes))), false),
		wasm.Field(wasm.StorageVal(wasm.I32()), false),
		wasm.Field(wasm.StorageVal(wasm.I32()), false),
	)))
	g.types.ByName["$go.string"] = str

	// 4: $go.type
	typ := g.wm.AddType(wasm.SubFinal([]uint32{obj}, wasm.StructType(
		wasm.Field(wasm.StorageVal(wasm.I32()), false),                          // id
		wasm.Field(wasm.StorageVal(wasm.I32()), false),                          // kind
		wasm.Field(wasm.StorageVal(wasm.RefNull(wasm.TypeIndexHeap(str))), false), // name
		wasm.Field(wasm.StorageVal(wasm.RefNull(wasm.TypeIndexHeap(obj))), false), // extra
	)))
	g.types.ByName["$go.type"] = typ

	// 5: $go.itab
	itab := g.wm.AddType(wasm.SubOpen([]uint32{obj}, wasm.StructType(
		wasm.Field(wasm.StorageVal(wasm.Ref(wasm.TypeIndexHeap(typ))), false),
	)))
	g.types.ByName["$go.itab"] = itab

	// 6: $go.iface
	iface := g.wm.AddType(wasm.SubFinal([]uint32{obj}, wasm.StructType(
		wasm.Field(wasm.StorageVal(wasm.Ref(wasm.TypeIndexHeap(itab))), false),
		wasm.Field(wasm.StorageVal(wasm.AnyRef()), false),
	)))
	g.types.ByName["$go.iface"] = iface

	// 7: $go.panicObj — self-referential via $next; explicit rec group.
	panicIdx := g.wm.NumTypes()
	panicComp := wasm.StructType(
		wasm.Field(wasm.StorageVal(wasm.RefNull(wasm.TypeIndexHeap(iface))), false),     // value
		wasm.Field(wasm.StorageVal(wasm.I32()), true),                                    // recovered mut
		wasm.Field(wasm.StorageVal(wasm.RefNull(wasm.TypeIndexHeap(panicIdx))), false), // next
	)
	got := g.wm.AddRecGroup(wasm.Rec(wasm.SubFinal([]uint32{obj}, panicComp)))
	g.types.ByName["$go.panicObj"] = got
}

func (g *gen) emitProgramTypes() error {
	// Emit IR TypeDefs with dependency-first ordering so field/super lookups
	// succeed (e.g. $go.clos.method.* fields may reference $go.struct.*).
	// inProgress breaks cycles for self-ref types ($go.defer) handled via RecGroup.
	inProgress := map[string]bool{}
	var emitOne func(td *ir.TypeDef) error
	emitNamed := func(name string) error {
		if name == "" {
			return nil
		}
		if _, ok := g.types.ByName[name]; ok {
			return nil
		}
		if inProgress[name] {
			return nil // cycle; self-ref resolved inside addTypeDef RecGroup
		}
		td := g.m.LookupType(name)
		if td == nil {
			return nil // prelude / abstract
		}
		return emitOne(td)
	}
	emitOne = func(td *ir.TypeDef) error {
		if _, ok := g.types.ByName[td.Name]; ok {
			return nil
		}
		if inProgress[td.Name] {
			return nil
		}
		inProgress[td.Name] = true
		if td.Super != "" {
			if err := emitNamed(td.Super); err != nil {
				return err
			}
		}
		for _, f := range td.Fields {
			if r, ok := f.Type.(*ir.Ref); ok && r.Named != "" && r.Named != td.Name {
				if err := emitNamed(r.Named); err != nil {
					return err
				}
			}
		}
		for _, p := range td.Params {
			if r, ok := p.(*ir.Ref); ok && r.Named != "" && r.Named != td.Name {
				if err := emitNamed(r.Named); err != nil {
					return err
				}
			}
		}
		for _, r := range td.Results {
			if ref, ok := r.(*ir.Ref); ok && ref.Named != "" && ref.Named != td.Name {
				if err := emitNamed(ref.Named); err != nil {
					return err
				}
			}
		}
		if td.Elem != nil {
			if r, ok := td.Elem.(*ir.Ref); ok && r.Named != "" && r.Named != td.Name {
				if err := emitNamed(r.Named); err != nil {
					return err
				}
			}
		}
		idx, err := g.addTypeDef(td)
		if err != nil {
			return err
		}
		g.types.ByName[td.Name] = idx
		delete(inProgress, td.Name)
		return nil
	}
	// Prefer $go.clos / cells early (open base before subtypes).
	for _, td := range g.m.Types {
		if td.Name == "$go.clos" || strings.HasPrefix(td.Name, "$go.cell.") {
			if err := emitOne(td); err != nil {
				return err
			}
		}
	}
	for _, td := range g.m.Types {
		if err := emitOne(td); err != nil {
			return err
		}
	}
	return nil
}

func (g *gen) addTypeDef(td *ir.TypeDef) (uint32, error) {
	var supers []uint32
	if td.Super != "" {
		sup, ok := g.types.ByName[td.Super]
		if !ok {
			return 0, fmt.Errorf("codegen: type %s: unknown super %s", td.Name, td.Super)
		}
		supers = append(supers, sup)
	}
	switch td.Kind {
	case ir.TypeStruct:
		// $go.defer is self-referential via link; emit as a rec group.
		if td.Name == "$go.defer" {
			obj := g.types.ByName["$go.object"]
			clos, ok := g.types.ByName["$go.clos"]
			if !ok {
				return 0, fmt.Errorf("codegen: type $go.defer: $go.clos not emitted yet")
			}
			deferIdx := g.wm.NumTypes()
			comp := wasm.StructType(
				wasm.Field(wasm.StorageVal(wasm.Ref(wasm.TypeIndexHeap(clos))), false),
				wasm.Field(wasm.StorageVal(wasm.RefNull(wasm.TypeIndexHeap(deferIdx))), true),
			)
			return g.wm.AddRecGroup(wasm.Rec(wasm.SubFinal([]uint32{obj}, comp))), nil
		}
		fields := make([]wasm.FieldType, 0, len(td.Fields))
		for _, f := range td.Fields {
			st, err := g.storageType(f.Type)
			if err != nil {
				return 0, fmt.Errorf("codegen: type %s field %s: %w", td.Name, f.Name, err)
			}
			fields = append(fields, wasm.Field(st, f.Mut))
		}
		comp := wasm.StructType(fields...)
		if len(supers) > 0 {
			// $go.clos is an open base for concrete closures (M5).
			if td.Name == "$go.clos" {
				return g.wm.AddType(wasm.SubOpen(supers, comp)), nil
			}
			return g.wm.AddType(wasm.SubFinal(supers, comp)), nil
		}
		return g.wm.AddType(wasm.FinalType(comp)), nil
	case ir.TypeArray:
		st, err := g.storageType(td.Elem)
		if err != nil {
			return 0, err
		}
		return g.wm.AddArrayType(wasm.Field(st, td.ElemMut)), nil
	case ir.TypeFunc:
		params, err := g.valTypes(td.Params)
		if err != nil {
			return 0, err
		}
		results, err := g.valTypes(td.Results)
		if err != nil {
			return 0, err
		}
		idx := g.wm.AddFuncType(params, results)
		g.types.FuncSig[sigKey(params, results)] = idx
		return idx, nil
	default:
		return 0, fmt.Errorf("codegen: unknown type kind for %s", td.Name)
	}
}

func (g *gen) storageType(t ir.Type) (wasm.StorageType, error) {
	if n, ok := t.(*ir.NumType); ok {
		switch n.Kind {
		case ir.I8:
			return wasm.StorageI8(), nil
		case ir.I16:
			return wasm.StorageI16(), nil
		}
	}
	vt, err := g.valType(t)
	if err != nil {
		return wasm.StorageType{}, err
	}
	return wasm.StorageVal(vt), nil
}

func (g *gen) valTypes(ts []ir.Type) ([]wasm.ValType, error) {
	out := make([]wasm.ValType, len(ts))
	for i, t := range ts {
		vt, err := g.valType(t)
		if err != nil {
			return nil, err
		}
		out[i] = vt
	}
	return out, nil
}

func (g *gen) valType(t ir.Type) (wasm.ValType, error) {
	if t == nil {
		return wasm.ValType{}, fmt.Errorf("nil type")
	}
	switch t := t.(type) {
	case *ir.NumType:
		switch t.Kind {
		case ir.I32:
			return wasm.I32(), nil
		case ir.I64:
			return wasm.I64(), nil
		case ir.F32:
			return wasm.F32(), nil
		case ir.F64:
			return wasm.F64(), nil
		case ir.I8, ir.I16:
			return wasm.ValType{}, fmt.Errorf("packed type %s is not a value type", t)
		}
	case *ir.Exn:
		return wasm.ExnRef(), nil
	case *ir.Extern:
		return wasm.ExternRef(), nil
	case *ir.Ref:
		ht, err := g.heapType(t)
		if err != nil {
			return wasm.ValType{}, err
		}
		if t.Null {
			return wasm.RefNull(ht), nil
		}
		return wasm.Ref(ht), nil
	case *ir.TypeDef:
		return wasm.ValType{}, fmt.Errorf("type def %s is not a value type; use ref", t.Name)
	case *ir.High:
		return wasm.ValType{}, fmt.Errorf("high type %s remains in low module", t)
	}
	return wasm.ValType{}, fmt.Errorf("unsupported type %s", t)
}

func (g *gen) heapType(r *ir.Ref) (wasm.HeapType, error) {
	if r.Func != nil {
		idx, ok := g.types.ByName[r.Func.Name]
		if !ok {
			return wasm.HeapType{}, fmt.Errorf("unknown func type %s", r.Func.Name)
		}
		return wasm.TypeIndexHeap(idx), nil
	}
	if r.Named != "" {
		if idx, ok := g.types.ByName[r.Named]; ok {
			return wasm.TypeIndexHeap(idx), nil
		}
		// Allow abstract names written as Named for heap kinds.
		switch r.Named {
		case "any":
			return wasm.HeapTypeAny(), nil
		case "eq":
			return wasm.HeapTypeEq(), nil
		case "i31":
			return wasm.HeapTypeI31(), nil
		case "struct":
			return wasm.HeapTypeStruct(), nil
		case "array":
			return wasm.HeapTypeArray(), nil
		case "none":
			return wasm.HeapTypeNone(), nil
		case "nofunc":
			return wasm.HeapTypeNoFunc(), nil
		case "noextern":
			return wasm.HeapTypeNoExtern(), nil
		case "func":
			return wasm.HeapTypeFunc(), nil
		}
		return wasm.HeapType{}, fmt.Errorf("unknown heap type %s", r.Named)
	}
	switch r.Heap {
	case ir.HeapAny:
		return wasm.HeapTypeAny(), nil
	case ir.HeapEq:
		return wasm.HeapTypeEq(), nil
	case ir.HeapI31:
		return wasm.HeapTypeI31(), nil
	case ir.HeapStruct:
		return wasm.HeapTypeStruct(), nil
	case ir.HeapArray:
		return wasm.HeapTypeArray(), nil
	case ir.HeapNone:
		return wasm.HeapTypeNone(), nil
	case ir.HeapNoFunc:
		return wasm.HeapTypeNoFunc(), nil
	case ir.HeapNoExtern:
		return wasm.HeapTypeNoExtern(), nil
	case ir.HeapFunc:
		return wasm.HeapTypeFunc(), nil
	}
	return wasm.HeapType{}, fmt.Errorf("invalid heap type")
}

func (g *gen) heapTypeOf(t ir.Type) (wasm.HeapType, error) {
	switch t := t.(type) {
	case *ir.Ref:
		return g.heapType(t)
	case *ir.TypeDef:
		idx, ok := g.types.ByName[t.Name]
		if !ok {
			return wasm.HeapType{}, fmt.Errorf("unknown type %s", t.Name)
		}
		return wasm.TypeIndexHeap(idx), nil
	default:
		// Type immediate may be written as a name-only Ref via parse.
		name := ir.TypeString(t)
		if idx, ok := g.types.ByName[name]; ok {
			return wasm.TypeIndexHeap(idx), nil
		}
		if strings.HasPrefix(name, "$") {
			return wasm.HeapType{}, fmt.Errorf("unknown type immediate %s", name)
		}
		return g.heapType(&ir.Ref{Named: name})
	}
}

func (g *gen) typeIndexOf(t ir.Type) (uint32, error) {
	name := typeName(t)
	if name == "" {
		return 0, fmt.Errorf("no type index for %s", t)
	}
	idx, ok := g.types.ByName[name]
	if !ok {
		return 0, fmt.Errorf("unknown type %s", name)
	}
	return idx, nil
}

func typeName(t ir.Type) string {
	switch t := t.(type) {
	case *ir.TypeDef:
		return t.Name
	case *ir.Ref:
		if t.Named != "" {
			return t.Named
		}
		if t.Func != nil {
			return t.Func.Name
		}
	}
	s := ir.TypeString(t)
	if strings.HasPrefix(s, "$") {
		return s
	}
	return ""
}

func (g *gen) internFuncType(params, results []wasm.ValType) uint32 {
	key := sigKey(params, results)
	if idx, ok := g.types.FuncSig[key]; ok {
		return idx
	}
	idx := g.wm.AddFuncType(params, results)
	g.types.FuncSig[key] = idx
	return idx
}

func sigKey(params, results []wasm.ValType) string {
	var b strings.Builder
	b.WriteByte('(')
	for i, p := range params {
		if i > 0 {
			b.WriteByte(',')
		}
		fmt.Fprintf(&b, "%d:%d", p.Kind(), p.HeapCode())
	}
	b.WriteByte(')')
	for i, r := range results {
		if i > 0 {
			b.WriteByte(',')
		}
		fmt.Fprintf(&b, "%d:%d", r.Kind(), r.HeapCode())
	}
	return b.String()
}

func (g *gen) fieldIndex(typeName, field string) (uint32, error) {
	td := g.m.LookupType(typeName)
	if td == nil {
		// Prelude types not in IR module — use fixed field layouts.
		switch typeName {
		case "$go.string":
			switch field {
			case "data", "0":
				return 0, nil
			case "off", "1":
				return 1, nil
			case "len", "2":
				return 2, nil
			}
		case "$go.iface":
			switch field {
			case "itab", "0":
				return 0, nil
			case "data", "1":
				return 1, nil
			}
		case "$go.panicObj":
			switch field {
			case "value", "0":
				return 0, nil
			case "recovered", "1":
				return 1, nil
			case "next", "2":
				return 2, nil
			}
		case "$go.defer":
			switch field {
			case "fn", "0":
				return 0, nil
			case "link", "1":
				return 1, nil
			}
		case "$go.g":
			switch field {
			case "atomicstatus", "0":
				return 0, nil
			case "throwsplit", "1":
				return 1, nil
			case "_panic", "2":
				return 2, nil
			case "racing", "3":
				return 3, nil
			}
		}
		if strings.HasPrefix(typeName, "$go.slice.") {
			switch field {
			case "data", "0":
				return 0, nil
			case "off", "1":
				return 1, nil
			case "len", "2":
				return 2, nil
			case "cap", "3":
				return 3, nil
			}
		}
		if strings.HasPrefix(typeName, "$go.class.") {
			switch field {
			case "get", "0":
				return 0, nil
			case "set", "1":
				return 1, nil
			}
		}
		if strings.HasPrefix(typeName, "$go.ptr.") {
			switch field {
			case "class", "0":
				return 0, nil
			case "base", "1":
				return 1, nil
			case "index", "2":
				return 2, nil
			}
		}
		return 0, fmt.Errorf("unknown type %s for field %s", typeName, field)
	}
	if n, err := parseUint(field); err == nil {
		return n, nil
	}
	for i, f := range td.Fields {
		if f.Name == field {
			return uint32(i), nil
		}
	}
	return 0, fmt.Errorf("type %s has no field %s", typeName, field)
}

func parseUint(s string) (uint32, error) {
	var n uint32
	if s == "" {
		return 0, fmt.Errorf("empty")
	}
	for _, c := range s {
		if c < '0' || c > '9' {
			return 0, fmt.Errorf("not a number")
		}
		n = n*10 + uint32(c-'0')
	}
	return n, nil
}
