package main

import (
	"bytes"
	"flag"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"github.com/achille-roussel/wasmgc-go/internal/pipeline"
	"github.com/achille-roussel/wasmgc-go/internal/stdlib"
	"github.com/achille-roussel/wasmgc-go/testdata/gotest"
)

func cmdTest(args []string) int {
	fs := flag.NewFlagSet("test", flag.ExitOnError)
	tags := fs.String("tags", "", "comma-separated build tags")
	run := fs.String("run", "", "run only tests matching this pattern (-test.run)")
	verbose := fs.Bool("v", false, "verbose test output (-test.v)")
	short := fs.Bool("short", false, "tell tests to run shorter suites (-test.short)")
	goroot := fs.String("goroot", "", "override materialized GOROOT")
	timeout := fs.Duration("timeout", 120*time.Second, "per-package run timeout")
	skipDir := fs.String("skipdir", "", "directory of per-package skip.txt trees (default: testdata/gotest/stdlib)")
	_ = fs.Parse(args)

	pkgs := fs.Args()
	if len(pkgs) == 0 {
		fmt.Fprintln(os.Stderr, "wasmgc-go test: missing package import path")
		return 2
	}

	root, err := moduleRoot()
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		return 1
	}
	skipRoot := *skipDir
	if skipRoot == "" {
		skipRoot = filepath.Join(root, "testdata", "gotest", "stdlib")
	}

	glue, err := findWasmgcJS()
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		return 1
	}
	if err := gotest.EnsureJSPI("node"); err != nil {
		fmt.Fprintln(os.Stderr, err)
		return 1
	}

	matGOROOT := *goroot
	if matGOROOT == "" {
		p, err := stdlib.DefaultPaths(root)
		if err != nil {
			fmt.Fprintln(os.Stderr, err)
			return 1
		}
		m, err := stdlib.Materialize(p)
		if err != nil {
			fmt.Fprintln(os.Stderr, err)
			return 1
		}
		matGOROOT = m.GOROOT
	}

	failed := 0
	for _, pkg := range pkgs {
		pkg = strings.TrimSpace(pkg)
		if pkg == "" || strings.HasPrefix(pkg, "#") {
			continue
		}
		code := runPackageTest(packageTestOpts{
			Root:       root,
			ImportPath: pkg,
			GOROOT:     matGOROOT,
			SkipRoot:   skipRoot,
			Glue:       glue,
			Tags:       splitTags(*tags),
			Run:        *run,
			Verbose:    *verbose,
			Short:      *short,
			Timeout:    *timeout,
		})
		if code != 0 {
			failed = 1
		}
	}
	return failed
}

type packageTestOpts struct {
	Root       string
	ImportPath string
	GOROOT     string
	SkipRoot   string
	Glue       string
	Tags       []string
	Run        string
	Verbose    bool
	Short      bool
	Timeout    time.Duration
}

func runPackageTest(opts packageTestOpts) int {
	skipPath := filepath.Join(opts.SkipRoot, filepath.FromSlash(opts.ImportPath), "skip.txt")
	skipMap := map[string]string{}
	if entries, err := gotest.ParseSkipFile(skipPath); err == nil {
		skipMap = gotest.SkipMap(entries)
	} else if !os.IsNotExist(err) {
		fmt.Fprintf(os.Stderr, "wasmgc-go test %s: skip list: %v\n", opts.ImportPath, err)
		return 1
	}

	work, err := os.MkdirTemp("", "wasmgc-test-*")
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		return 1
	}
	defer os.RemoveAll(work)

	res, tp, err := pipeline.CompileTest(pipeline.TestOptions{
		ImportPath: opts.ImportPath,
		Tags:       opts.Tags,
		Names:      true,
		GOROOT:     opts.GOROOT,
		Skip:       skipMap,
		ModuleRoot: opts.Root,
		WorkDir:    filepath.Join(work, "src"),
	})
	if err != nil {
		fmt.Fprintf(os.Stderr, "FAIL\t%s\t[build failed: %v]\n", opts.ImportPath, err)
		return 1
	}
	_ = tp
	printWarnings(res.Warnings)

	wasmPath := filepath.Join(work, "test.wasm")
	if err := os.WriteFile(wasmPath, res.Wasm, 0o644); err != nil {
		fmt.Fprintln(os.Stderr, err)
		return 1
	}

	nodeArgs := []string{opts.Glue, wasmPath, "--"}
	if opts.Verbose {
		nodeArgs = append(nodeArgs, "-test.v")
	}
	if opts.Short {
		nodeArgs = append(nodeArgs, "-test.short")
	}
	if opts.Run != "" {
		nodeArgs = append(nodeArgs, "-test.run", opts.Run)
	}
	cmd := exec.Command("node", nodeArgs...)
	cmd.Env = append(os.Environ(), "WASMGC_JS="+opts.Glue)
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	err = runCmdTimeout(cmd, opts.Timeout)
	out := stdout.String()
	errOut := stderr.String()
	if opts.Verbose {
		os.Stdout.Write(stdout.Bytes())
		os.Stderr.Write(stderr.Bytes())
	}
	// Judge by PASS/FAIL lines: os.Exit is a stub panic on wasmgc, so exit
	// codes from Node are not reliable for the test binary itself.
	pass := strings.Contains(out, "\nPASS\n") || strings.HasSuffix(strings.TrimSpace(out), "PASS")
	fail := strings.Contains(out, "\nFAIL\n") || strings.Contains(out, "--- FAIL:") || strings.HasSuffix(strings.TrimSpace(out), "FAIL")
	if err != nil && !pass {
		if !opts.Verbose {
			fmt.Fprint(os.Stderr, out)
			fmt.Fprint(os.Stderr, errOut)
		}
		if ee, ok := err.(*exec.ExitError); ok {
			fmt.Fprintf(os.Stderr, "FAIL\t%s\t[exit %d]\n", opts.ImportPath, ee.ExitCode())
			return 1
		}
		fmt.Fprintf(os.Stderr, "FAIL\t%s\t[%v]\n", opts.ImportPath, err)
		return 1
	}
	if fail || !pass {
		if !opts.Verbose {
			fmt.Fprint(os.Stderr, out)
			fmt.Fprint(os.Stderr, errOut)
		}
		fmt.Fprintf(os.Stderr, "FAIL\t%s\n", opts.ImportPath)
		return 1
	}
	fmt.Fprintf(os.Stdout, "ok\t%s\n", opts.ImportPath)
	return 0
}

func runCmdTimeout(cmd *exec.Cmd, d time.Duration) error {
	if d <= 0 {
		d = 120 * time.Second
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
		return fmt.Errorf("timeout after %s", d)
	}
}

func moduleRoot() (string, error) {
	wd, err := os.Getwd()
	if err != nil {
		return "", err
	}
	return stdlib.FindModuleRoot(wd)
}
