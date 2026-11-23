package gotest

import (
	"bytes"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"os/exec"
	"path"
	"path/filepath"
	"strings"
	"time"

	"github.com/achille-roussel/wasmgc-go/internal/load"
	"github.com/achille-roussel/wasmgc-go/internal/stdlib"
)

// SuiteDirs matches Go's testdir discovery set (top-level + fixedbugs/ etc.).
var SuiteDirs = []string{
	".", "ken", "chan", "interface", "syntax", "fixedbugs",
	"abi", "typeparam", "typeparam/mdempsky", "arenas", "simd",
}

// SmokeTests is the -short subset: simple no-import // run programs known to pass.
var SmokeTests = []string{
	"helloworld.go",
	"if.go",
	"varinit.go",
	"literal.go",
	"iota.go",
	"initcomma.go",
	"align.go",
	"ken/simpfun.go",
	"ken/simpvar.go",
	"fixedbugs/bug000.go",
}

// Case is one discovered $GOROOT/test file.
type Case struct {
	Rel  string // slash-separated path under test/, e.g. fixedbugs/bug000.go
	Path string // absolute path to the .go file
}

// ResultKind classifies an executed case.
type ResultKind int

const (
	Pass ResultKind = iota
	Fail
	SkipListed
	SkipAction
	SkipConstraint
)

// CaseResult is the outcome of one case.
type CaseResult struct {
	Case   Case
	Kind   ResultKind
	Action string
	Reason string
	Err    error
	Stdout string
	Stderr string
}

// Summary aggregates suite results.
type Summary struct {
	Pass, Fail, SkipListed, SkipAction, SkipConstraint int
	Failed                                             []CaseResult
}

// DiscoverCases walks SuiteDirs for *.go files under testRoot/test.
func DiscoverCases(testRoot string) ([]Case, error) {
	base := filepath.Join(testRoot, "test")
	var out []Case
	for _, d := range SuiteDirs {
		dir := base
		if d != "." {
			dir = filepath.Join(base, filepath.FromSlash(d))
		}
		entries, err := os.ReadDir(dir)
		if err != nil {
			if os.IsNotExist(err) {
				continue
			}
			return nil, err
		}
		for _, e := range entries {
			name := e.Name()
			if e.IsDir() || strings.HasPrefix(name, ".") || !strings.HasSuffix(name, ".go") {
				continue
			}
			rel := name
			if d != "." {
				rel = path.Join(d, name)
			}
			out = append(out, Case{Rel: rel, Path: filepath.Join(dir, name)})
		}
	}
	return out, nil
}

// FilterSmoke keeps only SmokeTests.
func FilterSmoke(cases []Case) []Case {
	want := make(map[string]bool, len(SmokeTests))
	for _, s := range SmokeTests {
		want[s] = true
	}
	var out []Case
	for _, c := range cases {
		if want[c.Rel] {
			out = append(out, c)
		}
	}
	return out
}

// Options configures a suite run.
type Options struct {
	TestRoot   string // Go source root containing test/
	ModuleRoot string // wasmgc-go repo root
	GlueJS     string // lib/wasmgc.js
	Node       string // node binary
	Driver     string // wasmgc-go binary (required; isolates compiler crashes)
	Skip       map[string]string
	Timeout    time.Duration
	// MaterializedGOROOT is the wasmgc cache GOROOT for programs with imports.
	MaterializedGOROOT string
}

// RunCase compiles and executes one // run test (or records auto-skip).
func RunCase(opts Options, c Case) (res CaseResult) {
	defer func() {
		if rec := recover(); rec != nil {
			res = CaseResult{
				Case:   c,
				Kind:   Fail,
				Action: res.Action,
				Reason: CategorizeFailure(fmt.Sprintf("panic: %v", rec)),
				Err:    fmt.Errorf("panic: %v", rec),
			}
		}
	}()

	src, err := os.ReadFile(c.Path)
	if err != nil {
		return CaseResult{Case: c, Kind: Fail, Err: err, Reason: err.Error()}
	}
	action, err := ParseAction(string(src))
	if err != nil {
		return CaseResult{Case: c, Kind: Fail, Err: err, Reason: err.Error()}
	}
	res = CaseResult{Case: c, Action: action.Kind}

	if action.Kind == "skip" {
		res.Kind = SkipAction
		res.Reason = "action skip"
		return res
	}
	if UnsupportedActions[action.Kind] || action.Kind != "run" {
		res.Kind = SkipAction
		res.Reason = "unsupported action: " + action.Kind
		return res
	}

	if reason, ok := opts.Skip[c.Rel]; ok {
		res.Kind = SkipListed
		res.Reason = reason
		return res
	}

	header, _, _ := strings.Cut(string(src), "\npackage")
	if ok, why := shouldTest(header); !ok {
		res.Kind = SkipConstraint
		res.Reason = why
		return res
	}

	stdout, stderr, exitCode, runErr := compileAndRun(opts, c, stripBuildConstraints(string(src)), action)
	res.Stdout = stdout
	res.Stderr = stderr
	if runErr != nil {
		res.Kind = Fail
		res.Err = runErr
		res.Reason = CategorizeFailure(runErr.Error() + "\n" + stderr + "\n" + stdout)
		return res
	}
	wantOut, err := expectedOutput(c)
	if err != nil {
		res.Kind = Fail
		res.Err = err
		res.Reason = err.Error()
		return res
	}
	got := strings.ReplaceAll(stdout, "\r\n", "\n")
	if got != wantOut {
		res.Kind = Fail
		res.Err = fmt.Errorf("output mismatch: got %q", got)
		res.Reason = "output mismatch"
		return res
	}
	if action.WantErr {
		if exitCode == 0 {
			res.Kind = Fail
			res.Err = fmt.Errorf("expected non-zero exit")
			res.Reason = "expected failure (-1) but exited 0"
			return res
		}
	} else if exitCode != 0 {
		res.Kind = Fail
		res.Err = fmt.Errorf("exit %d", exitCode)
		msg := stderr
		if msg == "" {
			msg = stdout
		}
		res.Reason = CategorizeFailure(msg)
		return res
	}
	res.Kind = Pass
	return res
}

func expectedOutput(c Case) (string, error) {
	outPath := strings.TrimSuffix(c.Path, ".go") + ".out"
	b, err := os.ReadFile(outPath)
	if errors.Is(err, fs.ErrNotExist) {
		return "", nil
	}
	if err != nil {
		return "", err
	}
	return string(b), nil
}

// compileAndRun builds via the wasmgc-go driver in a temp module, then runs
// under Node. Subprocess isolation keeps compiler stack overflows from killing
// the suite process.
func compileAndRun(opts Options, c Case, src string, action Action) (stdout, stderr string, exitCode int, err error) {
	if opts.Driver == "" {
		return "", "", 1, fmt.Errorf("gotest: driver binary not set")
	}
	node := opts.Node
	if node == "" {
		node = "node"
	}
	glue := opts.GlueJS
	if glue == "" {
		return "", "", 1, fmt.Errorf("gotest: glue JS not set")
	}

	dir, err := os.MkdirTemp("", "wasmgc-gotest-*")
	if err != nil {
		return "", "", 1, err
	}
	defer os.RemoveAll(dir)

	if err := os.WriteFile(filepath.Join(dir, "main.go"), []byte(src), 0o644); err != nil {
		return "", "", 1, err
	}
	if err := os.WriteFile(filepath.Join(dir, "go.mod"), []byte("module gotestcase\n\ngo 1.27\n"), 0o644); err != nil {
		return "", "", 1, err
	}

	wasmPath := filepath.Join(dir, "prog.wasm")
	buildArgs := []string{"build", "-o", wasmPath}
	needsStd := hasImport(src)
	if !needsStd {
		buildArgs = append(buildArgs, "-skip-stdlib")
	} else if opts.MaterializedGOROOT != "" {
		buildArgs = append(buildArgs, "-goroot", opts.MaterializedGOROOT)
	}
	buildArgs = append(buildArgs, ".")

	build := exec.Command(opts.Driver, buildArgs...)
	build.Dir = dir
	build.Env = append(os.Environ(), "WASMGC_JS="+glue)
	var buildOut bytes.Buffer
	build.Stdout = &buildOut
	build.Stderr = &buildOut
	if err := runWithTimeout(build, opts.Timeout); err != nil {
		msg := strings.TrimSpace(buildOut.String())
		if msg == "" {
			msg = err.Error()
		}
		return "", msg, 1, fmt.Errorf("build: %s", msg)
	}

	runArgs := []string{glue, wasmPath}
	if len(action.Args) > 0 {
		runArgs = append(runArgs, "--")
		runArgs = append(runArgs, action.Args...)
	}
	cmd := exec.Command(node, runArgs...)
	cmd.Env = append(os.Environ(), "WASMGC_JS="+glue)
	var stdoutBuf, stderrBuf bytes.Buffer
	cmd.Stdout = &stdoutBuf
	cmd.Stderr = &stderrBuf
	runErr := runWithTimeout(cmd, opts.Timeout)
	ec := 0
	if runErr != nil {
		if ee, ok := runErr.(*exec.ExitError); ok {
			ec = ee.ExitCode()
			runErr = nil
		} else if errors.Is(runErr, errTimeout) {
			return stdoutBuf.String(), stderrBuf.String() + "\ntimeout", 1, runErr
		} else {
			return stdoutBuf.String(), stderrBuf.String(), 1, runErr
		}
	}
	return stdoutBuf.String(), stderrBuf.String(), ec, nil
}

var errTimeout = errors.New("timeout")

func runWithTimeout(cmd *exec.Cmd, d time.Duration) error {
	if d <= 0 {
		d = 60 * time.Second
	}
	if err := cmd.Start(); err != nil {
		return err
	}
	done := make(chan error, 1)
	go func() { done <- cmd.Wait() }()
	select {
	case err := <-done:
		return err
	case <-time.After(d):
		_ = cmd.Process.Kill()
		<-done
		return errTimeout
	}
}

func hasImport(src string) bool {
	return strings.Contains(src, "\nimport ") ||
		strings.Contains(src, "\nimport\t") ||
		strings.Contains(src, "\nimport(") ||
		strings.Contains(src, "\nimport (")
}

// shouldTest evaluates build constraints under the wasmgc filter (js/wasmgc).
func shouldTest(header string) (bool, string) {
	filter := load.DefaultFilter()
	for _, line := range strings.Split(header, "\n") {
		trim := strings.TrimSpace(line)
		if !strings.HasPrefix(trim, "//go:build") && !strings.HasPrefix(trim, "// +build") {
			continue
		}
		ok, err := filter.EvalTags(trim)
		if err != nil {
			// Malformed constraint: let the compiler report it.
			continue
		}
		if !ok {
			return false, trim
		}
	}
	return true, ""
}

// stripBuildConstraints removes //go:build / // +build lines so the driver
// go list (GOARCH=wasm) does not drop files that the wasmgc filter accepts
// (notably //go:build !wasm). Constraints were already checked by shouldTest.
func stripBuildConstraints(src string) string {
	var b strings.Builder
	for _, line := range strings.Split(src, "\n") {
		trim := strings.TrimSpace(line)
		if strings.HasPrefix(trim, "//go:build") || strings.HasPrefix(trim, "// +build") {
			continue
		}
		b.WriteString(line)
		b.WriteByte('\n')
	}
	return b.String()
}

// EnsureJSPI fails hard when WebAssembly.Suspending is missing.
func EnsureJSPI(node string) error {
	if node == "" {
		node = "node"
	}
	cmd := exec.Command(node, "-e",
		`if(typeof WebAssembly==="undefined"||typeof WebAssembly.Suspending!=="function"||typeof WebAssembly.promising!=="function"){console.error("JSPI missing");process.exit(1)}`)
	out, err := cmd.CombinedOutput()
	if err != nil {
		return fmt.Errorf("JSPI (WebAssembly.Suspending) required on Node 26.13.1+: %v\n%s", err, out)
	}
	return nil
}

// ResolveGlue returns absolute path to lib/wasmgc.js.
func ResolveGlue(moduleRoot string) (string, error) {
	p := filepath.Join(moduleRoot, "lib", "wasmgc.js")
	if _, err := os.Stat(p); err != nil {
		return "", fmt.Errorf("lib/wasmgc.js: %w", err)
	}
	return p, nil
}

// MaterializeGOROOT builds the wasmgc cache GOROOT once for the suite.
func MaterializeGOROOT(moduleRoot string) (string, error) {
	p, err := stdlib.DefaultPaths(moduleRoot)
	if err != nil {
		return "", err
	}
	m, err := stdlib.Materialize(p)
	if err != nil {
		return "", err
	}
	return m.GOROOT, nil
}

// BuildDriver compiles cmd/wasmgc-go to outPath.
func BuildDriver(moduleRoot, outPath string) error {
	cmd := exec.Command("go", "build", "-o", outPath, "./cmd/wasmgc-go")
	cmd.Dir = moduleRoot
	out, err := cmd.CombinedOutput()
	if err != nil {
		return fmt.Errorf("go build driver: %v\n%s", err, out)
	}
	return nil
}

// RunSuite executes cases and returns a summary (sequential helper).
func RunSuite(opts Options, cases []Case) Summary {
	var s Summary
	for _, c := range cases {
		r := RunCase(opts, c)
		switch r.Kind {
		case Pass:
			s.Pass++
		case Fail:
			s.Fail++
			s.Failed = append(s.Failed, r)
		case SkipListed:
			s.SkipListed++
		case SkipAction:
			s.SkipAction++
		case SkipConstraint:
			s.SkipConstraint++
		}
	}
	return s
}
