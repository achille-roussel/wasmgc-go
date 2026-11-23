package stdlib

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/achille-roussel/wasmgc-go/internal/load"
)

func TestLoadManifestAndPIN(t *testing.T) {
	root, err := FindModuleRoot(".")
	if err != nil {
		t.Fatal(err)
	}
	pin, err := LoadPIN(filepath.Join(root, "std", "PIN"))
	if err != nil {
		t.Fatal(err)
	}
	if pin.Version != "go1.27.4" {
		t.Fatalf("version %s", pin.Version)
	}
	man, err := LoadManifest(filepath.Join(root, "std", "Manifest.json"))
	if err != nil {
		// case
		man, err = LoadManifest(filepath.Join(root, "std", "manifest.json"))
	}
	if err != nil {
		t.Fatal(err)
	}
	if man.Pin != pin.Version {
		t.Fatalf("pin mismatch")
	}
	e := man.Lookup("slices")
	if e.Tier != TierOverlay || len(e.Overlays) == 0 {
		t.Fatalf("slices: %+v", e)
	}
	e = man.Lookup("errors")
	if e.Tier != TierVendor {
		t.Fatalf("errors: %+v", e)
	}
	e = man.Lookup("plugin")
	if e.Tier != TierOut {
		t.Fatalf("plugin: %+v", e)
	}
	e = man.Lookup("some/unlisted")
	if e.Tier != TierVendor {
		t.Fatalf("default: %+v", e)
	}
}

func TestMaterializeAppliesFilterAndOverlay(t *testing.T) {
	root, err := FindModuleRoot(".")
	if err != nil {
		t.Fatal(err)
	}
	p, err := DefaultPaths(root)
	if err != nil {
		t.Fatal(err)
	}
	// Use a test-specific cache so we do not clobber the user cache marker.
	p.CacheDir = t.TempDir()
	mat, err := Materialize(p)
	if err != nil {
		t.Fatal(err)
	}
	bytealg := filepath.Join(mat.GOROOT, "src", "internal", "bytealg")
	entries, err := os.ReadDir(bytealg)
	if err != nil {
		t.Fatal(err)
	}
	names := map[string]bool{}
	for _, e := range entries {
		names[e.Name()] = true
	}
	if names["indexbyte_native.go"] {
		t.Fatal("indexbyte_native.go should be dropped by re-filter")
	}
	if !names["indexbyte_generic.go"] {
		t.Fatal("indexbyte_generic.go should be kept")
	}
	if names["indexbyte_wasm.s"] {
		t.Fatal(".s files must not be copied")
	}
	// Overlay header and overlaps rewrite.
	slicesPath := filepath.Join(mat.GOROOT, "src", "slices", "slices.go")
	data, err := os.ReadFile(slicesPath)
	if err != nil {
		t.Fatal(err)
	}
	if string(data[:len("// overlay of go1.27.4 slices/slices.go")]) != "// overlay of go1.27.4 slices/slices.go" {
		t.Fatalf("missing overlay header: %q", string(data[:60]))
	}
	if !strings.Contains(string(data), "wasmgc overlay") {
		t.Fatal("overlay body not applied")
	}
	if strings.Contains(string(data), "uintptr(unsafe.Pointer(&a[0]))") {
		t.Fatal("upstream overlaps still present")
	}
	// Rewrite replaces runtime.
	rt := filepath.Join(mat.GOROOT, "src", "runtime", "runtime.go")
	data, err = os.ReadFile(rt)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(data), "wasmgc rewrite") {
		t.Fatal("runtime rewrite not applied")
	}
	// Filtered list matches load.Filter
	files, err := load.DefaultFilter().ListGoFiles(bytealg)
	if err != nil {
		t.Fatal(err)
	}
	if len(files) == 0 {
		t.Fatal("no bytealg files")
	}
}
