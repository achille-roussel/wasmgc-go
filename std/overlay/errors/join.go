// overlay of go1.27.4 errors/join.go
package errors

// Join is deferred past M4 (needs robust slice range + multi-error).
func Join(errs ...error) error {
	return nil
}
