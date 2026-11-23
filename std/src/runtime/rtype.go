package runtime

import "unsafe"

// Rtype is the Go view of $go.type (docs/design/03). Field names match the
// Wasm layout; the concrete Wasm typedef is the prelude's $go.type (aliased).
// name/extra are unsafe.Pointer stand-ins for (ref null $go.string) /
// (ref null $go.object) — read them only via the Reflect* helpers below.
type Rtype struct {
	id    int32
	kind  int32
	name  unsafe.Pointer
	extra unsafe.Pointer
}

// Kind returns the abi/reflect kind discriminant stored on the descriptor.
func (t *Rtype) Kind() int32 {
	if t == nil {
		return 0
	}
	return t.kind
}

// ID returns the dense descriptor id.
func (t *Rtype) ID() int32 {
	if t == nil {
		return 0
	}
	return t.id
}

// ReflectType returns the concrete type descriptor of a non-nil interface.
// Body-less: desc fills the IR (iface.itab.type).
func ReflectType(i any) *Rtype

// ReflectData returns the interface data word (anyref).
// Body-less: desc fills the IR (iface.data).
func ReflectData(i any) unsafe.Pointer

// ReflectTypeName returns the descriptor name, or "" when null.
// Body-less: desc fills the IR.
func ReflectTypeName(t *Rtype) string

// ReflectExtra returns the kind-specific $extra object (may be nil).
// Body-less: desc fills the IR.
func ReflectExtra(t *Rtype) unsafe.Pointer

// ReflectCallGetField calls $go.extra.struct.getField(data, i) → anyref.
// Body-less: desc fills when struct extras exist.
func ReflectCallGetField(extra unsafe.Pointer, data unsafe.Pointer, i int) unsafe.Pointer

// ReflectCallSetField calls $go.extra.struct.setField(data, i, v).
func ReflectCallSetField(extra unsafe.Pointer, data unsafe.Pointer, i int, v unsafe.Pointer)

// ReflectCallIndex calls $go.extra.slice.index(data, i) → anyref.
func ReflectCallIndex(extra unsafe.Pointer, data unsafe.Pointer, i int) unsafe.Pointer

// ReflectCallSetIndex calls $go.extra.slice.setIndex(data, i, v).
func ReflectCallSetIndex(extra unsafe.Pointer, data unsafe.Pointer, i int, v unsafe.Pointer)

// ReflectCallLen calls $go.extra.slice.len(data) → i32.
func ReflectCallLen(extra unsafe.Pointer, data unsafe.Pointer) int

// ReflectString unboxes a string data word to a Go string.
// Body-less: desc fills (ref.cast to $go.string).
func ReflectString(data unsafe.Pointer) string

// ReflectUnboxI32 reads $go.box.i32.v (bool/int/uint family).
func ReflectUnboxI32(data unsafe.Pointer) int32

// ReflectUnboxI64 reads $go.box.i64.v.
func ReflectUnboxI64(data unsafe.Pointer) int64

// ReflectUnboxF32 reads $go.box.f32.v.
func ReflectUnboxF32(data unsafe.Pointer) float32

// ReflectUnboxF64 reads $go.box.f64.v.
func ReflectUnboxF64(data unsafe.Pointer) float64

// ReflectMapLen returns len of a map whose data word is an hmap ref.
// Body requires hmap from map.go — EnsureReflect typechecks them together.
func ReflectMapLen(data unsafe.Pointer) int {
	if data == nil {
		return 0
	}
	h := (*hmap)(data)
	if h == nil {
		return 0
	}
	return int(h.used)
}

// ReflectMapPairs returns snapshot key/value slices for map ranging (fmtsort).
func ReflectMapPairs(data unsafe.Pointer) (keys, vals []any) {
	if data == nil {
		return nil, nil
	}
	h := (*hmap)(data)
	if h == nil || h.ctrl == nil {
		return nil, nil
	}
	n := int(h.used)
	keys = make([]any, 0, n)
	vals = make([]any, 0, n)
	for i := 0; i < len(h.ctrl); i++ {
		if h.ctrl[i] == ctrlFull {
			keys = append(keys, h.keys[i])
			vals = append(vals, h.vals[i])
		}
	}
	return keys, vals
}

// ReflectIface returns a non-nil interface from a type descriptor and data word.
// Body-less: desc builds $go.iface{itab-for-any, data}.
func ReflectIface(typ *Rtype, data unsafe.Pointer) any

// ReflectStructNumField reads $go.extra.struct.numField.
func ReflectStructNumField(extra unsafe.Pointer) int

// ReflectStructFieldType calls extra.fieldType(i).
func ReflectStructFieldType(extra unsafe.Pointer, i int) *Rtype

// ReflectStructFieldName calls extra.fieldName(i).
func ReflectStructFieldName(extra unsafe.Pointer, i int) string

// ReflectStructFieldTag calls extra.fieldTag(i) → (*types.Struct).Tag(i).
func ReflectStructFieldTag(extra unsafe.Pointer, i int) string

// ReflectSliceElem reads $go.extra.slice.elem.
func ReflectSliceElem(extra unsafe.Pointer) *Rtype

// ReflectPtrElem reads $go.extra.ptr.elem.
func ReflectPtrElem(extra unsafe.Pointer) *Rtype

// ReflectMapKey reads $go.extra.map.key.
func ReflectMapKey(extra unsafe.Pointer) *Rtype

// ReflectMapElem reads $go.extra.map.elem.
func ReflectMapElem(extra unsafe.Pointer) *Rtype

// ReflectSliceOf returns the descriptor for []T given T's descriptor.
// Body-less: desc fills an id→global.get table over the closed universe.
func ReflectSliceOf(t *Rtype) *Rtype

// ReflectPointerTo returns the descriptor for *T given T's descriptor.
// Body-less: desc fills an id→global.get table over the closed universe.
func ReflectPointerTo(t *Rtype) *Rtype

// ReflectMapOfEmpty returns the descriptor for map[K]struct{} given K's descriptor.
// Body-less: desc fills an id→global.get table over the closed universe.
func ReflectMapOfEmpty(key *Rtype) *Rtype

// ReflectNew allocates a zero value of t (data word for New(t).Elem()).
// Body-less: desc fills an id→alloc table over the closed universe.
func ReflectNew(t *Rtype) unsafe.Pointer

// ReflectMakeSlice allocates a slice header for typ with the given len/cap.
// Body-less: desc fills an id→make.slice table.
func ReflectMakeSlice(t *Rtype, len, cap int) unsafe.Pointer

// ReflectSetBoxI32 writes v into a $go.box.i32 data word (New().Elem() scalars).
func ReflectSetBoxI32(data unsafe.Pointer, v int32)

// ReflectSetBoxI64 writes v into a $go.box.i64 data word.
func ReflectSetBoxI64(data unsafe.Pointer, v int64)

// ReflectSetBoxF32 writes v into a $go.box.f32 data word.
func ReflectSetBoxF32(data unsafe.Pointer, v float32)

// ReflectSetBoxF64 writes v into a $go.box.f64 data word.
func ReflectSetBoxF64(data unsafe.Pointer, v float64)

// ReflectSetBoxString writes v into a $go.box.string data word.
func ReflectSetBoxString(data unsafe.Pointer, v string)

// ReflectNewRefBox allocates a $go.box.ref holding v (may be nil).
// Used by reflect.New for map/slice/iface so Elem().Set is addressable.
func ReflectNewRefBox(v unsafe.Pointer) unsafe.Pointer

// ReflectSetBoxRef writes v into a $go.box.ref data word.
func ReflectSetBoxRef(data unsafe.Pointer, v unsafe.Pointer)

// ReflectUnboxRef reads the anyref from a $go.box.ref data word.
func ReflectUnboxRef(data unsafe.Pointer) unsafe.Pointer

// ReflectFatPtrGet loads through a $go.ptr.ref fat pointer (e.g. *map, *slice).
// Body-less: desc fills call_ref through ptr.class.get.
func ReflectFatPtrGet(data unsafe.Pointer) unsafe.Pointer

// ReflectFatPtrSet stores through a $go.ptr.ref fat pointer.
// Body-less: desc fills call_ref through ptr.class.set.
func ReflectFatPtrSet(data unsafe.Pointer, v unsafe.Pointer)

// ReflectMakeMap allocates an empty hmap (all map types share one layout).
func ReflectMakeMap() unsafe.Pointer {
	return unsafe.Pointer(makemap(0))
}

// ReflectMapAssign inserts or updates key→val in the map data word.
func ReflectMapAssign(data unsafe.Pointer, key, val any) {
	if data == nil {
		throw("assignment to entry in nil map")
	}
	mapAssign((*hmap)(data), key, val)
}

// ReflectMapAccess looks up key in the map data word.
func ReflectMapAccess(data unsafe.Pointer, key any) (any, bool) {
	if data == nil {
		return nil, false
	}
	return mapAccess((*hmap)(data), key)
}
