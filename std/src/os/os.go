// Package os is a wasmgc rewrite for stdio/args (M9).
// Upstream js/wasm file selection does not match GOARCH=wasmgc; see 06.
package os

import (
	"io"
	"runtime"
	"syscall"
)

var (
	Stdin  = NewFile(0, "/dev/stdin")
	Stdout = NewFile(1, "/dev/stdout")
	Stderr = NewFile(2, "/dev/stderr")
	Args   []string
)

func init() {
	// Build via append: make([]string, n) would array.new_default a
	// non-defaultable (ref $go.string) element type (design 03).
	n := runtime.GetArgc()
	for i := 0; i < n; i++ {
		Args = append(Args, runtime.GetArg(i))
	}
}

// FileMode represents a file's mode and permission bits.
type FileMode uint32

type File struct {
	fd   int
	name string
}

func NewFile(fd uintptr, name string) *File {
	return &File{fd: int(fd), name: name}
}

func (f *File) Name() string { return f.name }

func (f *File) Read(b []byte) (int, error) {
	if f == nil {
		return 0, ErrInvalid
	}
	n, err := syscall.Read(f.fd, b)
	return n, err
}

func (f *File) Write(b []byte) (int, error) {
	if f == nil {
		return 0, ErrInvalid
	}
	n, err := syscall.Write(f.fd, b)
	return n, err
}

func (f *File) Close() error { return nil }

var (
	ErrInvalid = errInvalid()
	ErrClosed  = errClosed()
)

type PathError struct {
	Op   string
	Path string
	Err  error
}

func (e *PathError) Error() string {
	return e.Op + " " + e.Path + ": " + e.Err.Error()
}

func (e *PathError) Unwrap() error { return e.Err }

func errInvalid() error { return syscall.EINVAL }
func errClosed() error  { return syscall.EINVAL }

func Exit(code int) { syscall.Exit(code) }

func Getenv(key string) string {
	v, _ := syscall.Getenv(key)
	return v
}

func Environ() []string { return syscall.Environ() }

func Getwd() (string, error) { return syscall.Getwd() }

// Write writes data to the named file. v1 has no host filesystem; it always
// returns a PathError wrapping syscall.ENOSYS (docs/design/06, 09).
func Write(name string, data []byte, perm FileMode) error {
	return &PathError{Op: "write", Path: name, Err: syscall.ENOSYS}
}

// Ensure File implements io.Reader / io.Writer.
var (
	_ io.Reader = (*File)(nil)
	_ io.Writer = (*File)(nil)
)
