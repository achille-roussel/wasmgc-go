// Package jsglue embeds lib/wasmgc.js so wasmgc-go run/build work outside
// the source tree without WASMGC_JS. Keep the bytes in sync with lib/wasmgc.js
// (TestMatchesLib enforces this).
package jsglue

import (
	_ "embed"
	"fmt"
	"os"
	"path/filepath"
	"sync"
)

//go:embed wasmgc.js
var Source string

var (
	materializeOnce sync.Once
	materializePath string
	materializeErr  error
)

// Path returns a filesystem path to wasmgc.js.
// Prefer WASMGC_JS when set by the caller; otherwise write the embedded
// source to a per-process temp file once and reuse it.
func Path() (string, error) {
	if p := os.Getenv("WASMGC_JS"); p != "" {
		return p, nil
	}
	materializeOnce.Do(func() {
		dir, err := os.MkdirTemp("", "wasmgc-js-*")
		if err != nil {
			materializeErr = err
			return
		}
		path := filepath.Join(dir, "wasmgc.js")
		if err := os.WriteFile(path, []byte(Source), 0o644); err != nil {
			materializeErr = err
			return
		}
		materializePath = path
	})
	if materializeErr != nil {
		return "", fmt.Errorf("jsglue: materialize embedded wasmgc.js: %w", materializeErr)
	}
	return materializePath, nil
}
