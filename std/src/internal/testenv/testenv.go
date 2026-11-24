// Package testenv is a wasmgc rewrite of internal/testenv.
// Upstream pulls os/exec, syscall.SysProcAttr, and full OS process APIs.
// Stdlib package tests only need a handful of predicates/skip helpers.
package testenv

import "testing"

// Builder returns the name of the Go builder running this binary.
// Empty under wasmgc (not a builder).
func Builder() string { return "" }

// OptimizationOff reports whether compiler optimizations are disabled.
// Always false under wasmgc.
func OptimizationOff() bool { return false }

// SkipIfOptimizationOff skips t when OptimizationOff is true.
// Takes *testing.T (not testing.TB) to avoid iface call_ref gaps in v1.
func SkipIfOptimizationOff(t *testing.T) {
	if OptimizationOff() {
		t.Skip("skipping test: optimizations are disabled")
	}
}

// MustHaveExec skips t; process execution is unsupported under wasmgc.
func MustHaveExec(t *testing.T) {
	t.Skip("skipping test: os/exec not supported on wasmgc")
}

// MustHaveExecPath skips t; process execution is unsupported under wasmgc.
func MustHaveExecPath(t *testing.T, path string) {
	_ = path
	t.Skip("skipping test: os/exec not supported on wasmgc")
}

// HasGoBuild reports whether the current environment can run 'go build'.
func HasGoBuild() bool { return false }

// MustHaveGoBuild skips t when HasGoBuild is false.
func MustHaveGoBuild(t *testing.T) {
	if !HasGoBuild() {
		t.Skip("skipping test: 'go build' not available on wasmgc")
	}
}

// HasGoRun reports whether 'go run' is available.
func HasGoRun() bool { return false }

// MustHaveGoRun skips t when HasGoRun is false.
func MustHaveGoRun(t *testing.T) {
	if !HasGoRun() {
		t.Skip("skipping test: 'go run' not available on wasmgc")
	}
}

// GOROOT returns a placeholder; wasmgc does not ship a host GOROOT for tests.
func GOROOT(t *testing.T) string {
	t.Helper()
	return ""
}
