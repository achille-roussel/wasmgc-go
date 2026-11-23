package runtime

// Slice / index panic helpers the lowerer emits for bounds and growslice
// overflow (docs/design/04). Bodies are empty until M5 EH: codegen fills
// them with unreachable (trap). After M5 they call throwPanic with the
// usual Go messages.

// panicIndex is called on slice/array/string index OOB.
func panicIndex()

// panicSliceB is called on growslice overflow / slice bounds failures.
func panicSliceB()
