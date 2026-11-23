package main

func main() {
	ch := make(chan int)
	select {
	case <-ch:
		println("recv")
	default:
		println("default")
	}
}
