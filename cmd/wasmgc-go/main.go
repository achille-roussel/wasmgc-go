// Command wasmgc-go is the driver for the wasmgc-go compiler.
package main

import (
	"fmt"
	"os"
)

func main() {
	if len(os.Args) < 2 {
		usage()
		os.Exit(2)
	}
	switch os.Args[1] {
	case "build":
		os.Exit(cmdBuild(os.Args[2:]))
	case "run":
		os.Exit(cmdRun(os.Args[2:]))
	case "dump-ir":
		os.Exit(cmdDumpIR(os.Args[2:]))
	case "version":
		os.Exit(cmdVersion(os.Args[2:]))
	case "std":
		os.Exit(cmdStd(os.Args[2:]))
	case "help", "-h", "--help":
		usage()
	default:
		fmt.Fprintf(os.Stderr, "wasmgc-go: unknown command %q\n", os.Args[1])
		usage()
		os.Exit(2)
	}
}

func usage() {
	fmt.Fprintf(os.Stderr, `usage: wasmgc-go <command> [arguments]

commands:
  build [-o out] [-target node|browser] [-tags a,b] [-names] [-vet-fail] [package]
  run   [-tags a,b] [-vet-fail] [package] [-- args]
  dump-ir [-o file] [-tags a,b] [package]
  version
  std check       type-check and unsafe-check manifest-supported packages
  std materialize build the cache GOROOT for the pin
  std inventory   scan packages for unsafe/finalizer/dynamic-reflect sites
`)
}
