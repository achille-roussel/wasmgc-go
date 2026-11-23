package ir_test

import (
	"flag"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/achille-roussel/wasmgc-go/internal/ir"
)

var update = flag.Bool("update", false, "update golden .ir files to match printer output")

func TestGoldenFiles(t *testing.T) {
	dir := "testdata"
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".ir") {
			continue
		}
		name := e.Name()
		t.Run(name, func(t *testing.T) {
			path := filepath.Join(dir, name)
			src, err := os.ReadFile(path)
			if err != nil {
				t.Fatal(err)
			}
			m, err := ir.Parse(path, string(src))
			if err != nil {
				t.Fatalf("parse: %v", err)
			}
			if err := ir.Verify(m); err != nil {
				t.Fatalf("verify: %v", err)
			}
			got := ir.Format(m)
			if *update {
				if err := os.WriteFile(path, []byte(got), 0o644); err != nil {
					t.Fatal(err)
				}
				return
			}
			want := string(src)
			// Normalize trailing newline.
			if !strings.HasSuffix(want, "\n") {
				want += "\n"
			}
			if !strings.HasSuffix(got, "\n") {
				got += "\n"
			}
			if got != want {
				t.Errorf("printer output differs from golden (re-run with -update to refresh)\n--- got\n%s--- want\n%s", got, want)
			}
			// Round-trip: parse(print(x)) equals x.
			m2, err := ir.Parse(path, got)
			if err != nil {
				t.Fatalf("re-parse: %v", err)
			}
			if !ir.Equal(m, m2) {
				t.Errorf("parse(print(x)) != x\nprint1:\n%s\nprint2:\n%s", got, ir.Format(m2))
			}
		})
	}
}
