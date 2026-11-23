// overlay of go1.27.4 errors/errors.go
package errors

// New returns an error that formats as the given text.
func New(text string) error {
	return &errorString{text}
}

type errorString struct {
	s string
}

func (e *errorString) Error() string {
	return e.s
}

// ErrUnsupported indicates that a requested operation cannot be performed.
var ErrUnsupported = New("unsupported operation")
