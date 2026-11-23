package load

import (
	"fmt"
	"os"
	"path/filepath"
)

const fakeRuntimeSource = `// Package runtime is a minimal fake for wasmgc-go loader tests.
// Prefer the materialized rewrite from stream H when available.
package runtime
`

// WriteFakeGOROOT writes a GOROOT-shaped tree containing only package runtime.
// If dir is empty, a directory under the user cache is used. Intended as a
// test fallback when the full materializer is not needed.
func WriteFakeGOROOT(dir string) (string, error) {
	var err error
	if dir == "" {
		cache, cerr := os.UserCacheDir()
		if cerr != nil {
			dir, err = os.MkdirTemp("", "wasmgc-fake-goroot-*")
			if err != nil {
				return "", err
			}
		} else {
			dir = filepath.Join(cache, "wasmgc-go", "fake-goroot-runtime")
		}
	}
	if err := os.MkdirAll(filepath.Join(dir, "src", "runtime"), 0o755); err != nil {
		return "", err
	}
	if err := os.WriteFile(filepath.Join(dir, "src", "runtime", "runtime.go"), []byte(fakeRuntimeSource), 0o644); err != nil {
		return "", err
	}
	if err := os.WriteFile(filepath.Join(dir, "VERSION"), []byte("go1.27.4\n"), 0o644); err != nil {
		return "", err
	}
	abs, err := filepath.Abs(dir)
	if err != nil {
		return "", fmt.Errorf("load: fake GOROOT: %w", err)
	}
	return abs, nil
}
