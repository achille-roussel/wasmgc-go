// Package reflect is a wasmgc rewrite on type descriptors (docs/design/05).
package reflect

import (
	"runtime"
	"unsafe"
)

type Kind uint

const (
	Invalid Kind = iota
	Bool
	Int
	Int8
	Int16
	Int32
	Int64
	Uint
	Uint8
	Uint16
	Uint32
	Uint64
	Uintptr
	Float32
	Float64
	Complex64
	Complex128
	Array
	Chan
	Func
	Interface
	Map
	Pointer
	Slice
	String
	Struct
	UnsafePointer
)

const Ptr = Pointer

var kindNames = []string{
	Invalid: "invalid", Bool: "bool", Int: "int", Int8: "int8", Int16: "int16",
	Int32: "int32", Int64: "int64", Uint: "uint", Uint8: "uint8", Uint16: "uint16",
	Uint32: "uint32", Uint64: "uint64", Uintptr: "uintptr", Float32: "float32",
	Float64: "float64", Complex64: "complex64", Complex128: "complex128",
	Array: "array", Chan: "chan", Func: "func", Interface: "interface",
	Map: "map", Pointer: "ptr", Slice: "slice", String: "string", Struct: "struct",
	UnsafePointer: "unsafe.Pointer",
}

func (k Kind) String() string {
	if int(k) < len(kindNames) {
		return kindNames[k]
	}
	return "kind"
}

type Type interface {
	Kind() Kind
	Name() string
	PkgPath() string
	String() string
	Elem() Type
	Key() Type
	Len() int
	Bits() int
	NumField() int
	Field(i int) StructField
	NumIn() int
	NumOut() int
	In(i int) Type
	Out(i int) Type
	Implements(u Type) bool
	AssignableTo(u Type) bool
	ConvertibleTo(u Type) bool
	Comparable() bool
	Size() uintptr
	Align() int
	FieldAlign() int
	NumMethod() int
	Method(int) Method
	IsVariadic() bool
	ChanDir() ChanDir
}

type ChanDir int

const (
	RecvDir ChanDir = 1 << iota
	SendDir
	BothDir = RecvDir | SendDir
)

type Method struct {
	Name  string
	Type  Type
	Func  Value
	Index int
}

type StructField struct {
	Name      string
	PkgPath   string
	Type      Type
	Tag       StructTag
	Offset    uintptr
	Index     []int
	Anonymous bool
}

// IsExported reports whether the field is exported.
func (f StructField) IsExported() bool {
	if f.Name == "" {
		return false
	}
	c := f.Name[0]
	return 'A' <= c && c <= 'Z'
}

type StructTag string

func (tag StructTag) Get(key string) string {
	v, _ := tag.Lookup(key)
	return v
}

// Lookup returns the value associated with key in the tag string.
// Copied from upstream reflect/type.go (strconv-free unquote).
func (tag StructTag) Lookup(key string) (value string, ok bool) {
	for tag != "" {
		i := 0
		for i < len(tag) && tag[i] == ' ' {
			i++
		}
		tag = tag[i:]
		if tag == "" {
			break
		}
		i = 0
		for i < len(tag) && tag[i] > ' ' && tag[i] != ':' && tag[i] != '"' && tag[i] != 0x7f {
			i++
		}
		if i == 0 || i+1 >= len(tag) || tag[i] != ':' || tag[i+1] != '"' {
			break
		}
		name := string(tag[:i])
		tag = tag[i+1:]
		i = 1
		for i < len(tag) && tag[i] != '"' {
			if tag[i] == '\\' {
				i++
			}
			i++
		}
		if i >= len(tag) {
			break
		}
		qvalue := string(tag[:i+1])
		tag = tag[i+1:]
		if key == name {
			value, ok = unquoteTag(qvalue)
			return value, ok
		}
	}
	return "", false
}

func unquoteTag(q string) (string, bool) {
	if len(q) < 2 || q[0] != '"' || q[len(q)-1] != '"' {
		return "", false
	}
	s := q[1 : len(q)-1]
	if !containsByte(s, '\\') && !containsByte(s, '"') {
		return s, true
	}
	var b []byte
	for i := 0; i < len(s); i++ {
		c := s[i]
		if c == '\\' {
			i++
			if i >= len(s) {
				return "", false
			}
			c = s[i]
			switch c {
			case '"', '\\', '\'':
				b = append(b, c)
			case 'n':
				b = append(b, '\n')
			case 't':
				b = append(b, '\t')
			default:
				b = append(b, c)
			}
			continue
		}
		b = append(b, c)
	}
	return string(b), true
}

func containsByte(s string, c byte) bool {
	for i := 0; i < len(s); i++ {
		if s[i] == c {
			return true
		}
	}
	return false
}

type Value struct {
	typ     *rtype
	data    unsafe.Pointer
	flag    flag
	base    unsafe.Pointer // parent struct/slice when flagField/flagIndex
	baseTyp *rtype
	slot    int
}

type flag uintptr

const (
	flagRO flag = 1 << iota
	flagIndir
	flagAddr
	flagField  // Set via setField(base, slot, …)
	flagIndex  // Set via setIndex(base, slot, …)
	flagRefBox // data is $go.box.ref holding a map/slice/iface/chan/func/pointer
	flagFatPtr // data is $go.ptr.ref; load/store through class get/set (*map, *slice, …)
)

func (v Value) Kind() Kind {
	if v.typ == nil {
		return Invalid
	}
	return v.typ.Kind()
}

func (v Value) Type() Type {
	if v.typ == nil {
		return nil
	}
	return v.typ
}

func (v Value) IsValid() bool { return v.typ != nil }

func (v Value) IsNil() bool {
	if v.typ == nil {
		return false
	}
	switch v.Kind() {
	case Pointer:
		if v.flag&flagRefBox != 0 {
			return false // box pointer is always non-nil
		}
		return v.data == nil
	case Interface, Slice, Map, Chan, Func, UnsafePointer:
		return v.dataWord() == nil
	}
	return false
}

func (v Value) CanInterface() bool {
	return v.typ != nil && v.flag&flagRO == 0
}

func (v Value) Interface() any {
	if !v.IsValid() {
		panic("reflect: call of reflect.Value.Interface on zero Value")
	}
	// Scalars/strings may live in mutable boxes (New().Elem(), fields).
	// Interface must return a snapshot so map storage / any assignment
	// does not alias the box (json SetMapIndex, MapIndex round-trips).
	switch v.Kind() {
	case String:
		return runtime.ReflectString(v.dataWord())
	case Bool:
		return v.Bool()
	case Int:
		return int(v.Int())
	case Int8:
		return int8(v.Int())
	case Int16:
		return int16(v.Int())
	case Int32:
		return int32(v.Int())
	case Int64:
		return v.Int()
	case Uint:
		return uint(v.Uint())
	case Uint8:
		return uint8(v.Uint())
	case Uint16:
		return uint16(v.Uint())
	case Uint32:
		return uint32(v.Uint())
	case Uint64:
		return v.Uint()
	case Uintptr:
		return uintptr(v.Uint())
	case Float32:
		return float32(v.Float())
	case Float64:
		return v.Float()
	}
	return runtime.ReflectIface(v.typ.rt, v.dataWord())
}

func (v Value) Elem() Value {
	if v.typ == nil {
		return Value{}
	}
	switch v.Kind() {
	case Pointer:
		if v.data == nil {
			return Value{}
		}
		et := runtime.ReflectPtrElem(runtime.ReflectExtra(v.typ.rt))
		elem := wrap(et)
		// Pointee of New(t) / *T is always addressable.
		fl := flagAddr
		if v.flag&flagRefBox != 0 {
			// reflect.New(map/slice/…) boxes the null ref.
			fl |= flagRefBox
		} else {
			// ValueOf(&localMap): *map is $go.ptr.ref (not New's $go.box.ref).
			// Only these kinds use flagRefBox in New; scalars use typed boxes
			// and structs/arrays use the object ref itself as *T.
			switch elem.Kind() {
			case Map, Slice, Interface, Chan, Func:
				fl |= flagFatPtr
			}
		}
		return Value{typ: elem, data: v.data, flag: fl}
	case Interface:
		if v.data == nil {
			return Value{}
		}
		return ValueOf(runtime.ReflectIface(v.typ.rt, v.data))
	}
	return Value{}
}

func (v Value) NumField() int {
	if v.Kind() != Struct {
		panic("reflect: NumField of non-struct type")
	}
	return runtime.ReflectStructNumField(runtime.ReflectExtra(v.typ.rt))
}

func (v Value) Field(i int) Value {
	if v.Kind() != Struct {
		panic("reflect: Field of non-struct type")
	}
	extra := runtime.ReflectExtra(v.typ.rt)
	ft := runtime.ReflectStructFieldType(extra, i)
	fd := runtime.ReflectCallGetField(extra, v.data, i)
	fl := v.flag & (flagAddr | flagRO)
	out := Value{typ: wrap(ft), data: fd, flag: fl}
	if fl&flagAddr != 0 {
		out.flag |= flagField
		out.base = v.data
		out.baseTyp = v.typ
		out.slot = i
	}
	return out
}

func (v Value) Len() int {
	switch v.Kind() {
	case String:
		return len(runtime.ReflectString(v.dataWord()))
	case Slice, Array:
		return runtime.ReflectCallLen(runtime.ReflectExtra(v.typ.rt), v.dataWord())
	case Map:
		return runtime.ReflectMapLen(v.dataWord())
	case Chan:
		return 0
	}
	panic("reflect: call of Len on " + v.Kind().String())
}

func (v Value) Cap() int { return v.Len() }

func (v Value) Index(i int) Value {
	switch v.Kind() {
	case Slice, Array:
		extra := runtime.ReflectExtra(v.typ.rt)
		et := runtime.ReflectSliceElem(extra)
		ed := runtime.ReflectCallIndex(extra, v.dataWord(), i)
		// Indexed elements are always addressable.
		return Value{
			typ:     wrap(et),
			data:    ed,
			flag:    flagAddr | flagIndex,
			base:    v.dataWord(),
			baseTyp: v.typ,
			slot:    i,
		}
	case String:
		s := runtime.ReflectString(v.dataWord())
		return ValueOf(s[i])
	}
	panic("reflect: call of Index on " + v.Kind().String())
}

// dataWord returns the current data pointer, re-fetching from a parent
// container when this Value is an addressable field/index slot.
func (v Value) dataWord() unsafe.Pointer {
	if v.flag&flagField != 0 && v.baseTyp != nil && v.baseTyp.rt != nil {
		return runtime.ReflectCallGetField(runtime.ReflectExtra(v.baseTyp.rt), v.base, v.slot)
	}
	if v.flag&flagIndex != 0 && v.baseTyp != nil && v.baseTyp.rt != nil {
		return runtime.ReflectCallIndex(runtime.ReflectExtra(v.baseTyp.rt), v.base, v.slot)
	}
	if v.flag&flagFatPtr != 0 && v.Kind() != Pointer {
		return runtime.ReflectFatPtrGet(v.data)
	}
	if v.flag&flagRefBox != 0 && v.Kind() != Pointer {
		return runtime.ReflectUnboxRef(v.data)
	}
	return v.data
}

func (v Value) Bool() bool {
	if v.Kind() != Bool {
		panic("reflect: call of Bool on " + v.Kind().String())
	}
	return runtime.ReflectUnboxI32(v.dataWord()) != 0
}

func (v Value) Int() int64 {
	switch v.Kind() {
	case Int, Int8, Int16, Int32:
		return int64(runtime.ReflectUnboxI32(v.dataWord()))
	case Int64:
		return runtime.ReflectUnboxI64(v.dataWord())
	}
	panic("reflect: call of Int on " + v.Kind().String())
}

func (v Value) Uint() uint64 {
	switch v.Kind() {
	case Uint, Uint8, Uint16, Uint32, Uintptr:
		return uint64(uint32(runtime.ReflectUnboxI32(v.dataWord())))
	case Uint64:
		return uint64(runtime.ReflectUnboxI64(v.dataWord()))
	}
	panic("reflect: call of Uint on " + v.Kind().String())
}

func (v Value) Float() float64 {
	switch v.Kind() {
	case Float32:
		return float64(runtime.ReflectUnboxF32(v.dataWord()))
	case Float64:
		return runtime.ReflectUnboxF64(v.dataWord())
	}
	panic("reflect: call of Float on " + v.Kind().String())
}

func (v Value) Complex() complex128 {
	panic("reflect: Complex not implemented")
}

func (v Value) String() string {
	switch v.Kind() {
	case Invalid:
		return "<invalid Value>"
	case String:
		return runtime.ReflectString(v.dataWord())
	}
	return "<" + v.Kind().String() + " Value>"
}

func (v Value) Bytes() []byte {
	switch v.Kind() {
	case Slice, Array:
		if v.Type().Elem().Kind() != Uint8 {
			panic("reflect: Bytes of non-byte slice/array")
		}
		n := v.Len()
		b := make([]byte, n)
		for i := 0; i < n; i++ {
			b[i] = byte(v.Index(i).Uint())
		}
		return b
	case String:
		return []byte(runtime.ReflectString(v.data))
	}
	panic("reflect: Bytes of non-byte type")
}

func (v Value) CanAddr() bool { return v.flag&flagAddr != 0 }

func (v Value) Addr() Value {
	if !v.CanAddr() {
		panic("reflect: call of Addr on unaddressable Value")
	}
	pt := PointerTo(v.Type())
	return Value{typ: asRtype(pt), data: v.dataWord()}
}

func (v Value) UnsafePointer() unsafe.Pointer {
	switch v.Kind() {
	case Pointer, Map, Chan, Func, Slice, UnsafePointer:
		return v.dataWord()
	}
	panic("reflect: call of UnsafePointer on " + v.Kind().String() + " (no integer address on wasmgc)")
}

func (v Value) Pointer() uintptr {
	panic("reflect: Value.Pointer is not supported on wasmgc (no integer address)")
}

func (v Value) MapRange() *MapIter {
	if v.Kind() != Map {
		panic("reflect: MapRange of non-map type")
	}
	keys, vals := runtime.ReflectMapPairs(v.dataWord())
	extra := runtime.ReflectExtra(v.typ.rt)
	return &MapIter{
		keys: keys,
		vals: vals,
		kt:   wrap(runtime.ReflectMapKey(extra)),
		vt:   wrap(runtime.ReflectMapElem(extra)),
		i:    -1,
	}
}

func (v Value) MapKeys() []Value {
	it := v.MapRange()
	var out []Value
	for it.Next() {
		out = append(out, it.Key())
	}
	return out
}

func (v Value) MapIndex(key Value) Value {
	if v.Kind() != Map {
		panic("reflect: MapIndex of non-map type")
	}
	d := v.dataWord()
	if d == nil {
		return Value{}
	}
	val, ok := runtime.ReflectMapAccess(d, key.Interface())
	if !ok {
		return Value{}
	}
	extra := runtime.ReflectExtra(v.typ.rt)
	et := wrap(runtime.ReflectMapElem(extra))
	return Value{typ: et, data: runtime.ReflectData(val)}
}

func (v Value) NumMethod() int   { return 0 }
func (v Value) Method(int) Value { return Value{} }

func (v Value) CanSet() bool {
	return v.flag&(flagAddr|flagRO) == flagAddr
}

func (v Value) mustBeSettable() {
	if !v.CanSet() {
		panic("reflect: call of Set on unaddressable or read-only Value")
	}
}

func (v Value) setSlot(boxed unsafe.Pointer) {
	if v.flag&flagField != 0 && v.baseTyp != nil && v.baseTyp.rt != nil {
		runtime.ReflectCallSetField(runtime.ReflectExtra(v.baseTyp.rt), v.base, v.slot, boxed)
		return
	}
	if v.flag&flagIndex != 0 && v.baseTyp != nil && v.baseTyp.rt != nil {
		runtime.ReflectCallSetIndex(runtime.ReflectExtra(v.baseTyp.rt), v.base, v.slot, boxed)
		return
	}
	// Whole addressable scalar from New().Elem(): mutate the box in place.
	switch v.Kind() {
	case Bool:
		runtime.ReflectSetBoxI32(v.data, runtime.ReflectUnboxI32(boxed))
	case Int, Int8, Int16, Int32:
		runtime.ReflectSetBoxI32(v.data, runtime.ReflectUnboxI32(boxed))
	case Int64:
		runtime.ReflectSetBoxI64(v.data, runtime.ReflectUnboxI64(boxed))
	case Uint, Uint8, Uint16, Uint32, Uintptr:
		runtime.ReflectSetBoxI32(v.data, runtime.ReflectUnboxI32(boxed))
	case Uint64:
		runtime.ReflectSetBoxI64(v.data, runtime.ReflectUnboxI64(boxed))
	case Float32:
		runtime.ReflectSetBoxF32(v.data, runtime.ReflectUnboxF32(boxed))
	case Float64:
		runtime.ReflectSetBoxF64(v.data, runtime.ReflectUnboxF64(boxed))
	case String:
		if v.flag&(flagField|flagIndex) == 0 {
			runtime.ReflectSetBoxString(v.data, runtime.ReflectString(boxed))
		}
	case Struct:
		// Whole addressable struct from New().Elem(): shallow-copy fields
		// into the existing object (encoding/json Marshal needs this).
		src := Value{typ: v.typ, data: boxed}
		n := v.NumField()
		for i := 0; i < n; i++ {
			df := v.Field(i)
			if !df.CanSet() {
				continue
			}
			df.Set(src.Field(i))
		}
	case Map, Slice, Pointer, Interface, Chan, Func:
		if v.flag&flagRefBox != 0 {
			runtime.ReflectSetBoxRef(v.data, boxed)
			return
		}
		if v.flag&flagFatPtr != 0 {
			runtime.ReflectFatPtrSet(v.data, boxed)
			return
		}
		// Composites without a box: Set through flagField/flagIndex parents.
	case Array:
		// Whole array Set from New().Elem() is not yet wired.
	}
}

func (v Value) Set(x Value) {
	v.mustBeSettable()
	if !x.IsValid() {
		return
	}
	v.setSlot(x.dataWord())
}

func (v Value) SetBool(x bool) {
	v.mustBeSettable()
	if v.Kind() != Bool {
		panic("reflect: SetBool of non-bool Value")
	}
	var i any = x
	v.setSlot(runtime.ReflectData(i))
}

func (v Value) SetInt(x int64) {
	v.mustBeSettable()
	switch v.Kind() {
	case Int, Int8, Int16, Int32:
		var i any = int32(x)
		v.setSlot(runtime.ReflectData(i))
	case Int64:
		var i any = x
		v.setSlot(runtime.ReflectData(i))
	default:
		panic("reflect: SetInt of non-int Value")
	}
}

func (v Value) SetUint(x uint64) {
	v.mustBeSettable()
	switch v.Kind() {
	case Uint, Uint8, Uint16, Uint32, Uintptr:
		var i any = uint32(x)
		v.setSlot(runtime.ReflectData(i))
	case Uint64:
		var i any = x
		v.setSlot(runtime.ReflectData(i))
	default:
		panic("reflect: SetUint of non-uint Value")
	}
}

func (v Value) SetFloat(x float64) {
	v.mustBeSettable()
	switch v.Kind() {
	case Float32:
		var i any = float32(x)
		v.setSlot(runtime.ReflectData(i))
	case Float64:
		var i any = x
		v.setSlot(runtime.ReflectData(i))
	default:
		panic("reflect: SetFloat of non-float Value")
	}
}

func (v Value) SetComplex(x complex128) {}
func (v Value) SetString(x string) {
	v.mustBeSettable()
	if v.Kind() != String {
		panic("reflect: SetString of non-string Value")
	}
	var i any = x
	v.setSlot(runtime.ReflectData(i))
}
func (v Value) SetBytes(x []byte)           {}
func (v Value) SetCap(n int)                {}
func (v Value) SetLen(n int)                {}
func (v Value) SetPointer(x unsafe.Pointer) {}
func (v Value) CanComplex() bool            { return false }
func (v Value) CanFloat() bool {
	k := v.Kind()
	return k == Float32 || k == Float64
}
func (v Value) CanInt() bool {
	switch v.Kind() {
	case Int, Int8, Int16, Int32, Int64:
		return true
	}
	return false
}
func (v Value) CanUint() bool {
	switch v.Kind() {
	case Uint, Uint8, Uint16, Uint32, Uint64, Uintptr:
		return true
	}
	return false
}
func (v Value) OverflowComplex(x complex128) bool { return false }
func (v Value) OverflowFloat(x float64) bool      { return false }
func (v Value) OverflowInt(x int64) bool          { return false }
func (v Value) OverflowUint(x uint64) bool        { return false }
func (v Value) Convert(t Type) Value              { return Value{} }
func (v Value) Slice(i, j int) Value              { return Value{} }
func (v Value) Slice3(i, j, k int) Value          { return Value{} }
func (v Value) FieldByIndex(index []int) Value {
	v2 := v
	for _, i := range index {
		v2 = v2.Field(i)
	}
	return v2
}
func (v Value) FieldByName(name string) Value { return Value{} }
func (v Value) Call(in []Value) []Value       { return nil }
func (v Value) CallSlice(in []Value) []Value  { return nil }

func (v Value) SetMapIndex(key, elem Value) {
	if v.Kind() != Map {
		panic("reflect: SetMapIndex of non-map type")
	}
	if !elem.IsValid() {
		// delete — not required for json exit; no-op for now
		return
	}
	runtime.ReflectMapAssign(v.dataWord(), key.Interface(), elem.Interface())
}

func (v Value) Grow(n int)   {}
func (v Value) Clear()       {}
func (v Value) IsZero() bool { return !v.IsValid() }
func (v Value) SetZero() {
	if !v.CanSet() {
		return
	}
	v.Set(Zero(v.Type()))
}

type MapIter struct {
	keys []any
	vals []any
	kt   *rtype
	vt   *rtype
	i    int
}

func (it *MapIter) Next() bool {
	if it == nil {
		return false
	}
	it.i++
	return it.i < len(it.keys)
}

func (it *MapIter) Key() Value   { return ValueOf(it.keys[it.i]) }
func (it *MapIter) Value() Value { return ValueOf(it.vals[it.i]) }

// SetIterKey assigns to v the key of iter's current map entry.
// Equivalent to v.Set(iter.Key()) for the wasmgc MapIter snapshot.
func (v Value) SetIterKey(iter *MapIter) {
	v.Set(iter.Key())
}

// SetIterValue assigns to v the value of iter's current map entry.
func (v Value) SetIterValue(iter *MapIter) {
	v.Set(iter.Value())
}

// Indirect returns the value that v points to when v is a pointer; otherwise v.
func Indirect(v Value) Value {
	if v.Kind() != Pointer {
		return v
	}
	return v.Elem()
}

// TypeAssert asserts that v's concrete value has type T.
func TypeAssert[T any](v Value) (T, bool) {
	var zero T
	if !v.IsValid() {
		return zero, false
	}
	i := v.Interface()
	if i == nil {
		return zero, false
	}
	t, ok := i.(T)
	return t, ok
}

func TypeOf(i any) Type {
	if i == nil {
		return nil
	}
	return wrap(runtime.ReflectType(i))
}

func ValueOf(i any) Value {
	if i == nil {
		return Value{}
	}
	return Value{typ: wrap(runtime.ReflectType(i)), data: runtime.ReflectData(i)}
}

func PointerTo(t Type) Type {
	r := asRtype(t)
	if r == nil || r.rt == nil {
		return t
	}
	return wrap(runtime.ReflectPointerTo(r.rt))
}
func PtrTo(t Type) Type { return PointerTo(t) }
func SliceOf(t Type) Type {
	r := asRtype(t)
	if r == nil || r.rt == nil {
		return t
	}
	return wrap(runtime.ReflectSliceOf(r.rt))
}
func ArrayOf(n int, t Type) Type { return t }
func MapOf(key, elem Type) Type {
	k := asRtype(key)
	e := asRtype(elem)
	if k != nil && k.rt != nil && e != nil && e.Kind() == Struct && e.NumField() == 0 {
		return wrap(runtime.ReflectMapOfEmpty(k.rt))
	}
	return &rtype{kind: uint8(Map), str: "map"}
}
func ChanOf(dir ChanDir, t Type) Type { return t }
func StructOf(fields []StructField) Type {
	return &rtype{kind: uint8(Struct), str: "struct"}
}
func FuncOf(in, out []Type, variadic bool) Type {
	return &rtype{kind: uint8(Func), str: "func"}
}
func TypeFor[T any]() Type { return TypeOf((*T)(nil)).Elem() }

func Zero(t Type) Value {
	r := asRtype(t)
	if r == nil || r.rt == nil {
		return Value{typ: r}
	}
	return Value{typ: r, data: runtime.ReflectNew(r.rt)}
}

func New(t Type) Value {
	r := asRtype(t)
	if r == nil || r.rt == nil {
		return Value{}
	}
	data := runtime.ReflectNew(r.rt)
	pt := asRtype(PointerTo(t))
	fl := flag(0)
	switch t.Kind() {
	case Map, Slice, Interface, Chan, Func:
		// Zero is a null ref; box it so New(t).Elem() is addressable and Set works.
		data = runtime.ReflectNewRefBox(data)
		fl = flagRefBox
	}
	return Value{typ: pt, data: data, flag: fl}
}

func MakeSlice(typ Type, len, cap int) Value {
	r := asRtype(typ)
	if r == nil || r.rt == nil || typ.Kind() != Slice {
		panic("reflect: MakeSlice of non-slice type")
	}
	if len < 0 || cap < 0 || len > cap {
		panic("reflect: MakeSlice of negative length or capacity less than length")
	}
	data := runtime.ReflectMakeSlice(r.rt, len, cap)
	return Value{typ: r, data: data, flag: flagAddr}
}

func MakeMap(typ Type) Value {
	return MakeMapWithSize(typ, 0)
}

func MakeMapWithSize(typ Type, n int) Value {
	r := asRtype(typ)
	if r == nil || typ.Kind() != Map {
		panic("reflect: MakeMap of non-map type")
	}
	_ = n
	return Value{typ: r, data: runtime.ReflectMakeMap(), flag: flagAddr}
}

func Append(s Value, x ...Value) Value {
	if s.Kind() != Slice {
		panic("reflect: Append of non-slice Value")
	}
	// Minimal grow: allocate len+n with MakeSlice and copy via Set/Index.
	n := len(x)
	oldLen := s.Len()
	out := MakeSlice(s.Type(), oldLen+n, oldLen+n)
	for i := 0; i < oldLen; i++ {
		out.Index(i).Set(s.Index(i))
	}
	for i, e := range x {
		out.Index(oldLen + i).Set(e)
	}
	return out
}
func Copy(dst, src Value) int { return 0 }

// DeepEqual reports whether a and b are deeply equal.
// Enough for stdlib tests: basics, arrays, slices, maps, structs, pointers,
// interfaces. Cycles among pointers are not detected (stdlib tests do not need it).
func DeepEqual(a, b any) bool {
	if a == nil || b == nil {
		return a == b
	}
	return deepValueEqual(ValueOf(a), ValueOf(b), 0)
}

func deepValueEqual(v1, v2 Value, depth int) bool {
	if depth > 1000 {
		return false
	}
	if !v1.IsValid() || !v2.IsValid() {
		return v1.IsValid() == v2.IsValid()
	}
	if v1.Kind() != v2.Kind() {
		return false
	}
	// Same concrete descriptor when both are available.
	r1, r2 := asRtype(v1.Type()), asRtype(v2.Type())
	if r1 != nil && r2 != nil && r1.rt != nil && r2.rt != nil {
		if r1.rt != r2.rt {
			return false
		}
	}

	switch v1.Kind() {
	case Array:
		if v1.Len() != v2.Len() {
			return false
		}
		for i := 0; i < v1.Len(); i++ {
			if !deepValueEqual(v1.Index(i), v2.Index(i), depth+1) {
				return false
			}
		}
		return true
	case Slice:
		if v1.IsNil() != v2.IsNil() {
			return false
		}
		if v1.Len() != v2.Len() {
			return false
		}
		for i := 0; i < v1.Len(); i++ {
			if !deepValueEqual(v1.Index(i), v2.Index(i), depth+1) {
				return false
			}
		}
		return true
	case Interface:
		if v1.IsNil() || v2.IsNil() {
			return v1.IsNil() && v2.IsNil()
		}
		return deepValueEqual(v1.Elem(), v2.Elem(), depth+1)
	case Pointer:
		if v1.IsNil() || v2.IsNil() {
			return v1.IsNil() && v2.IsNil()
		}
		// Same pointer identity (both non-nil refs).
		if !v1.IsNil() && !v2.IsNil() {
			// Compare via Interface only when both point to comparable elems
			// would be wrong; always recurse into elem.
		}
		return deepValueEqual(v1.Elem(), v2.Elem(), depth+1)
	case Struct:
		n := v1.NumField()
		if n != v2.NumField() {
			return false
		}
		for i := 0; i < n; i++ {
			if !deepValueEqual(v1.Field(i), v2.Field(i), depth+1) {
				return false
			}
		}
		return true
	case Map:
		if v1.IsNil() != v2.IsNil() {
			return false
		}
		if v1.Len() != v2.Len() {
			return false
		}
		if v1.Len() == 0 {
			return true
		}
		it := v1.MapRange()
		for it.Next() {
			k, val1 := it.Key(), it.Value()
			val2 := v2.MapIndex(k)
			if !val2.IsValid() || !deepValueEqual(val1, val2, depth+1) {
				return false
			}
		}
		return true
	case Func:
		return v1.IsNil() && v2.IsNil()
	case Bool:
		return v1.Bool() == v2.Bool()
	case Int, Int8, Int16, Int32, Int64:
		return v1.Int() == v2.Int()
	case Uint, Uint8, Uint16, Uint32, Uint64, Uintptr:
		return v1.Uint() == v2.Uint()
	case Float32, Float64:
		return v1.Float() == v2.Float()
	case String:
		return v1.String() == v2.String()
	case Chan, UnsafePointer, Complex64, Complex128:
		// Best-effort: nilness for chan; complex/unsafe via iface when possible.
		if v1.Kind() == Chan {
			return v1.IsNil() == v2.IsNil()
		}
		return false
	}
	return false
}

// rtype implements Type. When rt != nil it is backed by $go.type.
type rtype struct {
	rt   *runtime.Rtype
	kind uint8
	str  string
}

func wrap(t *runtime.Rtype) *rtype {
	if t == nil {
		return nil
	}
	return &rtype{rt: t}
}

func asRtype(t Type) *rtype {
	if t == nil {
		return nil
	}
	if r, ok := t.(*rtype); ok {
		return r
	}
	return nil
}

func (t *rtype) Kind() Kind {
	if t == nil {
		return Invalid
	}
	if t.rt != nil {
		return Kind(t.rt.Kind())
	}
	return Kind(t.kind)
}

func (t *rtype) Name() string {
	if t == nil {
		return ""
	}
	if t.rt != nil {
		return runtime.ReflectTypeName(t.rt)
	}
	return ""
}

func (t *rtype) PkgPath() string { return "" }

func (t *rtype) String() string {
	if t == nil {
		return "<nil>"
	}
	if t.str != "" {
		return t.str
	}
	if t.rt != nil {
		if n := runtime.ReflectTypeName(t.rt); n != "" {
			return n
		}
		// Descriptor name is often null (global-init cannot embed string
		// data yet). Synthesize a usable fmt/%T string from kind + shape.
		k := t.Kind()
		if k == Struct {
			return "struct"
		}
		if k == Slice {
			return "[]" + t.Elem().String()
		}
		if k == Pointer {
			return "*" + t.Elem().String()
		}
		if k == Map {
			return "map[" + t.Key().String() + "]" + t.Elem().String()
		}
	}
	return t.Kind().String()
}

func (t *rtype) Elem() Type {
	if t == nil || t.rt == nil {
		return t
	}
	// Call ReflectExtra inside each arm: switch cases are separate IR regions
	// and a shared temp from the outer seq is rejected by the verifier.
	k := t.Kind()
	if k == Pointer {
		return wrap(runtime.ReflectPtrElem(runtime.ReflectExtra(t.rt)))
	}
	if k == Slice || k == Array {
		return wrap(runtime.ReflectSliceElem(runtime.ReflectExtra(t.rt)))
	}
	if k == Map {
		return wrap(runtime.ReflectMapElem(runtime.ReflectExtra(t.rt)))
	}
	return t
}

func (t *rtype) Key() Type {
	if t == nil || t.rt == nil || t.Kind() != Map {
		return nil
	}
	return wrap(runtime.ReflectMapKey(runtime.ReflectExtra(t.rt)))
}

func (t *rtype) Len() int { return 0 }

func (t *rtype) Bits() int {
	if t == nil {
		return 0
	}
	switch t.Kind() {
	case Int8, Uint8:
		return 8
	case Int16, Uint16:
		return 16
	case Int32, Uint32, Float32, Complex64:
		return 32
	case Int64, Uint64, Float64, Complex128:
		return 64
	case Int, Uint, Uintptr:
		return 32 // wasmgc word size
	}
	return 0
}

func (t *rtype) NumField() int {
	if t == nil || t.rt == nil || t.Kind() != Struct {
		return 0
	}
	return runtime.ReflectStructNumField(runtime.ReflectExtra(t.rt))
}

func (t *rtype) Field(i int) StructField {
	if t == nil || t.rt == nil {
		return StructField{}
	}
	extra := runtime.ReflectExtra(t.rt)
	return StructField{
		Name:  runtime.ReflectStructFieldName(extra, i),
		Type:  wrap(runtime.ReflectStructFieldType(extra, i)),
		Tag:   StructTag(runtime.ReflectStructFieldTag(extra, i)),
		Index: []int{i},
	}
}

func (t *rtype) NumIn() int           { return 0 }
func (t *rtype) NumOut() int          { return 0 }
func (t *rtype) In(i int) Type        { return nil }
func (t *rtype) Out(i int) Type       { return nil }
func (t *rtype) Implements(Type) bool { return false }
func (t *rtype) AssignableTo(u Type) bool {
	return t != nil && u != nil && t.Kind() == u.Kind()
}
func (t *rtype) ConvertibleTo(Type) bool { return false }
func (t *rtype) Comparable() bool {
	if t == nil {
		return false
	}
	switch t.Kind() {
	case Slice, Map, Func:
		return false
	}
	return true
}
func (t *rtype) Size() uintptr     { return 0 }
func (t *rtype) Align() int        { return 1 }
func (t *rtype) FieldAlign() int   { return 1 }
func (t *rtype) NumMethod() int    { return 0 }
func (t *rtype) Method(int) Method { return Method{} }
func (t *rtype) IsVariadic() bool  { return false }
func (t *rtype) ChanDir() ChanDir  { return BothDir }

var _ = unsafe.Sizeof(0)
