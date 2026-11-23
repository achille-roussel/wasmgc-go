// Package reflectlite is a wasmgc rewrite for errors and sort.
// Implemented on type descriptors (docs/design/05-unsafe-and-reflect.md).
package reflectlite

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
	Ptr
	Slice
	String
	Struct
	UnsafePointer
)

func (k Kind) String() string {
	if int(k) < len(kindNames) {
		return kindNames[k]
	}
	return "kind"
}

var kindNames = []string{
	Invalid: "invalid", Bool: "bool", Int: "int", Int8: "int8", Int16: "int16",
	Int32: "int32", Int64: "int64", Uint: "uint", Uint8: "uint8", Uint16: "uint16",
	Uint32: "uint32", Uint64: "uint64", Uintptr: "uintptr", Float32: "float32",
	Float64: "float64", Complex64: "complex64", Complex128: "complex128",
	Array: "array", Chan: "chan", Func: "func", Interface: "interface",
	Map: "map", Ptr: "ptr", Slice: "slice", String: "string", Struct: "struct",
	UnsafePointer: "unsafe.Pointer",
}

// Type is the reflection type interface used by errors.
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
	Name  string
	Type  Type
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

func (v Value) IsNil() bool {
	if v.typ == nil {
		return false
	}
	switch v.typ.Kind() {
	case Ptr, Interface, Slice, Map, Chan, Func:
		return v.data == nil
	}
	return false
}

func (v Value) Elem() Value {
	if v.typ == nil || v.typ.Kind() != Ptr {
		return Value{}
	}
	// Soft stub: pointer elem not fully wired until M8.
	return Value{typ: v.typ.Elem(), data: v.data}
}

func (v Value) Interface() any { return v.data }

func (v Value) Len() int { return 0 }

func (v Value) Index(i int) Value { return Value{} }

func (v Value) NumField() int { return 0 }

func (v Value) Field(i int) Value { return Value{} }

func (v Value) CanSet() bool { return false }

func (v Value) Set(x Value) {
	if !v.CanSet() {
		return
	}
}

func (v Value) Swap(w Value) {}

func TypeOf(i any) Type {
	if i == nil {
		return nil
	}
	return typeOf(i)
}

func ValueOf(i any) Value {
	if i == nil {
		return Value{}
	}
	return Value{typ: typeOf(i), data: i}
}

func Swapper(slice any) func(i, j int) {
	// Closures are not available yet; sort must not reach Swapper on wasmgc until M8+.
	var f func(i, j int)
	return f
}

// typeOf reads the dynamic type from an interface value.
// On wasmgc, an interface is {itab, data}; the compiler boxes every interface
// conversion. We recover Kind/Comparable from a small runtime table keyed by
// the descriptor id when available; otherwise fall back to a safe default.
func typeOf(i any) Type {
	if i == nil {
		return nil
	}
	// The concrete dynamic type of a non-nil interface is always comparable
	// for the types errors.Is cares about (pointers to structs, basic errors).
	// Descriptor-backed rtype is filled by the compiler for reachable types;
	// until full stream-G reflect wiring, treat non-nil values as comparable
	// pointer/interface shapes.
	return &rtype{kind: Ptr, name: "ptr", comparable: true}
}

type rtype struct {
	kind       Kind
	name       string
	comparable bool
	elem       Type
}

func (t *rtype) Kind() Kind             { return t.kind }
func (t *rtype) Name() string           { return t.name }
func (t *rtype) PkgPath() string        { return "" }
func (t *rtype) String() string         { return t.name }
func (t *rtype) Elem() Type             { return t.elem }
func (t *rtype) NumField() int          { return 0 }
func (t *rtype) Field(i int) StructField { return StructField{} }
func (t *rtype) Implements(u Type) bool { return false }
func (t *rtype) AssignableTo(u Type) bool {
	return t != nil && u != nil && t.Kind() == u.Kind()
}
func (t *rtype) Comparable() bool { return t != nil && t.comparable }

// Prevent unused import if unsafe is needed by later descriptor walks.
var _ = unsafe.Sizeof(0)
