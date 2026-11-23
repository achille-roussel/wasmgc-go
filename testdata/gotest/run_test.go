package gotest_test

import (
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/achille-roussel/wasmgc-go/internal/stdlib"
	"github.com/achille-roussel/wasmgc-go/testdata/gotest"
)

var updateSkip = flag.Bool("update-skip", false, "append failing // run tests to skip.txt (requires WASMGC_UPDATE_SKIP=1)")

func TestMain(m *testing.M) {
	flag.Parse()
	os.Exit(m.Run())
}

func repoRoot(t *testing.T) string {
	t.Helper()
	root, err := stdlib.CompilerModuleRoot()
	if err != nil {
		// CompilerModuleRoot walks from internal/stdlib; also try from this file.
		wd, err2 := os.Getwd()
		if err2 != nil {
			t.Fatal(err)
		}
		root, err2 = stdlib.FindModuleRoot(wd)
		if err2 != nil {
			t.Fatalf("module root: %v / %v", err, err2)
		}
	}
	return root
}

func gotestDir(t *testing.T) string {
	t.Helper()
	return filepath.Join(repoRoot(t), "testdata", "gotest")
}

func TestSkipListFormat(t *testing.T) {
	path := filepath.Join(gotestDir(t), "skip.txt")
	entries, err := gotest.ParseSkipFile(path)
	if err != nil {
		t.Fatal(err)
	}
	for _, e := range entries {
		if e.Name == "" || e.Reason == "" {
			t.Fatalf("invalid skip entry: %+v", e)
		}
		if !strings.Contains(e.Raw, "—") && !strings.Contains(e.Raw, " --- ") {
			t.Fatalf("skip missing em dash: %q", e.Raw)
		}
	}
}

func TestSkipListGrowth(t *testing.T) {
	root := repoRoot(t)
	dir := gotestDir(t)
	skipRel := filepath.ToSlash(filepath.Join("testdata", "gotest", "skip.txt"))
	current, err := gotest.ParseSkipFile(filepath.Join(dir, "skip.txt"))
	if err != nil {
		t.Fatal(err)
	}
	base, err := gotest.LoadBaseSkipEntries(root, skipRel)
	if err != nil {
		t.Fatal(err)
	}
	added := gotest.SkipGrowth(base, current)
	if len(added) == 0 {
		return
	}
	justification, hasBump, err := gotest.ReadSkipBump(dir)
	if err != nil {
		t.Fatal(err)
	}
	if !hasBump {
		t.Fatalf("skip.txt grew by %d entries relative to base (%s); add testdata/gotest/skip_bump.txt with a one-line justification, or remove the new skips:\n  %s",
			len(added), envOr("WASMGC_SKIP_BASE", "origin/main"), strings.Join(added, "\n  "))
	}
	t.Logf("skip list growth allowed by skip_bump.txt: %s (%d new)", justification, len(added))
}

func envOr(k, def string) string {
	if v := os.Getenv(k); v != "" {
		return v
	}
	return def
}

func TestGOROOT(t *testing.T) {
	if err := gotest.EnsureJSPI("node"); err != nil {
		t.Fatal(err)
	}
	testRoot, err := gotest.FindTestRoot()
	if err != nil {
		t.Fatal(err)
	}
	t.Logf("test root: %s", testRoot)

	root := repoRoot(t)
	glue, err := gotest.ResolveGlue(root)
	if err != nil {
		t.Fatal(err)
	}
	driver := filepath.Join(t.TempDir(), "wasmgc-go")
	if err := gotest.BuildDriver(root, driver); err != nil {
		t.Fatal(err)
	}

	skipPath := filepath.Join(gotestDir(t), "skip.txt")
	skipEntries, err := gotest.ParseSkipFile(skipPath)
	if err != nil {
		t.Fatal(err)
	}
	skipMap := gotest.SkipMap(skipEntries)

	cases, err := gotest.DiscoverCases(testRoot)
	if err != nil {
		t.Fatal(err)
	}

	full := os.Getenv("WASMGC_GOTEST") == "1" || !testing.Short()
	if !full {
		cases = gotest.FilterSmoke(cases)
		t.Logf("short mode: %d smoke tests", len(cases))
	} else {
		t.Logf("full suite: %d files", len(cases))
	}

	opts := gotest.Options{
		TestRoot:   testRoot,
		ModuleRoot: root,
		GlueJS:     glue,
		Node:       "node",
		Driver:     driver,
		Skip:       skipMap,
		Timeout:    30 * time.Second,
	}

	// Materialize once when running more than smoke (imports need GOROOT).
	if full {
		goroot, err := gotest.MaterializeGOROOT(root)
		if err != nil {
			t.Logf("materialize GOROOT: %v (continuing; no-import tests still run)", err)
		} else {
			opts.MaterializedGOROOT = goroot
			t.Logf("materialized GOROOT: %s", goroot)
		}
	}

	doUpdate := *updateSkip && os.Getenv("WASMGC_UPDATE_SKIP") == "1"
	if *updateSkip && !doUpdate {
		t.Fatal("-update-skip requires WASMGC_UPDATE_SKIP=1")
	}
	if doUpdate && testing.Short() && os.Getenv("WASMGC_GOTEST") != "1" {
		t.Fatal("-update-skip refused under -short; use -short=false or WASMGC_GOTEST=1")
	}

	// When updating, clear the in-memory skip map so failures are observed.
	if doUpdate {
		opts.Skip = map[string]string{}
	}

	var pass, fail, skipListed, skipAction, skipConstraint atomic.Int64
	var skipMu sync.Mutex
	var newSkips []gotest.SkipEntry
	for _, c := range cases {
		c := c
		name := c.Rel
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			r := gotest.RunCase(opts, c)
			switch r.Kind {
			case gotest.Pass:
				pass.Add(1)
			case gotest.Fail:
				fail.Add(1)
				if doUpdate {
					reason := r.Reason
					if reason == "" {
						reason = gotest.CategorizeFailure(fmt.Sprint(r.Err))
					}
					skipMu.Lock()
					newSkips = append(newSkips, gotest.SkipEntry{Name: c.Rel, Reason: reason})
					skipMu.Unlock()
					t.Logf("will skip: %s — %s", c.Rel, reason)
				} else {
					t.Errorf("%s: %v\nstdout=%q\nstderr=%q", r.Reason, r.Err, r.Stdout, r.Stderr)
				}
			case gotest.SkipListed:
				skipListed.Add(1)
				t.Skip(r.Reason)
			case gotest.SkipAction:
				skipAction.Add(1)
				t.Skip(r.Reason)
			case gotest.SkipConstraint:
				skipConstraint.Add(1)
				t.Skip(r.Reason)
			}
		})
	}

	t.Cleanup(func() {
		// Runs after all Parallel subtests finish.
		t.Logf("gotest summary: pass=%d fail=%d skip_listed=%d skip_action=%d skip_constraint=%d",
			pass.Load(), fail.Load(), skipListed.Load(), skipAction.Load(), skipConstraint.Load())
		if doUpdate {
			skipMu.Lock()
			adds := append([]gotest.SkipEntry(nil), newSkips...)
			skipMu.Unlock()
			if len(adds) > 0 {
				if err := gotest.MergeSkipFile(skipPath, adds); err != nil {
					t.Errorf("merge skip.txt: %v", err)
					return
				}
				t.Logf("updated skip.txt with %d entries", len(adds))
			}
		}
	})
}
