package main

import (
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/achille-roussel/wasmgc-go/internal/check"
	"github.com/achille-roussel/wasmgc-go/internal/jsglue"
	"github.com/achille-roussel/wasmgc-go/internal/stdlib"
)

func modulePaths() (stdlib.Paths, error) {
	wd, err := os.Getwd()
	if err != nil {
		return stdlib.Paths{}, err
	}
	root, err := stdlib.FindModuleRoot(wd)
	if err != nil {
		return stdlib.Paths{}, err
	}
	return stdlib.DefaultPaths(root)
}

func splitTags(s string) []string {
	if s == "" {
		return nil
	}
	var out []string
	for _, t := range strings.Split(s, ",") {
		t = strings.TrimSpace(t)
		if t != "" {
			out = append(out, t)
		}
	}
	return out
}

func printWarnings(warnings []check.Diagnostic) {
	for _, w := range warnings {
		fmt.Fprintln(os.Stderr, w.String())
	}
}

func packageArg(fs *flag.FlagSet) string {
	args := fs.Args()
	if len(args) == 0 {
		return "."
	}
	return args[0]
}

// findWasmgcJS locates lib/wasmgc.js for the Node runner.
// Order: WASMGC_JS override, walk from cwd / executable for a source-tree
// checkout, then the bytes embedded in the binary (go:embed).
func findWasmgcJS() (string, error) {
	if p := os.Getenv("WASMGC_JS"); p != "" {
		return p, nil
	}
	var candidates []string
	if wd, err := os.Getwd(); err == nil {
		candidates = append(candidates, wd)
	}
	if exe, err := os.Executable(); err == nil {
		candidates = append(candidates, filepath.Dir(exe))
	}
	for _, start := range candidates {
		dir := start
		for {
			candidate := filepath.Join(dir, "lib", "wasmgc.js")
			if _, err := os.Stat(candidate); err == nil {
				return candidate, nil
			}
			candidate = filepath.Join(dir, "..", "lib", "wasmgc.js")
			if abs, err := filepath.Abs(candidate); err == nil {
				if _, err := os.Stat(abs); err == nil {
					return abs, nil
				}
			}
			parent := filepath.Dir(dir)
			if parent == dir {
				break
			}
			dir = parent
		}
	}
	return jsglue.Path()
}
