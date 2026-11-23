package main

func main() {
	ch := make(chan int)
	close(ch)
	_, ok := <-ch
	if ok {
		println(0)
	} else {
		println(1)
	}
}
