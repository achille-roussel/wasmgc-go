package main

type S struct{ X int }

var p *int

func main() {
	s := S{X: 0}
	p = &s.X
	*p = 7
	println(s.X)
}
