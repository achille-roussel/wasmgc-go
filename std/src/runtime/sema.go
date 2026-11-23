package runtime

// Semaphore hooks for sync.Mutex / WaitGroup (04, 06).
//
// One activation runs at a time, so the counter load/store does not race with
// another goroutine. Host callbacks do not preempt (07). No spin.
//
// Keys are a linked list (maps are M6).

type semaRoot struct {
	wait waitq
}

type semaEntry struct {
	addr *uint32
	root *semaRoot
	next *semaEntry
}

var semaHead *semaEntry

// semaPtrSame reports whether a and b address the same uint32 cell.
// Fat *T pointers are distinct objects per &expr; codegen fills this with
// base+index equality on $go.ptr.i32 (not object identity of the ptr itself).
func semaPtrSame(a, b *uint32) bool

func semRoot(s *uint32) *semaRoot {
	for e := semaHead; e != nil; e = e.next {
		if semaPtrSame(e.addr, s) {
			return e.root
		}
	}
	// wait is a ref field ($go.waitq); allocate an empty queue so enqueue
	// never sees a null waitq object (same as makechan's sendq/recvq).
	r := &semaRoot{wait: waitq{}}
	semaHead = &semaEntry{addr: s, root: r, next: semaHead}
	return r
}

// semacquire waits until *addr > 0 and decrements it.
// Linkname target for sync.runtime_Semacquire.
func semacquire(addr *uint32) {
	semacquire1(addr, false)
}

// semrelease increments *addr and readies one waiter if any.
func semrelease(addr *uint32) {
	semrelease1(addr, false)
}

func semacquire1(addr *uint32, lifo bool) {
	if addr == nil {
		throw("semacquire of nil")
	}
	if *addr > 0 {
		*addr--
		return
	}
	id := mintParkID()
	w := &waiter{park: id}
	r := semRoot(addr)
	if lifo {
		w.next = r.wait.head
		r.wait.head = w
		if r.wait.tail == nil {
			r.wait.tail = w
		}
	} else {
		r.wait.enqueue(w)
	}
	for {
		gopark(id, waitReasonSemacquire)
		if *addr > 0 {
			*addr--
			return
		}
		// Spurious or stolen; requeue.
		id = mintParkID()
		w.park = id
		r.wait.enqueue(w)
	}
}

func semrelease1(addr *uint32, handoff bool) {
	if addr == nil {
		throw("semrelease of nil")
	}
	*addr++
	r := semRoot(addr)
	w := r.wait.dequeue()
	if w != nil {
		goready(w.park)
		_ = handoff
	}
}

// Public wrappers for sync rewrite (no linkname yet).
func Semacquire(addr *uint32) { semacquire(addr) }
func Semrelease(addr *uint32) { semrelease(addr) }

// Linkname aliases matching sync/runtime.go names.
func runtime_Semacquire(s *uint32) { semacquire(s) }
func runtime_Semrelease(s *uint32, handoff bool, _ int) {
	semrelease1(s, handoff)
}
func runtime_SemacquireMutex(s *uint32, lifo bool, _ int) {
	semacquire1(s, lifo)
}
func runtime_SemacquireRWMutex(s *uint32, lifo bool, _ int) {
	semacquire1(s, lifo)
}
func runtime_SemacquireRWMutexR(s *uint32, lifo bool, _ int) {
	semacquire1(s, lifo)
}
func runtime_SemacquireWaitGroup(s *uint32, _ bool) {
	semacquire(s)
}

// Spin stubs used by sync; wasmgc never spins (single activation).
func runtime_canSpin(i int) bool { return false }
func runtime_doSpin()            {}
func runtime_nanotime() int64    { return 0 }
