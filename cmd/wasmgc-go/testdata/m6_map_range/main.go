package main

func firstKey() int {
	m := make(map[int]int)
	for i := 0; i < 8; i++ {
		m[i] = i
	}
	for k := range m {
		return k
	}
	return -1
}

func main() {
	// Iteration start is derived from fastrand per map; a fixed-order
	// implementation fails this check across enough trials.
	first0 := firstKey()
	diff := false
	for t := 0; t < 64; t++ {
		if firstKey() != first0 {
			diff = true
			break
		}
	}
	if !diff {
		println("fixed")
		return
	}
	println("ok")
}
