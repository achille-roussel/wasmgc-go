package load

import (
	"bytes"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"strings"
)

// listPackage is the subset of go list -json fields we need.
type listPackage struct {
	Dir            string
	ImportPath     string
	Name           string
	GoFiles        []string
	IgnoredGoFiles []string
	CgoFiles       []string
	SFiles         []string
	Goroot         bool
	Standard       bool
	DepOnly        bool
	Error          *listError
}

type listError struct {
	Err string
}

// goList runs `go list -json -deps -e` with GOOS=js GOARCH=wasm and the given
// tags. GOARCH=wasm is required because go list rejects unknown arches;
// callers must re-filter with GOARCH=wasmgc (see FilterConfig).
func goList(dir, pattern string, tags []string) ([]listPackage, error) {
	args := []string{"list", "-json", "-deps", "-e", "-tags=" + strings.Join(tags, ",")}
	args = append(args, pattern)
	cmd := exec.Command("go", args...)
	cmd.Dir = dir
	cmd.Env = append(os.Environ(),
		"GOOS=js",
		"GOARCH=wasm",
		"CGO_ENABLED=0",
		// Pin the wasmgc-go toolchain so ephemeral test modules resolve like the repo.
		"GOTOOLCHAIN=go1.27.4",
	)
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		return nil, fmt.Errorf("load: go list: %v\n%s", err, stderr.String())
	}
	var pkgs []listPackage
	dec := json.NewDecoder(&stdout)
	for dec.More() {
		var p listPackage
		if err := dec.Decode(&p); err != nil {
			return nil, fmt.Errorf("load: go list json: %w", err)
		}
		pkgs = append(pkgs, p)
	}
	return pkgs, nil
}
