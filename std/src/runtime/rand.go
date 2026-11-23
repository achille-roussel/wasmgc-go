package runtime

// fastrand is an xorshift on a global, seeded from go.runtime.rand (04).

var fastrandState uint32 = 1

func fastrand() uint32 {
	s := fastrandState
	if s == 0 {
		s = 1
	}
	// xorshift32
	s ^= s << 13
	s ^= s >> 17
	s ^= s << 5
	fastrandState = s
	return s
}

func fastrandn(n uint32) uint32 {
	if n == 0 {
		return 0
	}
	// Lemire's method truncated; fine for select shuffle.
	return fastrand() % n
}
