// Package runtime is the wasmgc rewrite of the Go runtime
// (docs/design/04-runtime-and-abi.md). Print and the linear bridge live in
// print.go / bridge.go. Scheduler, channels, select, timers, and sema are
// M7 (proc.go, chan.go, select.go, time.go, sema.go).
package runtime

import "unsafe"

const GOOS = "js"
const GOARCH = "wasmgc"

func GOMAXPROCS(n int) int {
	if n > 0 {
		return n
	}
	return 1
}

func Goexit() { goexit() }

func GOROOT() string { return "" }

func Version() string { return "wasmgc-go" }

func GC() {}

func KeepAlive(x any) {}

func SetFinalizer(obj any, finalizer any) {}

func AddCleanup[T, C any](ptr *T, cleanup func(C), arg C) Cleanup {
	return Cleanup{}
}

type Cleanup struct{}

func (c Cleanup) Stop() {}

func FuncForPC(pc uintptr) *Func { return nil }

type Func struct{}

func (f *Func) Name() string { return "" }

func (f *Func) Entry() uintptr { return 0 }

func (f *Func) FileLine(pc uintptr) (string, int) { return "", 0 }

func Caller(skip int) (pc uintptr, file string, line int, ok bool) {
	return 0, "", 0, false
}

func Callers(skip int, pc []uintptr) int { return 0 }

// Frames / CallersFrames are placeholders for internal/bisect and debug/stack.
type Frames struct{}

func (f *Frames) Next() (frame Frame, more bool) { return Frame{}, false }

type Frame struct {
	PC       uintptr
	Func     *Func
	Function string
	File     string
	Line     int
	Entry    uintptr
}

func CallersFrames(callers []uintptr) *Frames { return &Frames{} }

func Stack(buf []byte, all bool) int { return 0 }

type Error interface {
	RuntimeError()
	error
}

func memhash(p unsafe.Pointer, seed, s uintptr) uintptr { return seed }

func write(fd uintptr, p unsafe.Pointer, n int32) int32 { return n }

func randn(n uint32) uint32 { return fastrandn(n) }

func rand() uint64 { return uint64(fastrand()) | uint64(fastrand())<<32 }

// preemptCheck is reserved for a later yield on loop back-edges; v1 is a no-op (04).
func preemptCheck() {}

// Processor pin stubs used by sync.Pool.
func procPin() int { return 0 }
func procUnpin()   {}
func registerPoolCleanup(cleanup func()) {}

// RegisterPoolCleanup records a GC-cycle pool cleanup (no-op until GC hooks).
func RegisterPoolCleanup(cleanup func()) { registerPoolCleanup(cleanup) }

// ProcPin / ProcUnpin are sync.Pool pin stubs (single-activation wasmgc).
func ProcPin() int { return procPin() }
func ProcUnpin()   { procUnpin() }
