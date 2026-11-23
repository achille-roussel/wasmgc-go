package main

import (
	"encoding/json"
	"fmt"
)

// map[int]string exercises SliceOf/MapOf in encoding/json/v2/arshal_default.go
// (non-string key path; docs/design/05, 09).
func main() {
	m := map[int]string{1: "one", 2: "two"}
	b, err := json.Marshal(m)
	if err != nil {
		panic(err)
	}
	s := string(b)

	var n map[int]string
	if err := json.Unmarshal([]byte(s), &n); err != nil {
		panic(err)
	}
	if n[1] != "one" || n[2] != "two" {
		panic("round-trip mismatch: " + s)
	}
	fmt.Println(n[1])
	fmt.Println(n[2])
	fmt.Println(len(n))
}
