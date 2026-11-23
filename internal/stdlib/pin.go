package stdlib

import (
	"bufio"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"
)

// Pin identifies the locked Go standard library version.
type Pin struct {
	Version string
	URL     string
	SHA256  string
}

// LoadPIN reads std/PIN from the module root (or the given path).
func LoadPIN(path string) (Pin, error) {
	f, err := os.Open(path)
	if err != nil {
		return Pin{}, err
	}
	defer f.Close()
	var p Pin
	sc := bufio.NewScanner(f)
	for sc.Scan() {
		line := strings.TrimSpace(sc.Text())
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		k, v, ok := strings.Cut(line, "=")
		if !ok {
			continue
		}
		switch strings.TrimSpace(k) {
		case "version":
			p.Version = strings.TrimSpace(v)
		case "url":
			p.URL = strings.TrimSpace(v)
		case "sha256":
			p.SHA256 = strings.TrimSpace(v)
		}
	}
	if err := sc.Err(); err != nil {
		return Pin{}, err
	}
	if p.Version == "" || p.URL == "" || p.SHA256 == "" {
		return Pin{}, fmt.Errorf("stdlib: incomplete PIN in %s", path)
	}
	return p, nil
}

// FindModuleRoot walks up from start looking for go.mod.
func FindModuleRoot(start string) (string, error) {
	dir, err := filepath.Abs(start)
	if err != nil {
		return "", err
	}
	for {
		if _, err := os.Stat(filepath.Join(dir, "go.mod")); err == nil {
			return dir, nil
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			return "", fmt.Errorf("stdlib: go.mod not found from %s", start)
		}
		dir = parent
	}
}

// CompilerModuleRoot returns the wasmgc-go module root that contains
// std/src/runtime. Uses the source path of this package at compile time so
// drivers started outside the repo (e2e temp dirs) still find rewrites.
func CompilerModuleRoot() (string, error) {
	_, file, _, ok := runtime.Caller(0)
	if !ok {
		return "", fmt.Errorf("stdlib: runtime.Caller failed")
	}
	return FindModuleRoot(filepath.Dir(file))
}
