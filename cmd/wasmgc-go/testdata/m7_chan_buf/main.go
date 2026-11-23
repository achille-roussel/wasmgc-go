package main

func main() {
	ch := make(chan int, 1)
	go func() {
		ch <- 7
	}()
	println(<-ch)
}
