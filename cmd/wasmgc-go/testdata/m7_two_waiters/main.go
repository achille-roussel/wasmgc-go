package main

func main() {
	ch := make(chan int)
	out := make(chan int, 2)
	go func() { out <- <-ch }()
	go func() { out <- <-ch }()
	// Let both receivers park, then send in order.
	ch <- 10
	ch <- 20
	a := <-out
	b := <-out
	println(a + b)
}
