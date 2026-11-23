package wasm

import "fmt"

// AppendU32 appends the unsigned LEB128 encoding of v.
func AppendU32(buf []byte, v uint32) []byte {
	for {
		b := byte(v & 0x7f)
		v >>= 7
		if v != 0 {
			b |= 0x80
		}
		buf = append(buf, b)
		if v == 0 {
			return buf
		}
	}
}

// AppendU64 appends the unsigned LEB128 encoding of v.
func AppendU64(buf []byte, v uint64) []byte {
	for {
		b := byte(v & 0x7f)
		v >>= 7
		if v != 0 {
			b |= 0x80
		}
		buf = append(buf, b)
		if v == 0 {
			return buf
		}
	}
}

// AppendI32 appends the signed LEB128 encoding of v (s32).
func AppendI32(buf []byte, v int32) []byte {
	return AppendI64(buf, int64(v))
}

// AppendI64 appends the signed LEB128 encoding of v (s64 / s33).
// Used for heap types and block-type type indices.
func AppendI64(buf []byte, v int64) []byte {
	for {
		b := byte(v & 0x7f)
		v >>= 7
		done := (v == 0 && b&0x40 == 0) || (v == -1 && b&0x40 != 0)
		if !done {
			b |= 0x80
		}
		buf = append(buf, b)
		if done {
			return buf
		}
	}
}

// AppendS33 appends a 33-bit signed LEB128 (heap types, block type indices).
func AppendS33(buf []byte, v int64) []byte {
	if v < -(1<<32) || v >= (1<<32) {
		// Still encode; callers should keep indices in range.
	}
	return AppendI64(buf, v)
}

// ReadU32 reads an unsigned LEB128 from b, returning the value and bytes consumed.
func ReadU32(b []byte) (uint32, int, error) {
	var result uint32
	var shift uint
	for i := 0; i < len(b); i++ {
		by := b[i]
		result |= uint32(by&0x7f) << shift
		if by&0x80 == 0 {
			return result, i + 1, nil
		}
		shift += 7
		if shift >= 35 {
			return 0, 0, fmt.Errorf("wasm: u32 LEB128 overflow")
		}
	}
	return 0, 0, fmt.Errorf("wasm: truncated u32 LEB128")
}

// ReadS33 reads a signed LEB128 interpreted as s33 (heap types).
func ReadS33(b []byte) (int64, int, error) {
	var result int64
	var shift uint
	var by byte
	i := 0
	for ; i < len(b); i++ {
		by = b[i]
		result |= int64(by&0x7f) << shift
		shift += 7
		if by&0x80 == 0 {
			break
		}
		if shift >= 35 {
			return 0, 0, fmt.Errorf("wasm: s33 LEB128 overflow")
		}
	}
	if i == len(b) && (len(b) == 0 || b[len(b)-1]&0x80 != 0) {
		return 0, 0, fmt.Errorf("wasm: truncated s33 LEB128")
	}
	if shift < 64 && by&0x40 != 0 {
		result |= -1 << shift
	}
	return result, i + 1, nil
}
