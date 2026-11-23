package sync

import "runtime"

// A Mutex is a mutual exclusion lock. The zero value is an unlocked mutex.
// Single-activation wasmgc: no spin; contested Lock parks via Semacquire (04).
type Mutex struct {
	state uint32 // 0 unlocked, 1 locked
	sema  uint32
}

// Lock locks m. If the lock is already in use, the calling goroutine parks
// until it becomes available.
func (m *Mutex) Lock() {
	if m.state == 0 {
		m.state = 1
		return
	}
	for {
		runtime.Semacquire(&m.sema)
		if m.state == 0 {
			m.state = 1
			return
		}
	}
}

// Unlock unlocks m.
func (m *Mutex) Unlock() {
	m.state = 0
	runtime.Semrelease(&m.sema)
}

// TryLock tries to lock m and reports whether it succeeded.
func (m *Mutex) TryLock() bool {
	if m.state == 0 {
		m.state = 1
		return true
	}
	return false
}

// Locker is the interface for types that can be locked and unlocked.
type Locker interface {
	Lock()
	Unlock()
}
