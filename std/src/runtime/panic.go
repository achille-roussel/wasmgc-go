package runtime

// Panic / defer surface (docs/design/04). throwPanic / deferRun / gorecover /
// unrecoveredPanic are body-less: codegen emits EH opcodes. Static-message
// panics live here; panicIndex + itoa live in panic_index.go so programs that
// only nil-check (closures) do not pull digit tables (M5 size audit).
//
// deferRun / unrecoveredPanic take opaque pointers; rtlink rewrites their
// IR param types to $go.defer / $go.panicObj after linking.
//
// Runtime panics use plainError (implements error) so recover().(error) works
// like gc. Messages already include any "runtime error: " prefix.

func deferRun(d *byte)
func throwPanic(v interface{})
func gorecover(allow int32) interface{}
func goexit()
func unrecoveredPanic(p *byte)

// plainError is a runtime panic value that implements error (and Error).
// Struct (not defined-string) so desc ensureWrapperFromIR can cast iface
// data to $go.struct.runtime.plainError when go/types is unavailable
// (AppendFiles / -skip-stdlib). Error() returns msg as-is (like upstream
// runtime.plainError).
type plainError struct{ msg string }

func (e plainError) RuntimeError() {}

func (e plainError) Error() string { return e.msg }

func panicErr(msg string) plainError { return plainError{msg: msg} }

func panicSliceB() {
	throwPanic(panicErr("runtime error: slice bounds out of range"))
}

func panicNil() {
	throwPanic(panicErr("runtime error: invalid memory address or nil pointer dereference"))
}

// panicWrapNil reports a value-method call through a nil *T interface
// concrete (fixedbugs/issue19040). Matches gc runtime.panicwrap text:
// "value method pkg.T.F called using nil *T pointer".
func panicWrapNil(method, typ string) {
	throwPanic(panicErr("value method " + method + " called using nil " + typ + " pointer"))
}

func panicDivide() {
	throwPanic(panicErr("runtime error: integer divide by zero"))
}

func panicNegativeShift() {
	throwPanic(panicErr("runtime error: negative shift amount"))
}

func panicUncomparable() {
	throwPanic(panicErr("runtime error: comparing uncomparable type"))
}

func panicTypeAssert() {
	throwPanic(panicErr("interface conversion: type assertion failed"))
}

// panicTypeAssertNil reports x.(T) when x is a nil interface.
// Matches gc: "interface conversion: SRC is nil, not WANT"
// (typeparam/mdempsky/16 wants SRC like "interface { T() main.T }").
func panicTypeAssertNil(src, want string) {
	if src == "" {
		src = "interface"
	}
	if want == "" {
		want = "?"
	}
	throwPanic(panicErr("interface conversion: " + src + " is nil, not " + want))
}

// panicTypeAssertTypes reports a failed concrete type assertion.
// kind is $go.type.kind (abi/reflect); message contains "GOT, not WANT"
// so GOROOT recover3 checks match. Kind→name is inlined (no helper) so
// EnsurePanic re-link does not duplicate a second symbol.
func panicTypeAssertTypes(kind int32, want string) {
	got := "?"
	switch kind {
	case 1:
		got = "bool"
	case 2:
		got = "int"
	case 3:
		got = "int8"
	case 4:
		got = "int16"
	case 5:
		got = "int32"
	case 6:
		got = "int64"
	case 7:
		got = "uint"
	case 8:
		got = "uint8"
	case 9:
		got = "uint16"
	case 10:
		got = "uint32"
	case 11:
		got = "uint64"
	case 12:
		got = "uintptr"
	case 13:
		got = "float32"
	case 14:
		got = "float64"
	case 15:
		got = "complex64"
	case 16:
		got = "complex128"
	case 17:
		got = "array"
	case 18:
		got = "chan"
	case 19:
		got = "func"
	case 20:
		got = "interface"
	case 21:
		got = "map"
	case 22:
		got = "ptr"
	case 23:
		got = "slice"
	case 24:
		got = "string"
	case 25:
		got = "struct"
	case 26:
		got = "unsafe.Pointer"
	}
	if want == "" {
		want = "?"
	}
	// Strip LocalTypeSuffix (_pN) for display; gc Type.String is scope-blind.
	wantDisp := want
	for i := 0; i+2 < len(want); i++ {
		if want[i] != '_' || want[i+1] != 'p' {
			continue
		}
		j := i + 2
		if j >= len(want) {
			continue
		}
		allDigit := true
		for ; j < len(want); j++ {
			if want[j] < '0' || want[j] > '9' {
				allDigit = false
				break
			}
		}
		if allDigit {
			wantDisp = want[:i]
			break
		}
	}
	msg := "interface conversion: " + got + ", not " + wantDisp
	// issue26094: same name from different scopes. Check the type name
	// (last segment), not the package prefix — wantDisp may be "main.T".
	name := wantDisp
	if i := len(name) - 1; i >= 0 {
		for j := i; j >= 0; j-- {
			if name[j] == '.' {
				name = name[j+1:]
				break
			}
		}
	}
	if len(name) > 0 && name[0] >= 'A' && name[0] <= 'Z' {
		if got == "struct" || got == "ptr" {
			msg += ": types from different scopes"
		}
	}
	throwPanic(panicErr(msg))
}

// panicTypeAssertMissing reports a failed iface assertion (missing method).
func panicTypeAssertMissing(method string) {
	if method == "" {
		method = "?"
	}
	throwPanic(panicErr("interface conversion: missing method " + method))
}

func panicmakeslicelen() {
	throwPanic(panicErr("runtime error: makeslice: len out of range"))
}

func panicmakeslicecap() {
	throwPanic(panicErr("runtime error: makeslice: cap out of range"))
}

// panicrangestate reports iterator misuse for range-over-func (Go 1.22+).
// state matches internal/abi.RF_*: DONE=0, READY=1, PANIC=2, EXHAUSTED=3,
// MISSING_PANIC=4.
func panicrangestate(state int32) {
	switch state {
	case 0: // RF_DONE
		throwPanic(panicErr("range function continued iteration after function for loop body returned false"))
	case 2: // RF_PANIC
		throwPanic(panicErr("range function continued iteration after loop body panic"))
	case 3: // RF_EXHAUSTED
		throwPanic(panicErr("range function continued iteration after whole loop exit"))
	case 4: // RF_MISSING_PANIC
		throwPanic(panicErr("range function recovered a loop body panic and did not resume panicking"))
	}
	throwPanic(panicErr("unexpected state passed to panicrangestate"))
}
