package main

import "maps"

func main() {
	m := map[string]int{"a": 1, "b": 2}
	n := 0
	for k := range maps.Keys(m) {
		if k == "a" || k == "b" {
			n++
		}
	}
	println(n)
	c := maps.Clone(m)
	println(len(c))
	s := 0
	for v := range maps.Values(m) {
		s += v
	}
	println(s)
}
