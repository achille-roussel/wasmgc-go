package runtime

// Goroutine scheduler (docs/design/04, M7).
//
// JSPI is the stack: go.runtime.park / ready / spawn / gosched are host
// imports (07). The Go side owns the run queue, $g, park ids, and deadlock
// detection. The lowerer emits runtime.go_ for `go f()` and links this file
// via rtlink.EnsureSched.

// waitReason values passed to gopark. Host-callback parks are not deadlocks.
const (
	waitReasonChanSend     int32 = 1
	waitReasonChanRecv     int32 = 2
	waitReasonChanSelect   int32 = 3
	waitReasonSemacquire   int32 = 4
	waitReasonSleep        int32 = 5
	waitReasonHostCallback int32 = 6 // syscall/js, fetch — host may ready
	waitReasonGosched      int32 = 7
)

// g is $go.g (03 / 04): id, park id, panic chain, recoverOk, select in flight.
// The Wasm global $g is shared across JSPI activations; park save/restore is
// emitted around the park import (see gopark and the jspi-sched prototype).
type g struct {
	id        int32
	park      int32
	panic     *panicObj
	recoverOk int32
	sel       *select_
	waiting   int32 // 1 while parked on a channel/sema/select
}

// runqnode is one entry on the FIFO of closures waiting for _sched.
type runqnode struct {
	fn   func()
	next *runqnode
}

var (
	gp            *g // current goroutine; Wasm lowers this to global $g
	runqHead      *runqnode
	runqTail      *runqnode
	runqLen       int32
	goidGen       int32
	ngo           int32 // started and not yet returned
	nparked       int32 // currently parked (waiting for ready)
	npendingReady int32 // readied but not yet resumed (JSPI)
	ntimers       int32 // armed timers that can call ready
	parkIDGen     int32
)

func init() {
	gp = &g{id: 1}
	goidGen = 1
	ngo = 1 // main
}

// getg returns the current goroutine. Lowerer may emit global.get $g instead.
func getg() *g {
	return gp
}

// mintParkID returns a fresh park id. Ids are never reused while a waiter exists.
func mintParkID() int32 {
	parkIDGen++
	if parkIDGen <= 0 {
		parkIDGen = 1
	}
	return parkIDGen
}

// newproc implements `go fn()`: enqueue and spawn. Does not yield (10 §2).
func newproc(fn func()) {
	if fn == nil {
		throw("go of nil func")
	}
	n := &runqnode{fn: fn}
	if runqTail == nil {
		runqHead = n
		runqTail = n
	} else {
		runqTail.next = n
		runqTail = n
	}
	runqLen++
	ngo++
	spawn()
}

// go_ is the compiler-facing name for `go f()` (04 symbol table: runtime.go).
// Named go_ because go is a keyword.
func go_(fn func()) {
	newproc(fn)
}

// sched is the export `_sched`: dequeue one closure, install $g, call it.
func sched() {
	n := runqHead
	if n == nil {
		return
	}
	runqHead = n.next
	if runqHead == nil {
		runqTail = nil
	}
	runqLen--
	child := &g{id: nextgoid()}
	saved := gp
	gp = child
	n.fn()
	gp = saved
	ngo--
}

func nextgoid() int32 {
	goidGen++
	if goidGen <= 0 {
		goidGen = 2
	}
	return goidGen
}

// gopark saves $g, calls the suspending park import, restores $g (04).
// JSPI may clobber the global across the suspending import, so save/restore
// in Go around park.
func gopark(id int32, reason int32) {
	if id == 0 {
		throw("gopark: invalid park id")
	}
	checkDeadlock(reason)
	nparked++
	saved := gp
	if gp != nil {
		gp.park = id
		gp.waiting = 1
	}
	_ = park(id)
	gp = saved
	if gp != nil {
		gp.waiting = 0
		gp.park = 0
	}
	nparked--
	// Pair with goready's npendingReady++ when this park was readied early.
	if npendingReady > 0 {
		npendingReady--
	}
}

// goready signals park id. Non-suspending.
// A readied goroutine will resume when the current activation parks; count it
// as runnable for deadlock checks until its gopark returns.
func goready(id int32) {
	ready(id)
	npendingReady++
}

// Gosched yields to a macrotask so spawned goroutines and timers can run.
func Gosched() {
	checkDeadlock(waitReasonGosched)
	goschedImport()
}

// NumGoroutine returns started goroutines that have not returned.
func NumGoroutine() int {
	return int(ngo)
}

// checkDeadlock panics if this park would leave nothing runnable and nothing
// that can call ready. Host-callback parks are exempt (04).
func checkDeadlock(reason int32) {
	if reason == waitReasonHostCallback {
		return
	}
	// Runnable after this park: other unparked goroutines, the run queue,
	// timers, and goroutines already readied but not yet resumed.
	runnableElse := (ngo - nparked - 1) + runqLen + npendingReady
	if runnableElse > 0 || ntimers > 0 {
		return
	}
	// Only this goroutine would remain, and it is about to park with no timer.
	fatal("all goroutines are asleep - deadlock!")
}

// Host imports (07). Bodies are filled by wasmimport / WAT; scaffolds call
// through these names so the lowerer and glue share the contract in 04.

// park is go.runtime.park (suspending).
func park(id int32) int32

// ready is go.runtime.ready (non-suspending).
func ready(id int32)

// spawn is go.runtime.spawn (non-suspending): macrotask → promising(_sched).
func spawn()

// goschedImport is go.runtime.gosched (suspending): resolves on a macrotask.
func goschedImport()
