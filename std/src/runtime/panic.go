package runtime

// Panic / defer surface (docs/design/04). throwPanic / deferRun / gorecover /
// unrecoveredPanic are body-less: codegen emits EH opcodes. Static-message
// panics live here; panicIndex + itoa live in panic_index.go so programs that
// only nil-check (closures) do not pull digit tables (M5 size audit).
//
// deferRun / unrecoveredPanic take opaque pointers; rtlink rewrites their
// IR param types to $go.defer / $go.panicObj after linking.

func deferRun(d *byte)
func throwPanic(v interface{})
func gorecover(allow int32) interface{}
func goexit()
func unrecoveredPanic(p *byte)

func panicSliceB() {
	throwPanic("runtime error: slice bounds out of range")
}

func panicNil() {
	throwPanic("runtime error: invalid memory address or nil pointer dereference")
}

func panicDivide() {
	throwPanic("runtime error: integer divide by zero")
}

func panicUncomparable() {
	throwPanic("runtime error: comparing uncomparable type")
}

func panicTypeAssert() {
	throwPanic("interface conversion: type assertion failed")
}
