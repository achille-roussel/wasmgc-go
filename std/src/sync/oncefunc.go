package sync

// OnceFunc returns a function that invokes f only once.
func OnceFunc(f func()) func() {
	d := struct {
		f     func()
		once  Once
		valid bool
		p     any
	}{
		f: f,
	}
	return func() {
		d.once.Do(func() {
			defer func() {
				d.f = nil
				d.p = recover()
				if !d.valid {
					panic(d.p)
				}
			}()
			d.f()
			d.valid = true
		})
		if !d.valid {
			panic(d.p)
		}
	}
}

// OnceValue returns a function that invokes f only once and returns its value.
func OnceValue[T any](f func() T) func() T {
	d := struct {
		f      func() T
		once   Once
		valid  bool
		p      any
		result T
	}{
		f: f,
	}
	return func() T {
		d.once.Do(func() {
			defer func() {
				d.f = nil
				d.p = recover()
				if !d.valid {
					panic(d.p)
				}
			}()
			d.result = d.f()
			d.valid = true
		})
		if !d.valid {
			panic(d.p)
		}
		return d.result
	}
}

// OnceValues returns a function that invokes f only once and returns its values.
func OnceValues[T1, T2 any](f func() (T1, T2)) func() (T1, T2) {
	d := struct {
		f     func() (T1, T2)
		once  Once
		valid bool
		p     any
		r1    T1
		r2    T2
	}{
		f: f,
	}
	return func() (T1, T2) {
		d.once.Do(func() {
			defer func() {
				d.f = nil
				d.p = recover()
				if !d.valid {
					panic(d.p)
				}
			}()
			d.r1, d.r2 = d.f()
			d.valid = true
		})
		if !d.valid {
			panic(d.p)
		}
		return d.r1, d.r2
	}
}
