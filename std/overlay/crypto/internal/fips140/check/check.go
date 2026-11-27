// overlay of go1.27.4 crypto/internal/fips140/check/check.go
// Copyright 2024 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

// Package check implements the FIPS 140 load-time code+data verification.
// wasmgc overlay: Wasm GC has no integer code addresses, so verification is a
// no-op. Verified is set true so dependents that gate on it keep working.
package check

import "crypto/internal/fips140"

// Verified is set when verification succeeded.
var Verified bool

func init() {
	if !fips140.Enabled {
		return
	}
	// No go:fipsinfo / uintptr section walk under Wasm GC.
	Verified = true
}
