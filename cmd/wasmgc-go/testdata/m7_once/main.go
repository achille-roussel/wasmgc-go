package main

import "sync"

func main() {
	var once sync.Once
	n := 0
	f := func() { n++ }
	once.Do(f)
	once.Do(f)
	once.Do(f)
	println(n)
}
