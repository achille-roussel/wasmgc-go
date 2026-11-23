package wasm

// Abstract heap-type encodings as signed values (spec s33 negatives).
// Their single-byte forms equal the signed LEB128 of these values.
const (
	HeapNoExn   int64 = -0x0c // 0x74
	HeapNoFunc  int64 = -0x0d // 0x73
	HeapNoExtern int64 = -0x0e // 0x72
	HeapNone    int64 = -0x0f // 0x71
	HeapFunc    int64 = -0x10 // 0x70
	HeapExtern  int64 = -0x11 // 0x6F
	HeapAny     int64 = -0x12 // 0x6E
	HeapEq      int64 = -0x13 // 0x6D
	HeapI31     int64 = -0x14 // 0x6C
	HeapStruct  int64 = -0x15 // 0x6B
	HeapArray   int64 = -0x16 // 0x6A
	HeapExn     int64 = -0x17 // 0x69
)

// Number / packed / vector type opcodes.
const (
	TypeI32    byte = 0x7F
	TypeI64    byte = 0x7E
	TypeF32    byte = 0x7D
	TypeF64    byte = 0x7C
	TypeV128   byte = 0x7B
	TypeI8     byte = 0x78
	TypeI16    byte = 0x77
	TypeRef    byte = 0x64 // (ref ht)
	TypeRefNull byte = 0x63 // (ref null ht)
)

// Composite / recursive type opcodes.
const (
	TypeArray     byte = 0x5E
	TypeStruct    byte = 0x5F
	TypeFunc      byte = 0x60
	TypeSubFinal  byte = 0x4F
	TypeSub       byte = 0x50
	TypeRec       byte = 0x4E
)

// Mutability.
const (
	MutConst byte = 0x00
	MutVar   byte = 0x01
)

// HeapType is either an abstract heap type or a concrete type index.
// Concrete indices are encoded as signed LEB128 (s33).
type HeapType struct {
	code int64 // abstract negative code, or non-negative type index
}

func AbstractHeap(code int64) HeapType { return HeapType{code: code} }
func TypeIndexHeap(idx uint32) HeapType { return HeapType{code: int64(idx)} }

func (h HeapType) IsIndex() bool { return h.code >= 0 }
func (h HeapType) Index() uint32 {
	if h.code < 0 {
		return 0
	}
	return uint32(h.code)
}
func (h HeapType) Code() int64 { return h.code }

func (h HeapType) encode(buf []byte) []byte {
	return AppendS33(buf, h.code)
}

// Common abstract heap types.
func HeapTypeAny() HeapType     { return AbstractHeap(HeapAny) }
func HeapTypeEq() HeapType      { return AbstractHeap(HeapEq) }
func HeapTypeI31() HeapType     { return AbstractHeap(HeapI31) }
func HeapTypeStruct() HeapType  { return AbstractHeap(HeapStruct) }
func HeapTypeArray() HeapType   { return AbstractHeap(HeapArray) }
func HeapTypeFunc() HeapType    { return AbstractHeap(HeapFunc) }
func HeapTypeExtern() HeapType  { return AbstractHeap(HeapExtern) }
func HeapTypeNone() HeapType    { return AbstractHeap(HeapNone) }
func HeapTypeExn() HeapType     { return AbstractHeap(HeapExn) }
func HeapTypeNoFunc() HeapType  { return AbstractHeap(HeapNoFunc) }
func HeapTypeNoExtern() HeapType { return AbstractHeap(HeapNoExtern) }
func HeapTypeNoExn() HeapType   { return AbstractHeap(HeapNoExn) }

// ValType is a Wasm value type.
type ValType struct {
	kind byte // number/vector opcode, or TypeRef / TypeRefNull
	ht   HeapType
}

func I32() ValType  { return ValType{kind: TypeI32} }
func I64() ValType  { return ValType{kind: TypeI64} }
func F32() ValType  { return ValType{kind: TypeF32} }
func F64() ValType  { return ValType{kind: TypeF64} }
func V128() ValType { return ValType{kind: TypeV128} }

// Ref returns a non-nullable reference type (ref ht).
func Ref(ht HeapType) ValType { return ValType{kind: TypeRef, ht: ht} }

// RefNull returns a nullable reference type (ref null ht).
func RefNull(ht HeapType) ValType { return ValType{kind: TypeRefNull, ht: ht} }

// Convenience abbreviations matching WAT.
func AnyRef() ValType    { return RefNull(HeapTypeAny()) } // anyref
func EqRef() ValType     { return RefNull(HeapTypeEq()) }
func I31Ref() ValType    { return RefNull(HeapTypeI31()) }
func StructRef() ValType { return RefNull(HeapTypeStruct()) }
func ArrayRef() ValType  { return RefNull(HeapTypeArray()) }
func FuncRef() ValType   { return RefNull(HeapTypeFunc()) } // funcref
func ExternRef() ValType { return RefNull(HeapTypeExtern()) }
func NullRef() ValType   { return RefNull(HeapTypeNone()) }
func ExnRef() ValType    { return RefNull(HeapTypeExn()) } // exnref

func (v ValType) encode(buf []byte) []byte {
	switch v.kind {
	case TypeRef, TypeRefNull:
		buf = append(buf, v.kind)
		return v.ht.encode(buf)
	default:
		// Short form: abstract nullable refs may also be written as a
		// single absheaptype byte. We always use the long form for
		// TypeRef/TypeRefNull; number types are one byte.
		return append(buf, v.kind)
	}
}

// Equal reports whether two value types are identical.
func (v ValType) Equal(o ValType) bool {
	if v.kind != o.kind {
		return false
	}
	if v.kind == TypeRef || v.kind == TypeRefNull {
		return v.ht.code == o.ht.code
	}
	return true
}

// Kind returns the valtype opcode (i32/i64/… or ref/refnull).
func (v ValType) Kind() byte { return v.kind }

// HeapCode returns the heap-type code for ref types (0 otherwise).
func (v ValType) HeapCode() int64 {
	if v.kind == TypeRef || v.kind == TypeRefNull {
		return v.ht.code
	}
	return 0
}

// StorageType is a field storage type (valtype or packed i8/i16).
type StorageType struct {
	packed byte    // TypeI8, TypeI16, or 0 for valtype
	val    ValType // used when packed == 0
}

func StorageVal(v ValType) StorageType { return StorageType{val: v} }
func StorageI8() StorageType           { return StorageType{packed: TypeI8} }
func StorageI16() StorageType          { return StorageType{packed: TypeI16} }

func (s StorageType) encode(buf []byte) []byte {
	if s.packed != 0 {
		return append(buf, s.packed)
	}
	return s.val.encode(buf)
}

// FieldType is a struct/array field.
type FieldType struct {
	Storage StorageType
	Mutable bool
}

func Field(s StorageType, mutable bool) FieldType {
	return FieldType{Storage: s, Mutable: mutable}
}

func (f FieldType) encode(buf []byte) []byte {
	buf = f.Storage.encode(buf)
	if f.Mutable {
		return append(buf, MutVar)
	}
	return append(buf, MutConst)
}

// CompType is a function, struct, or array composite type.
type CompType struct {
	kind   byte // TypeFunc, TypeStruct, TypeArray
	params []ValType
	results []ValType
	fields []FieldType // struct
	elem   FieldType   // array
}

func FuncType(params, results []ValType) CompType {
	return CompType{kind: TypeFunc, params: append([]ValType(nil), params...), results: append([]ValType(nil), results...)}
}

func StructType(fields ...FieldType) CompType {
	return CompType{kind: TypeStruct, fields: append([]FieldType(nil), fields...)}
}

func ArrayType(elem FieldType) CompType {
	return CompType{kind: TypeArray, elem: elem}
}

func (c CompType) encode(buf []byte) []byte {
	switch c.kind {
	case TypeFunc:
		buf = append(buf, TypeFunc)
		buf = AppendU32(buf, uint32(len(c.params)))
		for _, p := range c.params {
			buf = p.encode(buf)
		}
		buf = AppendU32(buf, uint32(len(c.results)))
		for _, r := range c.results {
			buf = r.encode(buf)
		}
		return buf
	case TypeStruct:
		buf = append(buf, TypeStruct)
		buf = AppendU32(buf, uint32(len(c.fields)))
		for _, f := range c.fields {
			buf = f.encode(buf)
		}
		return buf
	case TypeArray:
		buf = append(buf, TypeArray)
		return c.elem.encode(buf)
	default:
		panic("wasm: unknown composite type")
	}
}

// SubType is a subtyping declaration wrapping a composite type.
type SubType struct {
	Final    bool
	Supers   []uint32 // super type indices
	Comp     CompType
	implicit bool // encode without sub/sub final prefix (final, no supers)
}

// FinalType is a final subtype with no declared supers (common case).
// Encoded as the bare composite type (implicit final empty supers).
func FinalType(c CompType) SubType {
	return SubType{Final: true, Comp: c, implicit: true}
}

// SubFinal is an explicit (sub final supers... comp).
func SubFinal(supers []uint32, c CompType) SubType {
	return SubType{Final: true, Supers: append([]uint32(nil), supers...), Comp: c}
}

// SubOpen is (sub supers... comp) — not final, may be further subtyped.
func SubOpen(supers []uint32, c CompType) SubType {
	return SubType{Final: false, Supers: append([]uint32(nil), supers...), Comp: c}
}

func (s SubType) encode(buf []byte) []byte {
	if s.implicit && s.Final && len(s.Supers) == 0 {
		return s.Comp.encode(buf)
	}
	if s.Final {
		buf = append(buf, TypeSubFinal)
	} else {
		buf = append(buf, TypeSub)
	}
	buf = AppendU32(buf, uint32(len(s.Supers)))
	for _, x := range s.Supers {
		buf = AppendU32(buf, x)
	}
	return s.Comp.encode(buf)
}

// RecGroup is a recursive type group. A single-type group may omit the
// explicit rec wrapper (implicit unary recursion).
type RecGroup struct {
	Types    []SubType
	explicit bool // force (rec ...) even for one type
}

// Rec wraps types in an explicit rec group.
func Rec(types ...SubType) RecGroup {
	return RecGroup{Types: append([]SubType(nil), types...), explicit: true}
}

// Singleton is a one-type group encoded without a rec prefix.
func Singleton(t SubType) RecGroup {
	return RecGroup{Types: []SubType{t}}
}

func (g RecGroup) encode(buf []byte) []byte {
	if len(g.Types) != 1 || g.explicit {
		buf = append(buf, TypeRec)
		buf = AppendU32(buf, uint32(len(g.Types)))
		for _, t := range g.Types {
			buf = t.encode(buf)
		}
		return buf
	}
	return g.Types[0].encode(buf)
}

// Limits for memories and tables.
type Limits struct {
	Min uint64
	Max *uint64 // nil = no max
	I64 bool    // memory64 / address type i64
}

func (l Limits) encode(buf []byte) []byte {
	var flag byte
	if l.Max != nil {
		flag |= 0x01
	}
	if l.I64 {
		flag |= 0x04
	}
	buf = append(buf, flag)
	buf = AppendU64(buf, l.Min)
	if l.Max != nil {
		buf = AppendU64(buf, *l.Max)
	}
	return buf
}

// GlobalType is a global's type and mutability.
type GlobalType struct {
	Type    ValType
	Mutable bool
}

func (g GlobalType) encode(buf []byte) []byte {
	buf = g.Type.encode(buf)
	if g.Mutable {
		return append(buf, MutVar)
	}
	return append(buf, MutConst)
}
