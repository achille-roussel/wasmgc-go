package main

func main() {
	ch := make(chan int)
	go func() {
		ch <- 7
	}()
	select {
	case v, ok := <-ch:
		if ok {
			println(v)
		} else {
			println(0)
		}
	}
}
