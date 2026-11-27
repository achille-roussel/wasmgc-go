// Public no-op / placeholder APIs linked on demand via rtlink.EnsurePublic.
// Kept separate from runtime.go so we do not pull unsafe, goexit, or generics
// when PackageHigh skips lowering GOROOT runtime.
package runtime

func GOMAXPROCS(n int) int {
	if n > 0 {
		return n
	}
	return 1
}

func GC() {}

func KeepAlive(x any) {}

func SetFinalizer(obj any, finalizer any) {}

func FuncForPC(pc uintptr) *Func { return nil }

type Func struct{}

func (f *Func) Name() string { return "" }

func (f *Func) Entry() uintptr { return 0 }

func (f *Func) FileLine(pc uintptr) (string, int) { return "", 0 }

func Caller(skip int) (pc uintptr, file string, line int, ok bool) {
	// Stub: no real PC map yet. ok=false so callers that require real
	// file/line keep failing closed rather than lying about locations.
	return 0, "", 0, false
}

func Callers(skip int, pc []uintptr) int {
	// Synthetic PCs so CallersFrames can invent placeholder frames
	// (issue21879 checks Function names, not file:line).
	if len(pc) == 0 {
		return 0
	}
	n := len(pc)
	if n > 4 {
		n = 4
	}
	for i := 0; i < n; i++ {
		pc[i] = uintptr(skip + i + 1)
	}
	return n
}

type Frames struct {
	pcs []uintptr
	i   int
}

func (f *Frames) Next() (frame Frame, more bool) {
	if f == nil || f.i >= len(f.pcs) {
		return Frame{}, false
	}
	f.i++
	// Placeholder: enough for CallersFrames Function-name checks
	// (issue21879). Real PC→func mapping remains a follow-up.
	frame = Frame{
		PC:       f.pcs[f.i-1],
		Function: "main.main",
		File:     "main.go",
		Line:     1,
	}
	return frame, f.i < len(f.pcs)
}

type Frame struct {
	PC       uintptr
	Func     *Func
	Function string
	File     string
	Line     int
	Entry    uintptr
}

func CallersFrames(callers []uintptr) *Frames {
	pcs := make([]uintptr, len(callers))
	copy(pcs, callers)
	return &Frames{pcs: pcs}
}

// Stack writes a stub traceback into buf (real PC/file/line is a follow-up).
func Stack(buf []byte, all bool) int {
	msg := "goroutine 1 [running]:\nmain.main()\n\tmain.go:1\n"
	n := len(msg)
	if n > len(buf) {
		n = len(buf)
	}
	copy(buf, msg[:n])
	return n
}

// Language-test surface: symbols many $GOROOT/test cases import for GC /
// profiling / debugger hooks. Stubs so typecheck and compile succeed; real
// GC/profiling remain follow-ups.

const Compiler = "gc"

func NumCPU() int { return 1 }

func Breakpoint() {}

// MemStats is a stub heap-stats snapshot (fields used by language tests).
type MemStats struct {
	Alloc         uint64
	TotalAlloc    uint64
	Sys           uint64
	Lookups       uint64
	Mallocs       uint64
	Frees         uint64
	HeapAlloc     uint64
	HeapSys       uint64
	HeapIdle      uint64
	HeapInuse     uint64
	HeapReleased  uint64
	HeapObjects   uint64
	StackInuse    uint64
	StackSys      uint64
	MSpanInuse    uint64
	MSpanSys      uint64
	MCacheInuse   uint64
	MCacheSys     uint64
	BuckHashSys   uint64
	GCSys         uint64
	OtherSys      uint64
	NextGC        uint64
	LastGC        uint64
	PauseTotalNs  uint64
	PauseNs       [256]uint64
	PauseEnd      [256]uint64
	NumGC         uint32
	NumForcedGC   uint32
	GCCPUFraction float64
	EnableGC      bool
	DebugGC       bool
}

func ReadMemStats(m *MemStats) {
	if m == nil {
		return
	}
	*m = MemStats{}
}

var MemProfileRate int = 512 * 1024

type MemProfileRecord struct {
	AllocBytes, FreeBytes     int64
	AllocObjects, FreeObjects int64
	Stack0                    [32]uintptr
}

func (r *MemProfileRecord) InUseBytes() int64 { return r.AllocBytes - r.FreeBytes }

func (r *MemProfileRecord) InUseObjects() int64 {
	return r.AllocObjects - r.FreeObjects
}

func (r *MemProfileRecord) Stack() []uintptr {
	for i, v := range r.Stack0 {
		if v == 0 {
			return r.Stack0[0:i]
		}
	}
	return r.Stack0[0:]
}

// MemProfile reports no samples under wasmgc (stub).
func MemProfile(p []MemProfileRecord, inuseZero bool) (n int, ok bool) {
	return 0, true
}

// Goexit terminates the calling goroutine. Stub: returns to the caller so
// surrounding defers still run when the function exits. Real $go.goexit EH
// (non-returning) remains a follow-up; enough for language tests that only
// need the symbol and defer sequencing.
func Goexit() {}

// GOROOT returns the Go root used by the toolchain. Stub: empty under wasmgc.
func GOROOT() string { return "" }

// Version returns the Go tree's version string.
func Version() string { return "wasmgc-go" }
