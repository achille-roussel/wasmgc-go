package runtime

// Panic / defer surface (docs/design/04). Full try_table lowering is M5 in
// stream D; this package owns deferRun, the panic object shape, and the
// unrecovered-panic print+exit(2) path. Bodies that need EH opcodes are
// exercised by std/src/runtime/testdata/m5_defer_panic.wat until the WAT
// hatch binds them.

// panicObj is the payload of tag $go.panic.
// Layout matches $go.panicObj in 03-type-mapping.md:
//   value (iface), recovered (i32), next (*panicObj).
type panicObj struct {
	value     interface{}
	recovered int32
	next      *panicObj
}

// defer_ is one frame's defer list node. The closure is called by deferRun.
// Argument values were captured when the defer statement ran.
type defer_ struct {
	fn   func()
	link *defer_
}

// deferRun walks d LIFO, setting recoverOk around each call (04).
// A panic inside a deferred call becomes the panic being propagated; remaining
// defers still run. Until the lowerer emits try_table, this Go body is a
// scaffold: it runs the list without EH. The WAT driver tests the real protocol.
func deferRun(d *defer_) {
	for d != nil {
		fn := d.fn
		d = d.link
		if fn != nil {
			fn()
		}
	}
}

// throwPanic throws tag $go.panic with value v.
// Wants WAT / EH intrinsic; scaffold panics via Go panic for host tests.
func throwPanic(v interface{}) {
	panic(v)
}

// gorecover implements the recover builtin when recoverOk is set.
func gorecover() interface{} {
	return nil
}

// goexit throws tag $go.goexit. Defers run; recover returns nil.
func goexit() {
	throw("runtime.Goexit")
}

// unrecoveredPanic is called from the goroutine entry catch_all_ref path when
// a $go.panic escapes with recovered clear. Prints the value and exits 2.
func unrecoveredPanic(p *panicObj) {
	printstring("panic: ")
	printIface(p.value)
	printnl()
	exit(2)
}

// printIface prints a panic value. M5 WAT driver uses i32 payloads; the Go
// path prints a placeholder until reflect/fmt exist.
func printIface(v interface{}) {
	if v == nil {
		printstring("<nil>")
		return
	}
	printstring("<panic>")
}
