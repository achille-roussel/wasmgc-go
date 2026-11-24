package desc

import "testing"

func TestSanitizeSliceVsArray(t *testing.T) {
	cases := []struct{ in, want string }{
		{"[]uint8", "arr_uint8"},
		{"[3]uint8", "arr_3uint8"},
		{"[]byte", "arr_byte"},
		{"[3]byte", "arr_3byte"},
		{"[]int", "arr_int"},
		{"[1]int", "arr_1int"},
	}
	for _, c := range cases {
		if g := Sanitize(c.in); g != c.want {
			t.Errorf("Sanitize(%q)=%q want %q", c.in, g, c.want)
		}
	}
	// Critical: slice and array must not collide
	if Sanitize("[]uint8") == Sanitize("[0]uint8") {
		t.Error("[]uint8 collides with [0]uint8")
	}
}
