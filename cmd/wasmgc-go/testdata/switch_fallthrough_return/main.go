package main

// Dense switch whose cases all return must still fall through for unmatched tags
// (strings.Repeat count switch).
func f(count int) string {
	switch count {
	case 0:
		return "z"
	case 1:
		return "o"
	}
	return "other"
}

func main() {
	println(f(0))
	println(f(1))
	println(f(3))
}
