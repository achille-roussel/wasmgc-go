package main

type Speaker interface{ Speak() string }

type Inner struct{}

func (Inner) Speak() string { return "hi" }

type Outer struct{ Inner }

func main() {
	var s Speaker = Outer{}
	println(s.Speak())
}
