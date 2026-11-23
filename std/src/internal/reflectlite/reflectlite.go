// Package reflectlite is a wasmgc rewrite for errors and sort.
// Placeholder API matching the subset used by those packages.
package reflectlite

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
	Ptr
	Slice
	String
	Struct
	UnsafePointer
)

func (k Kind) String() string {
	return "kind"
}

type Type interface {
	Kind() Kind
	Name() string
	PkgPath() string
	String() string
	Elem() Type
	NumField() int
	Field(i int) StructField
	Implements(u Type) bool
	AssignableTo(u Type) bool
	Comparable() bool
}

type StructField struct {
	Name string
	Type Type
	Index []int
}

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

func (v Value) Type() Type { return v.typ }

func (v Value) IsValid() bool { return v.typ != nil }

func (v Value) IsNil() bool { return v.data == nil }

func (v Value) Elem() Value { return Value{} }

func (v Value) Interface() any { return v.data }

func (v Value) Len() int { return 0 }

func (v Value) Index(i int) Value { return Value{} }

func (v Value) NumField() int { return 0 }

func (v Value) Field(i int) Value { return Value{} }

func (v Value) CanSet() bool { return false }

func (v Value) Set(x Value) {}

func (v Value) Swap(w Value) {}

func TypeOf(i any) Type {
	return typeOf(i)
}

func ValueOf(i any) Value {
	return Value{typ: typeOf(i), data: i}
}

func Swapper(slice any) func(i, j int) {
	return func(i, j int) {}
}

func typeOf(i any) Type {
	return &rtype{kind: Interface, name: "any"}
}

type rtype struct {
	kind Kind
	name string
}

func (t *rtype) Kind() Kind           { return t.kind }
func (t *rtype) Name() string         { return t.name }
func (t *rtype) PkgPath() string      { return "" }
func (t *rtype) String() string       { return t.name }
func (t *rtype) Elem() Type           { return t }
func (t *rtype) NumField() int        { return 0 }
func (t *rtype) Field(i int) StructField {
	return StructField{}
}
func (t *rtype) Implements(u Type) bool  { return false }
func (t *rtype) AssignableTo(u Type) bool { return false }
func (t *rtype) Comparable() bool        { return false }
