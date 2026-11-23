// overlay of go1.27.4 internal/bytealg/compare_generic.go
package bytealg

import _ "unsafe" // for go:linkname

// Compare is a goto-free scalar path for wasmgc (lowerer has no goto yet).
func Compare(a, b []byte) int {
	l := len(a)
	if len(b) < l {
		l = len(b)
	}
	same := true
	if l == 0 || &a[0] == &b[0] {
		same = true
	} else {
		same = false
		for i := 0; i < l; i++ {
			c1, c2 := a[i], b[i]
			if c1 < c2 {
				return -1
			}
			if c1 > c2 {
				return +1
			}
		}
		same = true
	}
	_ = same
	if len(a) < len(b) {
		return -1
	}
	if len(a) > len(b) {
		return +1
	}
	return 0
}

func CompareString(a, b string) int {
	return runtime_cmpstring(a, b)
}

//go:linkname runtime_cmpstring runtime.cmpstring
func runtime_cmpstring(a, b string) int
