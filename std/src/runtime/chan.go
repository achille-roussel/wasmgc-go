package runtime

// Channels (docs/design/04, M7). Queue manipulation is shared; payload moves
// are supplied by generated per-element functions through chanOps (narrow
// interface until the lowerer emits typed trampolines).
//
// Write-before-ready: the sender writes the receiver's slot before goready
// (wasm3 regression 65a5736298). Waiter queues, never 1 ms polling.

// hchan is the shared channel header ($go.chan in 03). Typed buffers live on
// $go.chan.T; the shared path touches only these fields plus ops.
type hchan struct {
	qcount   int32
	dataqsiz int32 // capacity; 0 = unbuffered
	sendx    int32
	recvx    int32
	closed   int32
	sendq    waitq
	recvq    waitq
	// buf is an anyref to the typed element array ($go.chan.T.$buf).
	buf any
}

// waitq is $go.waitq: FIFO of waiters.
type waitq struct {
	head *waiter
	tail *waiter
}

// waiter is $go.waiter plus an elem slot for the payload the shared path
// parks with. Generated code may use a typed subtype; the scaffold keeps
// elem as any so tests and early Go bodies share one shape.
type waiter struct {
	park   int32
	sel    *select_
	casi   int32 // case index when sel != nil
	next   *waiter
	elem   any // send: value waiting; recv: destination slot after write
	isSend bool
	cas    *scase // select case object (same ref as cases[casi])
}

// chanOps is the narrow payload-move interface (04). Generated send/recv
// trampolines supply these; the shared algorithm does not load typed arrays.
type chanOps struct {
	toRecv     func(w *waiter, v any)          // write into waiting receiver
	bufSet     func(c *hchan, i int32, v any)  // buffer[i] = v
	bufGet     func(c *hchan, i int32) any     // ← buffer[i]
	toWaiter   func(w *waiter, v any)          // park sender with v
	fromWaiter func(w *waiter) any             // read parked sender's v
	zero       func() any                      // closed-recv zero
}

func makechan(size int32) *hchan {
	if size < 0 {
		throw("makechan: negative cap")
	}
	var buf any
	if size > 0 {
		buf = make([]any, size)
	}
	// sendq/recvq are ref fields ($go.waitq); allocate empty queues so
	// enqueue/dequeue never see a null waitq object.
	return &hchan{dataqsiz: size, buf: buf, sendq: waitq{}, recvq: waitq{}}
}

func (q *waitq) enqueue(w *waiter) {
	w.next = nil
	if q.tail == nil {
		q.head = w
		q.tail = w
		return
	}
	q.tail.next = w
	q.tail = w
}

func (q *waitq) dequeue() *waiter {
	w := q.head
	if w == nil {
		return nil
	}
	q.head = w.next
	if q.head == nil {
		q.tail = nil
	}
	w.next = nil
	return w
}

func (q *waitq) remove(target *waiter) {
	var prev *waiter
	for w := q.head; w != nil; w = w.next {
		if w == target {
			if prev == nil {
				q.head = w.next
			} else {
				prev.next = w.next
			}
			if q.tail == w {
				q.tail = prev
			}
			w.next = nil
			return
		}
		prev = w
	}
}

func (q *waitq) releaseAll() {
	for {
		w := q.dequeue()
		if w == nil {
			return
		}
		if w.sel != nil {
			trySelectReady(w)
		}
		goready(w.park)
	}
}

// chanSend sends v on c. block parks when it cannot proceed.
// Returns false only for non-blocking failure (select probe).
func chanSend(c *hchan, v any, ops *chanOps, block bool) bool {
	if c == nil {
		if !block {
			return false
		}
		gopark(mintParkID(), waitReasonChanSend)
		return false
	}
	if c.closed != 0 {
		throw("send on closed channel")
	}

	// Waiting receiver: write then ready (04).
	for {
		w := c.recvq.dequeue()
		if w == nil {
			break
		}
		if w.sel != nil && !trySelectReady(w) {
			continue // lost select race
		}
		ops.toRecv(w, v)
		// Publish into the select case (write-before-ready).
		if w.cas != nil {
			w.cas.elem = v
			w.cas.ok = true
		}
		goready(w.park)
		return true
	}

	// Buffered with space.
	if c.qcount < c.dataqsiz {
		ops.bufSet(c, c.sendx, v)
		c.sendx++
		if c.sendx == c.dataqsiz {
			c.sendx = 0
		}
		c.qcount++
		return true
	}

	if !block {
		return false
	}

	id := mintParkID()
	w := &waiter{park: id, isSend: true}
	ops.toWaiter(w, v)
	c.sendq.enqueue(w)
	gopark(id, waitReasonChanSend)
	if c.closed != 0 {
		throw("send on closed channel")
	}
	return true
}

// chanRecv receives from c. ok is false when receiving from a closed empty channel.
func chanRecv(c *hchan, ops *chanOps, block bool) (v any, ok bool) {
	if c == nil {
		if !block {
			return ops.zero(), false
		}
		gopark(mintParkID(), waitReasonChanRecv)
		return ops.zero(), false
	}

	// Waiting sender.
	for {
		w := c.sendq.dequeue()
		if w == nil {
			break
		}
		if w.sel != nil && !trySelectReady(w) {
			continue
		}
		if c.dataqsiz == 0 {
			v = ops.fromWaiter(w)
			goready(w.park)
			return v, true
		}
		// Buffer was full: take head of ring, then enqueue sender's value.
		v = ops.bufGet(c, c.recvx)
		ops.bufSet(c, c.sendx, ops.fromWaiter(w))
		c.recvx++
		if c.recvx == c.dataqsiz {
			c.recvx = 0
		}
		c.sendx++
		if c.sendx == c.dataqsiz {
			c.sendx = 0
		}
		goready(w.park)
		return v, true
	}

	// Buffer non-empty.
	if c.qcount > 0 {
		v = ops.bufGet(c, c.recvx)
		c.recvx++
		if c.recvx == c.dataqsiz {
			c.recvx = 0
		}
		c.qcount--
		return v, true
	}

	if c.closed != 0 {
		return ops.zero(), false
	}

	if !block {
		return ops.zero(), false
	}

	id := mintParkID()
	w := &waiter{park: id, isSend: false}
	c.recvq.enqueue(w)
	gopark(id, waitReasonChanRecv)
	if w.elem == nil && c.closed != 0 {
		return ops.zero(), false
	}
	return w.elem, true
}

// chanClose closes c and readies every waiter.
func chanClose(c *hchan) {
	if c == nil {
		throw("close of nil channel")
	}
	if c.closed != 0 {
		throw("close of closed channel")
	}
	c.closed = 1
	c.recvq.releaseAll()
	c.sendq.releaseAll()
}

// defaultChanOps for any-typed scaffold tests.
func defaultChanOps() *chanOps {
	return &chanOps{
		toRecv:     func(w *waiter, v any) { w.elem = v },
		bufSet:     func(c *hchan, i int32, v any) { c.buf.([]any)[i] = v },
		bufGet:     func(c *hchan, i int32) any { return c.buf.([]any)[i] },
		toWaiter:   func(w *waiter, v any) { w.elem = v },
		fromWaiter: func(w *waiter) any { return w.elem },
		zero:       func() any { return nil },
	}
}
