package main

import "os"

func main() {
	err := os.Write("/tmp/x", []byte("hi"), 0644)
	if err != nil {
		println(1)
	} else {
		println(0)
	}
}
