package sync

import "runtime"

// A WaitGroup waits for a collection of goroutines to finish.
type WaitGroup struct {
	counter uint32
	wait    uint32
}

// Add adds delta to the WaitGroup counter.
func (wg *WaitGroup) Add(delta int) {
	if delta > 0 {
		wg.counter += uint32(delta)
		return
	}
	for i := 0; i > delta; i-- {
		if wg.counter == 0 {
			panic("sync: negative WaitGroup counter")
		}
		wg.counter--
	}
	if wg.counter == 0 {
		runtime.Semrelease(&wg.wait)
	}
}

// Done decrements the WaitGroup counter by one.
func (wg *WaitGroup) Done() {
	wg.Add(-1)
}

// Wait blocks until the WaitGroup counter is zero.
func (wg *WaitGroup) Wait() {
	for wg.counter > 0 {
		wg.wait = 0
		runtime.Semacquire(&wg.wait)
	}
}
