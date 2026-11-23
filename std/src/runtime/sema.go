package runtime

// Semaphore hooks for sync.Mutex / WaitGroup (04, 06).
//
// One activation runs at a time, so the counter load/store does not race with
// another goroutine. Host callbacks do not preempt (07). No spin.

type semaRoot struct {
	wait waitq
}

// Sparse map from semaphore address identity to waiter queue.
// Host Go uses uintptr keys; wasmgc will key by ref identity. The scaffold
// keys by the *uint32 pointer value as an any map.
var semaTable = map[any]*semaRoot{}

func semRoot(s *uint32) *semaRoot {
	r := semaTable[s]
	if r == nil {
		r = &semaRoot{}
		semaTable[s] = r
	}
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

// Linkname aliases matching sync/runtime.go names.
func runtime_Semacquire(s *uint32)                 { semacquire(s) }
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
