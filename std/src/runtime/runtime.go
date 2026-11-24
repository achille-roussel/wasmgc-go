// Package runtime is the wasmgc rewrite of the Go runtime
// (docs/design/04-runtime-and-abi.md). Print and the linear bridge live in
// print.go / bridge.go. Scheduler, channels, select, timers, and sema are
// M7 (proc.go, chan.go, select.go, time.go, sema.go).
package runtime

import "unsafe"

const GOOS = "js"
const GOARCH = "wasmgc"

// Goexit, GOROOT, and Version live in public_stubs.go so rtlink.EnsurePublic
// can link them when PackageHigh skips lowering GOROOT runtime.

// GC, KeepAlive, Caller, Stack, FuncForPC, GOMAXPROCS, SetFinalizer, and
// Frames live in public_stubs.go so rtlink.EnsurePublic can link them alone
// without pulling unsafe / goexit / generics from this file.

func AddCleanup[T, C any](ptr *T, cleanup func(C), arg C) Cleanup {
	return Cleanup{}
}

type Cleanup struct{}

func (c Cleanup) Stop() {}

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
