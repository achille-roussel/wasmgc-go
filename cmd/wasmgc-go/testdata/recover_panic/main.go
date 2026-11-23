package main

func main() {
	defer func() {
		r := recover()
		if r != nil {
			println(r.(string))
		}
	}()
	panic("boom")
}
