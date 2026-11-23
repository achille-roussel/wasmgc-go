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
	return 0, "", 0, false
}

func Callers(skip int, pc []uintptr) int { return 0 }

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

// Stack writes a stub traceback into buf (real PC/file/line is a follow-up).
func Stack(buf []byte, all bool) int {
	msg := "goroutine 1 [running]:\n"
	n := len(msg)
	if n > len(buf) {
		n = len(buf)
	}
	copy(buf, msg[:n])
	return n
}
