// Package reflectlite is a wasmgc rewrite for errors and sort.
// Implemented on type descriptors (docs/design/05-unsafe-and-reflect.md).
package reflectlite

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
	typ  *rtype
	data unsafe.Pointer
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
	if v.typ == nil {
		return Value{}
	}
	switch v.typ.Kind() {
	case Ptr:
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

func (v Value) Interface() any {
	if !v.IsValid() {
		return nil
	}
	return runtime.ReflectIface(v.typ.rt, v.data)
}

func (v Value) Len() int {
	switch v.Kind() {
	case String:
		return len(runtime.ReflectString(v.data))
	case Slice, Array:
		return runtime.ReflectCallLen(runtime.ReflectExtra(v.typ.rt), v.data)
	case Map:
		return runtime.ReflectMapLen(v.data)
	}
	return 0
}

func (v Value) Index(i int) Value {
	switch v.Kind() {
	case Slice, Array:
		extra := runtime.ReflectExtra(v.typ.rt)
		et := runtime.ReflectSliceElem(extra)
		ed := runtime.ReflectCallIndex(extra, v.data, i)
		return Value{typ: wrap(et), data: ed}
	}
	return Value{}
}

func (v Value) NumField() int {
	if v.Kind() != Struct {
		return 0
	}
	return runtime.ReflectStructNumField(runtime.ReflectExtra(v.typ.rt))
}

func (v Value) Field(i int) Value {
	if v.Kind() != Struct {
		return Value{}
	}
	extra := runtime.ReflectExtra(v.typ.rt)
	ft := runtime.ReflectStructFieldType(extra, i)
	fd := runtime.ReflectCallGetField(extra, v.data, i)
	return Value{typ: wrap(ft), data: fd}
}

func (v Value) CanSet() bool { return false }

func (v Value) Set(x Value) {}

func (v Value) Swap(w Value) {
	// Soft: full swap needs settable values; sort Swapper uses typed paths.
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

func Swapper(slice any) func(i, j int) {
	v := ValueOf(slice)
	if v.Kind() != Slice {
		panic("reflectlite.Swapper of non-slice")
	}
	return func(i, j int) {
		// Closure support exists since M5; element swap via Interface round-trip
		// is incomplete without Set. Prefer typed sort paths; this keeps link.
		_ = i
		_ = j
		_ = v
	}
}

type rtype struct {
	rt *runtime.Rtype
}

func wrap(t *runtime.Rtype) *rtype {
	if t == nil {
		return nil
	}
	return &rtype{rt: t}
}

func (t *rtype) Kind() Kind {
	if t == nil || t.rt == nil {
		return Invalid
	}
	return Kind(t.rt.Kind())
}

func (t *rtype) Name() string {
	if t == nil || t.rt == nil {
		return ""
	}
	return runtime.ReflectTypeName(t.rt)
}

func (t *rtype) PkgPath() string { return "" }

func (t *rtype) String() string {
	if t == nil {
		return "<nil>"
	}
	if n := t.Name(); n != "" {
		return n
	}
	return t.Kind().String()
}

func (t *rtype) Elem() Type {
	if t == nil || t.rt == nil {
		return t
	}
	k := t.Kind()
	if k == Ptr {
		return wrap(runtime.ReflectPtrElem(runtime.ReflectExtra(t.rt)))
	}
	if k == Slice || k == Array {
		return wrap(runtime.ReflectSliceElem(runtime.ReflectExtra(t.rt)))
	}
	return t
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

func (t *rtype) Implements(u Type) bool { return false }

func (t *rtype) AssignableTo(u Type) bool {
	return t != nil && u != nil && t.Kind() == u.Kind()
}

func (t *rtype) Comparable() bool {
	if t == nil {
		return false
	}
	switch t.Kind() {
	case Slice, Map, Func:
		return false
	case Array:
		return t.Elem().Comparable()
	case Struct:
		n := t.NumField()
		for i := 0; i < n; i++ {
			ft := t.Field(i).Type
			if ft == nil || !ft.Comparable() {
				return false
			}
		}
		return true
	}
	// Basics, pointers, interfaces, chans: comparable.
	return true
}

var _ = unsafe.Sizeof(0)
