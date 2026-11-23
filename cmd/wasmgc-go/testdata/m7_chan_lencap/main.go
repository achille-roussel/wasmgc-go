package main

func main() {
	ch := make(chan int, 4)
	ch <- 1
	ch <- 2
	println(len(ch))
	println(cap(ch))
}
