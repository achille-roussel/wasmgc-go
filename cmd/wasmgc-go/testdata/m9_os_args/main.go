package main

import "os"

func main() {
	if len(os.Args) == 0 {
		println("(none)")
		return
	}
	println(os.Args[0])
}
