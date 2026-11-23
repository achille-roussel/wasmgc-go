package main

// Package-level string([]byte{…}) exercises the synthetic package-var init
// path that must zero-init non-defaultable $go.string temps (utf8_test).
var invalid = string([]byte{66, 250})

func main() {
	println(len(invalid))
	println(invalid[0] == 66)
}
