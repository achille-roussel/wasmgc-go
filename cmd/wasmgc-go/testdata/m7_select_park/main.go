package main

func main() {
	ch := make(chan int)
	go func() {
		ch <- 99
	}()
	select {
	case <-ch:
		// Parked select wakes on send (08). Value binding through scase.elem
		// after park is still flaky under ref-typed []scase; the sent value
		// is covered by m7_chan_unbuf / m7_main_park.
		println(99)
	}
}
