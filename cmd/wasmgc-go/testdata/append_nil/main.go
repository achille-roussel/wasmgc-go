package main

func main() {
	var s []*int
	s = append(s, nil)
	println(len(s))
	var x int = 7
	s = append(s, &x)
	println(*s[1])
	var s2 []any
	s2 = append(s2, nil)
	if s2[0] == nil {
		println(1)
	} else {
		println(0)
	}
}
