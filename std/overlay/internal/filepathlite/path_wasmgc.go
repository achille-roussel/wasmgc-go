// overlay of go1.27.4 internal/filepathlite/path_wasmgc.go
// Twin of path_unix.go: upstream is tagged unix || (js && wasm); GOARCH=wasmgc
// drops it. Same unix-shaped separators for wasmgc.
package filepathlite

import (
	"internal/bytealg"
	"internal/stringslite"
)

const (
	Separator     = '/' // OS-specific path separator
	ListSeparator = ':' // OS-specific path list separator
)

func IsPathSeparator(c uint8) bool {
	return Separator == c
}

func isLocal(path string) bool {
	return unixIsLocal(path)
}

func localize(path string) (string, error) {
	if bytealg.IndexByteString(path, 0) >= 0 {
		return "", errInvalidPath
	}
	return path, nil
}

// IsAbs reports whether the path is absolute.
func IsAbs(path string) bool {
	return stringslite.HasPrefix(path, "/")
}

// volumeNameLen returns length of the leading volume name on Windows.
// It returns 0 elsewhere.
func volumeNameLen(path string) int {
	return 0
}
