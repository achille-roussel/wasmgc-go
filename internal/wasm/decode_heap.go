package wasm

import "fmt"

// DecodeHeapType decodes a heap type from b (signed LEB128 / absheaptype).
// Returns the HeapType and the number of bytes consumed.
//
// This exists so tests can prove a type index of 64 survives a round trip
// and is not confused with the unsigned single-byte 0x40 (== signed −64).
func DecodeHeapType(b []byte) (HeapType, int, error) {
	if len(b) == 0 {
		return HeapType{}, 0, fmt.Errorf("wasm: empty heap type")
	}
	v, n, err := ReadS33(b)
	if err != nil {
		return HeapType{}, 0, err
	}
	return HeapType{code: v}, n, nil
}

// EncodeHeapType returns the signed-LEB encoding of ht.
func EncodeHeapType(ht HeapType) []byte {
	return ht.encode(nil)
}
