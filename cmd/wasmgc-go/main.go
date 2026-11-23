// Command wasmgc-go is the driver. Stream H owns `std check` and materialize.
package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"text/tabwriter"

	"github.com/achille-roussel/wasmgc-go/internal/check"
	"github.com/achille-roussel/wasmgc-go/internal/load"
	"github.com/achille-roussel/wasmgc-go/internal/stdlib"
)

func main() {
	if len(os.Args) < 2 {
		usage()
		os.Exit(2)
	}
	switch os.Args[1] {
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
  std check       type-check and unsafe-check manifest-supported packages
  std materialize build the cache GOROOT for the pin
  std inventory   scan packages for unsafe/finalizer/dynamic-reflect sites
`)
}

func cmdStd(args []string) int {
	if len(args) < 1 {
		usage()
		return 2
	}
	switch args[0] {
	case "check":
		return cmdStdCheck(args[1:])
	case "materialize":
		return cmdStdMaterialize(args[1:])
	case "inventory":
		return cmdStdInventory(args[1:])
	default:
		fmt.Fprintf(os.Stderr, "wasmgc-go std: unknown subcommand %q\n", args[0])
		return 2
	}
}

func modulePaths() (stdlib.Paths, error) {
	wd, err := os.Getwd()
	if err != nil {
		return stdlib.Paths{}, err
	}
	root, err := stdlib.FindModuleRoot(wd)
	if err != nil {
		return stdlib.Paths{}, err
	}
	return stdlib.DefaultPaths(root)
}

func cmdStdMaterialize(args []string) int {
	fs := flag.NewFlagSet("std materialize", flag.ExitOnError)
	_ = fs.Parse(args)
	p, err := modulePaths()
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		return 1
	}
	m, err := stdlib.Materialize(p)
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		return 1
	}
	fmt.Printf("materialized GOROOT %s (pin %s)\n", m.GOROOT, m.Pin.Version)
	return 0
}

func cmdStdCheck(args []string) int {
	fs := flag.NewFlagSet("std check", flag.ExitOnError)
	packages := fs.String("p", "", "comma-separated package list (default: manifest supported set)")
	jsonOut := fs.Bool("json", false, "emit JSON results")
	_ = fs.Parse(args)

	p, err := modulePaths()
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		return 1
	}
	mat, err := stdlib.Materialize(p)
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		return 1
	}

	var list []string
	if *packages != "" {
		list = strings.Split(*packages, ",")
	} else {
		list = mat.Manifest.Supported()
		sort.Strings(list)
	}

	cfg := &check.Config{
		GOROOT:    mat.GOROOT,
		Filter:    load.DefaultFilter(),
		GoVersion: "go1.27",
	}

	type row struct {
		Path   string   `json:"path"`
		Tier   string   `json:"tier"`
		OK     bool     `json:"ok"`
		Errors []string `json:"errors,omitempty"`
	}
	var rows []row
	fail := 0
	for _, path := range list {
		entry := mat.Manifest.Lookup(path)
		if entry.Tier == stdlib.TierOut {
			continue
		}
		res := cfg.CheckPackage(path)
		r := row{Path: path, Tier: string(entry.Tier), OK: res.OK}
		for _, e := range res.TypeErrors {
			r.Errors = append(r.Errors, e.Error())
		}
		for _, d := range res.Unsafe {
			r.Errors = append(r.Errors, d.String())
		}
		if !res.OK {
			fail++
		}
		rows = append(rows, r)
	}

	if *jsonOut {
		enc := json.NewEncoder(os.Stdout)
		enc.SetIndent("", "  ")
		_ = enc.Encode(rows)
	} else {
		w := tabwriter.NewWriter(os.Stdout, 0, 4, 2, ' ', 0)
		fmt.Fprintf(w, "PACKAGE\tTIER\tSTATUS\n")
		for _, r := range rows {
			status := "ok"
			if !r.OK {
				status = "FAIL"
			}
			fmt.Fprintf(w, "%s\t%s\t%s\n", r.Path, r.Tier, status)
		}
		_ = w.Flush()
		for _, r := range rows {
			if r.OK {
				continue
			}
			fmt.Fprintf(os.Stderr, "\n# %s\n", r.Path)
			for _, e := range r.Errors {
				fmt.Fprintln(os.Stderr, e)
			}
		}
		fmt.Printf("\n%d packages, %d failed\n", len(rows), fail)
	}
	if fail > 0 {
		return 1
	}
	return 0
}

func cmdStdInventory(args []string) int {
	fs := flag.NewFlagSet("std inventory", flag.ExitOnError)
	outPath := fs.String("o", "", "write report to this path (default docs/stdlib/inventory.json)")
	_ = fs.Parse(args)

	p, err := modulePaths()
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		return 1
	}
	mat, err := stdlib.Materialize(p)
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		return 1
	}
	report, err := stdlib.Inventory(mat, p)
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		return 1
	}
	dest := *outPath
	if dest == "" {
		dest = filepath.Join(p.ModuleRoot, "docs", "stdlib", "inventory.json")
	}
	if err := os.MkdirAll(filepath.Dir(dest), 0o755); err != nil {
		fmt.Fprintln(os.Stderr, err)
		return 1
	}
	data, err := json.MarshalIndent(report, "", "  ")
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		return 1
	}
	data = append(data, '\n')
	if err := os.WriteFile(dest, data, 0o644); err != nil {
		fmt.Fprintln(os.Stderr, err)
		return 1
	}
	fmt.Printf("wrote %s (%d packages)\n", dest, len(report.Packages))
	return 0
}
