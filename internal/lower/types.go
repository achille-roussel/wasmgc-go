package lower

import (
	"fmt"
	"go/types"
	"strings"

	"github.com/achille-roussel/wasmgc-go/internal/ir"
	"github.com/achille-roussel/wasmgc-go/internal/mono"
)

// TypeStringRef is the IR type of a Go string value.
var typeString = &ir.Ref{Named: "$go.string"}

// mapType lowers a Go type to an IR value type (Wasm shape).
// int/uint/uintptr → i32 per StdSizes{4,4} and docs/design/03.
// Composite types are interned on the module.
func (l *pkgLower) mapType(t types.Type) (ir.Type, error) {
	if t == nil {
		return nil, fmt.Errorf("nil type")
	}
	if len(l.subst) > 0 {
		t = l.subst.Apply(t)
	}
	t = types.Unalias(t)
	switch t := t.(type) {
	case *types.TypeParam:
		return nil, fmt.Errorf("unsubstituted type parameter %s", t)
	case *types.Basic:
		return mapBasic(t)
	case *types.Named:
		if isJSValue(t) {
			// syscall/js.Value is externref (docs/design/07).
			return ir.TypeExtern, nil
		}
		u := t.Underlying()
		switch u.(type) {
		case *types.Struct:
			return l.mapNamedStruct(t)
		default:
			return l.mapType(u)
		}
	case *types.Alias:
		return l.mapType(t.Underlying())
	case *types.Pointer:
		return l.mapPointer(t)
	case *types.Slice:
		return l.mapSlice(t)
	case *types.Array:
		return l.mapArray(t)
	case *types.Struct:
		// Anonymous struct.
		return l.mapAnonStruct(t)
	case *types.Interface:
		return typeIface, nil
	case *types.Signature:
		return l.mapSignature(t)
	case *types.Tuple:
		return nil, fmt.Errorf("tuple type not a single value: %s", t)
	case *types.Map:
		return l.mapMap(t)
	case *types.Chan:
		return l.mapChan(t)
	default:
		return nil, fmt.Errorf("unsupported type in M4: %s", t)
	}
}

// mapMap lowers map[K]V to (ref null $go.hmap). Nil map is a null ref.
// Key/value types are erased at the header; payloads are boxed as any (04).
func (l *pkgLower) mapMap(t *types.Map) (ir.Type, error) {
	l.ensureMapBaseTypes()
	return &ir.Ref{Null: true, Named: "$go.hmap"}, nil
}

// mapChan lowers chan T to (ref null $go.chan). Nil channel is a null ref.
func (l *pkgLower) mapChan(t *types.Chan) (ir.Type, error) {
	l.ensureChanBaseTypes()
	return &ir.Ref{Null: true, Named: "$go.chan"}, nil
}

// ensureMapBaseTypes interns $go.hmap / $go.mapIter with the field layout of
// runtime.hmap / mapIter so PackageHigh and AppendFiles agree.
// ensureTypeDescLayout interns $go.type with the prelude field types (name is
// ref null $go.string, not any). Called when lowering runtime.Rtype before
// desc.Populate so AppendFiles cannot poison the descriptor layout.
func (l *pkgLower) ensureTypeDescLayout() {
	if l.mod.LookupType("$go.object") == nil {
		l.mod.InternType(&ir.TypeDef{Name: "$go.object", Kind: ir.TypeStruct})
	}
	if l.mod.LookupType("$go.bytes") == nil {
		l.mod.InternType(&ir.TypeDef{
			Name: "$go.bytes", Kind: ir.TypeArray, Elem: ir.TypeI8, ElemMut: true,
		})
	}
	if l.mod.LookupType("$go.string") == nil {
		l.mod.InternType(&ir.TypeDef{
			Name:  "$go.string",
			Kind:  ir.TypeStruct,
			Super: "$go.object",
			Fields: []ir.Field{
				{Name: "data", Type: &ir.Ref{Named: "$go.bytes"}},
				{Name: "off", Type: ir.TypeI32},
				{Name: "len", Type: ir.TypeI32},
			},
		})
	}
	fields := []ir.Field{
		{Name: "id", Type: ir.TypeI32},
		{Name: "kind", Type: ir.TypeI32},
		{Name: "name", Type: &ir.Ref{Null: true, Named: "$go.string"}},
		{Name: "extra", Type: &ir.Ref{Null: true, Named: "$go.object"}},
	}
	if td := l.mod.LookupType("$go.type"); td != nil {
		if len(td.Fields) == 0 {
			td.Fields = fields
		}
		return
	}
	l.mod.InternType(&ir.TypeDef{
		Name: "$go.type", Kind: ir.TypeStruct, Super: "$go.object", Fields: fields,
	})
}

func (l *pkgLower) ensureMapBaseTypes() {
	if l.mod.LookupType("$go.hmap") != nil && len(l.mod.LookupType("$go.hmap").Fields) > 0 {
		return
	}
	sliceU8, err := l.mapSlice(types.NewSlice(types.Typ[types.Uint8]))
	if err != nil {
		panic("ensureMapBaseTypes: []uint8: " + err.Error())
	}
	sliceIface, err := l.mapSlice(types.NewSlice(types.NewInterfaceType(nil, nil)))
	if err != nil {
		panic("ensureMapBaseTypes: []any: " + err.Error())
	}
	fields := []ir.Field{
		{Name: "used", Type: ir.TypeI32, Mut: true},
		{Name: "seed", Type: ir.TypeI32, Mut: true},
		{Name: "mask", Type: ir.TypeI32, Mut: true},
		{Name: "writing", Type: ir.TypeI32, Mut: true},
		{Name: "ctrl", Type: sliceU8, Mut: true},
		{Name: "keys", Type: sliceIface, Mut: true},
		{Name: "vals", Type: sliceIface, Mut: true},
	}
	if td := l.mod.LookupType("$go.hmap"); td != nil {
		td.Fields = fields
	} else {
		l.mod.InternType(&ir.TypeDef{
			Name: "$go.hmap", Kind: ir.TypeStruct, Super: "$go.object", Fields: fields,
		})
	}
	iterFields := []ir.Field{
		{Name: "keys", Type: sliceIface, Mut: true},
		{Name: "vals", Type: sliceIface, Mut: true},
		{Name: "ctrl", Type: sliceU8, Mut: true},
		{Name: "start", Type: ir.TypeI32, Mut: true},
		{Name: "i", Type: ir.TypeI32, Mut: true},
		{Name: "n", Type: ir.TypeI32, Mut: true},
	}
	if td := l.mod.LookupType("$go.mapIter"); td != nil {
		td.Fields = iterFields
	} else {
		l.mod.InternType(&ir.TypeDef{
			Name: "$go.mapIter", Kind: ir.TypeStruct, Super: "$go.object", Fields: iterFields,
		})
	}
}

// ensureChanBaseTypes interns the shared channel / scheduler layouts from
// docs/design/03 and proc.go so user chans and AppendFiles(runtime) agree.
func (l *pkgLower) ensureChanBaseTypes() {
	l.ensureClosBase()
	// $go.g must exist before waiter/select refs; match proc.go field set.
	l.ensureGType()

	if l.mod.LookupType("$go.waitq") == nil {
		// Forward-declare waiter for the recursive waitq ↔ waiter edge.
		l.mod.InternType(&ir.TypeDef{
			Name:   "$go.waiter",
			Kind:   ir.TypeStruct,
			Super:  "$go.object",
			Fields: nil,
		})
		l.mod.InternType(&ir.TypeDef{
			Name:  "$go.waitq",
			Kind:  ir.TypeStruct,
			Super: "$go.object",
			Fields: []ir.Field{
				{Name: "head", Type: &ir.Ref{Null: true, Named: "$go.waiter"}, Mut: true},
				{Name: "tail", Type: &ir.Ref{Null: true, Named: "$go.waiter"}, Mut: true},
			},
		})
	}
	if l.mod.LookupType("$go.select") == nil {
		l.mod.InternType(&ir.TypeDef{
			Name:   "$go.select",
			Kind:   ir.TypeStruct,
			Super:  "$go.object",
			Fields: nil, // filled below / by mapNamedStruct of select_
		})
	}
	if td := l.mod.LookupType("$go.waiter"); td != nil && len(td.Fields) == 0 {
		// Match runtime.waiter (incl. cas for select publish).
		td.Fields = []ir.Field{
			{Name: "park", Type: ir.TypeI32, Mut: true},
			{Name: "sel", Type: &ir.Ref{Null: true, Named: "$go.select"}, Mut: true},
			{Name: "casi", Type: ir.TypeI32, Mut: true},
			{Name: "next", Type: &ir.Ref{Null: true, Named: "$go.waiter"}, Mut: true},
			{Name: "elem", Type: typeIface, Mut: true},
			{Name: "isSend", Type: ir.TypeI32, Mut: true},
			{Name: "cas", Type: &ir.Ref{Null: true, Named: "$go.scase"}, Mut: true},
		}
	}
	if l.mod.LookupType("$go.chan") == nil {
		l.mod.InternType(&ir.TypeDef{
			Name:  "$go.chan",
			Kind:  ir.TypeStruct,
			Super: "$go.object",
			Fields: []ir.Field{
				{Name: "qcount", Type: ir.TypeI32, Mut: true},
				{Name: "dataqsiz", Type: ir.TypeI32, Mut: true},
				{Name: "sendx", Type: ir.TypeI32, Mut: true},
				{Name: "recvx", Type: ir.TypeI32, Mut: true},
				{Name: "closed", Type: ir.TypeI32, Mut: true},
				{Name: "sendq", Type: &ir.Ref{Null: true, Named: "$go.waitq"}, Mut: true},
				{Name: "recvq", Type: &ir.Ref{Null: true, Named: "$go.waitq"}, Mut: true},
				// any-payload scaffold: iface holding []any (or null).
				{Name: "buf", Type: typeIface, Mut: true},
			},
		})
	}
	if l.mod.LookupType("$go.chanOps") == nil {
		// Opaque until mapNamedStruct fills from runtime; reserve the name.
		l.mod.InternType(&ir.TypeDef{
			Name:   "$go.chanOps",
			Kind:   ir.TypeStruct,
			Super:  "$go.object",
			Fields: nil,
		})
	}
	if l.mod.LookupType("$go.scase") == nil {
		l.mod.InternType(&ir.TypeDef{
			Name:  "$go.scase",
			Kind:  ir.TypeStruct,
			Super: "$go.object",
			Fields: []ir.Field{
				{Name: "kind", Type: ir.TypeI32, Mut: true},
				{Name: "c", Type: &ir.Ref{Null: true, Named: "$go.chan"}, Mut: true},
				{Name: "ops", Type: &ir.Ref{Null: true, Named: "$go.chanOps"}, Mut: true},
				{Name: "elem", Type: typeIface, Mut: true},
				{Name: "ok", Type: ir.TypeI32, Mut: true},
			},
		})
	}
	if l.mod.LookupType("$go.runqnode") == nil {
		l.mod.InternType(&ir.TypeDef{
			Name:  "$go.runqnode",
			Kind:  ir.TypeStruct,
			Super: "$go.object",
			Fields: []ir.Field{
				{Name: "fn", Type: &ir.Ref{Null: true, Named: closBaseName}, Mut: true},
				{Name: "next", Type: &ir.Ref{Null: true, Named: "$go.runqnode"}, Mut: true},
			},
		})
	}
	if l.mod.LookupType("$go.semaRoot") == nil {
		l.mod.InternType(&ir.TypeDef{
			Name:  "$go.semaRoot",
			Kind:  ir.TypeStruct,
			Super: "$go.object",
			Fields: []ir.Field{
				{Name: "wait", Type: &ir.Ref{Null: true, Named: "$go.waitq"}, Mut: true},
			},
		})
	}
}

// ensureGType interns $go.g with the proc.go field set (6 fields).
func (l *pkgLower) ensureGType() {
	if l.mod.LookupType(panicObjName) == nil {
		l.mod.InternType(&ir.TypeDef{
			Name:  panicObjName,
			Kind:  ir.TypeStruct,
			Super: "$go.object",
			Fields: []ir.Field{
				{Name: "value", Type: typeIface},
				{Name: "recovered", Type: ir.TypeI32, Mut: true},
				{Name: "next", Type: &ir.Ref{Null: true, Named: panicObjName}},
			},
		})
	}
	// Stub $go.select so $go.g.sel type-checks even without the scheduler.
	if l.mod.LookupType("$go.select") == nil {
		l.mod.InternType(&ir.TypeDef{
			Name:   "$go.select",
			Kind:   ir.TypeStruct,
			Super:  "$go.object",
			Fields: nil,
		})
	}
	td := l.mod.LookupType("$go.g")
	if td == nil {
		l.mod.InternType(&ir.TypeDef{
			Name:  "$go.g",
			Kind:  ir.TypeStruct,
			Super: "$go.object",
			Fields: gFields(),
		})
		return
	}
	if len(td.Fields) < 6 {
		td.Fields = gFields()
	}
}

func gFields() []ir.Field {
	return []ir.Field{
		{Name: "id", Type: ir.TypeI32, Mut: true},
		{Name: "park", Type: ir.TypeI32, Mut: true},
		{Name: "panic", Type: &ir.Ref{Null: true, Named: panicObjName}, Mut: true},
		{Name: "recoverOk", Type: ir.TypeI32, Mut: true},
		{Name: "sel", Type: &ir.Ref{Null: true, Named: "$go.select"}, Mut: true},
		{Name: "waiting", Type: ir.TypeI32, Mut: true},
	}
}

// typeIface is (ref null $go.iface) — nil interface is a null ref (03).
var typeIface = &ir.Ref{Null: true, Named: "$go.iface"}

func (fl *funcLower) mapType(t types.Type) (ir.Type, error) {
	return fl.pkg.mapType(t)
}

func mapBasic(t *types.Basic) (ir.Type, error) {
	switch t.Kind() {
	case types.Bool,
		types.Int, types.Int8, types.Int16, types.Int32,
		types.Uint, types.Uint8, types.Uint16, types.Uint32, types.Uintptr:
		return ir.TypeI32, nil
	case types.Int64, types.Uint64:
		return ir.TypeI64, nil
	case types.Float32:
		return ir.TypeF32, nil
	case types.Float64:
		return ir.TypeF64, nil
	case types.String:
		return typeString, nil
	case types.UnsafePointer:
		// unsafe.Pointer is anyref (05-unsafe-and-reflect).
		return &ir.Ref{Null: true, Heap: ir.HeapAny}, nil
	case types.UntypedBool:
		return ir.TypeI32, nil
	case types.UntypedInt, types.UntypedRune:
		return ir.TypeI32, nil
	case types.UntypedFloat:
		return ir.TypeF64, nil
	case types.UntypedString:
		return typeString, nil
	case types.UntypedNil:
		return nil, fmt.Errorf("untyped nil needs a typed context") // mapBasic
	case types.Complex64:
		return &ir.Ref{Null: true, Named: "$go.complex64"}, nil
	case types.Complex128, types.UntypedComplex:
		return &ir.Ref{Null: true, Named: "$go.complex128"}, nil
	default:
		return nil, fmt.Errorf("unsupported basic type %s", t)
	}
}

func (l *pkgLower) mapPointer(t *types.Pointer) (ir.Type, error) {
	elem := types.Unalias(t.Elem())
	switch e := elem.(type) {
	case *types.Named:
		if isJSValue(e) {
			// *js.Value is not meaningful for externref; same shape as Value.
			return ir.TypeExtern, nil
		}
		switch u := e.Underlying().(type) {
		case *types.Struct:
			// *Struct is the same ref as the struct value.
			st, err := l.mapNamedStruct(e)
			if err != nil {
				return nil, err
			}
			return st, nil
		case *types.Array:
			// *[N]T and named array pointers share the array ref
			// (e.g. *byteReplacer / *[256]byte → $go.bytes).
			return l.mapArray(u)
		default:
			return l.mapPointerToScalarOrRef(e.Underlying())
		}
	case *types.Struct:
		st, err := l.mapAnonStruct(e)
		if err != nil {
			return nil, err
		}
		return st, nil
	case *types.Array:
		return l.mapArray(e)
	default:
		return l.mapPointerToScalarOrRef(elem)
	}
}

func (l *pkgLower) mapPointerToScalarOrRef(elem types.Type) (ir.Type, error) {
	kind, err := fatPtrKind(elem)
	if err != nil {
		return nil, err
	}
	l.ensureFatPtrTypes(kind)
	return &ir.Ref{Null: true, Named: "$go.ptr." + kind}, nil
}

func (l *pkgLower) mapSlice(t *types.Slice) (ir.Type, error) {
	arrName, _, err := l.ensureArrayType(t.Elem())
	if err != nil {
		return nil, err
	}
	sliceName := sliceTypeName(arrName)
	l.mod.InternType(&ir.TypeDef{
		Name:  sliceName,
		Kind:  ir.TypeStruct,
		Super: "$go.object",
		Fields: []ir.Field{
			{Name: "data", Type: &ir.Ref{Named: arrName}},
			{Name: "off", Type: ir.TypeI32},
			{Name: "len", Type: ir.TypeI32},
			{Name: "cap", Type: ir.TypeI32},
		},
	})
	return &ir.Ref{Null: true, Named: sliceName}, nil
}

func (l *pkgLower) mapArray(t *types.Array) (ir.Type, error) {
	arrName, _, err := l.ensureArrayType(t.Elem())
	if err != nil {
		return nil, err
	}
	return &ir.Ref{Null: true, Named: arrName}, nil
}

func (l *pkgLower) mapNamedStruct(t *types.Named) (ir.Type, error) {
	if alias := runtimeStructAlias(t); alias != "" {
		// Only pull the layouts this alias needs. Calling ensureChanBaseTypes
		// for hmap/mapIter used to intern chan/select/sema types into every
		// map-only module (~type-section bloat).
		switch alias {
		case "$go.type":
			// Prelude layout owned by desc (name=$go.string, extra=$go.object).
			// Do not fill from Go Rtype — unsafe.Pointer fields become any and
			// break ReflectTypeName / descriptor inits.
			l.ensureTypeDescLayout()
			return &ir.Ref{Null: true, Named: "$go.type"}, nil
		case "$go.hmap", "$go.mapIter":
			l.ensureMapBaseTypes()
		case "$go.g", "$go.panicObj":
			l.ensureGType()
		default:
			l.ensureChanBaseTypes()
		}
	}
	name := structTypeName(t)
	existing := l.mod.LookupType(name)
	if existing != nil {
		// Recursion guard: already reserved (possibly still filling). If the
		// type was only a name stub from ensureChanBaseTypes (no fields), fill
		// once using a sentinel so nested *T lookups return early.
		if len(existing.Fields) == 0 {
			existing.Fields = []ir.Field{{Name: "_", Type: ir.TypeI32}} // filling
			st, ok := t.Underlying().(*types.Struct)
			if !ok {
				existing.Fields = nil
				return nil, fmt.Errorf("named type %s is not a struct", t)
			}
			fields, err := l.structFields(st)
			if err != nil {
				existing.Fields = nil
				return nil, err
			}
			existing.Fields = fields
		}
		return &ir.Ref{Null: true, Named: name}, nil
	}
	// Reserve before walking fields (recursive types).
	l.mod.InternType(&ir.TypeDef{
		Name:   name,
		Kind:   ir.TypeStruct,
		Super:  "$go.object",
		Fields: nil,
	})
	st, ok := t.Underlying().(*types.Struct)
	if !ok {
		return nil, fmt.Errorf("named type %s is not a struct", t)
	}
	fields, err := l.structFields(st)
	if err != nil {
		return nil, err
	}
	td := l.mod.LookupType(name)
	td.Fields = fields
	return &ir.Ref{Null: true, Named: name}, nil
}

func (l *pkgLower) mapAnonStruct(t *types.Struct) (ir.Type, error) {
	// Stable name from field layout.
	var b strings.Builder
	b.WriteString("$go.struct.anon")
	for i := 0; i < t.NumFields(); i++ {
		f := t.Field(i)
		b.WriteByte('.')
		b.WriteString(f.Name())
		b.WriteByte('.')
		b.WriteString(strings.ReplaceAll(f.Type().String(), " ", "_"))
	}
	name := b.String()
	if existing := l.mod.LookupType(name); existing != nil {
		return &ir.Ref{Null: true, Named: name}, nil
	}
	l.mod.InternType(&ir.TypeDef{
		Name:   name,
		Kind:   ir.TypeStruct,
		Super:  "$go.object",
		Fields: nil,
	})
	fields, err := l.structFields(t)
	if err != nil {
		return nil, err
	}
	td := l.mod.LookupType(name)
	td.Fields = fields
	return &ir.Ref{Null: true, Named: name}, nil
}

func (l *pkgLower) structFields(st *types.Struct) ([]ir.Field, error) {
	var fields []ir.Field
	for i := 0; i < st.NumFields(); i++ {
		f := st.Field(i)
		if isZeroSize(f.Type()) {
			continue
		}
		ft, err := l.mapType(f.Type())
		if err != nil {
			return nil, fmt.Errorf("field %s: %w", f.Name(), err)
		}
		fld := ir.Field{Name: f.Name(), Type: ft, Mut: true}
		ftGo := types.Unalias(f.Type())
		if a, ok := ftGo.Underlying().(*types.Array); ok {
			fld.Len = a.Len()
		}
		// *NamedStruct / *[N]T share IR with the value; mark so structZero
		// leaves null (e.g. structFields.errUnsupportedFormat *SemanticError).
		if _, ok := ftGo.Underlying().(*types.Pointer); ok {
			if r, ok := ft.(*ir.Ref); ok && (strings.HasPrefix(r.Named, "$go.struct.") ||
				r.Named == "$go.bytes" || r.Named == "$go.i16s" || strings.HasPrefix(r.Named, "$go.array.")) {
				fld.Ptr = true
			}
		}
		fields = append(fields, fld)
	}
	return fields, nil
}

func isZeroSize(t types.Type) bool {
	t = types.Unalias(t)
	switch t := t.(type) {
	case *types.Struct:
		for i := 0; i < t.NumFields(); i++ {
			if !isZeroSize(t.Field(i).Type()) {
				return false
			}
		}
		return true
	case *types.Array:
		return t.Len() == 0 || isZeroSize(t.Elem())
	case *types.Named:
		// js.Value is externref in IR despite an empty Go struct underlying.
		if isJSValue(t) {
			return false
		}
		return isZeroSize(t.Underlying())
	default:
		return false
	}
}

func structTypeName(t *types.Named) string {
	if name := runtimeStructAlias(t); name != "" {
		return name
	}
	pkg := pkgIRPrefix(t.Obj().Pkg())
	return "$go.struct." + pkg + "." + t.Obj().Name() + mono.NamedIRSuffix(t) + mono.LocalTypeSuffix(t.Obj())
}

// runtimeStructAlias maps runtime named structs onto the shared $go.* layouts
// so AppendFiles of the rewrite runtime agrees with user chan / g types.
func runtimeStructAlias(t *types.Named) string {
	obj := t.Obj()
	if obj == nil || obj.Pkg() == nil || obj.Pkg().Path() != "runtime" && obj.Pkg().Name() != "runtime" {
		return ""
	}
	switch obj.Name() {
	case "hchan":
		return "$go.chan"
	case "waiter":
		return "$go.waiter"
	case "waitq":
		return "$go.waitq"
	case "g":
		return "$go.g"
	case "chanOps":
		return "$go.chanOps"
	case "scase":
		return "$go.scase"
	case "select_":
		return "$go.select"
	case "runqnode":
		return "$go.runqnode"
	case "semaRoot":
		return "$go.semaRoot"
	case "panicObj":
		return "$go.panicObj"
	case "hmap":
		return "$go.hmap"
	case "mapIter":
		return "$go.mapIter"
	case "Rtype":
		return "$go.type"
	}
	return ""
}

// ensureArrayType interns $go.array.* (or reuses $go.bytes / $go.i16s) and
// returns (arrayTypeName, elementIRType).
func (l *pkgLower) ensureArrayType(elem types.Type) (string, ir.Type, error) {
	elem = types.Unalias(elem)
	if b, ok := elem.Underlying().(*types.Basic); ok {
		switch b.Kind() {
		case types.Int8, types.Uint8, types.Bool:
			return "$go.bytes", ir.TypeI8, nil
		case types.Int16, types.Uint16:
			return "$go.i16s", ir.TypeI16, nil
		}
	}
	elemIR, err := l.mapType(elem)
	if err != nil {
		return "", nil, err
	}
	name := arrayTypeName(elemIR)
	l.mod.InternType(&ir.TypeDef{
		Name:    name,
		Kind:    ir.TypeArray,
		Elem:    storageElem(elemIR),
		ElemMut: true,
	})
	return name, elemIR, nil
}

func storageElem(t ir.Type) ir.Type {
	// Packed arrays already handled; value types stay as-is.
	return t
}

func arrayTypeName(elemIR ir.Type) string {
	switch t := elemIR.(type) {
	case *ir.NumType:
		return "$go.array." + t.Kind.String()
	case *ir.Ref:
		if t.Named != "" {
			return "$go.array." + strings.TrimPrefix(t.Named, "$")
		}
	}
	return "$go.array." + strings.ReplaceAll(ir.TypeString(elemIR), " ", "_")
}

func sliceTypeName(arrName string) string {
	switch arrName {
	case "$go.bytes":
		return "$go.slice.i8"
	case "$go.i16s":
		return "$go.slice.i16"
	}
	if strings.HasPrefix(arrName, "$go.array.") {
		return "$go.slice." + strings.TrimPrefix(arrName, "$go.array.")
	}
	return "$go.slice." + strings.TrimPrefix(arrName, "$")
}

// fatPtrKind returns the class suffix: i32, i64, f32, f64, or ref.
func fatPtrKind(elem types.Type) (string, error) {
	elem = types.Unalias(elem)
	switch u := elem.Underlying().(type) {
	case *types.Basic:
		switch u.Kind() {
		case types.Bool,
			types.Int, types.Int8, types.Int16, types.Int32,
			types.Uint, types.Uint8, types.Uint16, types.Uint32, types.Uintptr:
			return "i32", nil
		case types.Int64, types.Uint64:
			return "i64", nil
		case types.Float32:
			return "f32", nil
		case types.Float64:
			return "f64", nil
		case types.String,
			types.Complex64, types.Complex128, types.UntypedComplex:
			// Stored as $go.complex{64,128} struct refs (docs/design/03).
			return "ref", nil
		case types.UnsafePointer:
			// *unsafe.Pointer (sync/atomic.LoadPointer) — host ref cell.
			return "ref", nil
		}
	case *types.Slice, *types.Pointer, *types.Map, *types.Chan, *types.Signature, *types.Interface:
		return "ref", nil
	case *types.Struct, *types.Array:
		return "ref", nil
	case *types.Named:
		return fatPtrKind(u.Underlying())
	}
	return "", fmt.Errorf("cannot form fat pointer to %s", elem)
}

func (l *pkgLower) ensureFatPtrTypes(kind string) {
	getName := "$go.get." + kind
	setName := "$go.set." + kind
	className := "$go.class." + kind
	ptrName := "$go.ptr." + kind

	var val ir.Type
	switch kind {
	case "i32":
		val = ir.TypeI32
	case "i64":
		val = ir.TypeI64
	case "f32":
		val = ir.TypeF32
	case "f64":
		val = ir.TypeF64
	case "ref":
		val = &ir.Ref{Null: true, Heap: ir.HeapAny}
	default:
		val = ir.TypeI32
	}

	l.mod.InternType(&ir.TypeDef{
		Name:    getName,
		Kind:    ir.TypeFunc,
		Params:  []ir.Type{&ir.Ref{Null: true, Heap: ir.HeapAny}, ir.TypeI32},
		Results: []ir.Type{val},
	})
	l.mod.InternType(&ir.TypeDef{
		Name:   setName,
		Kind:   ir.TypeFunc,
		Params: []ir.Type{&ir.Ref{Null: true, Heap: ir.HeapAny}, ir.TypeI32, val},
	})
	l.mod.InternType(&ir.TypeDef{
		Name:  className,
		Kind:  ir.TypeStruct,
		Super: "$go.object",
		Fields: []ir.Field{
			{Name: "get", Type: &ir.Ref{Named: getName}},
			{Name: "set", Type: &ir.Ref{Named: setName}},
		},
	})
	l.mod.InternType(&ir.TypeDef{
		Name:  ptrName,
		Kind:  ir.TypeStruct,
		Super: "$go.object",
		Fields: []ir.Field{
			{Name: "class", Type: &ir.Ref{Named: className}},
			{Name: "base", Type: &ir.Ref{Null: true, Heap: ir.HeapAny}},
			{Name: "index", Type: ir.TypeI32},
		},
	})
}

func isUnsigned(t types.Type) bool {
	t = types.Unalias(t)
	if b, ok := t.Underlying().(*types.Basic); ok {
		switch b.Kind() {
		case types.Uint, types.Uint8, types.Uint16, types.Uint32, types.Uint64, types.Uintptr:
			return true
		}
	}
	return false
}

func isInteger(t types.Type) bool {
	t = types.Unalias(t)
	b, ok := t.Underlying().(*types.Basic)
	if !ok {
		return false
	}
	return b.Info()&types.IsInteger != 0
}

func isBoolean(t types.Type) bool {
	t = types.Unalias(t)
	b, ok := t.Underlying().(*types.Basic)
	if !ok {
		return false
	}
	return b.Info()&types.IsBoolean != 0
}

func isString(t types.Type) bool {
	t = types.Unalias(t)
	b, ok := t.Underlying().(*types.Basic)
	if !ok {
		return false
	}
	return b.Info()&types.IsString != 0
}

func isComplex(t types.Type) bool {
	t = types.Unalias(t)
	b, ok := t.Underlying().(*types.Basic)
	if !ok {
		return false
	}
	return b.Info()&types.IsComplex != 0
}

func isFloat(t types.Type) bool {
	t = types.Unalias(t)
	b, ok := t.Underlying().(*types.Basic)
	if !ok {
		return false
	}
	return b.Info()&types.IsFloat != 0
}

func isSlice(t types.Type) bool {
	t = types.Unalias(t)
	_, ok := t.Underlying().(*types.Slice)
	return ok
}

// isByteSliceOrArray reports []byte / [N]byte / *[N]byte.
func isByteSliceOrArray(t types.Type) bool {
	t = types.Unalias(t)
	if isPtrToArray(t) {
		p := t.Underlying().(*types.Pointer)
		t = types.Unalias(p.Elem())
	}
	switch u := t.Underlying().(type) {
	case *types.Slice:
		return isByteElem(u.Elem())
	case *types.Array:
		return isByteElem(u.Elem())
	}
	return false
}

// isRuneSlice reports []rune (or []int32 named as rune).
func isRuneSlice(t types.Type) bool {
	s, ok := types.Unalias(t).Underlying().(*types.Slice)
	if !ok {
		return false
	}
	e := types.Unalias(s.Elem())
	if b, ok := e.Underlying().(*types.Basic); ok {
		return b.Kind() == types.Int32 || b.Kind() == types.UntypedRune
	}
	// Named rune alias.
	if n, ok := e.(*types.Named); ok && n.Obj() != nil && n.Obj().Name() == "rune" {
		return true
	}
	return false
}

func isByteElem(t types.Type) bool {
	b, ok := types.Unalias(t).Underlying().(*types.Basic)
	if !ok {
		return false
	}
	k := b.Kind()
	return k == types.Uint8 || k == types.Int8 // Byte aliases Uint8
}

func isArray(t types.Type) bool {
	t = types.Unalias(t)
	_, ok := t.Underlying().(*types.Array)
	return ok
}

func isPtrToArray(t types.Type) bool {
	p, ok := types.Unalias(t).Underlying().(*types.Pointer)
	if !ok {
		return false
	}
	_, ok = types.Unalias(p.Elem()).Underlying().(*types.Array)
	return ok
}

// arrayRangeLen returns N for [N]T or *[N]T (range length is N even if nil).
func arrayRangeLen(t types.Type) (int64, bool) {
	t = types.Unalias(t)
	if a, ok := t.Underlying().(*types.Array); ok {
		return a.Len(), true
	}
	if p, ok := t.Underlying().(*types.Pointer); ok {
		if a, ok := types.Unalias(p.Elem()).Underlying().(*types.Array); ok {
			return a.Len(), true
		}
	}
	return 0, false
}

func arrayElemType(t types.Type) types.Type {
	if a, ok := types.Unalias(t).Underlying().(*types.Array); ok {
		return a.Elem()
	}
	return nil
}

// rangeElemType is the element type of a slice, array, or *[N]T range expression.
func rangeElemType(t types.Type) types.Type {
	if t == nil {
		return nil
	}
	u := types.Unalias(t).Underlying()
	switch u := u.(type) {
	case *types.Slice:
		return u.Elem()
	case *types.Array:
		return u.Elem()
	case *types.Pointer:
		if a, ok := types.Unalias(u.Elem()).Underlying().(*types.Array); ok {
			return a.Elem()
		}
	}
	return nil
}

func isStruct(t types.Type) bool {
	t = types.Unalias(t)
	_, ok := t.Underlying().(*types.Struct)
	return ok
}

// isJSValue reports the syscall/js.Value named type (mapped to externref).
func isJSValue(t *types.Named) bool {
	if t == nil || t.Obj() == nil || t.Obj().Pkg() == nil {
		return false
	}
	return t.Obj().Pkg().Path() == "syscall/js" && t.Obj().Name() == "Value"
}

func isJSValueType(t types.Type) bool {
	n, ok := types.Unalias(t).(*types.Named)
	return ok && isJSValue(n)
}

func isPointer(t types.Type) bool {
	t = types.Unalias(t)
	_, ok := t.Underlying().(*types.Pointer)
	return ok
}

func isSignature(t types.Type) bool {
	t = types.Unalias(t)
	_, ok := t.Underlying().(*types.Signature)
	return ok
}

func underlyingStruct(t types.Type) *types.Struct {
	t = types.Unalias(t)
	if n, ok := t.(*types.Named); ok {
		t = n.Underlying()
	}
	s, _ := t.(*types.Struct)
	return s
}

func basicKind(t types.Type) types.BasicKind {
	t = types.Unalias(t)
	if b, ok := t.Underlying().(*types.Basic); ok {
		return b.Kind()
	}
	return types.Invalid
}

func irNumWidth(t ir.Type) ir.NumKind {
	if n, ok := t.(*ir.NumType); ok {
		return n.Kind
	}
	return ir.I32
}

// isObjectPointer reports whether *T is represented as a plain ref (struct/array),
// not a fat pointer.
func isObjectPointer(t types.Type) bool {
	p, ok := types.Unalias(t).Underlying().(*types.Pointer)
	if !ok {
		return false
	}
	elem := types.Unalias(p.Elem())
	switch e := elem.(type) {
	case *types.Named:
		switch e.Underlying().(type) {
		case *types.Struct, *types.Array:
			return true
		}
	case *types.Struct, *types.Array:
		return true
	}
	return false
}

func refTypeName(t ir.Type) string {
	if r, ok := t.(*ir.Ref); ok {
		return r.Named
	}
	return ""
}
