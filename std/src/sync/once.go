package sync

// Once is an object that will perform exactly one action.
type Once struct {
	done uint32
	m    Mutex
}

// Do calls the function f if and only if Do is being called for the
// first time for this instance of Once.
func (o *Once) Do(f func()) {
	if o.done != 0 {
		return
	}
	o.m.Lock()
	defer o.m.Unlock()
	if o.done == 0 {
		defer func() { o.done = 1 }()
		f()
	}
}
