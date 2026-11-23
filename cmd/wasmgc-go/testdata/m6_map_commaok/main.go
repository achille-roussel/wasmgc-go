package main

func main() {
	m := make(map[int]int)
	m[1] = 7
	v, ok := m[1]
	println(v)
	if ok {
		println(1)
	} else {
		println(0)
	}
	v, ok = m[2]
	println(v)
	if ok {
		println(1)
	} else {
		println(0)
	}
}
