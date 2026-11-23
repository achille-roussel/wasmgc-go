package main

type E interface{ Error() string }

type myErr struct{ s string }

func (e *myErr) Error() string { return e.s }

func main() {
	var e E
	if e == nil {
		println("nil")
	}
	e = &myErr{"boom"}
	if e != nil {
		println(e.Error())
	}
}
