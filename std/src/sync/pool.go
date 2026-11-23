package sync

import "runtime"

// A Pool is a set of temporary objects that may be individually saved and
// retrieved. wasmgc rewrite: single local slot plus victim list (no per-P
// array; no uintptr pointer arithmetic). Adapted from std/overlay/sync/pool.go.
type Pool struct {
	noCopy noCopy

	mu     Mutex
	local  any
	shared []any
	victim []any

	// New optionally specifies a function to generate a value when Get would
	// otherwise return nil.
	New func() any
}

type noCopy struct{}

func (*noCopy) Lock()   {}
func (*noCopy) Unlock() {}

// Put adds x to the pool.
func (p *Pool) Put(x any) {
	if x == nil {
		return
	}
	p.mu.Lock()
	if p.local == nil {
		p.local = x
	} else {
		p.shared = append(p.shared, x)
	}
	p.mu.Unlock()
}

// Get selects an arbitrary item from the Pool.
func (p *Pool) Get() any {
	p.mu.Lock()
	var x any
	if p.local != nil {
		x = p.local
		p.local = nil
	} else if n := len(p.shared); n > 0 {
		x = p.shared[n-1]
		p.shared[n-1] = nil
		p.shared = p.shared[:n-1]
	} else if n := len(p.victim); n > 0 {
		x = p.victim[n-1]
		p.victim[n-1] = nil
		p.victim = p.victim[:n-1]
	}
	p.mu.Unlock()

	if x == nil && p.New != nil {
		x = p.New()
	}
	return x
}

func poolCleanup() {
	allPoolsMu.Lock()
	defer allPoolsMu.Unlock()
	for _, p := range oldPools {
		p.mu.Lock()
		p.victim = nil
		p.mu.Unlock()
	}
	for _, p := range allPools {
		p.mu.Lock()
		p.victim = append(p.victim[:0], p.shared...)
		if p.local != nil {
			p.victim = append(p.victim, p.local)
			p.local = nil
		}
		p.shared = nil
		p.mu.Unlock()
	}
	oldPools, allPools = allPools, nil
}

var (
	allPoolsMu Mutex
	allPools   []*Pool
	oldPools   []*Pool
)

func init() {
	runtime.RegisterPoolCleanup(poolCleanup)
}
