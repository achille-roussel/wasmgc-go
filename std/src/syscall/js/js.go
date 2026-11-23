// Package js is a wasmgc rewrite: js.Value is externref (docs/design/07).
//
// Host imports live under module "go" as js.get / js.set / js.call / js.new /
// js.value / js.await (suspending). String bridging copies through linear
// memory; codegen fills valueOfString / valueToString.
package js

import "errors"

// Value is a JavaScript value. The compiler maps this named type to externref.
type Value struct{}

// Type of a JavaScript value (subset; enough for callers that branch on Type).
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

// js.value kinds (must match lib/wasmgc.js).
const (
	valueUndefined = 0
	valueNull      = 1
	valueBool      = 2
	valueNumber    = 3
	valueString    = 4
	valueGlobal    = 5
)

// Body-less host imports (module go). Codegen emits them only when called.

func jsGet(obj, name Value) Value
func jsSet(obj, name, val Value)
func jsCall(obj, name, a0, a1, a2 Value, argc int32) Value
func jsNew(ctor, a0, a1, a2 Value, argc int32) Value
func jsValue(kind int32, num float64, ptr, length int32) Value
func jsAwait(p Value) (ok int32, result Value)
func jsHTTPGet(url Value) (ok, status int32, body Value)
func jsStringCopy(v Value, ptr, max int32) int32
func jsNumber(v Value) float64
func jsTruthy(v Value) int32
func jsTypeof(v Value) int32
func jsEqual(a, b Value) int32

// Codegen fills: Go string ↔ JS string via linear memory + js.value / js.stringCopy.
func valueOfString(s string) Value
func valueToString(v Value) string

var undefined Value

func init() {
	undefined = jsValue(valueUndefined, 0, 0, 0)
}

// Global returns the JavaScript global object (globalThis).
func Global() Value {
	return jsValue(valueGlobal, 0, 0, 0)
}

// Null returns the JavaScript null value.
func Null() Value {
	return jsValue(valueNull, 0, 0, 0)
}

// Undefined returns the JavaScript undefined value.
func Undefined() Value {
	return undefined
}

// ValueOf converts a Go string to a JS string. Other Go kinds use Bool/Int
// helpers; Values use SetValue / InvokeValue (externref↔any is not wired).
func ValueOf(x string) Value {
	return valueOfString(x)
}

func Bool(x bool) Value {
	if x {
		return jsValue(valueBool, 1, 0, 0)
	}
	return jsValue(valueBool, 0, 0, 0)
}

func Float(x float64) Value {
	return jsValue(valueNumber, x, 0, 0)
}

func (v Value) Get(name string) Value {
	return jsGet(v, valueOfString(name))
}

func (v Value) Set(name string, x string) {
	jsSet(v, valueOfString(name), valueOfString(x))
}

// SetValue sets a property to a JS Value.
func (v Value) SetValue(name string, x Value) {
	jsSet(v, valueOfString(name), x)
}

func (v Value) Call(name string, args ...Value) Value {
	a0, a1, a2, n := packValueArgs(args)
	return jsCall(v, valueOfString(name), a0, a1, a2, n)
}

func (v Value) Invoke(args ...Value) Value {
	a0, a1, a2, n := packValueArgs(args)
	return jsCall(v, Null(), a0, a1, a2, n)
}

func (v Value) New(args ...Value) Value {
	a0, a1, a2, n := packValueArgs(args)
	return jsNew(v, a0, a1, a2, n)
}

func packValueArgs(args []Value) (a0, a1, a2 Value, n int32) {
	n = int32(len(args))
	if n > 3 {
		panic("syscall/js: at most 3 arguments supported")
	}
	if n > 0 {
		a0 = args[0]
	}
	if n > 1 {
		a1 = args[1]
	}
	if n > 2 {
		a2 = args[2]
	}
	return
}

// Await parks until a Promise settles (JSPI suspending import js.await).
func Await(v Value) (Value, error) {
	ok, result := jsAwait(v)
	if ok == 0 {
		return Null(), errors.New(valueToString(result))
	}
	return result, nil
}

// HTTPGet performs globalThis.fetch(url) and reads text() on the host
// (suspending js.httpGet). Prefer this over Get("fetch").Invoke + Await when
// undici must not start from a non-suspending Wasm import under JSPI.
func HTTPGet(url string) (status int, body string, err error) {
	ok, st, b := jsHTTPGet(valueOfString(url))
	if ok == 0 {
		return 0, "", errors.New(valueToString(b))
	}
	return int(st), valueToString(b), nil
}

func (v Value) Type() Type {
	return Type(jsTypeof(v))
}

func (v Value) IsUndefined() bool {
	return jsEqual(v, undefined) != 0
}

func (v Value) IsNull() bool {
	return jsEqual(v, Null()) != 0
}

func (v Value) IsNaN() bool {
	return false
}

func (v Value) Bool() bool {
	return jsTruthy(v) != 0
}

func (v Value) Int() int {
	return int(jsNumber(v))
}

func (v Value) Float() float64 {
	return jsNumber(v)
}

func (v Value) String() string {
	return valueToString(v)
}

func (v Value) Length() int {
	return v.Get("length").Int()
}

func (v Value) Index(i int) Value {
	return jsGet(v, Float(float64(i)))
}

func (v Value) SetIndex(i int, x Value) {
	jsSet(v, Float(float64(i)), x)
}

func (v Value) Delete(p string) {}

func (v Value) Truthy() bool {
	return jsTruthy(v) != 0
}

func (v Value) Equal(w Value) bool {
	return jsEqual(v, w) != 0
}

// Error is a JavaScript exception converted to a Go error.
type Error struct {
	Value Value
}

func (e Error) Error() string {
	return "JavaScript error: " + e.Value.String()
}

// Func is a stub; makeFunc / callbacks are not in M9.
type Func struct {
	Value
}

func FuncOf(fn func(this Value, args []Value) any) Func {
	return Func{}
}

func (f Func) Release() {}

// CopyBytesToGo / CopyBytesToJS are deferred; HTTP Get uses Response.text().
func CopyBytesToGo(dst []byte, src Value) int { return 0 }
func CopyBytesToJS(dst Value, src []byte) int { return 0 }
