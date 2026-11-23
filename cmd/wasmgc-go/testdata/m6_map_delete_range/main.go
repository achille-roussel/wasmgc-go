package main

func main() {
	m := make(map[int]int)
	for i := 0; i < 5; i++ {
		m[i] = i * 10
	}
	// Delete the current key during range (Go-safe). Snapshot keeps
	// iteration over the pre-growth table; tombstones skip deleted slots.
	n := 0
	sum := 0
	for k, v := range m {
		delete(m, k)
		n++
		sum += v
	}
	println(n)
	println(sum)
	println(len(m))
}
