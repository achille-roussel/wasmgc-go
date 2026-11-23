package main

func main() {
	ch := make(chan int, 1)
	ch <- 42
	v := <-ch
	println(v)
}
