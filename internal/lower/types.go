package lower

import (
	"fmt"
	"go/types"
	"strings"

	"github.com/achille-roussel/wasmgc-go/internal/ir"
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
	t = types.Unalias(t)
	switch t := t.(type) {
	case *types.Basic:
		return mapBasic(t)
	case *types.Named:
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
		return nil, fmt.Errorf("function type not supported as value in M4: %s", t)
	case *types.Tuple:
		return nil, fmt.Errorf("tuple type not a single value: %s", t)
	case *types.Map:
		return nil, fmt.Errorf("map type not supported until M6: %s", t)
	case *types.Chan:
		return nil, fmt.Errorf("chan type not supported until M7: %s", t)
	default:
		return nil, fmt.Errorf("unsupported type in M4: %s", t)
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
	case types.UntypedBool:
		return ir.TypeI32, nil
	case types.UntypedInt, types.UntypedRune:
		return ir.TypeI32, nil
	case types.UntypedFloat:
		return ir.TypeF64, nil
	case types.UntypedString:
		return typeString, nil
	case types.UntypedNil:
		return nil, fmt.Errorf("untyped nil needs a typed context")
	case types.Complex64, types.Complex128, types.UntypedComplex:
		return nil, fmt.Errorf("complex numbers not supported in M3")
	default:
		return nil, fmt.Errorf("unsupported basic type %s", t)
	}
}

func (l *pkgLower) mapPointer(t *types.Pointer) (ir.Type, error) {
	elem := types.Unalias(t.Elem())
	switch e := elem.(type) {
	case *types.Named:
		switch e.Underlying().(type) {
		case *types.Struct:
			// *Struct is the same ref as the struct value.
			st, err := l.mapNamedStruct(e)
			if err != nil {
				return nil, err
			}
			return st, nil
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
	name := structTypeName(t)
	if existing := l.mod.LookupType(name); existing != nil {
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
		fields = append(fields, ir.Field{Name: f.Name(), Type: ft, Mut: true})
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
		return isZeroSize(t.Underlying())
	default:
		return false
	}
}

func structTypeName(t *types.Named) string {
	pkg := "main"
	if t.Obj().Pkg() != nil {
		pkg = t.Obj().Pkg().Name()
	}
	return "$go.struct." + pkg + "." + t.Obj().Name()
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
		case types.String:
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

func isArray(t types.Type) bool {
	t = types.Unalias(t)
	_, ok := t.Underlying().(*types.Array)
	return ok
}

func isStruct(t types.Type) bool {
	t = types.Unalias(t)
	_, ok := t.Underlying().(*types.Struct)
	return ok
}

func isPointer(t types.Type) bool {
	t = types.Unalias(t)
	_, ok := t.Underlying().(*types.Pointer)
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
