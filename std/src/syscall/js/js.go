// Package js is a wasmgc rewrite on externref (placeholder for std check).
package js

type Type int

const (
	TypeUndefined Type = iota
	TypeNull
	TypeBoolean
	TypeNumber
	TypeString
	TypeSymbol
	TypeObject
	TypeFunction
)

func (t Type) String() string {
	switch t {
	case TypeUndefined:
		return "undefined"
	case TypeNull:
		return "null"
	case TypeBoolean:
		return "boolean"
	case TypeNumber:
		return "number"
	case TypeString:
		return "string"
	case TypeSymbol:
		return "symbol"
	case TypeObject:
		return "object"
	case TypeFunction:
		return "function"
	default:
		return "unknown"
	}
}

type Value struct {
	ref uint64
}

func (v Value) Type() Type { return TypeUndefined }

func (v Value) IsUndefined() bool { return true }
func (v Value) IsNull() bool      { return false }
func (v Value) IsNaN() bool       { return false }

func (v Value) Bool() bool          { return false }
func (v Value) Int() int            { return 0 }
func (v Value) Float() float64      { return 0 }
func (v Value) String() string      { return "" }
func (v Value) Length() int         { return 0 }
func (v Value) Index(i int) Value   { return Value{} }
func (v Value) SetIndex(i int, x any) {}
func (v Value) Get(p string) Value  { return Value{} }
func (v Value) Set(p string, x any) {}
func (v Value) Delete(p string)     {}
func (v Value) Call(m string, args ...any) Value { return Value{} }
func (v Value) Invoke(args ...any) Value         { return Value{} }
func (v Value) New(args ...any) Value            { return Value{} }
func (v Value) Truthy() bool                     { return false }
func (v Value) Equal(w Value) bool               { return v.ref == w.ref }

func Global() Value { return Value{} }
func Null() Value   { return Value{} }
func Undefined() Value { return Value{} }

func ValueOf(x any) Value { return Value{} }

type Func struct {
	Value
}

func FuncOf(fn func(this Value, args []Value) any) Func {
	return Func{}
}

func (f Func) Release() {}

type Error struct {
	Value
}

func (e Error) Error() string { return "js error" }
