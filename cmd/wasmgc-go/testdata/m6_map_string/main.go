package main

func main() {
	m := map[string]int{"a": 1, "b": 2}
	m["c"] = 2
	// Sort by printing in fixed key order so iteration order is irrelevant.
	println(m["a"])
	println(m["b"])
	println(m["c"])
}
