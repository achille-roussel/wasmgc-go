// Package load owns build-tag re-filtering and (later) go list integration.
package load

import (
	"go/build"
	"go/build/constraint"
	"os"
	"path/filepath"
	"strings"
)

// FilterConfig is the wasmgc target used when re-evaluating build constraints.
type FilterConfig struct {
	GOOS      string   // typically "js"
	GOARCH    string   // typically "wasmgc"
	BuildTags []string // typically ["wasmgc"] plus user tags
}

// DefaultFilter is GOOS=js, GOARCH=wasmgc, tag wasmgc.
func DefaultFilter() FilterConfig {
	return FilterConfig{
		GOOS:      "js",
		GOARCH:    "wasmgc",
		BuildTags: []string{"wasmgc"},
	}
}

// Context returns a go/build.Context for MatchFile-based filtering.
func (c FilterConfig) Context() *build.Context {
	ctx := build.Default
	ctx.GOOS = c.GOOS
	ctx.GOARCH = c.GOARCH
	ctx.BuildTags = append([]string(nil), c.BuildTags...)
	ctx.CgoEnabled = false
	return &ctx
}

// MatchGoFile reports whether a .go file should be kept under the wasmgc filter.
// It evaluates //go:build lines with go/build/constraint and applies filename
// suffix rules: *_js.go and *_wasmgc.go stay, *_wasm.go and other GOARCH
// suffixes drop. A file tagged wasm drops; !wasm comes back.
func (c FilterConfig) MatchGoFile(dir, name string) (bool, error) {
	if !strings.HasSuffix(name, ".go") {
		return false, nil
	}
	// Use go/build.Context.MatchFile: it parses constraints via
	// go/build/constraint and applies goodOSArchFile for name suffixes.
	return c.Context().MatchFile(dir, name)
}

// EvalTags evaluates a //go:build line under the filter config.
// Exposed for tests that exercise go/build/constraint directly.
func (c FilterConfig) EvalTags(line string) (bool, error) {
	expr, err := constraint.Parse(line)
	if err != nil {
		return false, err
	}
	return expr.Eval(c.tagOK), nil
}

func (c FilterConfig) tagOK(tag string) bool {
	if tag == c.GOOS || tag == c.GOARCH {
		return true
	}
	for _, t := range c.BuildTags {
		if t == tag {
			return true
		}
	}
	// goexperiment.jsonv2 is on by default in Go 1.27.
	if tag == "goexperiment.jsonv2" {
		return true
	}
	return false
}

// ListGoFiles returns the .go file names in dir that pass the filter.
// Test files (*_test.go) are excluded.
func (c FilterConfig) ListGoFiles(dir string) ([]string, error) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil, err
	}
	var out []string
	for _, e := range entries {
		if e.IsDir() {
			continue
		}
		name := e.Name()
		if !strings.HasSuffix(name, ".go") || strings.HasSuffix(name, "_test.go") {
			continue
		}
		ok, err := c.MatchGoFile(dir, name)
		if err != nil {
			return nil, err
		}
		if ok {
			out = append(out, filepath.Join(dir, name))
		}
	}
	return out, nil
}
