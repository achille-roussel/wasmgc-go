// Package runtime is the wasmgc rewrite of the Go runtime
// (docs/design/04-runtime-and-abi.md). Print and the linear bridge live in
// print.go / bridge.go. Other symbols here keep dependents type-checking
// until later milestones (defer/panic M5, maps M6, goroutines M7).
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

func Gosched() {}

func Goexit() { panic("runtime.Goexit") }

func GOROOT() string { return "" }

func Version() string { return "wasmgc-go" }

func NumGoroutine() int { return 1 }

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

func nanotime() int64 { return 0 }

func walltime() (sec int64, nsec int32) { return 0, 0 }

func write(fd uintptr, p unsafe.Pointer, n int32) int32 { return n }

func throw(s string) { panic(s) }

func fatal(s string) { panic(s) }

func randn(n uint32) uint32 { return 0 }

func rand() uint64 { return 0 }

// Processor pin stubs used by sync.Pool overlay.
func procPin() int   { return 0 }
func procUnpin()     {}
func registerPoolCleanup(cleanup func()) {}
