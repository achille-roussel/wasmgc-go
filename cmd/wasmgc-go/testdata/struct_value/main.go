package main

type S struct{ X int }

func set(s S) { s.X = 99 }

func main() {
	s := S{X: 1}
	set(s)
	println(s.X)
}
