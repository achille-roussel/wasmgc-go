package main

import "fmt"

func main() {
	fmt.Println(struct{ A int }{3})
}
