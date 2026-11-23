package main

import "sync"

func main() {
	var wg sync.WaitGroup
	ch := make(chan int, 2)
	wg.Add(2)
	go func() {
		defer wg.Done()
		ch <- 1
	}()
	go func() {
		defer wg.Done()
		ch <- 2
	}()
	wg.Wait()
	println(<-ch + <-ch)
}
