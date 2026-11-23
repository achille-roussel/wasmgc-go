// Package reflect is a wasmgc rewrite placeholder for M4+.
// StructOf is diagnosed at compile time when the field list is not constant.
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

const Ptr = Pointer

func (k Kind) String() string {
	return "kind"
}

type Type interface {
	Kind() Kind
	Name() string
	String() string
	Elem() Type
	NumField() int
	Field(i int) StructField
	Implements(u Type) bool
	AssignableTo(u Type) bool
	Comparable() bool
}

type StructField struct {
	Name      string
	PkgPath   string
	Type      Type
	Tag       StructTag
	Offset    uintptr
	Anonymous bool
}

type StructTag string

func (tag StructTag) Get(key string) string { return "" }

type Value struct {
	typ  Type
	data any
	flag uintptr
}

func (v Value) Kind() Kind {
	if v.typ == nil {
		return Invalid
	}
	return v.typ.Kind()
}
func (v Value) Type() Type     { return v.typ }
func (v Value) IsValid() bool  { return v.typ != nil }
func (v Value) IsNil() bool    { return v.data == nil }
func (v Value) Interface() any { return v.data }
func (v Value) Elem() Value    { return Value{} }
func (v Value) String() string { return "value" }

func TypeOf(i any) Type {
	return &rtype{kind: Interface, str: "any"}
}

func ValueOf(i any) Value {
	return Value{typ: TypeOf(i), data: i}
}

func PointerTo(t Type) Type { return t }
func PtrTo(t Type) Type     { return PointerTo(t) }
func SliceOf(t Type) Type   { return t }
func ArrayOf(n int, t Type) Type { return t }
func MapOf(key, elem Type) Type {
	return &rtype{kind: Map, str: "map"}
}
func ChanOf(dir int, t Type) Type { return t }
func StructOf(fields []StructField) Type {
	return &rtype{kind: Struct, str: "struct"}
}
func FuncOf(in, out []Type, variadic bool) Type {
	return &rtype{kind: Func, str: "func"}
}

type rtype struct {
	kind Kind
	str  string
}

func (t *rtype) Kind() Kind             { return t.kind }
func (t *rtype) Name() string           { return "" }
func (t *rtype) String() string         { return t.str }
func (t *rtype) Elem() Type             { return t }
func (t *rtype) NumField() int          { return 0 }
func (t *rtype) Field(i int) StructField { return StructField{} }
func (t *rtype) Implements(Type) bool   { return false }
func (t *rtype) AssignableTo(Type) bool { return false }
func (t *rtype) Comparable() bool       { return false }

var _ = unsafe.Sizeof(0)
