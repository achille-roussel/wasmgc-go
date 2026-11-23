package runtime

// Timers (docs/design/04). time.Sleep and time.Timer share scheduleTimer /
// clearTimer host imports. Fire path is a macrotask that calls ready(id).

// timeSleep parks until ns nanoseconds elapse (linkname for time.Sleep).
func timeSleep(ns int64) {
	if ns <= 0 {
		return
	}
	id := mintParkID()
	ntimers++
	scheduleTimer(id, int32(ns), int32(ns>>32))
	gopark(id, waitReasonSleep)
	ntimers--
	clearTimer(id)
}

// scheduleTimer is go.runtime.scheduleTimer(id, lo, hi) — ns as two i32 limbs.
func scheduleTimer(id, lo, hi int32)

// clearTimer is go.runtime.clearTimer(id).
func clearTimer(id int32)

// nanotime / walltime: prefer host imports when wasmimport lands; scaffolds
// keep the stubs in runtime.go until then. Override here with real names.

// nanotimeImport / walltimeImport are the go.runtime.* clocks (07).
func nanotimeImport() int64
func walltimeImport() (sec int64, nsec int32)
