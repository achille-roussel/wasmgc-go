package main

func main() {
	ch := make(chan int)
	go func() {
		ch <- 7
	}()
	println(<-ch)
}
