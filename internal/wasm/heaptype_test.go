package wasm_test

import (
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"testing"

	"github.com/achille-roussel/wasmgc-go/internal/wasm"
)

// TestTypeIndex64RefCast is the signed-LEB heap-type round trip from
// docs/design/08-testing.md: a module with 64 types, a ref.cast of the
// last, decoded back, with the immediate not equal to the single byte 0x40.
func TestTypeIndex64RefCast(t *testing.T) {
	m := wasm.NewModule()

	// Types 0..62: trivial final empty structs (distinct indices).
	for i := 0; i < 63; i++ {
		m.AddStructType(wasm.Field(wasm.StorageVal(wasm.I32()), false))
	}
	// Type 63: a struct we will cast to. Index 63 is fine as one-byte signed
	// (0x3f). We need index 64 for the bug, so add one more after a func type.
	funcTy := m.AddFuncType(nil, []wasm.ValType{wasm.Ref(wasm.TypeIndexHeap(0))})
	// Now NumTypes should be 64; next type is index 64.
	if m.NumTypes() != 64 {
		t.Fatalf("NumTypes=%d, want 64 before target", m.NumTypes())
	}
	target := m.AddStructType(
		wasm.Field(wasm.StorageVal(wasm.I32()), true),
	)
	if target != 64 {
		t.Fatalf("target type index = %d, want 64", target)
	}

	_ = funcTy
	enc := wasm.NewEncoder()
	// Produce an anyref (null), cast to (ref $target).
	enc.RefNull(wasm.HeapTypeAny())
	enc.RefCastNull(wasm.TypeIndexHeap(target))
	enc.Drop()
	runTy := m.AddFuncType(nil, nil)
	m.DefineFunc(runTy, wasm.CodeFrom(nil, enc))
	m.ExportFunc("run", 0)

	b, err := m.Encode()
	if err != nil {
		t.Fatal(err)
	}

	// Locate the ref.cast_null immediate: opcode sequence fb 17 <s33>.
	imm := findRefCastNullImmediate(t, b)
	if bytes.Equal(imm, []byte{0x40}) {
		t.Fatalf("type index 64 encoded as unsigned 0x40 (wasm3 bug); bytes=%x", imm)
	}
	ht, n, err := wasm.DecodeHeapType(imm)
	if err != nil {
		t.Fatal(err)
	}
	if n != len(imm) {
		// imm may include trailing bytes; DecodeHeapType reads one heap type.
		ht, n, err = wasm.DecodeHeapType(imm[:n])
		if err != nil {
			t.Fatal(err)
		}
	}
	if !ht.IsIndex() || ht.Index() != 64 {
		t.Fatalf("decoded heap type %#v, want index 64 (consumed %d, imm %x)", ht, n, imm)
	}
	if !bytes.Equal(wasm.EncodeHeapType(wasm.TypeIndexHeap(64)), []byte{0xc0, 0x00}) {
		t.Fatalf("EncodeHeapType(64)=%x", wasm.EncodeHeapType(wasm.TypeIndexHeap(64)))
	}

	dir := t.TempDir()
	path := filepath.Join(dir, "idx64.wasm")
	if err := os.WriteFile(path, b, 0o644); err != nil {
		t.Fatal(err)
	}
	validateWithWasmTools(t, path)

	// Cross-check: wasm-tools print should mention type 64 / cast.
	if _, err := exec.LookPath("wasm-tools"); err == nil {
		out, err := exec.Command("wasm-tools", "print", path).CombinedOutput()
		if err != nil {
			t.Fatalf("print: %v\n%s", err, out)
		}
		t.Logf("idx64 module excerpt:\n%s", head(string(out), 40))
	}
}

func findRefCastNullImmediate(t *testing.T, mod []byte) []byte {
	t.Helper()
	// Scan for GC prefix + subopcode 0x17 (ref.cast null).
	for i := 0; i+2 < len(mod); i++ {
		if mod[i] == wasm.OpGCPrefix && mod[i+1] == byte(wasm.GCRefCastNull) {
			// subopcode fits in one byte (< 128). Immediate follows.
			return mod[i+2:]
		}
		// subopcode as u32 LEB: fb 17
		if mod[i] == wasm.OpGCPrefix {
			v, n, err := wasm.ReadU32(mod[i+1:])
			if err == nil && v == wasm.GCRefCastNull {
				return mod[i+1+n:]
			}
		}
	}
	t.Fatal("ref.cast null not found in module bytes")
	return nil
}

func head(s string, lines int) string {
	n := 0
	for i, c := range s {
		if c == '\n' {
			n++
			if n >= lines {
				return s[:i]
			}
		}
	}
	return s
}
