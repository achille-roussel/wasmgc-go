package main

func main() {
	n := 0
	for _, r := range "a☺\x80" {
		n++
		println(r)
	}
	println(n)
}
