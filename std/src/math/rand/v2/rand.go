// Package rand is a wasmgc rewrite stub of math/rand/v2.
// Enough for sort/slices tests: IntN, New, NewPCG, *Rand.
package rand

// Source is a source of uniformly-distributed uint64 values.
type Source interface {
	Uint64() uint64
}

// Rand generates other random values from a Source.
type Rand struct {
	src Source
}

// New returns a Rand wrapping src.
func New(src Source) *Rand {
	return &Rand{src: src}
}

func (r *Rand) Uint64() uint64 { return r.src.Uint64() }

// IntN returns a non-negative int in [0, n). Panics if n <= 0.
func (r *Rand) IntN(n int) int {
	if n <= 0 {
		panic("invalid argument to IntN")
	}
	return int(r.src.Uint64() % uint64(n))
}

var globalRand = New(NewPCG(1, 2))

// IntN returns a non-negative int in [0, n) from the global source.
func IntN(n int) int { return globalRand.IntN(n) }

func (r *Rand) Float32() float32 {
	return float32(r.src.Uint64()>>40) / (1 << 24)
}

func (r *Rand) Float64() float64 {
	return float64(r.src.Uint64()>>11) / (1 << 53)
}
