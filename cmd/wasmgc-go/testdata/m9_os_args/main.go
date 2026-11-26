package main

import "os"

func main() {
	// Args[0] is the program name; user args start at Args[1].
	if len(os.Args) < 2 {
		println("(none)")
		return
	}
	println(os.Args[1])
}
