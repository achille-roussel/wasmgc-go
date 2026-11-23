// overlay of go1.27.4 errors/wrap.go
package errors

// Unwrap returns nil; full unwrap walks wait for iface-to-iface asserts.
func Unwrap(err error) error {
	return nil
}

// Is reports whether err == target. Wrapping walks deferred past M4.
func Is(err, target error) bool {
	return err == target
}

// As is a stub until reflectlite Value.Set is wired.
func As(err error, target any) bool {
	return false
}

// AsType finds the first error in err's tree that matches type E.
// wasmgc: no Unwrap walk yet; only direct type assertion (enough for json inject).
func AsType[E error](err error) (E, bool) {
	var zero E
	if err == nil {
		return zero, false
	}
	if e, ok := err.(E); ok {
		return e, true
	}
	return zero, false
}
