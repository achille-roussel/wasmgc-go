// Package debug is a wasmgc rewrite stub of runtime/debug.
// Upstream pulls internal/poll and incomplete MemStats; language tests only
// need Stack / SetGCPercent / SetMaxStack / FreeOSMemory as no-ops.
package debug

import (
	"os"
	"runtime"
	"time"
)

// PrintStack prints to standard error the stack trace returned by Stack.
func PrintStack() {
	os.Stderr.Write(Stack())
}

// Stack returns a formatted stack trace of the calling goroutine.
func Stack() []byte {
	buf := make([]byte, 1024)
	for {
		n := runtime.Stack(buf, false)
		if n < len(buf) {
			return buf[:n]
		}
		buf = make([]byte, 2*len(buf))
	}
}

// SetGCPercent sets the garbage collection target percentage (no-op stub).
// Returns the previous setting; wasmgc always reports percent as the prior value.
func SetGCPercent(percent int) int {
	return percent
}

// FreeOSMemory is a no-op under wasmgc (host GC owns memory).
func FreeOSMemory() {}

// SetMaxStack sets the maximum stack size (no-op stub). Returns bytes.
func SetMaxStack(bytes int) int { return bytes }

// SetMaxThreads sets the maximum number of OS threads (no-op stub).
func SetMaxThreads(threads int) int { return threads }

// SetPanicOnFault controls panic-on-fault (no-op stub).
func SetPanicOnFault(enabled bool) bool { return false }

// SetMemoryLimit sets a soft memory limit (no-op stub).
func SetMemoryLimit(limit int64) int64 { return limit }

// SetTraceback sets the amount of output on panics (no-op stub).
func SetTraceback(level string) { _ = level }

// WriteHeapDump is a no-op stub (no heap dump on wasmgc).
func WriteHeapDump(fd uintptr) { _ = fd }

// GCStats is a stub snapshot of GC statistics.
type GCStats struct {
	LastGC         time.Time
	NumGC          int64
	PauseTotal     time.Duration
	Pause          []time.Duration
	PauseEnd       []time.Time
	PauseQuantiles []time.Duration
}

// ReadGCStats fills stats with zeros (stub).
func ReadGCStats(stats *GCStats) {
	if stats == nil {
		return
	}
	*stats = GCStats{}
}

// BuildInfo is a stub module build record.
type BuildInfo struct {
	GoVersion string
	Path      string
	Main      Module
	Deps      []*Module
	Settings  []BuildSetting
}

// Module is a stub module path/version.
type Module struct {
	Path    string
	Version string
	Sum     string
	Replace *Module
}

// BuildSetting is a stub key/value build setting.
type BuildSetting struct {
	Key, Value string
}

// ReadBuildInfo reports no build info under wasmgc rewrite stubs.
func ReadBuildInfo() (info *BuildInfo, ok bool) { return nil, false }

// CrashOptions controls crash output formatting (unused stub).
type CrashOptions struct{}

// SetCrashOutput is a no-op stub.
func SetCrashOutput(f *os.File, opts CrashOptions) error {
	_, _ = f, opts
	return nil
}
