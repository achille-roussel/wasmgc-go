package ir

import "strconv"

// Expr is an instruction that produces zero or more values.
// Nested exprs are allowed in the text format; temps are only required
// when a value must be named across statements in a seq.
type Expr interface {
	exprNode()
	GetPos() Pos
}

// Const is a literal immediate: integer, float, or string.
type Const struct {
	Op  Op // OpI32Const, OpI64Const, OpF32Const, OpF64Const, or OpStringLit
	I64 int64
	F64 float64
	Str string // for OpStringLit, the Go string contents
	Pos Pos
}

func (c *Const) exprNode()   {}
func (c *Const) GetPos() Pos { return c.Pos }
func (c *Const) String() string {
	switch c.Op {
	case OpI32Const, OpI64Const:
		return strconv.FormatInt(c.I64, 10)
	case OpF32Const, OpF64Const:
		return strconv.FormatFloat(c.F64, 'g', -1, 64)
	case OpStringLit:
		return strconv.Quote(c.Str)
	default:
		return "?"
	}
}

// Instr is a value-producing (or side-effecting) operation.
// Immediates that are not value operands live in the dedicated fields.
type Instr struct {
	Op Op

	// Value operands.
	Args []Expr

	// Immediates.
	Type   Type   // ref.null T, struct.get T, call_ref T, conv T, make.* T, …
	Type2  Type   // second type immediate when needed (map.make K V, br_on_cast)
	Field  string // struct field name (or decimal index as string)
	Func   string // call / ref.func target
	Global string // global.get / global.set
	Label  string // br_on_null / br_on_cast target
	Data   string // array.new_data segment name
	Str    string // OpStringLit contents
	I32    int32  // small immediate (array.new_fixed length hint, etc.)

	Pos Pos
}

func (e *Instr) exprNode()   {}
func (e *Instr) GetPos() Pos { return e.Pos }
