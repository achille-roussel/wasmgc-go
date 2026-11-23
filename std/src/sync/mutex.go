package sync

import "runtime"

// A Mutex is a mutual exclusion lock. The zero value is an unlocked mutex.
type Mutex struct {
	locked uint32
	wait   uint32
}

// Lock locks m.
func (m *Mutex) Lock() {
	if m.locked == 0 {
		m.locked = 1
		return
	}
	for {
		m.wait = 0
		runtime.Semacquire(&m.wait)
		if m.locked == 0 {
			m.locked = 1
			return
		}
	}
}

// Unlock unlocks m.
func (m *Mutex) Unlock() {
	m.locked = 0
	runtime.Semrelease(&m.wait)
}

// TryLock tries to lock m and reports whether it succeeded.
func (m *Mutex) TryLock() bool {
	if m.locked == 0 {
		m.locked = 1
		return true
	}
	return false
}

// Locker is the interface for types that can be locked and unlocked.
type Locker interface {
	Lock()
	Unlock()
}
