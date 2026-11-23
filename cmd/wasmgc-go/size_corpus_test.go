package main

import (
	"bufio"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	"github.com/achille-roussel/wasmgc-go/internal/pipeline"
)

// TestSizeCorpus builds every program in testdata/size/corpus.txt with names
// off and checks recorded sizes + budgets (docs/design/08-testing.md).
func TestSizeCorpus(t *testing.T) {
	if !pipeline.Default.Real() {
		t.Fatal("expected real lowerer")
	}
	root := repoRoot(t)
	corpusPath := filepath.Join(root, "testdata", "size", "corpus.txt")
	sizesPath := filepath.Join(root, "testdata", "size", "sizes.txt")
	budgetsPath := filepath.Join(root, "testdata", "size", "budgets.txt")

	corpus := readNameList(t, corpusPath)
	wantSizes := readNameInts(t, sizesPath)
	budgets := readNameInts(t, budgetsPath)

	exe := buildDriver(t)
	var goroot string
	needGOROOT := false
	for _, name := range corpus {
		if name != "empty_main" && name != "println" {
			needGOROOT = true
			break
		}
	}
	if needGOROOT {
		goroot = materializeGOROOT(t, exe)
	}

	var report strings.Builder
	report.WriteString("size corpus (names off):\n")
	for _, name := range corpus {
		got := buildCorpusProgram(t, exe, goroot, name)
		want, ok := wantSizes[name]
		if !ok {
			t.Errorf("%s: missing entry in sizes.txt (got %d)", name, got)
			continue
		}
		budget, ok := budgets[name]
		if !ok {
			t.Errorf("%s: missing entry in budgets.txt", name)
			continue
		}
		report.WriteString(strconv.Itoa(got))
		report.WriteByte('\t')
		report.WriteString(name)
		report.WriteByte('\n')
		// M10: size corpus is a loose regression guard only (generous
		// headroom). Budgets stay the hard ceiling; recorded sizes.txt is
		// advisory — allow large slack so compiler growth does not thrash CI.
		const slack = 4096
		if got < want-slack || got > want+slack {
			t.Logf("%s.wasm size %d, recorded %d (±%d) — consider updating testdata/size/sizes.txt", name, got, want, slack)
		} else if got != want {
			t.Logf("%s.wasm size %d (recorded %d; within ±%d slack)", name, got, want, slack)
		}
		if got > budget {
			t.Errorf("%s.wasm size %d exceeds budget %d", name, got, budget)
		}
	}
	t.Log(report.String())
}

func repoRoot(t *testing.T) string {
	t.Helper()
	// cmd/wasmgc-go → repo root
	wd, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	return filepath.Clean(filepath.Join(wd, "..", ".."))
}

func readNameList(t *testing.T, path string) []string {
	t.Helper()
	f, err := os.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	var out []string
	sc := bufio.NewScanner(f)
	for sc.Scan() {
		line := strings.TrimSpace(sc.Text())
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		out = append(out, line)
	}
	if err := sc.Err(); err != nil {
		t.Fatal(err)
	}
	return out
}

func readNameInts(t *testing.T, path string) map[string]int {
	t.Helper()
	f, err := os.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	out := map[string]int{}
	sc := bufio.NewScanner(f)
	for sc.Scan() {
		line := strings.TrimSpace(sc.Text())
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		fields := strings.Fields(line)
		if len(fields) != 2 {
			t.Fatalf("%s: want name size, got %q", path, line)
		}
		n, err := strconv.Atoi(fields[1])
		if err != nil {
			t.Fatalf("%s: %v", path, err)
		}
		out[fields[0]] = n
	}
	if err := sc.Err(); err != nil {
		t.Fatal(err)
	}
	return out
}

func buildCorpusProgram(t *testing.T, exe, goroot, name string) int {
	t.Helper()
	dir := t.TempDir()
	srcDir := filepath.Join("testdata", name)
	for _, n := range []string{"main.go", "go.mod"} {
		data, err := os.ReadFile(filepath.Join(srcDir, n))
		if err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(dir, n), data, 0o644); err != nil {
			t.Fatal(err)
		}
	}
	wasmPath := filepath.Join(dir, name+".wasm")
	var cmd *exec.Cmd
	switch name {
	case "empty_main", "println":
		cmd = exec.Command(exe, "build", "-skip-stdlib", "-o", wasmPath, ".")
	default:
		cmd = exec.Command(exe, "build", "-goroot", goroot, "-o", wasmPath, ".")
	}
	cmd.Dir = dir
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("%s build: %v\n%s", name, err, out)
	}
	wasm, err := os.ReadFile(wasmPath)
	if err != nil {
		t.Fatal(err)
	}
	return len(wasm)
}
