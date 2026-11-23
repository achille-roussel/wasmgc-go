package main

func main() {
	m := make(map[int]int)
	m[1] = 10
	m[1] += 5
	m[1]++
	println(m[1])
	m[2] += 3 // missing key: read zero, then assign
	println(m[2])
}
