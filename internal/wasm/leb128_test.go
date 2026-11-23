package wasm

import (
	"bytes"
	"testing"
)

func TestSignedLEB128RoundTrip(t *testing.T) {
	cases := []int64{
		0, 1, -1, 63, 64, 65, 127, 128, 255, 256,
		-18, -23, -64, -65, 1<<20, -(1 << 20),
	}
	for _, v := range cases {
		enc := AppendS33(nil, v)
		got, n, err := ReadS33(enc)
		if err != nil {
			t.Fatalf("ReadS33(%d): %v", v, err)
		}
		if n != len(enc) {
			t.Fatalf("ReadS33(%d): consumed %d of %d", v, n, len(enc))
		}
		if got != v {
			t.Fatalf("round-trip %d: got %d bytes=%x", v, got, enc)
		}
	}
}

func TestTypeIndex64NotUnsignedByte(t *testing.T) {
	// The wasm3 bug: unsigned LEB of 64 is 0x40, which signed-decodes as −64.
	unsigned := AppendU32(nil, 64)
	if !bytes.Equal(unsigned, []byte{0x40}) {
		t.Fatalf("unsigned 64 = %x, want 40", unsigned)
	}
	asSigned, _, err := ReadS33(unsigned)
	if err != nil {
		t.Fatal(err)
	}
	if asSigned != -64 {
		t.Fatalf("unsigned 0x40 as s33 = %d, want -64", asSigned)
	}

	correct := AppendS33(nil, 64)
	if bytes.Equal(correct, []byte{0x40}) {
		t.Fatal("signed encoding of 64 must not be the single byte 0x40")
	}
	if !bytes.Equal(correct, []byte{0xc0, 0x00}) {
		t.Fatalf("signed 64 = %x, want c0 00", correct)
	}
	got, _, err := ReadS33(correct)
	if err != nil {
		t.Fatal(err)
	}
	if got != 64 {
		t.Fatalf("decoded %d, want 64", got)
	}
}

func TestAbstractHeapBytes(t *testing.T) {
	// Abstract heap types encode as their single-byte absheaptype forms.
	cases := []struct {
		ht   HeapType
		want byte
	}{
		{HeapTypeAny(), 0x6E},
		{HeapTypeEq(), 0x6D},
		{HeapTypeI31(), 0x6C},
		{HeapTypeStruct(), 0x6B},
		{HeapTypeArray(), 0x6A},
		{HeapTypeExn(), 0x69},
		{HeapTypeFunc(), 0x70},
		{HeapTypeExtern(), 0x6F},
		{HeapTypeNone(), 0x71},
	}
	for _, c := range cases {
		enc := EncodeHeapType(c.ht)
		if len(enc) != 1 || enc[0] != c.want {
			t.Errorf("%v: got %x, want %02x", c.ht, enc, c.want)
		}
	}
}
