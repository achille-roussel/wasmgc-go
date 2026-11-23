package main

import "time"

func main() {
	before := time.Now()
	time.Sleep(50 * time.Millisecond)
	after := time.Now()
	if after.After(before) {
		println(1)
	} else {
		println(0)
	}
}
