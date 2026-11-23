package pipeline_test

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/achille-roussel/wasmgc-go/internal/pipeline"
)

func TestCompileFilesMode(t *testing.T) {
	if !pipeline.Default.Real() {
		t.Fatal("expected real backends")
	}
	dir := t.TempDir()
	src := filepath.Join(dir, "hello.go")
	if err := os.WriteFile(src, []byte("// run\n\npackage main\nfunc main() { println(\"hi\") }\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	res, err := pipeline.Compile(pipeline.Options{
		Files:      []string{src},
		Dir:        dir,
		SkipStdlib: true,
		Names:      true,
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(res.Wasm) == 0 {
		t.Fatal("empty wasm")
	}
}
