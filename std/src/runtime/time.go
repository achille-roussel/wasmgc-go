package runtime

// Timers (docs/design/04). time.Sleep and time.Timer share scheduleTimer /
// clearTimer host imports. Fire path is a macrotask that calls ready(id).
//
// Clocks are body-less host imports (07): walltime → (sec i64, nsec i32),
// nanotime → i64. Codegen imports them only when called so empty_main stays
// at the size budget.

// timeSleep parks until ns nanoseconds elapse (linkname for time.Sleep).
func timeSleep(ns int64) {
	if ns <= 0 {
		return
	}
	id := mintParkID()
	ntimers++
	// Split ns into lo/hi i32 limbs without an i64 shift count (Wasm i64.shr
	// wants an i64 count; the untyped 32 would lower as i32).
	scheduleTimer(id, int32(ns), int32(uint64(ns)>>32))
	gopark(id, waitReasonSleep)
	ntimers--
	clearTimer(id)
}

// TimeSleep is the exported entry for package time's Sleep overlay.
func TimeSleep(ns int64) { timeSleep(ns) }

// scheduleTimer is go.runtime.scheduleTimer(id, lo, hi) — ns as two i32 limbs.
func scheduleTimer(id, lo, hi int32)

// clearTimer is go.runtime.clearTimer(id).
func clearTimer(id int32)

// walltime is go.runtime.walltime() -> (i64, i32).
func walltime() (sec int64, nsec int32)

// nanotime is go.runtime.nanotime() -> i64.
func nanotime() int64

// Walltime is the exported entry for package time's Now.
func Walltime() (sec int64, nsec int32) { return walltime() }

// Nanotime is the exported monotonic clock for package time / sync.
func Nanotime() int64 { return nanotime() }
