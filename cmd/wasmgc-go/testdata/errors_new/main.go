package main

import "errors"

func main() {
	err := errors.New("boom")
	if err == nil {
		println("nil")
		return
	}
	println(err.Error())
}
