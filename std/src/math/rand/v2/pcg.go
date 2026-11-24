package rand

// PCG is a tiny seedable Source for tests (not the real PCG algorithm).
type PCG struct {
	x uint64
}

// NewPCG returns a PCG seeded with seed1 and seed2.
func NewPCG(seed1, seed2 uint64) *PCG {
	x := seed1 ^ seed2
	if x == 0 {
		x = 0x9E3779B97F4A7C15
	}
	return &PCG{x: x}
}

// Uint64 implements Source.
func (p *PCG) Uint64() uint64 {
	// SplitMix64 step — good enough for sort table tests.
	p.x += 0x9E3779B97F4A7C15
	z := p.x
	z = (z ^ (z >> 30)) * 0xBF58476D1CE4E5B9
	z = (z ^ (z >> 27)) * 0x94D049BB133111EB
	return z ^ (z >> 31)
}
