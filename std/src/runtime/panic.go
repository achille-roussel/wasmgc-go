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

// panicrangestate reports iterator misuse for range-over-func (Go 1.22+).
// state matches internal/abi.RF_*: DONE=0, READY=1, PANIC=2, EXHAUSTED=3,
// MISSING_PANIC=4.
func panicrangestate(state int32) {
	switch state {
	case 0: // RF_DONE
		throwPanic("range function continued iteration after function for loop body returned false")
	case 2: // RF_PANIC
		throwPanic("range function continued iteration after loop body panic")
	case 3: // RF_EXHAUSTED
		throwPanic("range function continued iteration after whole loop exit")
	case 4: // RF_MISSING_PANIC
		throwPanic("range function recovered a loop body panic and did not resume panicking")
	}
	throwPanic("unexpected state passed to panicrangestate")
}
