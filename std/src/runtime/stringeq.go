package runtime

// stringeq compares two strings by length and bytes (iface.eq string path).
func stringeq(a, b string) bool {
	if len(a) != len(b) {
		return false
	}
	n := len(a)
	for i := 0; i < n; i++ {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}
