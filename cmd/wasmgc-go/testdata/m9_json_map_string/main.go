package main

import (
	"encoding/json"
	"fmt"
)

func main() {
	m := map[string]int{"a": 1, "b": 2}
	b, err := json.Marshal(m)
	if err != nil {
		panic(err)
	}
	s := string(b)
	// Map key order is not guaranteed; accept either order by unmarshaling.
	var n map[string]int
	if err := json.Unmarshal(b, &n); err != nil {
		panic(err)
	}
	if n["a"] != 1 || n["b"] != 2 {
		panic("round-trip mismatch: " + s)
	}
	fmt.Println(n["a"])
	fmt.Println(n["b"])
	fmt.Println(len(n))
}
