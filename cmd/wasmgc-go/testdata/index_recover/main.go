package main

func main() {
	defer func() {
		r := recover()
		if r != nil {
			println(r.(string))
		}
	}()
	s := []int{1, 2, 3}
	println(s[5])
}
