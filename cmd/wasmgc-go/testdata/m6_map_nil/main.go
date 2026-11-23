package main

func main() {
	var m map[int]int
	println(len(m))
	println(m[1]) // nil read → zero
	// Nil write: expect unrecovered panic message on stderr, exit 2.
	m[1] = 1
}
