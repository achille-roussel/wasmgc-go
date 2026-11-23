package gotest_test

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/achille-roussel/wasmgc-go/testdata/gotest"
)

// TestStdlibPackages runs wasmgc-go test on packages.txt.
//
//	go test ./testdata/gotest/ -run TestStdlibPackages -count=1 -short
//
// Full suite (all packages.txt entries):
//
//	WASMGC_STDLIB_TEST=1 go test ./testdata/gotest/ -run TestStdlibPackages -count=1
//	# or: go test ./testdata/gotest/ -run TestStdlibPackages -count=1 -short=false
func TestStdlibPackages(t *testing.T) {
	if err := gotest.EnsureJSPI("node"); err != nil {
		t.Fatal(err)
	}
	root := repoRoot(t)
	dir := gotestDir(t)
	glue, err := gotest.ResolveGlue(root)
	if err != nil {
		t.Fatal(err)
	}
	_ = glue

	driver := filepath.Join(t.TempDir(), "wasmgc-go")
	if err := gotest.BuildDriver(root, driver); err != nil {
		t.Fatal(err)
	}

	pkgs, err := gotest.ReadPackagesTxt(filepath.Join(dir, "packages.txt"))
	if err != nil {
		t.Fatal(err)
	}
	full := os.Getenv("WASMGC_STDLIB_TEST") == "1" || !testing.Short()
	if !full {
		want := map[string]bool{}
		for _, p := range gotest.StdlibSmokePackages {
			want[p] = true
		}
		var filtered []gotest.StdlibPackage
		for _, p := range pkgs {
			if want[p.ImportPath] {
				filtered = append(filtered, p)
			}
		}
		pkgs = filtered
		t.Logf("short mode: %d smoke packages", len(pkgs))
	} else {
		t.Logf("full stdlib suite: %d packages", len(pkgs))
	}

	skipRoot := filepath.Join(dir, "stdlib")
	opts := gotest.StdlibOptions{
		ModuleRoot: root,
		Driver:     driver,
		SkipRoot:   skipRoot,
		Timeout:    180 * time.Second,
		Verbose:    testing.Verbose(),
		Short:      testing.Short(),
	}

	var pass, fail int
	for _, p := range pkgs {
		p := p
		t.Run(p.ImportPath, func(t *testing.T) {
			if err := gotest.EnsureStdlibSkipFile(
				gotest.FormatStdlibSkipPath(skipRoot, p.ImportPath), p.ImportPath); err != nil {
				t.Fatal(err)
			}
			r := gotest.RunStdlibPackage(opts, p.ImportPath)
			switch r.Kind {
			case gotest.Pass:
				pass++
				t.Logf("ok (listed skips≈%d)", r.SkipTests)
			default:
				fail++
				t.Errorf("%s: %s\n%s", r.Reason, r.Err, r.Output)
			}
		})
	}
	t.Cleanup(func() {
		t.Logf("stdlib summary: pass=%d fail=%d", pass, fail)
	})
}

func TestStdlibSkipListFormat(t *testing.T) {
	dir := filepath.Join(gotestDir(t), "stdlib")
	err := filepath.Walk(dir, func(path string, info os.FileInfo, err error) error {
		if err != nil || info.IsDir() || info.Name() != "skip.txt" {
			return err
		}
		entries, err := gotest.ParseSkipFile(path)
		if err != nil {
			return err
		}
		for _, e := range entries {
			if e.Name == "" || e.Reason == "" {
				t.Errorf("%s: invalid skip %+v", path, e)
			}
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
}
