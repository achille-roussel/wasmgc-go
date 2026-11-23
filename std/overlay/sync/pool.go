// overlay of go1.27.4 sync/pool.go
// Copyright 2013 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package sync

// A Pool is a set of temporary objects that may be individually saved and
// retrieved.
//
// Any item stored in the Pool may be removed automatically at any time without
// notification. If the Pool holds the only reference when this happens, the
// item might be deallocated.
//
// A Pool is safe for use by multiple goroutines simultaneously.
//
// Pool's purpose is to cache allocated but unused items for later reuse,
// relieving pressure on the garbage collector. That is, it makes it easy to
// build efficient, thread-safe free lists. However, it is not suitable for all
// free lists.
//
// An appropriate use of a Pool is to manage a group of temporary items
// silently shared among and potentially reused by concurrent independent
// clients of a package. Pool provides a way to amortize allocation overhead
// across many clients.
//
// An example of good use of a Pool is in the fmt package, which maintains a
// dynamically-sized store of temporary output buffers. The store scales under
// load (when many goroutines are actively printing) and shrinks when
// quiescent.
//
// On the other hand, a free list maintained as part of a short-lived object is
// not a suitable use for a Pool, since the overhead does not amortize well in
// that scenario. It is more efficient to have such objects implement their own
// free list.
//
// A Pool must not be copied after first use.
//
// In the terminology of [the Go memory model], a call to Put(x) “synchronizes before”
// a call to [Pool.Get] returning that same value x.
// Similarly, a call to New returning x “synchronizes before”
// a call to Get returning that same value x.
//
// [the Go memory model]: https://go.dev/ref/mem
//
// wasmgc overlay: single local slot plus victim list (no per-P array).
// runtime_procPin returns 0. No uintptr pointer arithmetic.
type Pool struct {
	noCopy noCopy

	mu     Mutex
	local  any   // private cached item
	shared []any // shared stack
	victim []any // from previous cleanup cycle

	// New optionally specifies a function to generate
	// a value when Get would otherwise return nil.
	// It may not be changed concurrently with calls to Get.
	New func() any
}

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

// Get selects an arbitrary item from the Pool, removes it from the
// Pool, and returns it to the caller.
// Get may choose to ignore the pool and treat it as empty.
// Callers should not assume any relation between values passed to Put and
// the values returned by Get.
//
// If Get would otherwise return nil and p.New is non-nil, Get returns
// the result of calling p.New.
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

// poolCleanup moves primary cache to victim and drops the previous victim.
//
//go:linkname poolCleanup
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
	runtime_registerPoolCleanup(poolCleanup)
}

// Implemented in runtime.
func runtime_registerPoolCleanup(cleanup func())
func runtime_procPin() int
func runtime_procUnpin()
