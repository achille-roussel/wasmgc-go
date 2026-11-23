package main

type E interface{ Error() string }

type A struct{ s string }

func (a *A) Error() string { return a.s }

type B struct{ n int }

func (b *B) Error() string { return "B" }

func classify(e E) string {
	var r string
	switch e.(type) {
	case *A:
		r = "a"
	case *B:
		r = "b"
	default:
		r = "other"
	}
	return r
}

func main() {
	println(classify(&A{"x"}))
	println(classify(&B{1}))
	var e E
	println(classify(e))
}
