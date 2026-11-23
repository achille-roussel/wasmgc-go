package main

// Exercises error → .(interface{Unwrap() error}): assert-site itab synthesis
// must emit $go.itab for the anonymous Unwrap iface × live concretes.

type base struct{ s string }

func (b *base) Error() string { return b.s }

type wrap struct {
	msg string
	err error
}

func (w *wrap) Error() string { return w.msg }
func (w *wrap) Unwrap() error { return w.err }

func main() {
	inner := error(&base{"inner"})
	err := error(&wrap{"outer", inner})
	u, ok := err.(interface{ Unwrap() error })
	if !ok {
		println("assert-fail")
		return
	}
	got := u.Unwrap()
	if got == nil {
		println("nil")
		return
	}
	println(got.Error())

	// Type switch to the same anonymous iface (errors.Is path).
	switch x := err.(type) {
	case interface{ Unwrap() error }:
		println(x.Unwrap().Error())
	default:
		println("switch-fail")
	}
}
