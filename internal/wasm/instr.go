package wasm

import "math"

// Core opcodes used by M0–M2 plus GC and EH.
const (
	OpUnreachable byte = 0x00
	OpNop         byte = 0x01
	OpBlock       byte = 0x02
	OpLoop        byte = 0x03
	OpIf          byte = 0x04
	OpElse        byte = 0x05
	OpTryTable    byte = 0x1F
	OpThrow       byte = 0x08
	OpThrowRef    byte = 0x0A
	OpEnd         byte = 0x0B
	OpBr          byte = 0x0C
	OpBrIf        byte = 0x0D
	OpBrTable     byte = 0x0E
	OpReturn      byte = 0x0F
	OpCall        byte = 0x10
	OpCallIndirect byte = 0x11
	OpCallRef     byte = 0x14
	OpDrop        byte = 0x1A
	OpSelect      byte = 0x1B
	OpSelectTyped byte = 0x1C

	OpLocalGet  byte = 0x20
	OpLocalSet  byte = 0x21
	OpLocalTee  byte = 0x22
	OpGlobalGet byte = 0x23
	OpGlobalSet byte = 0x24

	OpI32Load    byte = 0x28
	OpI64Load    byte = 0x29
	OpF32Load    byte = 0x2A
	OpF64Load    byte = 0x2B
	OpI32Load8S  byte = 0x2C
	OpI32Load8U  byte = 0x2D
	OpI32Load16S byte = 0x2E
	OpI32Load16U byte = 0x2F
	OpI32Store   byte = 0x36
	OpI64Store   byte = 0x37
	OpF32Store   byte = 0x38
	OpF64Store   byte = 0x39
	OpI32Store8  byte = 0x3A
	OpI32Store16 byte = 0x3B

	OpMemorySize byte = 0x3F
	OpMemoryGrow byte = 0x40

	OpI32Const byte = 0x41
	OpI64Const byte = 0x42
	OpF32Const byte = 0x43
	OpF64Const byte = 0x44

	OpI32Eqz byte = 0x45
	OpI32Eq  byte = 0x46
	OpI32Ne  byte = 0x47
	OpI32LtS byte = 0x48
	OpI32LtU byte = 0x49
	OpI32GtS byte = 0x4A
	OpI32GtU byte = 0x4B
	OpI32LeS byte = 0x4C
	OpI32LeU byte = 0x4D
	OpI32GeS byte = 0x4E
	OpI32GeU byte = 0x4F

	OpI64Eqz byte = 0x50
	OpI64Eq  byte = 0x51
	OpI64Ne  byte = 0x52
	OpI64LtS byte = 0x53
	OpI64LtU byte = 0x54
	OpI64GtS byte = 0x55
	OpI64GtU byte = 0x56
	OpI64LeS byte = 0x57
	OpI64LeU byte = 0x58
	OpI64GeS byte = 0x59
	OpI64GeU byte = 0x5A

	OpI32Clz    byte = 0x67
	OpI32Ctz    byte = 0x68
	OpI32Popcnt byte = 0x69
	OpI32Add    byte = 0x6A
	OpI32Sub    byte = 0x6B
	OpI32Mul    byte = 0x6C
	OpI32DivS   byte = 0x6D
	OpI32DivU   byte = 0x6E
	OpI32RemS   byte = 0x6F
	OpI32RemU   byte = 0x70
	OpI32And    byte = 0x71
	OpI32Or     byte = 0x72
	OpI32Xor    byte = 0x73
	OpI32Shl    byte = 0x74
	OpI32ShrS   byte = 0x75
	OpI32ShrU   byte = 0x76
	OpI32Rotl   byte = 0x77
	OpI32Rotr   byte = 0x78

	OpI64Clz  byte = 0x79
	OpI64Ctz  byte = 0x7A
	OpI64Add  byte = 0x7C
	OpI64Sub  byte = 0x7D
	OpI64Mul  byte = 0x7E
	OpI64DivS byte = 0x7F
	OpI64DivU byte = 0x80
	OpI64And  byte = 0x83
	OpI64Or   byte = 0x84
	OpI64Xor  byte = 0x85
	OpI64Shl  byte = 0x86
	OpI64ShrS byte = 0x87
	OpI64ShrU byte = 0x88

	OpI32WrapI64    byte = 0xA7
	OpI64ExtendI32S byte = 0xAC
	OpI64ExtendI32U byte = 0xAD

	OpRefNull       byte = 0xD0
	OpRefIsNull     byte = 0xD1
	OpRefFunc       byte = 0xD2
	OpRefEq         byte = 0xD3
	OpRefAsNonNull  byte = 0xD4
	OpBrOnNull      byte = 0xD5
	OpBrOnNonNull   byte = 0xD6

	OpGCPrefix byte = 0xFB
	OpFCPrefix byte = 0xFC
)

// GC / typed-ref subopcodes under 0xFB.
const (
	GCStructNew        uint32 = 0x00
	GCStructNewDefault uint32 = 0x01
	GCStructGet        uint32 = 0x02
	GCStructGetS       uint32 = 0x03
	GCStructGetU       uint32 = 0x04
	GCStructSet        uint32 = 0x05
	GCArrayNew         uint32 = 0x06
	GCArrayNewDefault  uint32 = 0x07
	GCArrayNewFixed    uint32 = 0x08
	GCArrayNewData     uint32 = 0x09
	GCArrayNewElem     uint32 = 0x0A
	GCArrayGet         uint32 = 0x0B
	GCArrayGetS        uint32 = 0x0C
	GCArrayGetU        uint32 = 0x0D
	GCArraySet         uint32 = 0x0E
	GCArrayLen         uint32 = 0x0F
	GCArrayFill        uint32 = 0x10
	GCArrayCopy        uint32 = 0x11
	GCArrayInitData    uint32 = 0x12
	GCArrayInitElem    uint32 = 0x13
	GCRefTest          uint32 = 0x14
	GCRefTestNull      uint32 = 0x15
	GCRefCast          uint32 = 0x16
	GCRefCastNull      uint32 = 0x17
	GCBrOnCast         uint32 = 0x18
	GCBrOnCastFail     uint32 = 0x19
	GCAnyConvertExtern uint32 = 0x1A
	GCExternConvertAny uint32 = 0x1B
	GCRefI31           uint32 = 0x1C
	GCI31GetS          uint32 = 0x1D
	GCI31GetU          uint32 = 0x1E
)

// Catch kinds for try_table.
const (
	Catch     byte = 0x00
	CatchRef  byte = 0x01
	CatchAll  byte = 0x02
	CatchAllRef byte = 0x03
)

// BlockTypeEmpty is the empty block type (0x40).
const BlockTypeEmpty int64 = -0x40 // encoded as single byte 0x40

// Encoder builds an instruction stream.
type Encoder struct {
	buf []byte
}

// NewEncoder returns an empty instruction encoder.
func NewEncoder() *Encoder { return &Encoder{} }

// Bytes returns the encoded instructions (no trailing End unless emitted).
func (e *Encoder) Bytes() []byte { return e.buf }

// Reset clears the buffer.
func (e *Encoder) Reset() { e.buf = e.buf[:0] }

func (e *Encoder) op(b byte) *Encoder {
	e.buf = append(e.buf, b)
	return e
}

// Raw appends a raw opcode byte. Used by codegen for numeric ops not
// yet wrapped as named methods.
func (e *Encoder) Raw(b byte) *Encoder { return e.op(b) }

func (e *Encoder) u32(v uint32) *Encoder {
	e.buf = AppendU32(e.buf, v)
	return e
}

func (e *Encoder) i32(v int32) *Encoder {
	e.buf = AppendI32(e.buf, v)
	return e
}

func (e *Encoder) i64(v int64) *Encoder {
	e.buf = AppendI64(e.buf, v)
	return e
}

func (e *Encoder) s33(v int64) *Encoder {
	e.buf = AppendS33(e.buf, v)
	return e
}

func (e *Encoder) heap(ht HeapType) *Encoder {
	e.buf = ht.encode(e.buf)
	return e
}

func (e *Encoder) gc(sub uint32) *Encoder {
	e.buf = append(e.buf, OpGCPrefix)
	e.buf = AppendU32(e.buf, sub)
	return e
}

// Block type encoding: empty (0x40), a single value type, or a type index as s33.
func (e *Encoder) blockType(bt BlockType) *Encoder {
	switch {
	case bt.empty:
		e.buf = append(e.buf, 0x40)
	case bt.val != nil:
		e.buf = bt.val.encode(e.buf)
	default:
		e.s33(int64(bt.typeIdx))
	}
	return e
}

// BlockType describes a block/loop/if/try_table type.
type BlockType struct {
	empty   bool
	val     *ValType
	typeIdx uint32
	useIdx  bool
}

func BlockEmpty() BlockType { return BlockType{empty: true} }

func BlockVal(v ValType) BlockType { return BlockType{val: &v} }

// BlockTypeIndex uses a function type index (multi-value), encoded as s33.
func BlockTypeIndex(typeIdx uint32) BlockType {
	return BlockType{typeIdx: typeIdx, useIdx: true}
}

// CatchClause is one arm of try_table.
type CatchClause struct {
	Kind    byte
	TagIdx  uint32 // Catch / CatchRef
	LabelIdx uint32
}

// --- Control ---

func (e *Encoder) Unreachable() *Encoder { return e.op(OpUnreachable) }
func (e *Encoder) Nop() *Encoder         { return e.op(OpNop) }
func (e *Encoder) End() *Encoder         { return e.op(OpEnd) }
func (e *Encoder) Return() *Encoder      { return e.op(OpReturn) }
func (e *Encoder) Drop() *Encoder        { return e.op(OpDrop) }
func (e *Encoder) Select() *Encoder      { return e.op(OpSelect) }

func (e *Encoder) Block(bt BlockType) *Encoder {
	return e.op(OpBlock).blockType(bt)
}
func (e *Encoder) Loop(bt BlockType) *Encoder {
	return e.op(OpLoop).blockType(bt)
}
func (e *Encoder) If(bt BlockType) *Encoder {
	return e.op(OpIf).blockType(bt)
}
func (e *Encoder) Else() *Encoder { return e.op(OpElse) }

func (e *Encoder) Br(label uint32) *Encoder   { return e.op(OpBr).u32(label) }
func (e *Encoder) BrIf(label uint32) *Encoder { return e.op(OpBrIf).u32(label) }

func (e *Encoder) BrTable(labels []uint32, defaultLabel uint32) *Encoder {
	e.op(OpBrTable)
	e.u32(uint32(len(labels)))
	for _, l := range labels {
		e.u32(l)
	}
	return e.u32(defaultLabel)
}

func (e *Encoder) Call(funcIdx uint32) *Encoder { return e.op(OpCall).u32(funcIdx) }

func (e *Encoder) CallIndirect(typeIdx, tableIdx uint32) *Encoder {
	return e.op(OpCallIndirect).u32(typeIdx).u32(tableIdx)
}

func (e *Encoder) CallRef(typeIdx uint32) *Encoder { return e.op(OpCallRef).u32(typeIdx) }

// TryTable emits try_table bt catch* … (caller emits body then End).
func (e *Encoder) TryTable(bt BlockType, catches []CatchClause) *Encoder {
	e.op(OpTryTable).blockType(bt)
	e.u32(uint32(len(catches)))
	for _, c := range catches {
		e.buf = append(e.buf, c.Kind)
		switch c.Kind {
		case Catch, CatchRef:
			e.u32(c.TagIdx)
			e.u32(c.LabelIdx)
		case CatchAll, CatchAllRef:
			e.u32(c.LabelIdx)
		}
	}
	return e
}

func (e *Encoder) Throw(tagIdx uint32) *Encoder { return e.op(OpThrow).u32(tagIdx) }
func (e *Encoder) ThrowRef() *Encoder           { return e.op(OpThrowRef) }

// --- Locals / globals ---

func (e *Encoder) LocalGet(i uint32) *Encoder  { return e.op(OpLocalGet).u32(i) }
func (e *Encoder) LocalSet(i uint32) *Encoder  { return e.op(OpLocalSet).u32(i) }
func (e *Encoder) LocalTee(i uint32) *Encoder  { return e.op(OpLocalTee).u32(i) }
func (e *Encoder) GlobalGet(i uint32) *Encoder { return e.op(OpGlobalGet).u32(i) }
func (e *Encoder) GlobalSet(i uint32) *Encoder { return e.op(OpGlobalSet).u32(i) }

// --- Memory ---

func (e *Encoder) memarg(align, offset uint32) *Encoder {
	return e.u32(align).u32(offset)
}

func (e *Encoder) I32Load(align, offset uint32) *Encoder {
	return e.op(OpI32Load).memarg(align, offset)
}
func (e *Encoder) I32Store(align, offset uint32) *Encoder {
	return e.op(OpI32Store).memarg(align, offset)
}
func (e *Encoder) I32Store8(align, offset uint32) *Encoder {
	return e.op(OpI32Store8).memarg(align, offset)
}
func (e *Encoder) I32Load8U(align, offset uint32) *Encoder {
	return e.op(OpI32Load8U).memarg(align, offset)
}
func (e *Encoder) MemorySize() *Encoder { return e.op(OpMemorySize).op(0x00) }
func (e *Encoder) MemoryGrow() *Encoder { return e.op(OpMemoryGrow).op(0x00) }

func (e *Encoder) MemoryCopy() *Encoder {
	e.buf = append(e.buf, OpFCPrefix)
	e.u32(0x0A)
	e.buf = append(e.buf, 0x00, 0x00) // dst mem, src mem
	return e
}

func (e *Encoder) MemoryFill() *Encoder {
	e.buf = append(e.buf, OpFCPrefix)
	e.u32(0x0B)
	e.buf = append(e.buf, 0x00)
	return e
}

func (e *Encoder) DataDrop(dataIdx uint32) *Encoder {
	e.buf = append(e.buf, OpFCPrefix)
	e.u32(0x09)
	return e.u32(dataIdx)
}

// --- Consts / numeric ---

func (e *Encoder) I32Const(v int32) *Encoder { return e.op(OpI32Const).i32(v) }
func (e *Encoder) I64Const(v int64) *Encoder { return e.op(OpI64Const).i64(v) }

func (e *Encoder) F32Const(v float32) *Encoder {
	e.op(OpF32Const)
	bits := math.Float32bits(v)
	e.buf = append(e.buf, byte(bits), byte(bits>>8), byte(bits>>16), byte(bits>>24))
	return e
}

func (e *Encoder) F64Const(v float64) *Encoder {
	e.op(OpF64Const)
	bits := math.Float64bits(v)
	e.buf = append(e.buf,
		byte(bits), byte(bits>>8), byte(bits>>16), byte(bits>>24),
		byte(bits>>32), byte(bits>>40), byte(bits>>48), byte(bits>>56))
	return e
}

func (e *Encoder) I32Eqz() *Encoder  { return e.op(OpI32Eqz) }
func (e *Encoder) I32Eq() *Encoder   { return e.op(OpI32Eq) }
func (e *Encoder) I32Ne() *Encoder   { return e.op(OpI32Ne) }
func (e *Encoder) I32LtS() *Encoder  { return e.op(OpI32LtS) }
func (e *Encoder) I32LtU() *Encoder  { return e.op(OpI32LtU) }
func (e *Encoder) I32GtS() *Encoder  { return e.op(OpI32GtS) }
func (e *Encoder) I32GtU() *Encoder  { return e.op(OpI32GtU) }
func (e *Encoder) I32LeS() *Encoder  { return e.op(OpI32LeS) }
func (e *Encoder) I32LeU() *Encoder  { return e.op(OpI32LeU) }
func (e *Encoder) I32GeS() *Encoder  { return e.op(OpI32GeS) }
func (e *Encoder) I32GeU() *Encoder  { return e.op(OpI32GeU) }
func (e *Encoder) I32Add() *Encoder  { return e.op(OpI32Add) }
func (e *Encoder) I32Sub() *Encoder  { return e.op(OpI32Sub) }
func (e *Encoder) I32Mul() *Encoder  { return e.op(OpI32Mul) }
func (e *Encoder) I32DivS() *Encoder { return e.op(OpI32DivS) }
func (e *Encoder) I32DivU() *Encoder { return e.op(OpI32DivU) }
func (e *Encoder) I32RemS() *Encoder { return e.op(OpI32RemS) }
func (e *Encoder) I32RemU() *Encoder { return e.op(OpI32RemU) }
func (e *Encoder) I32And() *Encoder  { return e.op(OpI32And) }
func (e *Encoder) I32Or() *Encoder   { return e.op(OpI32Or) }
func (e *Encoder) I32Xor() *Encoder  { return e.op(OpI32Xor) }
func (e *Encoder) I32Shl() *Encoder  { return e.op(OpI32Shl) }
func (e *Encoder) I32ShrS() *Encoder { return e.op(OpI32ShrS) }
func (e *Encoder) I32ShrU() *Encoder { return e.op(OpI32ShrU) }

func (e *Encoder) I64Eqz() *Encoder  { return e.op(OpI64Eqz) }
func (e *Encoder) I64Eq() *Encoder   { return e.op(OpI64Eq) }
func (e *Encoder) I64Ne() *Encoder   { return e.op(OpI64Ne) }
func (e *Encoder) I64LtS() *Encoder  { return e.op(OpI64LtS) }
func (e *Encoder) I64LtU() *Encoder  { return e.op(OpI64LtU) }
func (e *Encoder) I64GtS() *Encoder  { return e.op(OpI64GtS) }
func (e *Encoder) I64GtU() *Encoder  { return e.op(OpI64GtU) }
func (e *Encoder) I64LeS() *Encoder  { return e.op(OpI64LeS) }
func (e *Encoder) I64LeU() *Encoder  { return e.op(OpI64LeU) }
func (e *Encoder) I64GeS() *Encoder  { return e.op(OpI64GeS) }
func (e *Encoder) I64GeU() *Encoder  { return e.op(OpI64GeU) }
func (e *Encoder) I64Add() *Encoder  { return e.op(OpI64Add) }
func (e *Encoder) I64Sub() *Encoder  { return e.op(OpI64Sub) }
func (e *Encoder) I64Mul() *Encoder  { return e.op(OpI64Mul) }
func (e *Encoder) I64And() *Encoder  { return e.op(OpI64And) }
func (e *Encoder) I64Or() *Encoder   { return e.op(OpI64Or) }
func (e *Encoder) I64Xor() *Encoder  { return e.op(OpI64Xor) }
func (e *Encoder) I64Shl() *Encoder  { return e.op(OpI64Shl) }
func (e *Encoder) I64ShrS() *Encoder { return e.op(OpI64ShrS) }
func (e *Encoder) I64ShrU() *Encoder { return e.op(OpI64ShrU) }

func (e *Encoder) I32WrapI64() *Encoder    { return e.op(OpI32WrapI64) }
func (e *Encoder) I64ExtendI32S() *Encoder { return e.op(OpI64ExtendI32S) }
func (e *Encoder) I64ExtendI32U() *Encoder { return e.op(OpI64ExtendI32U) }

// --- References ---

func (e *Encoder) RefNull(ht HeapType) *Encoder {
	return e.op(OpRefNull).heap(ht)
}
func (e *Encoder) RefIsNull() *Encoder          { return e.op(OpRefIsNull) }
func (e *Encoder) RefFunc(funcIdx uint32) *Encoder { return e.op(OpRefFunc).u32(funcIdx) }
func (e *Encoder) RefEq() *Encoder              { return e.op(OpRefEq) }
func (e *Encoder) RefAsNonNull() *Encoder       { return e.op(OpRefAsNonNull) }
func (e *Encoder) BrOnNull(label uint32) *Encoder    { return e.op(OpBrOnNull).u32(label) }
func (e *Encoder) BrOnNonNull(label uint32) *Encoder { return e.op(OpBrOnNonNull).u32(label) }

// --- GC ---

func (e *Encoder) StructNew(typeIdx uint32) *Encoder {
	return e.gc(GCStructNew).u32(typeIdx)
}
func (e *Encoder) StructNewDefault(typeIdx uint32) *Encoder {
	return e.gc(GCStructNewDefault).u32(typeIdx)
}
func (e *Encoder) StructGet(typeIdx, fieldIdx uint32) *Encoder {
	return e.gc(GCStructGet).u32(typeIdx).u32(fieldIdx)
}
func (e *Encoder) StructGetS(typeIdx, fieldIdx uint32) *Encoder {
	return e.gc(GCStructGetS).u32(typeIdx).u32(fieldIdx)
}
func (e *Encoder) StructGetU(typeIdx, fieldIdx uint32) *Encoder {
	return e.gc(GCStructGetU).u32(typeIdx).u32(fieldIdx)
}
func (e *Encoder) StructSet(typeIdx, fieldIdx uint32) *Encoder {
	return e.gc(GCStructSet).u32(typeIdx).u32(fieldIdx)
}

func (e *Encoder) ArrayNew(typeIdx uint32) *Encoder {
	return e.gc(GCArrayNew).u32(typeIdx)
}
func (e *Encoder) ArrayNewDefault(typeIdx uint32) *Encoder {
	return e.gc(GCArrayNewDefault).u32(typeIdx)
}
func (e *Encoder) ArrayNewFixed(typeIdx, n uint32) *Encoder {
	return e.gc(GCArrayNewFixed).u32(typeIdx).u32(n)
}
func (e *Encoder) ArrayNewData(typeIdx, dataIdx uint32) *Encoder {
	return e.gc(GCArrayNewData).u32(typeIdx).u32(dataIdx)
}
func (e *Encoder) ArrayGet(typeIdx uint32) *Encoder {
	return e.gc(GCArrayGet).u32(typeIdx)
}
func (e *Encoder) ArrayGetS(typeIdx uint32) *Encoder {
	return e.gc(GCArrayGetS).u32(typeIdx)
}
func (e *Encoder) ArrayGetU(typeIdx uint32) *Encoder {
	return e.gc(GCArrayGetU).u32(typeIdx)
}
func (e *Encoder) ArraySet(typeIdx uint32) *Encoder {
	return e.gc(GCArraySet).u32(typeIdx)
}
func (e *Encoder) ArrayLen() *Encoder { return e.gc(GCArrayLen) }
func (e *Encoder) ArrayCopy(dstType, srcType uint32) *Encoder {
	return e.gc(GCArrayCopy).u32(dstType).u32(srcType)
}
func (e *Encoder) ArrayFill(typeIdx uint32) *Encoder {
	return e.gc(GCArrayFill).u32(typeIdx)
}
func (e *Encoder) ArrayInitData(typeIdx, dataIdx uint32) *Encoder {
	return e.gc(GCArrayInitData).u32(typeIdx).u32(dataIdx)
}

func (e *Encoder) RefTest(ht HeapType) *Encoder {
	return e.gc(GCRefTest).heap(ht)
}
func (e *Encoder) RefTestNull(ht HeapType) *Encoder {
	return e.gc(GCRefTestNull).heap(ht)
}
func (e *Encoder) RefCast(ht HeapType) *Encoder {
	return e.gc(GCRefCast).heap(ht)
}
func (e *Encoder) RefCastNull(ht HeapType) *Encoder {
	return e.gc(GCRefCastNull).heap(ht)
}

// BrOnCast emits br_on_cast with nullability flags.
// nullSrc/nullDst true means the corresponding reftype is nullable.
func (e *Encoder) BrOnCast(label uint32, nullSrc, nullDst bool, htSrc, htDst HeapType) *Encoder {
	var flags byte
	if nullSrc {
		flags |= 0x01
	}
	if nullDst {
		flags |= 0x02
	}
	e.gc(GCBrOnCast)
	e.buf = append(e.buf, flags)
	return e.u32(label).heap(htSrc).heap(htDst)
}

func (e *Encoder) BrOnCastFail(label uint32, nullSrc, nullDst bool, htSrc, htDst HeapType) *Encoder {
	var flags byte
	if nullSrc {
		flags |= 0x01
	}
	if nullDst {
		flags |= 0x02
	}
	e.gc(GCBrOnCastFail)
	e.buf = append(e.buf, flags)
	return e.u32(label).heap(htSrc).heap(htDst)
}

func (e *Encoder) RefI31() *Encoder   { return e.gc(GCRefI31) }
func (e *Encoder) I31GetS() *Encoder  { return e.gc(GCI31GetS) }
func (e *Encoder) I31GetU() *Encoder  { return e.gc(GCI31GetU) }

// ConstI32 builds a constant expression for global/data/element offsets.
func ConstI32(v int32) []byte {
	e := NewEncoder()
	e.I32Const(v)
	e.End()
	return e.Bytes()
}

// ConstRefNull builds a null-ref constant expression.
func ConstRefNull(ht HeapType) []byte {
	e := NewEncoder()
	e.RefNull(ht)
	e.End()
	return e.Bytes()
}

// CodeFrom builds a Code from locals and an encoder body (End appended by Encode).
func CodeFrom(locals []Local, enc *Encoder) Code {
	return Code{Locals: locals, Body: append([]byte(nil), enc.Bytes()...)}
}
