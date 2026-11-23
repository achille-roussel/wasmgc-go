package main

func main() {
	defer func() {
		r := recover()
		if r != nil {
			println(r.(string))
		}
	}()
	defer func() {
		recover()
		panic("again")
	}()
	panic("first")
}
