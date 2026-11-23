package ir

import (
	"fmt"
	"strings"
)

// Type is an IR type. Low types are Wasm shapes from 03-type-mapping.md.
// High types are placeholders the runtime-lowering pass replaces.
type Type interface {
	typeNode()
	String() string
	Equal(Type) bool
}

// Num is a numeric Wasm type, or a packed array element type.
type NumKind uint8

const (
	I8  NumKind = iota // array element only
	I16                // array element only
	I32
	I64
	F32
	F64
)

func (k NumKind) String() string {
	switch k {
	case I8:
		return "i8"
	case I16:
		return "i16"
	case I32:
		return "i32"
	case I64:
		return "i64"
	case F32:
		return "f32"
	case F64:
		return "f64"
	default:
		return fmt.Sprintf("NumKind(%d)", k)
	}
}

func ParseNumKind(s string) (NumKind, bool) {
	switch s {
	case "i8":
		return I8, true
	case "i16":
		return I16, true
	case "i32":
		return I32, true
	case "i64":
		return I64, true
	case "f32":
		return F32, true
	case "f64":
		return F64, true
	default:
		return 0, false
	}
}

// NumType is i8/i16/i32/i64/f32/f64.
type NumType struct {
	Kind NumKind
}

func (t *NumType) typeNode()      {}
func (t *NumType) String() string { return t.Kind.String() }
func (t *NumType) Equal(o Type) bool {
	x, ok := o.(*NumType)
	return ok && x.Kind == t.Kind
}

// Singleton numeric types.
var (
	TypeI8  = &NumType{Kind: I8}
	TypeI16 = &NumType{Kind: I16}
	TypeI32 = &NumType{Kind: I32}
	TypeI64 = &NumType{Kind: I64}
	TypeF32 = &NumType{Kind: F32}
	TypeF64 = &NumType{Kind: F64}
)

func Num(k NumKind) *NumType {
	switch k {
	case I8:
		return TypeI8
	case I16:
		return TypeI16
	case I32:
		return TypeI32
	case I64:
		return TypeI64
	case F32:
		return TypeF32
	case F64:
		return TypeF64
	default:
		return &NumType{Kind: k}
	}
}

// Special heap / ref bases that are not module-defined types.
type HeapKind uint8

const (
	HeapAny HeapKind = iota
	HeapEq
	HeapI31
	HeapStruct // abstract struct
	HeapArray  // abstract array
	HeapNone
	HeapNoFunc
	HeapNoExtern
	HeapFunc // abstract funcref (no concrete sig)
)

func (k HeapKind) String() string {
	switch k {
	case HeapAny:
		return "any"
	case HeapEq:
		return "eq"
	case HeapI31:
		return "i31"
	case HeapStruct:
		return "struct"
	case HeapArray:
		return "array"
	case HeapNone:
		return "none"
	case HeapNoFunc:
		return "nofunc"
	case HeapNoExtern:
		return "noextern"
	case HeapFunc:
		return "func"
	default:
		return fmt.Sprintf("HeapKind(%d)", k)
	}
}

// Ref is (ref T) or (ref null T). T is a TypeDef name, a HeapKind name,
// or a FuncType for funcref S.
type Ref struct {
	Null bool
	// Exactly one of Heap, Named, Func is set.
	Heap  HeapKind // used when Named == "" && Func == nil; HeapFunc means abstract funcref
	Named string   // module type name, e.g. "$go.string"
	Func  *TypeDef // concrete function type for funcref S; TypeDef.Kind == TypeFunc
}

func (t *Ref) typeNode() {}

func (t *Ref) String() string {
	prefix := "ref"
	if t.Null {
		prefix = "refnull"
	}
	switch {
	case t.Func != nil:
		return fmt.Sprintf("%s %s", prefix, t.Func.Name)
	case t.Named != "":
		return fmt.Sprintf("%s %s", prefix, t.Named)
	default:
		return fmt.Sprintf("%s %s", prefix, t.Heap.String())
	}
}

func (t *Ref) Equal(o Type) bool {
	x, ok := o.(*Ref)
	if !ok || x.Null != t.Null {
		return false
	}
	if t.Func != nil || x.Func != nil {
		if t.Func == nil || x.Func == nil {
			return false
		}
		return t.Func.Name == x.Func.Name
	}
	if t.Named != "" || x.Named != "" {
		return t.Named == x.Named
	}
	return t.Heap == x.Heap
}

// Exn is exnref.
type Exn struct{}

func (t *Exn) typeNode()      {}
func (t *Exn) String() string { return "exn" }
func (t *Exn) Equal(o Type) bool {
	_, ok := o.(*Exn)
	return ok
}

var TypeExn = &Exn{}

// Extern is externref.
type Extern struct{}

func (t *Extern) typeNode()      {}
func (t *Extern) String() string { return "extern" }
func (t *Extern) Equal(o Type) bool {
	_, ok := o.(*Extern)
	return ok
}

var TypeExtern = &Extern{}

// High is a Go-level type not yet lowered to a Wasm shape.
// The text format prints it as go.Name. ir does not import go/types;
// the lowerer stores whatever it needs in Aux.
type High struct {
	Name string // printable name, e.g. "slice.int" → written "go.slice.int"
	Aux  any    // typically types.Type; opaque to this package
}

func (t *High) typeNode() {}
func (t *High) String() string {
	if strings.HasPrefix(t.Name, "go.") {
		return t.Name
	}
	return "go." + t.Name
}
func (t *High) Equal(o Type) bool {
	x, ok := o.(*High)
	return ok && x.Name == t.Name
}

// TypeKind classifies a module-level type definition.
type TypeKind uint8

const (
	TypeStruct TypeKind = iota
	TypeArray
	TypeFunc
)

// Field is one field of a struct type.
type Field struct {
	Name string
	Type Type
	Mut  bool
	// Len is the Go array length when Type is a bare array ref ($go.bytes /
	// $go.array.*). Zero means "not a fixed array" (or length 0). Used by
	// structZero so new(T) allocates [N]elem instead of leaving a null array.
	Len int64
	// Ptr is true when the Go field type is a pointer (*T) that shares the
	// same IR ref as a named struct / array value. structZero must leave
	// these null rather than allocating a nested zero object.
	Ptr bool
}

// TypeDef is an interned module type: struct, array, or function signature.
type TypeDef struct {
	Name    string
	Kind    TypeKind
	Super   string  // optional subtype parent name, e.g. "$go.object"
	Fields  []Field // struct
	Elem    Type    // array element
	ElemMut bool
	Params  []Type // func
	Results []Type
	Pos     Pos
}

func (t *TypeDef) typeNode()      {}
func (t *TypeDef) String() string { return t.Name }
func (t *TypeDef) Equal(o Type) bool {
	x, ok := o.(*TypeDef)
	return ok && x.Name == t.Name
}

// TypeString formats a type for the text format.
func TypeString(t Type) string {
	if t == nil {
		return "<nil>"
	}
	return t.String()
}

// TypesEqual reports whether a and b are the same type.
func TypesEqual(a, b Type) bool {
	if a == nil || b == nil {
		return a == b
	}
	return a.Equal(b)
}

// TypeListEqual reports whether two type slices are equal elementwise.
func TypeListEqual(a, b []Type) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if !TypesEqual(a[i], b[i]) {
			return false
		}
	}
	return true
}

// IsPacked reports whether t is i8 or i16 (array-element-only).
func IsPacked(t Type) bool {
	n, ok := t.(*NumType)
	return ok && (n.Kind == I8 || n.Kind == I16)
}

// IsValueType reports whether t may appear as a local, param, or result.
func IsValueType(t Type) bool {
	if t == nil || IsPacked(t) {
		return false
	}
	switch t.(type) {
	case *NumType, *Ref, *Exn, *Extern, *High:
		return true
	case *TypeDef:
		// A TypeDef name alone is not a value; use ref/refnull of it.
		return false
	default:
		return false
	}
}
