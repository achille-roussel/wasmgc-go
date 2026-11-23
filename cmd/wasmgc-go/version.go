package main

import (
	"flag"
	"fmt"
	"runtime"
	"runtime/debug"

	"github.com/achille-roussel/wasmgc-go/internal/stdlib"
)

func cmdVersion(args []string) int {
	fs := flag.NewFlagSet("version", flag.ExitOnError)
	_ = fs.Parse(args)

	fmt.Printf("wasmgc-go devel\n")
	fmt.Printf("go version %s %s/%s\n", runtime.Version(), runtime.GOOS, runtime.GOARCH)
	if p, err := modulePaths(); err == nil {
		if pin, err := stdlib.LoadPIN(p.PIN); err == nil {
			fmt.Printf("stdlib pin %s\n", pin.Version)
		}
	}
	if bi, ok := debug.ReadBuildInfo(); ok {
		fmt.Printf("module %s\n", bi.Main.Path)
	}
	return 0
}
