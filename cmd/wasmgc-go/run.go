package main

import (
	"flag"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"

	"github.com/achille-roussel/wasmgc-go/internal/pipeline"
)

func cmdRun(args []string) int {
	var progArgs []string
	dash := -1
	for i, a := range args {
		if a == "--" {
			dash = i
			break
		}
	}
	flagArgs := args
	if dash >= 0 {
		flagArgs = args[:dash]
		progArgs = args[dash+1:]
	}

	fs := flag.NewFlagSet("run", flag.ExitOnError)
	tags := fs.String("tags", "", "comma-separated build tags")
	vetFail := fs.Bool("vet-fail", false, "treat int64→int conversion warnings as errors")
	goroot := fs.String("goroot", "", "override materialized GOROOT")
	skipStd := fs.Bool("skip-stdlib", false, "do not materialize GOROOT")
	_ = fs.Parse(flagArgs)

	pkg := packageArg(fs)
	res, err := pipeline.Compile(pipeline.Options{
		Package:           pkg,
		Tags:              splitTags(*tags),
		Names:             true, // run leaves names on
		VetFail:           *vetFail,
		GOROOT:            *goroot,
		SkipStdlib:        *skipStd,
		SynthPrintRuntime: false, // std/src/runtime linked via rtlink
	})
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		return 1
	}
	printWarnings(res.Warnings)

	dir, err := os.MkdirTemp("", "wasmgc-run-*")
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		return 1
	}
	defer os.RemoveAll(dir)
	wasmPath := filepath.Join(dir, "prog.wasm")
	if err := os.WriteFile(wasmPath, res.Wasm, 0o644); err != nil {
		fmt.Fprintln(os.Stderr, err)
		return 1
	}

	glue, err := findWasmgcJS()
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		return 1
	}
	nodeArgs := []string{glue, wasmPath}
	if len(progArgs) > 0 {
		nodeArgs = append(nodeArgs, "--")
		nodeArgs = append(nodeArgs, progArgs...)
	}
	cmd := exec.Command("node", nodeArgs...)
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	cmd.Stdin = os.Stdin
	if err := cmd.Run(); err != nil {
		if ee, ok := err.(*exec.ExitError); ok {
			return ee.ExitCode()
		}
		fmt.Fprintln(os.Stderr, err)
		return 1
	}
	return 0
}
