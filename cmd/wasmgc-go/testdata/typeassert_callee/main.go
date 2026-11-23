package main

// Call through a type-asserted func value: f.(func(T))(x).
// Same shape as reflect.Value.Interface().(func(...))(...).

func main() {
	var f any = func(x int) int { return x + 1 }
	println(f.(func(int) int)(41))
}
