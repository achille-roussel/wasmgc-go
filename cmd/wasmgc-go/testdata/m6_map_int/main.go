package main

func main() {
	m := make(map[int]int)
	m[1] = 10
	m[2] = 20
	m[3] = 30
	delete(m, 1)
	println(len(m))
	println(m[2])
	println(m[3])
}
