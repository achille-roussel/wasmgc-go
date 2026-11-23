package ir

// Op is an IR instruction opcode. High ops are deleted by runtime lowering.
// Low ops are what codegen emits (Wasm opcodes or short emitter sequences).
type Op uint16

const (
	OpInvalid Op = iota

	// ---- Constants (low) ----
	OpI32Const
	OpI64Const
	OpF32Const
	OpF64Const
	OpRefNull
	OpRefFunc

	// ---- Locals (low) ----
	OpLocalGet
	OpLocalSet
	OpLocalTee

	// ---- Numeric (low) ----
	OpI32Add
	OpI32Sub
	OpI32Mul
	OpI32DivS
	OpI32DivU
	OpI32RemS
	OpI32RemU
	OpI32And
	OpI32Or
	OpI32Xor
	OpI32Shl
	OpI32ShrS
	OpI32ShrU
	OpI32Rotl
	OpI32Rotr
	OpI32Clz
	OpI32Ctz
	OpI32Popcnt
	OpI32Eqz
	OpI32Eq
	OpI32Ne
	OpI32LtS
	OpI32LtU
	OpI32GtS
	OpI32GtU
	OpI32LeS
	OpI32LeU
	OpI32GeS
	OpI32GeU
	OpI32WrapI64
	OpI32Extend8S
	OpI32Extend16S
	OpI32TruncF32S
	OpI32TruncF32U
	OpI32TruncF64S
	OpI32TruncF64U
	OpI32TruncSatF32S
	OpI32TruncSatF32U
	OpI32TruncSatF64S
	OpI32TruncSatF64U
	OpI32ReinterpretF32

	OpI64Add
	OpI64Sub
	OpI64Mul
	OpI64DivS
	OpI64DivU
	OpI64RemS
	OpI64RemU
	OpI64And
	OpI64Or
	OpI64Xor
	OpI64Shl
	OpI64ShrS
	OpI64ShrU
	OpI64Rotl
	OpI64Rotr
	OpI64Clz
	OpI64Ctz
	OpI64Popcnt
	OpI64Eqz
	OpI64Eq
	OpI64Ne
	OpI64LtS
	OpI64LtU
	OpI64GtS
	OpI64GtU
	OpI64LeS
	OpI64LeU
	OpI64GeS
	OpI64GeU
	OpI64ExtendI32S
	OpI64ExtendI32U
	OpI64Extend8S
	OpI64Extend16S
	OpI64Extend32S
	OpI64TruncF32S
	OpI64TruncF32U
	OpI64TruncF64S
	OpI64TruncF64U
	OpI64TruncSatF32S
	OpI64TruncSatF32U
	OpI64TruncSatF64S
	OpI64TruncSatF64U
	OpI64ReinterpretF64

	OpF32Add
	OpF32Sub
	OpF32Mul
	OpF32Div
	OpF32Min
	OpF32Max
	OpF32Abs
	OpF32Neg
	OpF32Sqrt
	OpF32Ceil
	OpF32Floor
	OpF32Trunc
	OpF32Nearest
	OpF32Copysign
	OpF32Eq
	OpF32Ne
	OpF32Lt
	OpF32Gt
	OpF32Le
	OpF32Ge
	OpF32ConvertI32S
	OpF32ConvertI32U
	OpF32ConvertI64S
	OpF32ConvertI64U
	OpF32DemoteF64
	OpF32ReinterpretI32

	OpF64Add
	OpF64Sub
	OpF64Mul
	OpF64Div
	OpF64Min
	OpF64Max
	OpF64Abs
	OpF64Neg
	OpF64Sqrt
	OpF64Ceil
	OpF64Floor
	OpF64Trunc
	OpF64Nearest
	OpF64Copysign
	OpF64Eq
	OpF64Ne
	OpF64Lt
	OpF64Gt
	OpF64Le
	OpF64Ge
	OpF64ConvertI32S
	OpF64ConvertI32U
	OpF64ConvertI64S
	OpF64ConvertI64U
	OpF64PromoteF32
	OpF64ReinterpretI64

	// ---- Control / call (low) ----
	OpCall
	OpCallRef
	OpDrop
	OpBrTable // used inside dispatch; rare as a free op

	// ---- GC (low) ----
	OpStructNew
	OpStructNewDefault
	OpStructGet
	OpStructGetS
	OpStructGetU
	OpStructSet
	OpArrayNew
	OpArrayNewDefault
	OpArrayNewFixed
	OpArrayNewData
	OpArrayGet
	OpArrayGetS
	OpArrayGetU
	OpArraySet
	OpArrayLen
	OpArrayCopy
	OpArrayFill
	OpRefCast
	OpRefTest
	OpRefEq
	OpRefIsNull
	OpRefAsNonNull
	OpBrOnNull
	OpBrOnCast

	// ---- Exception (low) ----
	OpThrow
	OpThrowRef

	// ---- Memory (low) ----
	OpMemorySize
	OpMemoryGrow
	OpMemoryCopy
	OpMemoryFill

	// ---- Global (low) ----
	OpGlobalGet
	OpGlobalSet

	// ---- High-level (deleted by runtime lowering) ----
	OpMakeSlice
	OpSliceAppend
	OpSliceSlice
	OpSliceGet
	OpSliceSet
	OpStringLit // string "…" → string ref
	OpStringGet
	OpStringLen
	OpStringSlice
	OpMapMake
	OpMapGet
	OpMapSet
	OpMapDelete
	OpMapLen
	OpMapIter
	OpMapNext
	OpChanMake
	OpChanSend
	OpChanRecv
	OpChanClose
	OpSelect
	OpIfaceMake
	OpIfaceCall
	OpIfaceAssert
	OpTypeSwitch
	OpConv
	OpPanic
	OpRecover
	OpDefer
	OpGo
	OpLen
	OpCap
	OpReal
	OpImag
	OpComplex
	OpAddr
	OpDeref
	OpStore
	OpLiteral

	opEnd
)

var opNames = [...]string{
	OpInvalid: "invalid",

	OpI32Const: "i32.const",
	OpI64Const: "i64.const",
	OpF32Const: "f32.const",
	OpF64Const: "f64.const",
	OpRefNull:  "ref.null",
	OpRefFunc:  "ref.func",

	OpLocalGet: "local.get",
	OpLocalSet: "local.set",
	OpLocalTee: "local.tee",

	OpI32Add: "i32.add", OpI32Sub: "i32.sub", OpI32Mul: "i32.mul",
	OpI32DivS: "i32.div_s", OpI32DivU: "i32.div_u",
	OpI32RemS: "i32.rem_s", OpI32RemU: "i32.rem_u",
	OpI32And: "i32.and", OpI32Or: "i32.or", OpI32Xor: "i32.xor",
	OpI32Shl: "i32.shl", OpI32ShrS: "i32.shr_s", OpI32ShrU: "i32.shr_u",
	OpI32Rotl: "i32.rotl", OpI32Rotr: "i32.rotr",
	OpI32Clz: "i32.clz", OpI32Ctz: "i32.ctz", OpI32Popcnt: "i32.popcnt",
	OpI32Eqz: "i32.eqz",
	OpI32Eq:  "i32.eq", OpI32Ne: "i32.ne",
	OpI32LtS: "i32.lt_s", OpI32LtU: "i32.lt_u",
	OpI32GtS: "i32.gt_s", OpI32GtU: "i32.gt_u",
	OpI32LeS: "i32.le_s", OpI32LeU: "i32.le_u",
	OpI32GeS: "i32.ge_s", OpI32GeU: "i32.ge_u",
	OpI32WrapI64:  "i32.wrap_i64",
	OpI32Extend8S: "i32.extend8_s", OpI32Extend16S: "i32.extend16_s",
	OpI32TruncF32S: "i32.trunc_f32_s", OpI32TruncF32U: "i32.trunc_f32_u",
	OpI32TruncF64S: "i32.trunc_f64_s", OpI32TruncF64U: "i32.trunc_f64_u",
	OpI32TruncSatF32S: "i32.trunc_sat_f32_s", OpI32TruncSatF32U: "i32.trunc_sat_f32_u",
	OpI32TruncSatF64S: "i32.trunc_sat_f64_s", OpI32TruncSatF64U: "i32.trunc_sat_f64_u",
	OpI32ReinterpretF32: "i32.reinterpret_f32",

	OpI64Add: "i64.add", OpI64Sub: "i64.sub", OpI64Mul: "i64.mul",
	OpI64DivS: "i64.div_s", OpI64DivU: "i64.div_u",
	OpI64RemS: "i64.rem_s", OpI64RemU: "i64.rem_u",
	OpI64And: "i64.and", OpI64Or: "i64.or", OpI64Xor: "i64.xor",
	OpI64Shl: "i64.shl", OpI64ShrS: "i64.shr_s", OpI64ShrU: "i64.shr_u",
	OpI64Rotl: "i64.rotl", OpI64Rotr: "i64.rotr",
	OpI64Clz: "i64.clz", OpI64Ctz: "i64.ctz", OpI64Popcnt: "i64.popcnt",
	OpI64Eqz: "i64.eqz",
	OpI64Eq:  "i64.eq", OpI64Ne: "i64.ne",
	OpI64LtS: "i64.lt_s", OpI64LtU: "i64.lt_u",
	OpI64GtS: "i64.gt_s", OpI64GtU: "i64.gt_u",
	OpI64LeS: "i64.le_s", OpI64LeU: "i64.le_u",
	OpI64GeS: "i64.ge_s", OpI64GeU: "i64.ge_u",
	OpI64ExtendI32S: "i64.extend_i32_s", OpI64ExtendI32U: "i64.extend_i32_u",
	OpI64Extend8S: "i64.extend8_s", OpI64Extend16S: "i64.extend16_s", OpI64Extend32S: "i64.extend32_s",
	OpI64TruncF32S: "i64.trunc_f32_s", OpI64TruncF32U: "i64.trunc_f32_u",
	OpI64TruncF64S: "i64.trunc_f64_s", OpI64TruncF64U: "i64.trunc_f64_u",
	OpI64TruncSatF32S: "i64.trunc_sat_f32_s", OpI64TruncSatF32U: "i64.trunc_sat_f32_u",
	OpI64TruncSatF64S: "i64.trunc_sat_f64_s", OpI64TruncSatF64U: "i64.trunc_sat_f64_u",
	OpI64ReinterpretF64: "i64.reinterpret_f64",

	OpF32Add: "f32.add", OpF32Sub: "f32.sub", OpF32Mul: "f32.mul", OpF32Div: "f32.div",
	OpF32Min: "f32.min", OpF32Max: "f32.max",
	OpF32Abs: "f32.abs", OpF32Neg: "f32.neg", OpF32Sqrt: "f32.sqrt",
	OpF32Ceil: "f32.ceil", OpF32Floor: "f32.floor", OpF32Trunc: "f32.trunc", OpF32Nearest: "f32.nearest",
	OpF32Copysign: "f32.copysign",
	OpF32Eq:       "f32.eq", OpF32Ne: "f32.ne", OpF32Lt: "f32.lt", OpF32Gt: "f32.gt", OpF32Le: "f32.le", OpF32Ge: "f32.ge",
	OpF32ConvertI32S: "f32.convert_i32_s", OpF32ConvertI32U: "f32.convert_i32_u",
	OpF32ConvertI64S: "f32.convert_i64_s", OpF32ConvertI64U: "f32.convert_i64_u",
	OpF32DemoteF64: "f32.demote_f64", OpF32ReinterpretI32: "f32.reinterpret_i32",

	OpF64Add: "f64.add", OpF64Sub: "f64.sub", OpF64Mul: "f64.mul", OpF64Div: "f64.div",
	OpF64Min: "f64.min", OpF64Max: "f64.max",
	OpF64Abs: "f64.abs", OpF64Neg: "f64.neg", OpF64Sqrt: "f64.sqrt",
	OpF64Ceil: "f64.ceil", OpF64Floor: "f64.floor", OpF64Trunc: "f64.trunc", OpF64Nearest: "f64.nearest",
	OpF64Copysign: "f64.copysign",
	OpF64Eq:       "f64.eq", OpF64Ne: "f64.ne", OpF64Lt: "f64.lt", OpF64Gt: "f64.gt", OpF64Le: "f64.le", OpF64Ge: "f64.ge",
	OpF64ConvertI32S: "f64.convert_i32_s", OpF64ConvertI32U: "f64.convert_i32_u",
	OpF64ConvertI64S: "f64.convert_i64_s", OpF64ConvertI64U: "f64.convert_i64_u",
	OpF64PromoteF32: "f64.promote_f32", OpF64ReinterpretI64: "f64.reinterpret_i64",

	OpCall: "call", OpCallRef: "call_ref", OpDrop: "drop", OpBrTable: "br_table",

	OpStructNew: "struct.new", OpStructNewDefault: "struct.new_default",
	OpStructGet: "struct.get", OpStructGetS: "struct.get_s", OpStructGetU: "struct.get_u",
	OpStructSet: "struct.set",
	OpArrayNew:  "array.new", OpArrayNewDefault: "array.new_default",
	OpArrayNewFixed: "array.new_fixed", OpArrayNewData: "array.new_data",
	OpArrayGet: "array.get", OpArrayGetS: "array.get_s", OpArrayGetU: "array.get_u",
	OpArraySet: "array.set", OpArrayLen: "array.len",
	OpArrayCopy: "array.copy", OpArrayFill: "array.fill",
	OpRefCast: "ref.cast", OpRefTest: "ref.test", OpRefEq: "ref.eq",
	OpRefIsNull: "ref.is_null", OpRefAsNonNull: "ref.as_non_null",
	OpBrOnNull: "br_on_null", OpBrOnCast: "br_on_cast",

	OpThrow: "throw", OpThrowRef: "throw_ref",

	OpMemorySize: "memory.size", OpMemoryGrow: "memory.grow",
	OpMemoryCopy: "memory.copy", OpMemoryFill: "memory.fill",

	OpGlobalGet: "global.get", OpGlobalSet: "global.set",

	OpMakeSlice: "make.slice", OpSliceAppend: "slice.append", OpSliceSlice: "slice.slice",
	OpSliceGet: "slice.get", OpSliceSet: "slice.set",
	OpStringLit: "string", OpStringGet: "string.get", OpStringLen: "string.len", OpStringSlice: "string.slice",
	OpMapMake: "map.make", OpMapGet: "map.get", OpMapSet: "map.set",
	OpMapDelete: "map.delete", OpMapLen: "map.len", OpMapIter: "map.iter", OpMapNext: "map.next",
	OpChanMake: "chan.make", OpChanSend: "chan.send", OpChanRecv: "chan.recv", OpChanClose: "chan.close",
	OpSelect:    "select",
	OpIfaceMake: "iface.make", OpIfaceCall: "iface.call", OpIfaceAssert: "iface.assert",
	OpTypeSwitch: "type.switch", OpConv: "conv",
	OpPanic: "panic", OpRecover: "recover", OpDefer: "defer", OpGo: "go",
	OpLen: "len", OpCap: "cap", OpReal: "real", OpImag: "imag", OpComplex: "complex",
	OpAddr: "addr", OpDeref: "deref", OpStore: "store", OpLiteral: "literal",
}

func (op Op) String() string {
	if int(op) < len(opNames) && opNames[op] != "" {
		return opNames[op]
	}
	return "op?"
}

// IsHigh reports whether op is a high-level instruction that runtime
// lowering must delete before codegen.
func (op Op) IsHigh() bool {
	return op >= OpMakeSlice && op < opEnd
}

var opByName map[string]Op

func init() {
	opByName = make(map[string]Op, len(opNames))
	for op, name := range opNames {
		if name != "" && name != "invalid" {
			opByName[name] = Op(op)
		}
	}
}

// LookupOp returns the opcode for a text name, or OpInvalid.
func LookupOp(name string) Op {
	if op, ok := opByName[name]; ok {
		return op
	}
	return OpInvalid
}

// OpArity returns the number of value operands an op consumes, or -1 if
// variable / special. Immediates (types, field indices, function names)
// are not counted here.
func OpArity(op Op) int {
	switch op {
	case OpI32Const, OpI64Const, OpF32Const, OpF64Const, OpRefNull, OpRefFunc,
		OpMemorySize, OpRecover, OpStringLit:
		return 0
	case OpLocalGet, OpGlobalGet, OpI32Eqz, OpI64Eqz,
		OpI32Clz, OpI32Ctz, OpI32Popcnt, OpI64Clz, OpI64Ctz, OpI64Popcnt,
		OpI32WrapI64, OpI32Extend8S, OpI32Extend16S,
		OpI64ExtendI32S, OpI64ExtendI32U, OpI64Extend8S, OpI64Extend16S, OpI64Extend32S,
		OpI32TruncF32S, OpI32TruncF32U, OpI32TruncF64S, OpI32TruncF64U,
		OpI32TruncSatF32S, OpI32TruncSatF32U, OpI32TruncSatF64S, OpI32TruncSatF64U,
		OpI64TruncF32S, OpI64TruncF32U, OpI64TruncF64S, OpI64TruncF64U,
		OpI64TruncSatF32S, OpI64TruncSatF32U, OpI64TruncSatF64S, OpI64TruncSatF64U,
		OpI32ReinterpretF32, OpI64ReinterpretF64, OpF32ReinterpretI32, OpF64ReinterpretI64,
		OpF32Abs, OpF32Neg, OpF32Sqrt, OpF32Ceil, OpF32Floor, OpF32Trunc, OpF32Nearest,
		OpF64Abs, OpF64Neg, OpF64Sqrt, OpF64Ceil, OpF64Floor, OpF64Trunc, OpF64Nearest,
		OpF32ConvertI32S, OpF32ConvertI32U, OpF32ConvertI64S, OpF32ConvertI64U, OpF32DemoteF64,
		OpF64ConvertI32S, OpF64ConvertI32U, OpF64ConvertI64S, OpF64ConvertI64U, OpF64PromoteF32,
		OpDrop, OpLocalSet, OpGlobalSet, OpArrayLen, OpRefIsNull, OpRefAsNonNull,
		OpThrow, OpThrowRef, OpMemoryGrow, OpPanic, OpDefer, OpGo, OpLen, OpCap,
		OpReal, OpImag, OpAddr, OpDeref, OpStringLen, OpMapLen, OpMapIter, OpChanClose,
		OpTypeSwitch, OpConv:
		return 1
	case OpI32Add, OpI32Sub, OpI32Mul, OpI32DivS, OpI32DivU, OpI32RemS, OpI32RemU,
		OpI32And, OpI32Or, OpI32Xor, OpI32Shl, OpI32ShrS, OpI32ShrU, OpI32Rotl, OpI32Rotr,
		OpI32Eq, OpI32Ne, OpI32LtS, OpI32LtU, OpI32GtS, OpI32GtU, OpI32LeS, OpI32LeU, OpI32GeS, OpI32GeU,
		OpI64Add, OpI64Sub, OpI64Mul, OpI64DivS, OpI64DivU, OpI64RemS, OpI64RemU,
		OpI64And, OpI64Or, OpI64Xor, OpI64Shl, OpI64ShrS, OpI64ShrU, OpI64Rotl, OpI64Rotr,
		OpI64Eq, OpI64Ne, OpI64LtS, OpI64LtU, OpI64GtS, OpI64GtU, OpI64LeS, OpI64LeU, OpI64GeS, OpI64GeU,
		OpF32Add, OpF32Sub, OpF32Mul, OpF32Div, OpF32Min, OpF32Max, OpF32Copysign,
		OpF32Eq, OpF32Ne, OpF32Lt, OpF32Gt, OpF32Le, OpF32Ge,
		OpF64Add, OpF64Sub, OpF64Mul, OpF64Div, OpF64Min, OpF64Max, OpF64Copysign,
		OpF64Eq, OpF64Ne, OpF64Lt, OpF64Gt, OpF64Le, OpF64Ge,
		OpLocalTee, OpRefEq, OpRefCast, OpRefTest, OpStructGet, OpStructGetS, OpStructGetU,
		OpArrayGet, OpArrayGetS, OpArrayGetU, OpStore, OpStringGet, OpSliceGet,
		OpMapDelete, OpChanSend, OpComplex, OpBrOnNull:
		return 2
	case OpStructSet, OpArraySet, OpSliceSet, OpMapSet, OpArrayFill,
		OpMakeSlice, OpStringSlice, OpBrOnCast:
		return 3
	case OpSliceSlice: // s, lo, hi [, hiCap]
		return -1
	case OpCall, OpCallRef, OpStructNew, OpArrayNew, OpArrayNewFixed, OpArrayNewData,
		OpArrayNewDefault, OpStructNewDefault, OpArrayCopy, OpSliceAppend,
		OpMapMake, OpMapGet, OpChanMake, OpChanRecv, OpSelect,
		OpIfaceMake, OpIfaceCall, OpIfaceAssert, OpLiteral, OpMemoryCopy, OpMemoryFill:
		return -1
	default:
		return -1
	}
}
