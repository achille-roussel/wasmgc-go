// Package ioutil is a wasmgc rewrite wrapping io/os helpers.
// Upstream tempfile pulls os.CreateTemp/MkdirTemp which the thin os rewrite
// lacks; language-suite imports still need ReadAll/NopCloser/Discard/ReadFile.
package ioutil

import (
	"io"
	"io/fs"
	"os"
)

// ReadAll reads from r until error or EOF.
func ReadAll(r io.Reader) ([]byte, error) {
	return io.ReadAll(r)
}

// ReadFile reads the named file via os.Open (host FS may return PathError).
func ReadFile(filename string) ([]byte, error) {
	f, err := os.Open(filename)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	return io.ReadAll(f)
}

// WriteFile writes data to the named file via os.Write.
func WriteFile(filename string, data []byte, perm fs.FileMode) error {
	return os.Write(filename, data, os.FileMode(perm))
}

// ReadDir is a stub: wasmgc os.File has no Readdir. Always returns PathError.
func ReadDir(dirname string) ([]fs.FileInfo, error) {
	return nil, &os.PathError{Op: "readdir", Path: dirname, Err: os.ErrInvalid}
}

// NopCloser returns a ReadCloser with a no-op Close wrapping r.
func NopCloser(r io.Reader) io.ReadCloser {
	return io.NopCloser(r)
}

// Discard is an io.Writer on which all Write calls succeed without doing anything.
var Discard io.Writer = io.Discard

// TempFile is unsupported on wasmgc (no host temp FS).
func TempFile(dir, pattern string) (*os.File, error) {
	_, _ = dir, pattern
	return nil, &os.PathError{Op: "createtemp", Path: dir, Err: os.ErrInvalid}
}

// TempDir is unsupported on wasmgc (no host temp FS).
func TempDir(dir, pattern string) (string, error) {
	_, _ = dir, pattern
	return "", &os.PathError{Op: "mkdirtemp", Path: dir, Err: os.ErrInvalid}
}
