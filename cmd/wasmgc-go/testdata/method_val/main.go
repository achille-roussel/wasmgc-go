package main

type T struct{ N int }

func (t T) Get() int { return t.N }

func main() {
	t := T{N: 7}
	f := t.Get
	println(f())
}
