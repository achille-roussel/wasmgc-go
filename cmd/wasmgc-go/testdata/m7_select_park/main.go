package main

func main() {
	ch := make(chan int)
	go func() {
		ch <- 99
	}()
	select {
	case v := <-ch:
		println(v)
	}
}
