package main

func Max[T ~int | ~int64](a, b T) T {
	if a > b {
		return a
	}
	return b
}

type Pair[T any] struct{ X, Y T }

func (p Pair[T]) First() T { return p.X }

func Identity[T any](x T) T { return x }

func main() {
	println(Max(3, 7))
	println(Max[int64](1, 2))
	p := Pair[int]{X: 9, Y: 1}
	println(p.First())
	println(Identity(Identity(5)))
}
