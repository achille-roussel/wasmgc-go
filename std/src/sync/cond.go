package sync

import "runtime"

// Cond implements a condition variable (thin wasmgc stub).
type Cond struct {
	L Locker
	// noCopy intentionally omitted; wasmgc ignores copy detection.
	n waiters
}

type waiters struct {
	sema uint32
	n    int
}

// NewCond returns a new Cond with Locker l.
func NewCond(l Locker) *Cond {
	return &Cond{L: l}
}

// Wait atomically unlocks c.L and suspends. After resume, Wait locks c.L before
// returning. Stub uses Semacquire; spurious wakeups are possible.
func (c *Cond) Wait() {
	c.n.n++
	c.L.Unlock()
	runtime.Semacquire(&c.n.sema)
	c.L.Lock()
}

// Signal wakes one goroutine waiting on c, if any.
func (c *Cond) Signal() {
	if c.n.n > 0 {
		c.n.n--
		runtime.Semrelease(&c.n.sema)
	}
}

// Broadcast wakes all goroutines waiting on c.
func (c *Cond) Broadcast() {
	for c.n.n > 0 {
		c.n.n--
		runtime.Semrelease(&c.n.sema)
	}
}
