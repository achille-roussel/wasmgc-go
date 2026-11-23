package main

func main() {
	go func() {
		panic("boom")
	}()
	// Park so the child runs under _sched.
	ch := make(chan int)
	<-ch
}
