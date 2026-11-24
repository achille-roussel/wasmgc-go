package main

type I interface {
	m() int
}

type S struct{}

func (S) m() int { return 7 }

func main() {
	var i I = S{}
	f := I.m
	println(f(i))
	println(I.m(S{}))
	var c Closer = C{}
	defer c.Close()
}

type Closer interface{ Close() }
type C struct{}

func (C) Close() { println("closed") }
