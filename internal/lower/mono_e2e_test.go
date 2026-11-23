package lower_test

import (
	"path/filepath"
	"runtime"
	"testing"

	"github.com/achille-roussel/wasmgc-go/internal/load"
	"github.com/achille-roussel/wasmgc-go/internal/lower"
	"github.com/achille-roussel/wasmgc-go/internal/mono"
)

func TestGenericMaxLower(t *testing.T) {
	_, file, _, _ := runtime.Caller(0)
	dir := filepath.Join(filepath.Dir(file), "..", "..", "cmd", "wasmgc-go", "testdata", "generic_max")
	prog, err := load.Load(load.Config{Dir: dir, Pattern: "."})
	if err != nil {
		t.Fatal(err)
	}
	tab, err := mono.Program(prog)
	if err != nil {
		t.Fatal(err)
	}
	t.Logf("instances=%d", len(tab.ByName))
	for n, inst := range tab.ByName {
		t.Logf("  %s subst=%d", n, len(inst.Subst))
	}
	_, err = lower.PackageHighMono(prog.Packages, prog.Main, tab)
	if err != nil {
		t.Fatal(err)
	}
}
