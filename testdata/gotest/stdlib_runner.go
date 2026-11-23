package gotest

import (
	"bufio"
	"bytes"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"
)

// StdlibPackage is one import path from packages.txt.
type StdlibPackage struct {
	ImportPath string
}

// StdlibResult is the outcome of wasmgc-go test for one package.
type StdlibResult struct {
	ImportPath string
	Kind       ResultKind // Pass, Fail, SkipListed (package-level)
	PassTests  int
	SkipTests  int
	Err        error
	Output     string
	Reason     string
}

// ReadPackagesTxt reads testdata/gotest/packages.txt.
func ReadPackagesTxt(path string) ([]StdlibPackage, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	var out []StdlibPackage
	sc := bufio.NewScanner(f)
	for sc.Scan() {
		line := strings.TrimSpace(sc.Text())
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		out = append(out, StdlibPackage{ImportPath: line})
	}
	return out, sc.Err()
}

// StdlibSmokePackages is the -short subset.
var StdlibSmokePackages = []string{
	"errors",
	"unicode/utf8",
	"container/ring",
}

// StdlibOptions configures a stdlib package-test run via the wasmgc-go driver.
type StdlibOptions struct {
	ModuleRoot string
	Driver     string
	SkipRoot   string // testdata/gotest/stdlib
	Timeout    time.Duration
	Verbose    bool
	Short      bool
	Run        string
}

// RunStdlibPackage invokes `wasmgc-go test` for one import path.
func RunStdlibPackage(opts StdlibOptions, importPath string) StdlibResult {
	args := []string{"test"}
	if opts.Verbose {
		args = append(args, "-v")
	}
	if opts.Short {
		args = append(args, "-short")
	}
	if opts.Run != "" {
		args = append(args, "-run", opts.Run)
	}
	if opts.SkipRoot != "" {
		args = append(args, "-skipdir", opts.SkipRoot)
	}
	args = append(args, importPath)

	cmd := exec.Command(opts.Driver, args...)
	cmd.Dir = opts.ModuleRoot
	var buf bytes.Buffer
	cmd.Stdout = &buf
	cmd.Stderr = &buf
	err := runWithTimeout(cmd, opts.Timeout)
	out := buf.String()
	res := StdlibResult{ImportPath: importPath, Output: out, Err: err}
	skipPath := filepath.Join(opts.SkipRoot, filepath.FromSlash(importPath), "skip.txt")
	if entries, e := ParseSkipFile(skipPath); e == nil {
		res.SkipTests = len(entries)
	}
	trim := strings.TrimSpace(out)
	if strings.Contains(out, "\nok\t"+importPath) || strings.HasPrefix(trim, "ok\t"+importPath) ||
		strings.Contains(out, "ok\t"+importPath+"\n") {
		res.Kind = Pass
		return res
	}
	if err != nil || strings.Contains(out, "FAIL\t"+importPath) {
		res.Kind = Fail
		res.Reason = CategorizeFailure(out)
		if res.Reason == "" || res.Reason == "failed" {
			res.Reason = clipReason(out)
		}
		return res
	}
	res.Kind = Fail
	res.Reason = "no ok line"
	return res
}

// FormatStdlibSkipPath returns testdata/gotest/stdlib/<importpath>/skip.txt.
func FormatStdlibSkipPath(skipRoot, importPath string) string {
	return filepath.Join(skipRoot, filepath.FromSlash(importPath), "skip.txt")
}

// EnsureStdlibSkipFile creates an empty commented skip.txt if missing.
func EnsureStdlibSkipFile(path, importPath string) error {
	if _, err := os.Stat(path); err == nil {
		return nil
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	body := fmt.Sprintf("# %s — per-test skips for wasmgc-go test (format: <TestName> — <reason>)\n", importPath)
	return os.WriteFile(path, []byte(body), 0o644)
}
