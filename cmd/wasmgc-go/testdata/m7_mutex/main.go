package main

import "sync"

func main() {
	var mu sync.Mutex
	ch := make(chan int, 1)
	started := make(chan int)
	mu.Lock()
	go func() {
		started <- 1
		mu.Lock() // parks until main Unlock
		ch <- 1
		mu.Unlock()
	}()
	<-started // park so child runs and blocks on Lock
	mu.Unlock()
	println(<-ch)
}
