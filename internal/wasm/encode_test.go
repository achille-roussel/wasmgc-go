package wasm_test

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/achille-roussel/wasmgc-go/internal/wasm"
)

func TestStructArrayGCModule(t *testing.T) {
	m := wasm.NewModule()
	bytesTy := m.AddArrayType(wasm.Field(wasm.StorageI8(), true))
	point := m.AddType(wasm.SubOpen(nil, wasm.StructType(
		wasm.Field(wasm.StorageVal(wasm.I32()), true),
		wasm.Field(wasm.StorageVal(wasm.I32()), true),
	)))
	box := m.AddType(wasm.SubFinal([]uint32{point}, wasm.StructType(
		wasm.Field(wasm.StorageVal(wasm.I32()), true),
		wasm.Field(wasm.StorageVal(wasm.I32()), true),
		wasm.Field(wasm.StorageVal(wasm.RefNull(wasm.TypeIndexHeap(bytesTy))), false),
	)))

	dataIdx := m.AddPassiveData([]byte("hi"))
	m.EmitDataCount = true

	enc := wasm.NewEncoder()
	enc.I32Const(0)
	enc.I32Const(2)
	enc.ArrayNewData(bytesTy, dataIdx)
	enc.LocalSet(0)
	enc.I32Const(3)
	enc.I32Const(4)
	enc.LocalGet(0)
	enc.StructNew(box)
	enc.StructGet(box, 0)
	enc.Drop()
	enc.DataDrop(dataIdx)

	ft := m.AddFuncType(nil, nil)
	m.DefineFunc(ft, wasm.CodeFrom(
		[]wasm.Local{{Count: 1, Type: wasm.Ref(wasm.TypeIndexHeap(bytesTy))}},
		enc,
	))
	m.ExportFunc("run", 0)

	b, err := m.Encode()
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(t.TempDir(), "gc.wasm")
	if err := os.WriteFile(path, b, 0o644); err != nil {
		t.Fatal(err)
	}
	validateWithWasmTools(t, path)
}

func TestTryTableThrow(t *testing.T) {
	m := wasm.NewModule()
	panicObj := m.AddStructType(
		wasm.Field(wasm.StorageVal(wasm.I32()), false),
		wasm.Field(wasm.StorageVal(wasm.I32()), true),
	)
	tagTy := m.AddFuncType([]wasm.ValType{wasm.Ref(wasm.TypeIndexHeap(panicObj))}, nil)
	tag := m.AddTag(tagTy)
	handlerTy := m.AddFuncType(nil, []wasm.ValType{
		wasm.Ref(wasm.TypeIndexHeap(panicObj)),
		wasm.ExnRef(),
	})

	boomTy := m.AddFuncType([]wasm.ValType{wasm.I32()}, nil)
	encBoom := wasm.NewEncoder()
	encBoom.LocalGet(0)
	encBoom.I32Const(0)
	encBoom.StructNew(panicObj)
	encBoom.Throw(tag)
	boom := m.DefineFunc(boomTy, wasm.CodeFrom(nil, encBoom))

	runTy := m.AddFuncType(nil, []wasm.ValType{wasm.I32()})
	enc := wasm.NewEncoder()
	enc.Block(wasm.BlockVal(wasm.I32()))          // $return
	enc.Block(wasm.BlockTypeIndex(handlerTy))     // $handler
	enc.TryTable(wasm.BlockEmpty(), []wasm.CatchClause{{
		Kind:     wasm.CatchRef,
		TagIdx:   tag,
		LabelIdx: 0,
	}})
	enc.I32Const(7)
	enc.Call(boom)
	enc.I32Const(0)
	enc.Br(2)
	enc.End() // try_table
	enc.Unreachable()
	enc.End() // $handler — leaves (ref panic) exnref on stack
	enc.Drop() // exnref
	enc.StructGet(panicObj, 0)
	enc.I32Const(1)
	enc.I32Add()
	enc.End() // $return

	m.DefineFunc(runTy, wasm.CodeFrom(nil, enc))
	m.ExportFunc("run", 1)

	b, err := m.Encode()
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(t.TempDir(), "eh.wasm")
	if err := os.WriteFile(path, b, 0o644); err != nil {
		t.Fatal(err)
	}
	validateWithWasmTools(t, path)
}

func TestRecGroupSubtyping(t *testing.T) {
	m := wasm.NewModule()
	start := m.AddRecGroup(wasm.Rec(
		wasm.SubOpen(nil, wasm.StructType()),
		wasm.SubOpen([]uint32{0}, wasm.StructType(
			wasm.Field(wasm.StorageVal(wasm.I32()), false),
			wasm.Field(wasm.StorageVal(wasm.RefNull(wasm.TypeIndexHeap(1))), true),
		)),
	))
	if start != 0 {
		t.Fatalf("start=%d", start)
	}
	ft := m.AddFuncType(nil, nil)
	m.DefineFunc(ft, wasm.CodeFrom(nil, wasm.NewEncoder()))
	m.ExportFunc("run", 0)
	b, err := m.Encode()
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(t.TempDir(), "rec.wasm")
	if err := os.WriteFile(path, b, 0o644); err != nil {
		t.Fatal(err)
	}
	validateWithWasmTools(t, path)
}

func TestNameSection(t *testing.T) {
	m := wasm.EmptyRunModule()
	m.Names = &wasm.NameSection{
		ModuleName: "m0",
		Functions:  []wasm.FuncName{{Index: 0, Name: "run"}},
		Types:      []wasm.TypeName{{Index: 0, Name: "run_t"}},
	}
	b, err := m.Encode()
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(t.TempDir(), "named.wasm")
	if err := os.WriteFile(path, b, 0o644); err != nil {
		t.Fatal(err)
	}
	validateWithWasmTools(t, path)
}

func TestMemoryAndImport(t *testing.T) {
	m := wasm.NewModule()
	writeTy := m.AddFuncType([]wasm.ValType{wasm.I32(), wasm.I32(), wasm.I32()}, []wasm.ValType{wasm.I32()})
	m.ImportFunc("go", "runtime.write", writeTy)
	m.AddMemory(wasm.Limits{Min: 1})
	runTy := m.AddFuncType(nil, nil)
	enc := wasm.NewEncoder()
	enc.I32Const(1)
	enc.I32Const(0)
	enc.I32Const(0)
	enc.Call(0)
	enc.Drop()
	m.DefineFunc(runTy, wasm.CodeFrom(nil, enc))
	m.ExportFunc("run", 1)
	m.ExportMemory("memory", 0)
	b, err := m.Encode()
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(t.TempDir(), "mem.wasm")
	if err := os.WriteFile(path, b, 0o644); err != nil {
		t.Fatal(err)
	}
	validateWithWasmTools(t, path)
}

func TestDuplicateExportRejected(t *testing.T) {
	m := wasm.EmptyRunModule()
	m.ExportFunc("run", 0)
	if _, err := m.Encode(); err == nil {
		t.Fatal("expected duplicate export error")
	}
}

func TestFuncCodeMismatchRejected(t *testing.T) {
	m := wasm.NewModule()
	ft := m.AddFuncType(nil, nil)
	m.AddFunc(ft)
	if _, err := m.Encode(); err == nil {
		t.Fatal("expected func/code mismatch error")
	}
}
