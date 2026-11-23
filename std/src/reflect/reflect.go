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

type StructTag string

func (tag StructTag) Get(key string) string { return "" }

type Value struct {
	typ  *rtype
	data unsafe.Pointer
	flag flag
}

type flag uintptr

const (
	flagRO flag = 1 << iota
	flagIndir
	flagAddr
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
	case Pointer, Interface, Slice, Map, Chan, Func, UnsafePointer:
		return v.data == nil
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
	return runtime.ReflectIface(v.typ.rt, v.data)
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
		return Value{typ: wrap(et), data: v.data}
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
	return Value{typ: wrap(ft), data: fd}
}

func (v Value) Len() int {
	switch v.Kind() {
	case String:
		return len(runtime.ReflectString(v.data))
	case Slice, Array:
		return runtime.ReflectCallLen(runtime.ReflectExtra(v.typ.rt), v.data)
	case Map:
		return runtime.ReflectMapLen(v.data)
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
		ed := runtime.ReflectCallIndex(extra, v.data, i)
		return Value{typ: wrap(et), data: ed}
	case String:
		s := runtime.ReflectString(v.data)
		return ValueOf(s[i])
	}
	panic("reflect: call of Index on " + v.Kind().String())
}

func (v Value) Bool() bool {
	if v.Kind() != Bool {
		panic("reflect: call of Bool on " + v.Kind().String())
	}
	return runtime.ReflectUnboxI32(v.data) != 0
}

func (v Value) Int() int64 {
	switch v.Kind() {
	case Int, Int8, Int16, Int32:
		return int64(runtime.ReflectUnboxI32(v.data))
	case Int64:
		return runtime.ReflectUnboxI64(v.data)
	}
	panic("reflect: call of Int on " + v.Kind().String())
}

func (v Value) Uint() uint64 {
	switch v.Kind() {
	case Uint, Uint8, Uint16, Uint32, Uintptr:
		return uint64(uint32(runtime.ReflectUnboxI32(v.data)))
	case Uint64:
		return uint64(runtime.ReflectUnboxI64(v.data))
	}
	panic("reflect: call of Uint on " + v.Kind().String())
}

func (v Value) Float() float64 {
	switch v.Kind() {
	case Float32:
		return float64(runtime.ReflectUnboxF32(v.data))
	case Float64:
		return runtime.ReflectUnboxF64(v.data)
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
		return runtime.ReflectString(v.data)
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

func (v Value) Addr() Value { return Value{} }

func (v Value) UnsafePointer() unsafe.Pointer {
	switch v.Kind() {
	case Pointer, Map, Chan, Func, Slice, UnsafePointer:
		return v.data
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
	keys, vals := runtime.ReflectMapPairs(v.data)
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

func (v Value) MapIndex(key Value) Value { return Value{} }

func (v Value) NumMethod() int                    { return 0 }
func (v Value) Method(int) Value                  { return Value{} }
func (v Value) CanSet() bool                      { return false }
func (v Value) Set(x Value)                       {}
func (v Value) SetBool(x bool)                    {}
func (v Value) SetInt(x int64)                    {}
func (v Value) SetUint(x uint64)                  {}
func (v Value) SetFloat(x float64)                {}
func (v Value) SetComplex(x complex128)           {}
func (v Value) SetString(x string)                {}
func (v Value) SetBytes(x []byte)                 {}
func (v Value) SetCap(n int)                      {}
func (v Value) SetLen(n int)                      {}
func (v Value) SetPointer(x unsafe.Pointer)       {}
func (v Value) CanComplex() bool                  { return false }
func (v Value) CanFloat() bool                    { return false }
func (v Value) CanInt() bool                      { return false }
func (v Value) CanUint() bool                     { return false }
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
func (v Value) SetMapIndex(key, elem Value)   {}
func (v Value) Grow(n int)                    {}
func (v Value) Clear()                        {}
func (v Value) IsZero() bool                  { return !v.IsValid() }

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

func Zero(t Type) Value                      { return Value{typ: asRtype(t)} }
func New(t Type) Value                       { return Value{} }
func MakeSlice(typ Type, len, cap int) Value { return Value{} }
func MakeMap(typ Type) Value                 { return Value{} }
func MakeMapWithSize(typ Type, n int) Value  { return Value{} }
func Append(s Value, x ...Value) Value       { return s }
func Copy(dst, src Value) int                { return 0 }
func DeepEqual(a, b any) bool                { return a == b }

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
