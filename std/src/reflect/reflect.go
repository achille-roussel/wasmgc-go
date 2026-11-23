// Package reflect is a wasmgc rewrite placeholder.
// Signatures match the subset used by fmt, encoding/binary, and internal/fmtsort.
package reflect

import "unsafe"

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

// Ptr is an alias for Pointer for older code.
const Ptr = Pointer

func (k Kind) String() string {
	if int(k) < len(kindNames) {
		return kindNames[k]
	}
	return "kind"
}

var kindNames = []string{
	Invalid:       "invalid",
	Bool:          "bool",
	Int:           "int",
	Int8:          "int8",
	Int16:         "int16",
	Int32:         "int32",
	Int64:         "int64",
	Uint:          "uint",
	Uint8:         "uint8",
	Uint16:        "uint16",
	Uint32:        "uint32",
	Uint64:        "uint64",
	Uintptr:       "uintptr",
	Float32:       "float32",
	Float64:       "float64",
	Complex64:     "complex64",
	Complex128:    "complex128",
	Array:         "array",
	Chan:          "chan",
	Func:          "func",
	Interface:     "interface",
	Map:           "map",
	Pointer:       "ptr",
	Slice:         "slice",
	String:        "string",
	Struct:        "struct",
	UnsafePointer: "unsafe.Pointer",
}

type ChanDir int

const (
	RecvDir ChanDir             = 1 << iota
	SendDir
	BothDir = RecvDir | SendDir
)

type Type interface {
	Align() int
	FieldAlign() int
	Method(int) Method
	MethodByName(string) (Method, bool)
	NumMethod() int
	Name() string
	PkgPath() string
	Size() uintptr
	String() string
	Kind() Kind
	Implements(u Type) bool
	AssignableTo(u Type) bool
	ConvertibleTo(u Type) bool
	Comparable() bool
	Bits() int
	ChanDir() ChanDir
	IsVariadic() bool
	Elem() Type
	Field(i int) StructField
	FieldByIndex(index []int) StructField
	FieldByName(name string) (StructField, bool)
	FieldByNameFunc(match func(string) bool) (StructField, bool)
	In(i int) Type
	Key() Type
	Len() int
	NumField() int
	NumIn() int
	NumOut() int
	Out(i int) Type
}

type Method struct {
	Name    string
	PkgPath string
	Type    Type
	Func    Value
	Index   int
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
	typ  Type
	data any
	flag uintptr
}

func (v Value) IsValid() bool              { return v.typ != nil }
func (v Value) Type() Type                 { return v.typ }
func (v Value) Kind() Kind {
	if v.typ == nil {
		return Invalid
	}
	return v.typ.Kind()
}
func (v Value) IsNil() bool                { return v.data == nil }
func (v Value) IsZero() bool               { return false }
func (v Value) CanAddr() bool              { return false }
func (v Value) CanInterface() bool         { return true }
func (v Value) CanSet() bool               { return false }
func (v Value) CanComplex() bool           { return false }
func (v Value) CanFloat() bool             { return false }
func (v Value) CanInt() bool               { return false }
func (v Value) CanUint() bool              { return false }
func (v Value) Addr() Value                { return Value{} }
func (v Value) Interface() any             { return v.data }
func (v Value) InterfaceData() [2]uintptr  { return [2]uintptr{} }
func (v Value) Elem() Value                { return Value{} }
func (v Value) Field(i int) Value          { return Value{} }
func (v Value) FieldByIndex(idx []int) Value { return Value{} }
func (v Value) FieldByName(name string) Value { return Value{} }
func (v Value) Index(i int) Value          { return Value{} }
func (v Value) Len() int                   { return 0 }
func (v Value) Cap() int                   { return 0 }
func (v Value) NumField() int              { return 0 }
func (v Value) NumMethod() int             { return 0 }
func (v Value) Method(i int) Value         { return Value{} }
func (v Value) MethodByName(name string) Value { return Value{} }
func (v Value) Bool() bool                 { return false }
func (v Value) Bytes() []byte              { return nil }
func (v Value) Complex() complex128        { return 0 }
func (v Value) Float() float64             { return 0 }
func (v Value) Int() int64                 { return 0 }
func (v Value) Uint() uint64               { return 0 }
func (v Value) String() string {
	if v.Kind() == String {
		if s, ok := v.data.(string); ok {
			return s
		}
	}
	return "<"+v.Kind().String()+" Value>"
}
func (v Value) Pointer() uintptr { panic("reflect.Value.Pointer: no integer address on wasmgc") }
func (v Value) UnsafePointer() unsafe.Pointer {
	if v.data == nil {
		return nil
	}
	return nil
}
func (v Value) UnsafeAddr() uintptr { panic("reflect.Value.UnsafeAddr") }
func (v Value) Set(x Value)         {}
func (v Value) SetBool(b bool)      {}
func (v Value) SetBytes(x []byte)   {}
func (v Value) SetComplex(x complex128) {}
func (v Value) SetFloat(x float64)  {}
func (v Value) SetInt(x int64)      {}
func (v Value) SetUint(x uint64)    {}
func (v Value) SetString(x string)  {}
func (v Value) SetPointer(x unsafe.Pointer) {}
func (v Value) SetCap(n int)               {}
func (v Value) SetLen(n int)               {}
func (v Value) SetMapIndex(key, elem Value) {}
func (v Value) SetZero()                   {}
func (v Value) Slice(i, j int) Value       { return Value{} }
func (v Value) Slice3(i, j, k int) Value   { return Value{} }
func (v Value) Grow(n int)                 {}
func (v Value) Clear()                     {}
func (v Value) MapIndex(key Value) Value   { return Value{} }
func (v Value) MapKeys() []Value           { return nil }
func (v Value) MapRange() *MapIter         { return &MapIter{} }
func (v Value) OverflowComplex(x complex128) bool { return false }
func (v Value) OverflowFloat(x float64) bool      { return false }
func (v Value) OverflowInt(x int64) bool          { return false }
func (v Value) OverflowUint(x uint64) bool        { return false }
func (v Value) Call(in []Value) []Value           { return nil }
func (v Value) CallSlice(in []Value) []Value      { return nil }
func (v Value) Convert(t Type) Value              { return Value{} }
func (v Value) ConvertibleTo(t Type) bool         { return false }
func (v Value) Comparable() bool                  { return false }
func (v Value) Equal(u Value) bool                { return false }
func (v Value) GoString() string                  { return v.String() }

type MapIter struct{}

func (it *MapIter) Key() Value   { return Value{} }
func (it *MapIter) Value() Value { return Value{} }
func (it *MapIter) Next() bool   { return false }
func (it *MapIter) Reset(v Value) {}

type ValueError struct {
	Method string
	Kind   Kind
}

func (e *ValueError) Error() string { return "reflect: " + e.Method }

func TypeOf(i any) Type {
	return &rtype{kind: Interface, str: "any"}
}

func TypeFor[T any]() Type {
	var zero T
	return TypeOf(zero)
}

func ValueOf(i any) Value {
	return Value{typ: TypeOf(i), data: i}
}

func Zero(t Type) Value { return Value{typ: t} }

func New(t Type) Value { return Value{typ: t} }

func NewAt(t Type, p unsafe.Pointer) Value { return Value{typ: t} }

func MakeSlice(typ Type, len, cap int) Value { return Value{typ: typ} }

func MakeMap(typ Type) Value { return Value{typ: typ} }

func MakeMapWithSize(typ Type, n int) Value { return Value{typ: typ} }

func MakeChan(typ Type, buffer int) Value { return Value{typ: typ} }

func MakeFunc(typ Type, fn func(args []Value) (results []Value)) Value {
	return Value{typ: typ}
}

func Indirect(v Value) Value { return v }

func Append(s Value, x ...Value) Value { return s }

func AppendSlice(s, t Value) Value { return s }

func Copy(dst, src Value) int { return 0 }

func Select(cases []SelectCase) (chosen int, recv Value, recvOK bool) {
	return 0, Value{}, false
}

type SelectDir int

const (
	SelectSend SelectDir = iota + 1
	SelectRecv
	SelectDefault
)

type SelectCase struct {
	Dir  SelectDir
	Chan Value
	Send Value
}

func DeepEqual(x, y any) bool { return false }

func PointerTo(t Type) Type { return t }
func PtrTo(t Type) Type     { return PointerTo(t) }
func SliceOf(t Type) Type   { return t }
func ArrayOf(n int, t Type) Type { return t }
func MapOf(key, elem Type) Type {
	return &rtype{kind: Map, str: "map"}
}
func ChanOf(dir ChanDir, t Type) Type { return t }
func StructOf(fields []StructField) Type { return &rtype{kind: Struct, str: "struct"} }
func FuncOf(in, out []Type, variadic bool) Type { return &rtype{kind: Func, str: "func"} }

type rtype struct {
	kind Kind
	str  string
}

func (t *rtype) Align() int              { return 4 }
func (t *rtype) FieldAlign() int         { return 4 }
func (t *rtype) Method(int) Method       { return Method{} }
func (t *rtype) MethodByName(string) (Method, bool) { return Method{}, false }
func (t *rtype) NumMethod() int          { return 0 }
func (t *rtype) Name() string            { return "" }
func (t *rtype) PkgPath() string         { return "" }
func (t *rtype) Size() uintptr           { return 0 }
func (t *rtype) String() string          { return t.str }
func (t *rtype) Kind() Kind              { return t.kind }
func (t *rtype) Implements(Type) bool    { return false }
func (t *rtype) AssignableTo(Type) bool  { return false }
func (t *rtype) ConvertibleTo(Type) bool { return false }
func (t *rtype) Comparable() bool        { return false }
func (t *rtype) Bits() int               { return 0 }
func (t *rtype) ChanDir() ChanDir        { return BothDir }
func (t *rtype) IsVariadic() bool        { return false }
func (t *rtype) Elem() Type              { return t }
func (t *rtype) Field(int) StructField   { return StructField{} }
func (t *rtype) FieldByIndex([]int) StructField { return StructField{} }
func (t *rtype) FieldByName(string) (StructField, bool) { return StructField{}, false }
func (t *rtype) FieldByNameFunc(func(string) bool) (StructField, bool) {
	return StructField{}, false
}
func (t *rtype) In(int) Type  { return t }
func (t *rtype) Key() Type    { return t }
func (t *rtype) Len() int     { return 0 }
func (t *rtype) NumField() int { return 0 }
func (t *rtype) NumIn() int   { return 0 }
func (t *rtype) NumOut() int  { return 0 }
func (t *rtype) Out(int) Type { return t }
