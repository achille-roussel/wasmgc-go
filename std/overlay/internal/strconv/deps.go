// overlay of go1.27.4 internal/strconv/deps.go
// Copyright 2025 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package strconv

// Implementations to avoid importing other dependencies.
// float*bits / float*frombits are body-less; rtlink fills reinterpret
// (same as runtime.f32bits/f64bits — docs/design/12 nosuspend).

func float64frombits(b uint64) float64
func float32frombits(b uint32) float32
func float64bits(f float64) uint64
func float32bits(f float32) uint32

func inf(sign int) float64 {
	var v uint64
	if sign >= 0 {
		v = 0x7FF0000000000000
	} else {
		v = 0xFFF0000000000000
	}
	return float64frombits(v)
}

func isNaN(f float64) (is bool) { return f != f }

func nan() float64 { return float64frombits(0x7FF8000000000001) }
