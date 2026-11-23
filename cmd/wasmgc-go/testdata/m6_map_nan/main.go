package main

func main() {
	m := make(map[float64]int)
	nan := 0.0
	nan = nan / nan // NaN
	m[nan] = 1
	m[nan] = 2 // second NaN is a distinct entry
	_, ok := m[nan]
	if ok {
		println("found")
	} else {
		println("notfound")
	}
	println(len(m))
}
