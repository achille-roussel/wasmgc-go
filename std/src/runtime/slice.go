package runtime

// Slice helpers. Bounds panics live in panic.go (M5 EH).
// concatstrings is body-less; codegen fills array.new + array.copy until the
// WAT hatch binds a kernel (docs/design/04, 12).

// concatstrings returns the concatenation of a and b.
func concatstrings(a, b string) string
