package main

import (
	"net/http"
	"os"
)

func main() {
	url := os.Args[0]
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
