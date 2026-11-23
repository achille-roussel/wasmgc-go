package main

import (
	"flag"
	"fmt"
	"os"

	"github.com/achille-roussel/wasmgc-go/internal/pipeline"
)

func cmdDumpIR(args []string) int {
	fs := flag.NewFlagSet("dump-ir", flag.ExitOnError)
	out := fs.String("o", "", "write IR text to this path (default stdout)")
	tags := fs.String("tags", "", "comma-separated build tags")
	vetFail := fs.Bool("vet-fail", false, "treat int64→int conversion warnings as errors")
	goroot := fs.String("goroot", "", "override materialized GOROOT")
	skipStd := fs.Bool("skip-stdlib", false, "do not materialize GOROOT")
	_ = fs.Parse(args)

	pkg := packageArg(fs)
	text, res, err := pipeline.DumpIR(pipeline.Options{
		Package:    pkg,
		Tags:       splitTags(*tags),
		VetFail:    *vetFail,
		GOROOT:     *goroot,
		SkipStdlib: *skipStd,
	})
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		return 1
	}
	if res != nil {
		printWarnings(res.Warnings)
	}
	if *out == "" {
		fmt.Print(text)
		return 0
	}
	if err := os.WriteFile(*out, []byte(text), 0o644); err != nil {
		fmt.Fprintln(os.Stderr, err)
		return 1
	}
	return 0
}
