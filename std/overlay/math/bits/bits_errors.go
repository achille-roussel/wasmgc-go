// overlay of go1.27.4 math/bits/bits_errors.go
// Copyright 2019 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package bits

// wasmgc: go:linkname to runtime.divideError / overflowError is not wired for
// variables yet. Provide local runtime.Error values so Div*/Rem* panics are
// recoverable with the messages bits_test expects.

type errorString string

func (e errorString) RuntimeError() {}

func (e errorString) Error() string {
	return "runtime error: " + string(e)
}

var overflowError = error(errorString("integer overflow"))

var divideError = error(errorString("integer divide by zero"))
