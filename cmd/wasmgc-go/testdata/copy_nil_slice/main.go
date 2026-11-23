package main

func main() {
	buf := make([]byte, 0, 3)
	var b []byte
	println(copy(buf, b))
	buf2 := make([]byte, 2)
	println(copy(buf2, []byte("hi")))
}
