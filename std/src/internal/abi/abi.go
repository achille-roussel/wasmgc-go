// Package abi is a wasmgc rewrite of internal/abi.
// Placeholder signatures for std check until the descriptor rewrite lands.
package abi

import "unsafe"

// NoEscape is the identity. The escape pass treats a call as not a leak.
//
//go:noinline
func NoEscape(p unsafe.Pointer) unsafe.Pointer {
	return p
}

// Escape marks its argument as escaping.
func Escape[T any](x T) T {
	return x
}

func FuncPCABI0(f any) uintptr         { return 0 }
func FuncPCABIInternal(f any) uintptr  { return 0 }

// EmptyInterface is the header for an interface{} value.
type EmptyInterface struct {
	Type *Type
	Data unsafe.Pointer
}

type TFlag uint8
type Kind uint8
type NameOff int32
type TypeOff int32
type TextOff int32

const (
	KindBool Kind = iota
	KindInt
	KindInt8
	KindInt16
	KindInt32
	KindInt64
	KindUint
	KindUint8
	KindUint16
	KindUint32
	KindUint64
	KindUintptr
	KindFloat32
	KindFloat64
	KindComplex64
	KindComplex128
	KindArray
	KindChan
	KindFunc
	KindInterface
	KindMap
	KindPointer
	KindSlice
	KindString
	KindStruct
	KindUnsafePointer
)

const (
	TFlagUncommon TFlag = 1 << iota
	TFlagExtraStar
	TFlagNamed
	TFlagRegularMemory
	TFlagGCMaskOnDemand
	TFlagDirectIface
)

// Type is a placeholder descriptor handle.
type Type struct {
	Size_       uintptr
	PtrBytes    uintptr
	Hash        uint32
	TFlag       TFlag
	Align_      uint8
	FieldAlign_ uint8
	Kind_       Kind
	Equal func(unsafe.Pointer, unsafe.Pointer) bool
	GCData      *byte
	Str         NameOff
	PtrToThis   TypeOff
}

func (t *Type) Kind() Kind {
	if t == nil {
		return 0
	}
	return t.Kind_
}

func (t *Type) Size() uintptr {
	if t == nil {
		return 0
	}
	return t.Size_
}

func (t *Type) Align() int {
	if t == nil {
		return 0
	}
	return int(t.Align_)
}

func (t *Type) FieldAlign() int {
	if t == nil {
		return 0
	}
	return int(t.FieldAlign_)
}

func (t *Type) Pointers() bool { return t != nil && t.PtrBytes != 0 }


func (t *Type) MapType() *MapType {
	if t == nil {
		return nil
	}
	return (*MapType)(unsafe.Pointer(t))
}

func (t *Type) PtrType() *PtrType {
	return (*PtrType)(unsafe.Pointer(t))
}

func (t *Type) ArrayType() *ArrayType {
	return (*ArrayType)(unsafe.Pointer(t))
}

func (t *Type) ChanType() *ChanType {
	return (*ChanType)(unsafe.Pointer(t))
}

func (t *Type) StructType() *StructType {
	return (*StructType)(unsafe.Pointer(t))
}

func (t *Type) InterfaceType() *InterfaceType {
	return (*InterfaceType)(unsafe.Pointer(t))
}

func (t *Type) FuncType() *FuncType {
	return (*FuncType)(unsafe.Pointer(t))
}

// TypeOf returns the abi.Type of some value.
func TypeOf(a any) *Type {
	eface := *(*EmptyInterface)(unsafe.Pointer(&a))
	return (*Type)(NoEscape(unsafe.Pointer(eface.Type)))
}

// TypeFor returns the abi.Type for a type parameter.
func TypeFor[T any]() *Type {
	return TypeOf((*T)(nil)).PtrType().Elem
}

type PtrType struct {
	Type
	Elem *Type
}

type ArrayType struct {
	Type
	Elem  *Type
	Slice *Type
	Len   uintptr
}

type ChanType struct {
	Type
	Elem *Type
	Dir  ChanDir
}

type ChanDir int

const (
	RecvDir    ChanDir = 1
	SendDir    ChanDir = 2
	BothDir    ChanDir = RecvDir | SendDir
	InvalidDir ChanDir = 0
)

type MapType struct {
	Type
	Key        *Type
	Elem       *Type
	Group      *Type
	Hasher     func(unsafe.Pointer, uintptr) uintptr
	GroupSize  uintptr
	KeysOff    uintptr
	KeyStride  uintptr
	ElemsOff   uintptr
	ElemStride uintptr
	ElemOff    uintptr
	Flags      uint32
}

func (mt *MapType) NeedKeyUpdate() bool  { return mt.Flags&1 != 0 }
func (mt *MapType) HashMightPanic() bool { return mt.Flags&2 != 0 }
func (mt *MapType) IndirectKey() bool    { return mt.Flags&4 != 0 }
func (mt *MapType) IndirectElem() bool   { return mt.Flags&8 != 0 }

type StructType struct {
	Type
	PkgPath Name
	Fields  []StructField
}

type StructField struct {
	Name   Name
	Typ    *Type
	Offset uintptr
}

type InterfaceType struct {
	Type
	PkgPath Name
	Methods []Imethod
}

type Imethod struct {
	Name NameOff
	Typ  TypeOff
}

type FuncType struct {
	Type
	InCount  uint16
	OutCount uint16
}

type Name struct {
	Bytes *byte
}

func (n Name) Name() string { return "" }
func (n Name) Tag() string  { return "" }

// ITab is a placeholder itable.
type ITab struct {
	Inter *InterfaceType
	Type  *Type
	Hash  uint32
	Fun   [1]uintptr
}
