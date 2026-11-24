package sync

// A RWMutex is a reader/writer mutual exclusion lock.
// Thin wasmgc stub: writers and readers share one Mutex (no reader parallelism).
type RWMutex struct {
	w Mutex
}

// RLock locks rw for reading.
func (rw *RWMutex) RLock() { rw.w.Lock() }

// TryRLock tries to lock rw for reading.
func (rw *RWMutex) TryRLock() bool { return rw.w.TryLock() }

// RUnlock undoes a single RLock.
func (rw *RWMutex) RUnlock() { rw.w.Unlock() }

// Lock locks rw for writing.
func (rw *RWMutex) Lock() { rw.w.Lock() }

// TryLock tries to lock rw for writing.
func (rw *RWMutex) TryLock() bool { return rw.w.TryLock() }

// Unlock unlocks rw for writing.
func (rw *RWMutex) Unlock() { rw.w.Unlock() }

// RLocker returns a Locker interface that implements Lock/Unlock by calling
// RLock/RUnlock.
func (rw *RWMutex) RLocker() Locker { return (*rlocker)(rw) }

type rlocker RWMutex

func (r *rlocker) Lock()   { (*RWMutex)(r).RLock() }
func (r *rlocker) Unlock() { (*RWMutex)(r).RUnlock() }
