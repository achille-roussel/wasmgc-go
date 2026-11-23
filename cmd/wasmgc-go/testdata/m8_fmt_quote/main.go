package main

import (
	"fmt"
	"strconv"
)

func main() {
	fmt.Printf("%q %q\n", "café", "\x00\n\t")
	fmt.Println(strconv.Quote("café"), strconv.Quote("\x00\n\t"))
	fmt.Printf("%q\n", "日本語")
	fmt.Println(strconv.QuoteRune('€'), strconv.QuoteRune('\x7f'))
}
