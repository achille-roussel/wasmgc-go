// overlay of go1.27.4 crypto/internal/fips140/subtle/xor_generic.go
// Copyright 2013 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package subtle

import "unsafe"

// wasmgc: no uintptr(unsafe.Pointer) — byte-wise XOR only (no word path).
func xorBytes(dstb, xb, yb *byte, n int) {
	dst := unsafe.Slice(dstb, n)
	x := unsafe.Slice(xb, n)
	y := unsafe.Slice(yb, n)
	xorLoop(dst, x, y)
}

func xorLoop[T byte](dst, x, y []T) {
	x = x[:len(dst)]
	y = y[:len(dst)]
	for i := range dst {
		dst[i] = x[i] ^ y[i]
	}
}
