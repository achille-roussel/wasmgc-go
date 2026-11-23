package main

func main() {
	x := 1
	f := func() { x = 2 }
	f()
	println(x)
}
