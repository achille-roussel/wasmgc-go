package main

func main() {
	m := make(map[int]int)
	m[1] = 1
	m[2] = 2
	clear(m)
	println(len(m))
	println(m[1])
}
