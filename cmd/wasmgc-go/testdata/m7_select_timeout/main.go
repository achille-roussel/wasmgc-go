package main

import "time"

func main() {
	ch := make(chan int)
	select {
	case <-ch:
		println("recv")
	case <-time.After(1 * time.Millisecond):
		println("timeout")
	}
}
