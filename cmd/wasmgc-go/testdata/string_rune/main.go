package main

// string(rune) must link unicode.utf8.AppendRune without importing utf8.
func main() {
	println(string('A'))
	println(string(rune(0x4e16)))
}
