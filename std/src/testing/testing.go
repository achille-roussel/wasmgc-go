// Package testing is a wasmgc rewrite for running stdlib package tests (M10).
// Minimal T/M/MainStart for wasmgc-go test under Node/JSPI — no testdeps/fuzz.
package testing

import (
	"fmt"
	"os"
)

// InternalTest is registered by generated _testmain.go.
type InternalTest struct {
	Name string
	F    func(*T)
}

// InternalBenchmark is accepted by MainStart; v1 leaves the table empty.
type InternalBenchmark struct {
	Name string
	F    func(*B)
}

// InternalFuzzTarget is accepted by MainStart; v1 leaves the table empty.
type InternalFuzzTarget struct {
	Name string
	F    func(*F)
}

// InternalExample is accepted by MainStart; v1 leaves the table empty.
type InternalExample struct {
	Name      string
	F         func()
	Output    string
	Unordered bool
}

// B is a stub benchmark harness (not executed in v1).
type B struct{}

// F is a stub fuzz harness (not executed in v1).
type F struct{}

// T is the test state passed to TestXxx.
type T struct {
	name     string
	failed   bool
	skipped  bool
	chatty   bool
	matchPat string
}

// M drives the suite.
type M struct {
	tests    []InternalTest
	exitCode int
}

// Package-level bools only — assigning os.Args strings to package-level
// string vars triggers a wasmgc Wasm null-ref global init bug.
var (
	verbose     bool
	shortFlag   bool
	flagsParsed bool
)

// MainStart builds an M for generated test mains (no testdeps).
func MainStart(tests []InternalTest, benchmarks []InternalBenchmark, fuzzTargets []InternalFuzzTarget, examples []InternalExample) *M {
	_ = benchmarks
	_ = fuzzTargets
	_ = examples
	return &M{tests: tests}
}

// Main is the legacy entry point used by some $GOROOT/test programs.
// It runs the provided tests via MainStart. On success it returns so main can
// fall through (syscall.Exit is still a stub panic); on failure it calls
// os.Exit which may panic under wasmgc.
func Main(matchString func(pat, str string) (bool, error), tests []InternalTest, benchmarks []InternalBenchmark, examples []InternalExample) {
	_ = matchString
	m := MainStart(tests, benchmarks, nil, examples)
	if code := m.Run(); code != 0 {
		os.Exit(code)
	}
}

// Run executes matching tests and returns a process exit code.
func (m *M) Run() int {
	matchPat := parseTestFlags()
	failed := false
	for _, tc := range m.tests {
		if matchPat != "" && !nameMatch(tc.Name, matchPat) {
			continue
		}
		t := &T{name: tc.Name, chatty: verbose, matchPat: matchPat}
		if verbose {
			fmt.Fprintf(os.Stdout, "=== RUN   %s\n", t.name)
		}
		tc.F(t)
		if t.skipped {
			if verbose {
				fmt.Fprintf(os.Stdout, "--- SKIP: %s\n", t.name)
			}
			continue
		}
		if t.failed {
			failed = true
			fmt.Fprintf(os.Stdout, "--- FAIL: %s\n", t.name)
			continue
		}
		if verbose {
			fmt.Fprintf(os.Stdout, "--- PASS: %s\n", t.name)
		}
	}
	if failed {
		m.exitCode = 1
		fmt.Fprintln(os.Stdout, "FAIL")
	} else {
		m.exitCode = 0
		fmt.Fprintln(os.Stdout, "PASS")
	}
	return m.exitCode
}

// parseTestFlags reads -test.v / -test.short / -test.run from os.Args.
// Returns the -test.run pattern. Must not store Args strings in package vars.
func parseTestFlags() string {
	matchPat := ""
	if !flagsParsed {
		flagsParsed = true
		args := os.Args
		// wasmgc os.Args is only args after "--" (no argv[0] program name).
		for i := 0; i < len(args); i++ {
			a := args[i]
			switch a {
			case "-test.v", "-test.v=true":
				verbose = true
			case "-test.v=false":
				verbose = false
			case "-test.short", "-test.short=true":
				shortFlag = true
			case "-test.short=false":
				shortFlag = false
			case "-test.run":
				if i+1 < len(args) {
					i++
					matchPat = args[i]
				}
			}
		}
		return matchPat
	}
	// Second call (e.g. Short): re-scan for pattern only into a local.
	args := os.Args
	for i := 0; i < len(args); i++ {
		if args[i] == "-test.run" && i+1 < len(args) {
			return args[i+1]
		}
	}
	return ""
}

func nameMatch(name, pat string) bool {
	if pat == "" {
		return true
	}
	if name == pat {
		return true
	}
	return contains(name, pat)
}

func contains(s, sub string) bool {
	n, m := len(s), len(sub)
	if m == 0 {
		return true
	}
	if m > n {
		return false
	}
	for i := 0; i+m <= n; i++ {
		ok := true
		for j := 0; j < m; j++ {
			if s[i+j] != sub[j] {
				ok = false
				break
			}
		}
		if ok {
			return true
		}
	}
	return false
}

func (t *T) Name() string { return t.name }
func (t *T) Failed() bool { return t.failed }
func (t *T) Helper()      {}
func (t *T) Parallel()    {}
func (t *T) Fail()        { t.failed = true }
func (t *T) FailNow()     { t.failed = true }
func (t *T) Error(args ...any) {
	fmt.Fprint(os.Stdout, fmt.Sprintln(args...))
	t.Fail()
}
func (t *T) Errorf(format string, args ...any) {
	fmt.Fprintf(os.Stdout, format+"\n", args...)
	t.Fail()
}
func (t *T) Fatal(args ...any) {
	t.Error(args...)
	t.FailNow()
}
func (t *T) Fatalf(format string, args ...any) {
	t.Errorf(format, args...)
	t.FailNow()
}
func (t *T) Log(args ...any) {
	if t.chatty || t.failed {
		fmt.Fprint(os.Stdout, fmt.Sprintln(args...))
	}
}
func (t *T) Logf(format string, args ...any) {
	if t.chatty || t.failed {
		fmt.Fprintf(os.Stdout, format+"\n", args...)
	}
}
func (t *T) Skip(args ...any) {
	fmt.Fprint(os.Stdout, fmt.Sprintln(args...))
	t.skipped = true
}
func (t *T) Skipf(format string, args ...any) {
	fmt.Fprintf(os.Stdout, format+"\n", args...)
	t.skipped = true
}
func (t *T) SkipNow()                 { t.skipped = true }
func (t *T) Cleanup(f func())         { _ = f }
func (t *T) TempDir() string          { t.Skip("TempDir not supported on wasmgc"); return "" }
func (t *T) Setenv(key, value string) { _, _ = key, value }

// Run runs a subtest.
func (t *T) Run(name string, f func(t *T)) bool {
	full := t.name + "/" + name
	if t.matchPat != "" && !nameMatch(full, t.matchPat) && !nameMatch(name, t.matchPat) {
		return true
	}
	sub := &T{name: full, chatty: t.chatty, matchPat: t.matchPat}
	if t.chatty {
		fmt.Fprintf(os.Stdout, "=== RUN   %s\n", full)
	}
	f(sub)
	if sub.skipped {
		if t.chatty {
			fmt.Fprintf(os.Stdout, "--- SKIP: %s\n", full)
		}
		return true
	}
	if sub.failed {
		t.failed = true
		fmt.Fprintf(os.Stdout, "--- FAIL: %s\n", full)
		return false
	}
	if t.chatty {
		fmt.Fprintf(os.Stdout, "--- PASS: %s\n", full)
	}
	return true
}

// Short reports whether -test.short was set.
func Short() bool { parseTestFlags(); return shortFlag }

// Verbose reports whether -test.v was set.
func Verbose() bool { parseTestFlags(); return verbose }

// CoverMode is always empty in the wasmgc rewrite.
func CoverMode() string { return "" }

// AllocsPerRun returns 0; allocation accounting is not available.
func AllocsPerRun(n int, f func()) float64 {
	if n <= 0 {
		n = 1
	}
	for i := 0; i < n; i++ {
		f()
	}
	return 0
}

// Init is a no-op retained for API familiarity.
func Init() {}
