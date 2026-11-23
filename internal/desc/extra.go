package desc

import (
	"fmt"
	"go/types"

	"github.com/achille-roussel/wasmgc-go/internal/ir"
)

// Kind-specific $extra shapes (docs/design/03, 05). Interned only when a
// reachable type needs them so empty_main / println type sections stay small.

func (g *gen) ensureExtraShapes() {
	anyT := &ir.Ref{Null: true, Heap: ir.HeapAny}
	g.mod.InternType(&ir.TypeDef{
		Name:    "$go.fn.getField",
		Kind:    ir.TypeFunc,
		Params:  []ir.Type{anyT, ir.TypeI32},
		Results: []ir.Type{anyT},
	})
	g.mod.InternType(&ir.TypeDef{
		Name:   "$go.fn.setField",
		Kind:   ir.TypeFunc,
		Params: []ir.Type{anyT, ir.TypeI32, anyT},
	})
	g.mod.InternType(&ir.TypeDef{
		Name:    "$go.fn.index",
		Kind:    ir.TypeFunc,
		Params:  []ir.Type{anyT, ir.TypeI32},
		Results: []ir.Type{anyT},
	})
	g.mod.InternType(&ir.TypeDef{
		Name:   "$go.fn.setIndex",
		Kind:   ir.TypeFunc,
		Params: []ir.Type{anyT, ir.TypeI32, anyT},
	})
	g.mod.InternType(&ir.TypeDef{
		Name:    "$go.fn.len",
		Kind:    ir.TypeFunc,
		Params:  []ir.Type{anyT},
		Results: []ir.Type{ir.TypeI32},
	})
	g.mod.InternType(&ir.TypeDef{
		Name:    "$go.fn.fieldType",
		Kind:    ir.TypeFunc,
		Params:  []ir.Type{ir.TypeI32},
		Results: []ir.Type{&ir.Ref{Named: "$go.type"}},
	})
	g.mod.InternType(&ir.TypeDef{
		Name:    "$go.fn.fieldName",
		Kind:    ir.TypeFunc,
		Params:  []ir.Type{ir.TypeI32},
		Results: []ir.Type{&ir.Ref{Named: "$go.string"}},
	})
	g.mod.InternType(&ir.TypeDef{
		Name:    "$go.fn.fieldTag",
		Kind:    ir.TypeFunc,
		Params:  []ir.Type{ir.TypeI32},
		Results: []ir.Type{&ir.Ref{Named: "$go.string"}},
	})
	structExtraFields := []ir.Field{
		{Name: "numField", Type: ir.TypeI32},
		{Name: "getField", Type: &ir.Ref{Named: "$go.fn.getField"}},
		{Name: "setField", Type: &ir.Ref{Named: "$go.fn.setField"}},
		{Name: "fieldType", Type: &ir.Ref{Named: "$go.fn.fieldType"}},
		{Name: "fieldName", Type: &ir.Ref{Named: "$go.fn.fieldName"}},
		{Name: "fieldTag", Type: &ir.Ref{Named: "$go.fn.fieldTag"}},
	}
	if td := g.mod.LookupType("$go.extra.struct"); td != nil {
		td.Fields = structExtraFields
	} else {
		g.mod.InternType(&ir.TypeDef{
			Name: "$go.extra.struct", Kind: ir.TypeStruct, Super: "$go.object",
			Fields: structExtraFields,
		})
	}
	sliceExtraFields := []ir.Field{
		{Name: "elem", Type: &ir.Ref{Named: "$go.type"}},
		{Name: "index", Type: &ir.Ref{Named: "$go.fn.index"}},
		{Name: "setIndex", Type: &ir.Ref{Named: "$go.fn.setIndex"}},
		{Name: "len", Type: &ir.Ref{Named: "$go.fn.len"}},
	}
	if td := g.mod.LookupType("$go.extra.slice"); td != nil {
		td.Fields = sliceExtraFields
	} else {
		g.mod.InternType(&ir.TypeDef{
			Name: "$go.extra.slice", Kind: ir.TypeStruct, Super: "$go.object",
			Fields: sliceExtraFields,
		})
	}
	g.mod.InternType(&ir.TypeDef{
		Name:  "$go.extra.ptr",
		Kind:  ir.TypeStruct,
		Super: "$go.object",
		Fields: []ir.Field{
			{Name: "elem", Type: &ir.Ref{Named: "$go.type"}},
		},
	})
	g.mod.InternType(&ir.TypeDef{
		Name:  "$go.extra.map",
		Kind:  ir.TypeStruct,
		Super: "$go.object",
		Fields: []ir.Field{
			{Name: "key", Type: &ir.Ref{Named: "$go.type"}},
			{Name: "elem", Type: &ir.Ref{Named: "$go.type"}},
		},
	})
	// Reflect Set* on New().Elem() scalars mutates the interface box in place.
	g.ensureMutableBoxes()
}

// emitTypeExtra builds $extra for tr and returns an IR expr for the extra
// field (possibly ref.null). May register dependent type keys.
func (g *gen) emitTypeExtra(tr *typeRec) ir.Expr {
	nullObj := &ir.Instr{Op: ir.OpRefNull, Type: &ir.Ref{Null: true, Named: "$go.object"}}
	if tr.goType == nil && tr.kind != KindStruct && tr.kind != KindSlice &&
		tr.kind != KindArray && tr.kind != KindMap && tr.kind != KindPointer {
		return nullObj
	}
	switch tr.kind {
	case KindStruct:
		return g.emitStructExtra(tr)
	case KindSlice, KindArray:
		return g.emitSliceExtra(tr)
	case KindPointer:
		return g.emitPtrExtra(tr)
	case KindMap:
		return g.emitMapExtra(tr)
	default:
		return nullObj
	}
}

func (g *gen) emitStructExtra(tr *typeRec) ir.Expr {
	st, wasmName := g.structShape(tr)
	if st == nil || wasmName == "" {
		return &ir.Instr{Op: ir.OpRefNull, Type: &ir.Ref{Null: true, Named: "$go.object"}}
	}
	g.ensureExtraShapes()
	// Collect non-zero-size fields (Wasm layout skips zero-size).
	var fields []*types.Var
	for i := 0; i < st.NumFields(); i++ {
		f := st.Field(i)
		if isZeroSizeType(f.Type()) {
			continue
		}
		fields = append(fields, f)
		// Ensure field type descriptors exist (may recurse).
		fk := TypeKey(f.Type())
		g.needTypeKey(fk)
		if t := g.types[fk]; t != nil && t.goType == nil {
			t.goType = f.Type()
		}
	}
	getFn := g.emitGetFieldFunc(tr, st, wasmName)
	setFn := g.emitSetFieldFunc(tr, st, wasmName)
	ftFn := g.emitFieldTypeFunc(tr, fields)
	fnFn := g.emitFieldNameFunc(tr, fields)
	tagFn := g.emitFieldTagFunc(tr, st, fields)
	return &ir.Instr{
		Op:   ir.OpStructNew,
		Type: &ir.Ref{Named: "$go.extra.struct"},
		Args: []ir.Expr{
			&ir.Const{Op: ir.OpI32Const, I64: int64(len(fields))},
			&ir.Instr{Op: ir.OpRefFunc, Func: getFn},
			&ir.Instr{Op: ir.OpRefFunc, Func: setFn},
			&ir.Instr{Op: ir.OpRefFunc, Func: ftFn},
			&ir.Instr{Op: ir.OpRefFunc, Func: fnFn},
			&ir.Instr{Op: ir.OpRefFunc, Func: tagFn},
		},
	}
}

func (g *gen) structShape(tr *typeRec) (*types.Struct, string) {
	if tr.goType == nil {
		return nil, ""
	}
	t := types.Unalias(tr.goType)
	if n, ok := t.(*types.Named); ok {
		if runtimeStructAliasName(n) != "" {
			return nil, ""
		}
		st, _ := n.Underlying().(*types.Struct)
		return st, structName(n)
	}
	if s, ok := t.(*types.Struct); ok {
		return s, anonStructWasmName(s)
	}
	return nil, ""
}

// ensureStructWasmType interns a Wasm struct layout for getField cast targets.
// Zero-size named structs (e.g. runtime.Func) may never be lowered into the
// module yet still get descriptor extras once resolveGoTypes attaches them.
func (g *gen) ensureStructWasmType(wasmName string, st *types.Struct) {
	if wasmName == "" || st == nil {
		return
	}
	if td := g.mod.LookupType(wasmName); td != nil && (len(td.Fields) > 0 || st.NumFields() == 0) {
		// Already present, or empty struct stub is enough.
		if td != nil {
			return
		}
	}
	var fields []ir.Field
	for i := 0; i < st.NumFields(); i++ {
		f := st.Field(i)
		if isZeroSizeType(f.Type()) {
			continue
		}
		ft, err := mapTypeApprox(f.Type())
		if err != nil {
			ft = &ir.Ref{Null: true, Heap: ir.HeapAny}
		}
		fields = append(fields, ir.Field{Name: f.Name(), Type: ft, Mut: true})
	}
	if td := g.mod.LookupType(wasmName); td != nil {
		if len(td.Fields) == 0 {
			td.Fields = fields
		} else {
			// Ensure fields are mutable so setField can struct.set.
			for i := range td.Fields {
				td.Fields[i].Mut = true
			}
		}
		return
	}
	g.mod.InternType(&ir.TypeDef{
		Name:   wasmName,
		Kind:   ir.TypeStruct,
		Super:  "$go.object",
		Fields: fields,
	})
}

// ensureMutableBoxes marks $go.box.*.v mutable for Reflect Set* on New().Elem().
func (g *gen) ensureMutableBoxes() {
	for _, name := range []string{"$go.box.i32", "$go.box.i64", "$go.box.f32", "$go.box.f64"} {
		td := g.mod.LookupType(name)
		if td == nil {
			continue
		}
		for i := range td.Fields {
			if td.Fields[i].Name == "v" {
				td.Fields[i].Mut = true
			}
		}
	}
}

func (g *gen) emitGetFieldFunc(tr *typeRec, st *types.Struct, wasmName string) string {
	name := "$go.getField." + sanitize(tr.key)
	if g.mod.LookupFunc(name) != nil {
		return name
	}
	g.ensureStructWasmType(wasmName, st)
	data := &ir.Var{Name: "data", Typ: &ir.Ref{Null: true, Heap: ir.HeapAny}}
	idx := &ir.Var{Name: "i", Typ: ir.TypeI32}
	sty := &ir.Ref{Named: wasmName}
	sv := &ir.Var{Name: "s", Typ: sty}
	anyT := &ir.Ref{Null: true, Heap: ir.HeapAny}

	var logical []int // go field index → wasm field (skipping zero-size)
	for i := 0; i < st.NumFields(); i++ {
		if !isZeroSizeType(st.Field(i).Type()) {
			logical = append(logical, i)
		}
	}
	// if/else chain on i
	var chain ir.Stmt = &ir.Return{Results: []ir.Expr{
		&ir.Instr{Op: ir.OpRefNull, Type: anyT},
	}}
	for li := len(logical) - 1; li >= 0; li-- {
		fi := logical[li]
		f := st.Field(fi)
		get := &ir.Instr{Op: ir.OpStructGet, Type: sty, Field: f.Name(), Args: []ir.Expr{&ir.Name{Raw: "s", Var: sv}}}
		boxed := g.boxFieldToAny(f.Type(), get)
		chain = &ir.If{
			Cond: &ir.Instr{Op: ir.OpI32Eq, Args: []ir.Expr{
				&ir.Name{Raw: "i", Var: idx},
				&ir.Const{Op: ir.OpI32Const, I64: int64(li)},
			}},
			Then: &ir.Seq{Stmts: []ir.Stmt{&ir.Return{Results: []ir.Expr{boxed}}}},
			Else: &ir.Seq{Stmts: []ir.Stmt{chain}},
		}
	}
	body := &ir.Seq{Stmts: []ir.Stmt{
		&ir.VarDef{Var: sv},
		&ir.Assign{Lhs: []ir.Dest{{Name: "s", Var: sv}}, Rhs: &ir.Instr{
			Op: ir.OpRefCast, Type: sty, Args: []ir.Expr{&ir.Name{Raw: "data", Var: data}},
		}},
		chain,
		&ir.Unreachable{},
	}}
	g.mod.Funcs = append(g.mod.Funcs, &ir.Func{
		Name:    name,
		Params:  []*ir.Var{data, idx},
		Results: []ir.Type{anyT},
		Body:    body,
	})
	return name
}

func (g *gen) emitSetFieldFunc(tr *typeRec, st *types.Struct, wasmName string) string {
	name := "$go.setField." + sanitize(tr.key)
	if g.mod.LookupFunc(name) != nil {
		return name
	}
	g.ensureStructWasmType(wasmName, st)
	anyT := &ir.Ref{Null: true, Heap: ir.HeapAny}
	data := &ir.Var{Name: "data", Typ: anyT}
	idx := &ir.Var{Name: "i", Typ: ir.TypeI32}
	val := &ir.Var{Name: "v", Typ: anyT}
	sty := &ir.Ref{Named: wasmName}
	sv := &ir.Var{Name: "s", Typ: sty}

	var logical []int
	for i := 0; i < st.NumFields(); i++ {
		if !isZeroSizeType(st.Field(i).Type()) {
			logical = append(logical, i)
		}
	}
	// Prefer the Wasm field type from the TypeDef so struct.set validates.
	var wasmFields []ir.Field
	if td := g.mod.LookupType(wasmName); td != nil {
		wasmFields = td.Fields
	}
	var chain ir.Stmt = &ir.Return{}
	for li := len(logical) - 1; li >= 0; li-- {
		fi := logical[li]
		f := st.Field(fi)
		var fieldIR ir.Type
		if li < len(wasmFields) {
			fieldIR = wasmFields[li].Type
		}
		unboxed := g.unboxAnyToField(f.Type(), &ir.Name{Raw: "v", Var: val}, fieldIR)
		set := &ir.ExprStmt{X: &ir.Instr{
			Op: ir.OpStructSet, Type: sty, Field: f.Name(),
			Args: []ir.Expr{&ir.Name{Raw: "s", Var: sv}, unboxed},
		}}
		chain = &ir.If{
			Cond: &ir.Instr{Op: ir.OpI32Eq, Args: []ir.Expr{
				&ir.Name{Raw: "i", Var: idx},
				&ir.Const{Op: ir.OpI32Const, I64: int64(li)},
			}},
			Then: &ir.Seq{Stmts: []ir.Stmt{set, &ir.Return{}}},
			Else: &ir.Seq{Stmts: []ir.Stmt{chain}},
		}
	}
	body := &ir.Seq{Stmts: []ir.Stmt{
		&ir.VarDef{Var: sv},
		&ir.Assign{Lhs: []ir.Dest{{Name: "s", Var: sv}}, Rhs: &ir.Instr{
			Op: ir.OpRefCast, Type: sty, Args: []ir.Expr{&ir.Name{Raw: "data", Var: data}},
		}},
		chain,
		&ir.Return{},
	}}
	g.mod.Funcs = append(g.mod.Funcs, &ir.Func{
		Name:   name,
		Params: []*ir.Var{data, idx, val},
		Body:   body,
	})
	return name
}

func (g *gen) boxFieldToAny(ft types.Type, val ir.Expr) ir.Expr {
	anyT := &ir.Ref{Null: true, Heap: ir.HeapAny}
	asAny := func(ex ir.Expr) ir.Expr {
		return &ir.Instr{Op: ir.OpRefCast, Type: anyT, Args: []ir.Expr{ex}}
	}
	ft = types.Unalias(ft)
	switch u := ft.Underlying().(type) {
	case *types.Basic:
		switch u.Kind() {
		case types.Bool, types.Int, types.Int8, types.Int16, types.Int32,
			types.Uint, types.Uint8, types.Uint16, types.Uint32, types.Uintptr:
			return asAny(&ir.Instr{Op: ir.OpStructNew, Type: &ir.Ref{Named: "$go.box.i32"}, Args: []ir.Expr{val}})
		case types.Int64, types.Uint64:
			return asAny(&ir.Instr{Op: ir.OpStructNew, Type: &ir.Ref{Named: "$go.box.i64"}, Args: []ir.Expr{val}})
		case types.Float32:
			return asAny(&ir.Instr{Op: ir.OpStructNew, Type: &ir.Ref{Named: "$go.box.f32"}, Args: []ir.Expr{val}})
		case types.Float64:
			return asAny(&ir.Instr{Op: ir.OpStructNew, Type: &ir.Ref{Named: "$go.box.f64"}, Args: []ir.Expr{val}})
		case types.String:
			return asAny(val)
		}
	}
	return asAny(val)
}

// unboxAnyToField converts a boxed anyref (from reflect.Value.data) into the
// Wasm storage type stored in a struct/array field. fieldIR is the concrete
// Wasm field type when known (preferred for struct.set validation).
func (g *gen) unboxAnyToField(ft types.Type, val ir.Expr, fieldIR ir.Type) ir.Expr {
	if ft != nil {
		ft = types.Unalias(ft)
	}
	if ft != nil {
		switch u := ft.Underlying().(type) {
		case *types.Basic:
			switch u.Kind() {
			case types.Bool, types.Int, types.Int8, types.Int16, types.Int32,
				types.Uint, types.Uint8, types.Uint16, types.Uint32, types.Uintptr:
				box := &ir.Ref{Named: "$go.box.i32"}
				cast := &ir.Instr{Op: ir.OpRefCast, Type: box, Args: []ir.Expr{val}}
				return &ir.Instr{Op: ir.OpStructGet, Type: box, Field: "v", Args: []ir.Expr{cast}}
			case types.Int64, types.Uint64:
				box := &ir.Ref{Named: "$go.box.i64"}
				cast := &ir.Instr{Op: ir.OpRefCast, Type: box, Args: []ir.Expr{val}}
				return &ir.Instr{Op: ir.OpStructGet, Type: box, Field: "v", Args: []ir.Expr{cast}}
			case types.Float32:
				box := &ir.Ref{Named: "$go.box.f32"}
				cast := &ir.Instr{Op: ir.OpRefCast, Type: box, Args: []ir.Expr{val}}
				return &ir.Instr{Op: ir.OpStructGet, Type: box, Field: "v", Args: []ir.Expr{cast}}
			case types.Float64:
				box := &ir.Ref{Named: "$go.box.f64"}
				cast := &ir.Instr{Op: ir.OpRefCast, Type: box, Args: []ir.Expr{val}}
				return &ir.Instr{Op: ir.OpStructGet, Type: box, Field: "v", Args: []ir.Expr{cast}}
			case types.String:
				want := fieldIR
				if want == nil {
					want = &ir.Ref{Named: "$go.string"}
				}
				return &ir.Instr{Op: ir.OpRefCast, Type: want, Args: []ir.Expr{val}}
			}
		}
	}
	want := fieldIR
	if want == nil && ft != nil {
		var err error
		want, err = mapTypeApprox(ft)
		if err != nil || want == nil {
			want = &ir.Ref{Null: true, Heap: ir.HeapAny}
		}
	}
	if want == nil {
		want = &ir.Ref{Null: true, Heap: ir.HeapAny}
	}
	// Numeric Wasm fields still need unboxing when Go type was not *Basic
	// (e.g. defined int types already handled). If fieldIR is a num type,
	// unbox from the matching box.
	switch want {
	case ir.TypeI32:
		box := &ir.Ref{Named: "$go.box.i32"}
		cast := &ir.Instr{Op: ir.OpRefCast, Type: box, Args: []ir.Expr{val}}
		return &ir.Instr{Op: ir.OpStructGet, Type: box, Field: "v", Args: []ir.Expr{cast}}
	case ir.TypeI64:
		box := &ir.Ref{Named: "$go.box.i64"}
		cast := &ir.Instr{Op: ir.OpRefCast, Type: box, Args: []ir.Expr{val}}
		return &ir.Instr{Op: ir.OpStructGet, Type: box, Field: "v", Args: []ir.Expr{cast}}
	case ir.TypeF32:
		box := &ir.Ref{Named: "$go.box.f32"}
		cast := &ir.Instr{Op: ir.OpRefCast, Type: box, Args: []ir.Expr{val}}
		return &ir.Instr{Op: ir.OpStructGet, Type: box, Field: "v", Args: []ir.Expr{cast}}
	case ir.TypeF64:
		box := &ir.Ref{Named: "$go.box.f64"}
		cast := &ir.Instr{Op: ir.OpRefCast, Type: box, Args: []ir.Expr{val}}
		return &ir.Instr{Op: ir.OpStructGet, Type: box, Field: "v", Args: []ir.Expr{cast}}
	}
	return &ir.Instr{Op: ir.OpRefCast, Type: want, Args: []ir.Expr{val}}
}

func (g *gen) emitSliceExtra(tr *typeRec) ir.Expr {
	g.ensureExtraShapes()
	elemKey, elemT := g.elemOf(tr)
	if elemKey == "" {
		return &ir.Instr{Op: ir.OpRefNull, Type: &ir.Ref{Null: true, Named: "$go.object"}}
	}
	g.needTypeKey(elemKey)
	if et := g.types[elemKey]; et != nil && et.goType == nil {
		et.goType = elemT
	}
	indexFn := g.emitIndexFunc(tr)
	setIdxFn := g.emitSetIndexFunc(tr)
	lenFn := g.emitLenFunc(tr)
	return &ir.Instr{
		Op:   ir.OpStructNew,
		Type: &ir.Ref{Named: "$go.extra.slice"},
		Args: []ir.Expr{
			&ir.Instr{Op: ir.OpGlobalGet, Global: "$go.type." + elemKey},
			&ir.Instr{Op: ir.OpRefFunc, Func: indexFn},
			&ir.Instr{Op: ir.OpRefFunc, Func: setIdxFn},
			&ir.Instr{Op: ir.OpRefFunc, Func: lenFn},
		},
	}
}

func (g *gen) elemOf(tr *typeRec) (string, types.Type) {
	if tr.goType != nil {
		switch u := tr.goType.Underlying().(type) {
		case *types.Slice:
			return TypeKey(u.Elem()), u.Elem()
		case *types.Array:
			return TypeKey(u.Elem()), u.Elem()
		case *types.Pointer:
			return TypeKey(u.Elem()), u.Elem()
		case *types.Map:
			return TypeKey(u.Elem()), u.Elem()
		}
	}
	if tr.kind == KindSlice || tr.kind == KindArray || tr.kind == KindPointer {
		if len(tr.key) > 2 && (tr.key[0] == '[' || tr.key[0] == '*') {
			// best-effort from key
		}
	}
	if tr.kind == KindSlice && len(tr.key) > 2 && tr.key[:2] == "[]" {
		ek := tr.key[2:]
		return ek, builtinType(ek)
	}
	if tr.kind == KindPointer && len(tr.key) > 1 && tr.key[0] == '*' {
		ek := tr.key[1:]
		return ek, builtinType(ek)
	}
	return "", nil
}

func (g *gen) emitIndexFunc(tr *typeRec) string {
	name := "$go.index." + sanitize(tr.key)
	if g.mod.LookupFunc(name) != nil {
		return name
	}
	anyT := &ir.Ref{Null: true, Heap: ir.HeapAny}
	data := &ir.Var{Name: "data", Typ: anyT}
	idx := &ir.Var{Name: "i", Typ: ir.TypeI32}
	elemT, sliceName := g.sliceElemIR(tr)
	if sliceName == "" {
		g.mod.Funcs = append(g.mod.Funcs, &ir.Func{
			Name: name, Params: []*ir.Var{data, idx}, Results: []ir.Type{anyT},
			Body: &ir.Seq{Stmts: []ir.Stmt{&ir.Return{Results: []ir.Expr{
				&ir.Instr{Op: ir.OpRefNull, Type: anyT},
			}}}},
		})
		return name
	}
	sty := &ir.Ref{Named: sliceName}
	sv := &ir.Var{Name: "s", Typ: sty}
	// slice.get → box
	get := &ir.Instr{Op: ir.OpSliceGet, Type: elemT, Args: []ir.Expr{
		&ir.Name{Raw: "s", Var: sv}, &ir.Name{Raw: "i", Var: idx},
	}}
	var elemGo types.Type
	if tr.goType != nil {
		switch u := tr.goType.Underlying().(type) {
		case *types.Slice:
			elemGo = u.Elem()
		case *types.Array:
			elemGo = u.Elem()
		}
	}
	var boxed ir.Expr = get
	if elemGo != nil {
		boxed = g.boxFieldToAny(elemGo, get)
	} else {
		boxed = &ir.Instr{Op: ir.OpRefCast, Type: anyT, Args: []ir.Expr{get}}
	}
	g.mod.Funcs = append(g.mod.Funcs, &ir.Func{
		Name:    name,
		Params:  []*ir.Var{data, idx},
		Results: []ir.Type{anyT},
		Body: &ir.Seq{Stmts: []ir.Stmt{
			&ir.VarDef{Var: sv},
			&ir.Assign{Lhs: []ir.Dest{{Name: "s", Var: sv}}, Rhs: &ir.Instr{
				Op: ir.OpRefCast, Type: sty, Args: []ir.Expr{&ir.Name{Raw: "data", Var: data}},
			}},
			&ir.Return{Results: []ir.Expr{boxed}},
		}},
	})
	return name
}

func (g *gen) emitSetIndexFunc(tr *typeRec) string {
	name := "$go.setIndex." + sanitize(tr.key)
	if g.mod.LookupFunc(name) != nil {
		return name
	}
	anyT := &ir.Ref{Null: true, Heap: ir.HeapAny}
	data := &ir.Var{Name: "data", Typ: anyT}
	idx := &ir.Var{Name: "i", Typ: ir.TypeI32}
	val := &ir.Var{Name: "v", Typ: anyT}
	elemIR, sliceName := g.sliceElemIR(tr)
	if sliceName == "" {
		g.mod.Funcs = append(g.mod.Funcs, &ir.Func{
			Name: name, Params: []*ir.Var{data, idx, val},
			Body: &ir.Seq{Stmts: []ir.Stmt{&ir.Return{}}},
		})
		return name
	}
	g.ensureSliceHeaderMutable(sliceName)
	sty := &ir.Ref{Named: sliceName}
	sv := &ir.Var{Name: "s", Typ: sty}
	var elemGo types.Type
	if tr.goType != nil {
		switch u := tr.goType.Underlying().(type) {
		case *types.Slice:
			elemGo = u.Elem()
		case *types.Array:
			elemGo = u.Elem()
		}
	}
	var store ir.Expr = &ir.Name{Raw: "v", Var: val}
	if elemGo != nil {
		store = g.unboxAnyToField(elemGo, store, elemIR)
	} else if elemIR != nil {
		store = g.unboxAnyToField(nil, store, elemIR)
	}
	// Prefer high-level slice.set; RuntimeOps expands to array.set.
	g.mod.Funcs = append(g.mod.Funcs, &ir.Func{
		Name:   name,
		Params: []*ir.Var{data, idx, val},
		Body: &ir.Seq{Stmts: []ir.Stmt{
			&ir.VarDef{Var: sv},
			&ir.Assign{Lhs: []ir.Dest{{Name: "s", Var: sv}}, Rhs: &ir.Instr{
				Op: ir.OpRefCast, Type: sty, Args: []ir.Expr{&ir.Name{Raw: "data", Var: data}},
			}},
			&ir.ExprStmt{X: &ir.Instr{
				Op:   ir.OpSliceSet,
				Type: sty,
				Args: []ir.Expr{
					&ir.Name{Raw: "s", Var: sv},
					&ir.Name{Raw: "i", Var: idx},
					store,
				},
			}},
			&ir.Return{},
		}},
	})
	return name
}

func (g *gen) emitLenFunc(tr *typeRec) string {
	name := "$go.len." + sanitize(tr.key)
	if g.mod.LookupFunc(name) != nil {
		return name
	}
	anyT := &ir.Ref{Null: true, Heap: ir.HeapAny}
	data := &ir.Var{Name: "data", Typ: anyT}
	_, sliceName := g.sliceElemIR(tr)
	if sliceName == "" {
		g.mod.Funcs = append(g.mod.Funcs, &ir.Func{
			Name: name, Params: []*ir.Var{data}, Results: []ir.Type{ir.TypeI32},
			Body: &ir.Seq{Stmts: []ir.Stmt{&ir.Return{Results: []ir.Expr{
				&ir.Const{Op: ir.OpI32Const, I64: 0},
			}}}},
		})
		return name
	}
	sty := &ir.Ref{Named: sliceName}
	sv := &ir.Var{Name: "s", Typ: sty}
	g.mod.Funcs = append(g.mod.Funcs, &ir.Func{
		Name:    name,
		Params:  []*ir.Var{data},
		Results: []ir.Type{ir.TypeI32},
		Body: &ir.Seq{Stmts: []ir.Stmt{
			&ir.VarDef{Var: sv},
			&ir.Assign{Lhs: []ir.Dest{{Name: "s", Var: sv}}, Rhs: &ir.Instr{
				Op: ir.OpRefCast, Type: sty, Args: []ir.Expr{&ir.Name{Raw: "data", Var: data}},
			}},
			&ir.Return{Results: []ir.Expr{&ir.Instr{
				Op: ir.OpStructGet, Type: sty, Field: "len",
				Args: []ir.Expr{&ir.Name{Raw: "s", Var: sv}},
			}}},
		}},
	})
	return name
}

func (g *gen) ensureSliceHeaderMutable(sliceName string) {
	td := g.mod.LookupType(sliceName)
	if td == nil {
		return
	}
	for i := range td.Fields {
		switch td.Fields[i].Name {
		case "data", "off", "len", "cap":
			td.Fields[i].Mut = true
		}
	}
}

func (g *gen) sliceElemIR(tr *typeRec) (ir.Type, string) {
	if tr.goType == nil {
		return nil, ""
	}
	var elem types.Type
	switch u := tr.goType.Underlying().(type) {
	case *types.Slice:
		elem = u.Elem()
	case *types.Array:
		elem = u.Elem()
		// Arrays are bare array refs; treat like slice headerless — skip for M8.
		return nil, ""
	default:
		return nil, ""
	}
	elemIR, err := mapTypeApprox(elem)
	if err != nil {
		elemIR = &ir.Ref{Null: true, Heap: ir.HeapAny}
	}
	arrName := arrayNameFor(elemIR)
	sliceName := sliceNameFor(arrName)
	// Ensure types exist (may already from lower).
	g.mod.InternType(&ir.TypeDef{Name: arrName, Kind: ir.TypeArray, Elem: storageElemApprox(elemIR), ElemMut: true})
	g.mod.InternType(&ir.TypeDef{
		Name:  sliceName,
		Kind:  ir.TypeStruct,
		Super: "$go.object",
		Fields: []ir.Field{
			{Name: "data", Type: &ir.Ref{Named: arrName}, Mut: true},
			{Name: "off", Type: ir.TypeI32, Mut: true},
			{Name: "len", Type: ir.TypeI32, Mut: true},
			{Name: "cap", Type: ir.TypeI32, Mut: true},
		},
	})
	g.ensureSliceHeaderMutable(sliceName)
	return elemIR, sliceName
}

func arrayNameFor(elemIR ir.Type) string {
	switch t := elemIR.(type) {
	case *ir.NumType:
		if t.Kind == ir.I8 {
			return "$go.bytes"
		}
		return "$go.array." + t.Kind.String()
	case *ir.Ref:
		if t.Named == "$go.string" {
			return "$go.array.go.string"
		}
		if t.Named != "" {
			return "$go.array." + sanitize(t.Named)
		}
	}
	return "$go.array.any"
}

func sliceNameFor(arrName string) string {
	switch arrName {
	case "$go.bytes":
		return "$go.slice.i8"
	case "$go.i16s":
		return "$go.slice.i16"
	}
	if len(arrName) > 10 && arrName[:10] == "$go.array." {
		return "$go.slice." + arrName[10:]
	}
	return "$go.slice." + sanitize(arrName)
}

func storageElemApprox(t ir.Type) ir.Type {
	if n, ok := t.(*ir.NumType); ok && n.Kind == ir.I32 {
		// byte/bool packed as i8 when in []byte — mapTypeApprox already returns i32 for uint8.
		// Prefer $go.bytes path via arrayNameFor when basic uint8.
	}
	return t
}

func (g *gen) emitPtrExtra(tr *typeRec) ir.Expr {
	g.ensureExtraShapes()
	elemKey, elemT := g.elemOf(tr)
	if elemKey == "" {
		return &ir.Instr{Op: ir.OpRefNull, Type: &ir.Ref{Null: true, Named: "$go.object"}}
	}
	g.needTypeKey(elemKey)
	if et := g.types[elemKey]; et != nil && et.goType == nil {
		et.goType = elemT
	}
	return &ir.Instr{
		Op:   ir.OpStructNew,
		Type: &ir.Ref{Named: "$go.extra.ptr"},
		Args: []ir.Expr{
			&ir.Instr{Op: ir.OpGlobalGet, Global: "$go.type." + elemKey},
		},
	}
}

func (g *gen) emitMapExtra(tr *typeRec) ir.Expr {
	g.ensureExtraShapes()
	var keyT, elemT types.Type
	if tr.goType != nil {
		if m, ok := tr.goType.Underlying().(*types.Map); ok {
			keyT, elemT = m.Key(), m.Elem()
		}
	}
	if keyT == nil {
		return &ir.Instr{Op: ir.OpRefNull, Type: &ir.Ref{Null: true, Named: "$go.object"}}
	}
	kk, ek := TypeKey(keyT), TypeKey(elemT)
	g.needTypeKey(kk)
	g.needTypeKey(ek)
	if t := g.types[kk]; t != nil && t.goType == nil {
		t.goType = keyT
	}
	if t := g.types[ek]; t != nil && t.goType == nil {
		t.goType = elemT
	}
	return &ir.Instr{
		Op:   ir.OpStructNew,
		Type: &ir.Ref{Named: "$go.extra.map"},
		Args: []ir.Expr{
			&ir.Instr{Op: ir.OpGlobalGet, Global: "$go.type." + kk},
			&ir.Instr{Op: ir.OpGlobalGet, Global: "$go.type." + ek},
		},
	}
}

func (g *gen) emitFieldTypeFunc(tr *typeRec, fields []*types.Var) string {
	name := "$go.fieldType." + sanitize(tr.key)
	if g.mod.LookupFunc(name) != nil {
		return name
	}
	idx := &ir.Var{Name: "i", Typ: ir.TypeI32}
	typeTy := &ir.Ref{Named: "$go.type"}
	var chain ir.Stmt = &ir.Return{Results: []ir.Expr{
		&ir.Instr{Op: ir.OpGlobalGet, Global: tr.global}, // fallback
	}}
	for li := len(fields) - 1; li >= 0; li-- {
		fk := TypeKey(fields[li].Type())
		g.needTypeKey(fk)
		chain = &ir.If{
			Cond: &ir.Instr{Op: ir.OpI32Eq, Args: []ir.Expr{
				&ir.Name{Raw: "i", Var: idx},
				&ir.Const{Op: ir.OpI32Const, I64: int64(li)},
			}},
			Then: &ir.Seq{Stmts: []ir.Stmt{&ir.Return{Results: []ir.Expr{
				&ir.Instr{Op: ir.OpGlobalGet, Global: "$go.type." + fk},
			}}}},
			Else: &ir.Seq{Stmts: []ir.Stmt{chain}},
		}
	}
	g.mod.Funcs = append(g.mod.Funcs, &ir.Func{
		Name:    name,
		Params:  []*ir.Var{idx},
		Results: []ir.Type{typeTy},
		Body:    &ir.Seq{Stmts: []ir.Stmt{chain, &ir.Unreachable{}}},
	})
	return name
}

func (g *gen) emitFieldNameFunc(tr *typeRec, fields []*types.Var) string {
	name := "$go.fieldName." + sanitize(tr.key)
	if g.mod.LookupFunc(name) != nil {
		return name
	}
	idx := &ir.Var{Name: "i", Typ: ir.TypeI32}
	strTy := &ir.Ref{Named: "$go.string"}
	var chain ir.Stmt = &ir.Return{Results: []ir.Expr{g.stringConstExpr("")}}
	for li := len(fields) - 1; li >= 0; li-- {
		n := fields[li].Name()
		chain = &ir.If{
			Cond: &ir.Instr{Op: ir.OpI32Eq, Args: []ir.Expr{
				&ir.Name{Raw: "i", Var: idx},
				&ir.Const{Op: ir.OpI32Const, I64: int64(li)},
			}},
			Then: &ir.Seq{Stmts: []ir.Stmt{&ir.Return{Results: []ir.Expr{g.stringConstExpr(n)}}}},
			Else: &ir.Seq{Stmts: []ir.Stmt{chain}},
		}
	}
	g.mod.Funcs = append(g.mod.Funcs, &ir.Func{
		Name:    name,
		Params:  []*ir.Var{idx},
		Results: []ir.Type{strTy},
		Body:    &ir.Seq{Stmts: []ir.Stmt{chain, &ir.Unreachable{}}},
	})
	return name
}

func (g *gen) emitFieldTagFunc(tr *typeRec, st *types.Struct, fields []*types.Var) string {
	name := "$go.fieldTag." + sanitize(tr.key)
	if g.mod.LookupFunc(name) != nil {
		return name
	}
	idx := &ir.Var{Name: "i", Typ: ir.TypeI32}
	strTy := &ir.Ref{Named: "$go.string"}
	// Map logical (non-zero-size) index → go/types field index for Tag(i).
	var goIdx []int
	for i := 0; i < st.NumFields(); i++ {
		if !isZeroSizeType(st.Field(i).Type()) {
			goIdx = append(goIdx, i)
		}
	}
	var chain ir.Stmt = &ir.Return{Results: []ir.Expr{g.stringConstExpr("")}}
	for li := len(fields) - 1; li >= 0; li-- {
		tag := ""
		if li < len(goIdx) {
			tag = st.Tag(goIdx[li])
		}
		chain = &ir.If{
			Cond: &ir.Instr{Op: ir.OpI32Eq, Args: []ir.Expr{
				&ir.Name{Raw: "i", Var: idx},
				&ir.Const{Op: ir.OpI32Const, I64: int64(li)},
			}},
			Then: &ir.Seq{Stmts: []ir.Stmt{&ir.Return{Results: []ir.Expr{g.stringConstExpr(tag)}}}},
			Else: &ir.Seq{Stmts: []ir.Stmt{chain}},
		}
	}
	g.mod.Funcs = append(g.mod.Funcs, &ir.Func{
		Name:    name,
		Params:  []*ir.Var{idx},
		Results: []ir.Type{strTy},
		Body:    &ir.Seq{Stmts: []ir.Stmt{chain, &ir.Unreachable{}}},
	})
	return name
}

func (g *gen) stringConstExpr(s string) ir.Expr {
	// Reuse one empty data segment for "" to avoid bloating modules that only
	// need a zero name fallback.
	litName := ""
	if s == "" {
		litName = "$descstr.empty"
		found := false
		for _, d := range g.mod.Data {
			if d.Name == litName {
				found = true
				break
			}
		}
		if !found {
			g.mod.Data = append(g.mod.Data, &ir.Data{Name: litName, Bytes: nil})
		}
	} else {
		litName = fmt.Sprintf("$descstr%d", len(g.mod.Data))
		g.mod.Data = append(g.mod.Data, &ir.Data{Name: litName, Bytes: []byte(s)})
	}
	data := &ir.Instr{Op: ir.OpArrayNewData, Type: &ir.Ref{Named: "$go.bytes"}, Data: litName}
	return &ir.Instr{
		Op:   ir.OpStructNew,
		Type: &ir.Ref{Named: "$go.string"},
		Args: []ir.Expr{
			data,
			&ir.Const{Op: ir.OpI32Const, I64: 0},
			&ir.Const{Op: ir.OpI32Const, I64: int64(len(s))},
		},
	}
}

func (g *gen) nameExpr(tr *typeRec) ir.Expr {
	// Keep name null in global init: codegen emits globals before data
	// segments, so array.new_data in a descriptor init cannot resolve.
	// reflect.Type.String synthesizes from kind / field metadata instead.
	return &ir.Instr{Op: ir.OpRefNull, Type: &ir.Ref{Null: true, Named: "$go.string"}}
}
