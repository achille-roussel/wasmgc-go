package main

import (
	"net/http"
	"os"
)

func main() {
	if len(os.Args) < 2 {
		println("err missing url")
		return
	}
	url := os.Args[1]
	resp, err := http.Get(url)
	if err != nil {
		println("err", err.Error())
		return
	}
	if resp == nil || resp.Body == nil {
		println("nil")
		return
	}
	println(resp.Body.String())
}
