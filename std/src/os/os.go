// Package os is a wasmgc rewrite for stdio/args (M9).
// Upstream js/wasm file selection does not match GOARCH=wasmgc; see 06.
package os

import (
	"io"
	"io/fs"
	"runtime"
	"syscall"
)

// Path separators for GOOS=js / wasmgc (unix-shaped).
const (
	PathSeparator     = '/'
	PathListSeparator = ':'
)

// IsPathSeparator reports whether c is a directory separator character.
func IsPathSeparator(c uint8) bool { return PathSeparator == c }

// FileInfo is the os alias of fs.FileInfo.
type FileInfo = fs.FileInfo

// DirEntry is the os alias of fs.DirEntry.
type DirEntry = fs.DirEntry

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

// Readdirnames reads the names of directory entries. Stub: no host FS.
func (f *File) Readdirnames(n int) ([]string, error) {
	_ = n
	if f == nil {
		return nil, ErrInvalid
	}
	return nil, &PathError{Op: "readdirnames", Path: f.name, Err: syscall.ENOSYS}
}

// Readdir reads directory entries as FileInfo values. Stub: no host FS.
func (f *File) Readdir(n int) ([]FileInfo, error) {
	_ = n
	if f == nil {
		return nil, ErrInvalid
	}
	return nil, &PathError{Op: "readdir", Path: f.name, Err: syscall.ENOSYS}
}

var (
	ErrInvalid = errInvalid()
	ErrClosed  = errClosed()
	ErrExist   = errExist()
	ErrNotExist = errNotExist()
	ErrPermission = errPermission()
)

// PathError is the os alias of fs.PathError (matches upstream).
type PathError = fs.PathError

func errInvalid() error    { return syscall.EINVAL }
func errClosed() error     { return syscall.EINVAL }
func errExist() error      { return syscall.EEXIST }
func errNotExist() error   { return syscall.ENOENT }
func errPermission() error { return syscall.EPERM }

func Exit(code int) { syscall.Exit(code) }

func Getenv(key string) string {
	v, ok := runtime.Getenv(key)
	if !ok {
		return ""
	}
	return v
}

func Environ() []string { return syscall.Environ() }

func Getwd() (string, error) { return syscall.Getwd() }

// Write writes data to the named file. v1 has no host filesystem; it always
// returns a PathError wrapping syscall.ENOSYS (docs/design/06, 09).
func Write(name string, data []byte, perm FileMode) error {
	return &PathError{Op: "write", Path: name, Err: syscall.ENOSYS}
}

// Open opens the named file for reading. v1 has no host filesystem.
func Open(name string) (*File, error) {
	return nil, &PathError{Op: "open", Path: name, Err: syscall.ENOENT}
}

// Stat returns FileInfo for the named file. Stub: always not found.
func Stat(name string) (FileInfo, error) {
	return nil, &PathError{Op: "stat", Path: name, Err: syscall.ENOENT}
}

// Lstat is like Stat but does not follow symlinks. Stub: always not found.
func Lstat(name string) (FileInfo, error) {
	return nil, &PathError{Op: "lstat", Path: name, Err: syscall.ENOENT}
}

// ReadDir reads the named directory. Stub: always not found.
func ReadDir(name string) ([]DirEntry, error) {
	return nil, &PathError{Op: "readdir", Path: name, Err: syscall.ENOENT}
}

// Readlink returns the destination of the named symbolic link. Stub.
func Readlink(name string) (string, error) {
	return "", &PathError{Op: "readlink", Path: name, Err: syscall.EINVAL}
}

// IsNotExist reports whether err indicates a file does not exist.
func IsNotExist(err error) bool {
	return isPathError(err, syscall.ENOENT)
}

// IsExist reports whether err indicates a file already exists.
func IsExist(err error) bool {
	return isPathError(err, syscall.EEXIST)
}

// IsPermission reports whether err indicates a permission error.
func IsPermission(err error) bool {
	return isPathError(err, syscall.EPERM)
}

func isPathError(err error, errno syscall.Errno) bool {
	for err != nil {
		if pe, ok := err.(*PathError); ok {
			if e, ok := pe.Err.(syscall.Errno); ok && e == errno {
				return true
			}
		}
		u, ok := err.(interface{ Unwrap() error })
		if !ok {
			return false
		}
		err = u.Unwrap()
	}
	return false
}

// Ensure File implements io.Reader / io.Writer.
var (
	_ io.Reader = (*File)(nil)
	_ io.Writer = (*File)(nil)
)
