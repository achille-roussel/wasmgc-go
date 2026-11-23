package main

func main() {
	go func() {
		println("child")
	}()
	println("main")
}
