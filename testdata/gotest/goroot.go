package gotest

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"github.com/achille-roussel/wasmgc-go/internal/stdlib"
)

// DefaultGoSrcFallback is used when no env / pin tree exposes $GOROOT/test.
// Cloud Agent images ship go1.27.4 sources here.
const DefaultGoSrcFallback = "/home/ubuntu/.local/go-src"

// FindTestRoot locates the Go source tree that contains test/ (the language
// suite). Preference order:
//  1. WASMGC_GOROOT
//  2. GOROOT
//  3. stdlib pin upstream extract (src-<pin>) if it has test/
//  4. DefaultGoSrcFallback
//  5. `go env GOROOT` (may be a binary distribution without test/)
func FindTestRoot() (string, error) {
	candidates := []string{}
	if v := os.Getenv("WASMGC_GOROOT"); v != "" {
		candidates = append(candidates, v)
	}
	if v := os.Getenv("GOROOT"); v != "" {
		candidates = append(candidates, v)
	}
	if root, err := stdlib.CompilerModuleRoot(); err == nil {
		if p, err := stdlib.DefaultPaths(root); err == nil {
			if pin, err := stdlib.LoadPIN(p.PIN); err == nil {
				candidates = append(candidates, filepath.Join(p.CacheDir, "src-"+pin.Version))
			}
		}
	}
	candidates = append(candidates, DefaultGoSrcFallback)
	if out, err := exec.Command("go", "env", "GOROOT").Output(); err == nil {
		if g := strings.TrimSpace(string(out)); g != "" {
			candidates = append(candidates, g)
		}
	}

	var tried []string
	seen := map[string]bool{}
	for _, c := range candidates {
		if c == "" {
			continue
		}
		c = filepath.Clean(c)
		if seen[c] {
			continue
		}
		seen[c] = true
		testDir := filepath.Join(c, "test")
		if st, err := os.Stat(testDir); err == nil && st.IsDir() {
			return c, nil
		}
		tried = append(tried, c)
	}
	return "", fmt.Errorf("gotest: no Go source tree with test/ found (tried %s); set WASMGC_GOROOT", strings.Join(tried, ", "))
}
