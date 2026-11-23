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

// nanotime / walltime: prefer host imports when wasmimport lands; scaffolds
// keep the stubs in runtime.go until then. Override here with real names.

// nanotimeImport / walltimeImport are placeholders until wasmimport clocks
// land. Stub bodies avoid empty-result funcs in the Wasm code section.
func nanotimeImport() int64                  { return 0 }
func walltimeImport() (sec int64, nsec int32) { return 0, 0 }
