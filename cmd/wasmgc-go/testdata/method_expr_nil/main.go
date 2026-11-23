package main

type T int

func (t T) M(x *int) {
	println(int(t))
	if x != nil {
		println(*x)
	} else {
		println(0)
	}
}

func main() {
	var x int = 3
	T.M(T(5), &x)
	T.M(T(5), nil)
}
