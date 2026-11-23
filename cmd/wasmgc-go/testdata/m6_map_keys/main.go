package main

type pair struct {
	A int
	B int
}

type empty struct{}

func main() {
	bm := make(map[bool]int)
	bm[true] = 1
	bm[false] = 2
	println(bm[true])
	println(bm[false])

	fm := make(map[float64]int)
	z := 0.0
	fm[z] = 7
	fm[-z] = 8
	println(len(fm))
	println(fm[z])

	sm := make(map[pair]int)
	sm[pair{1, 2}] = 9
	println(len(sm))
	println(sm[pair{1, 2}])

	vm := make(map[int]pair)
	vm[1] = pair{3, 4}
	println(vm[1].A)
	println(vm[1].B)

	em := make(map[int]empty)
	em[1] = empty{}
	_, ok := em[1]
	if ok {
		println(1)
	} else {
		println(0)
	}

	px := new(int)
	py := new(int)
	*px = 1
	*py = 1
	pm := make(map[*int]int)
	pm[px] = 11
	pm[py] = 12
	println(pm[px])
	println(pm[py])
}
