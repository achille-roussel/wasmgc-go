package main

func seq(yield func(int) bool) {
	if !yield(1) {
		return
	}
	if !yield(2) {
		return
	}
}

func main() {
	for x := range seq {
		println(x)
	}
}
