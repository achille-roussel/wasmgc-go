package main

import "reflect"

// Compile-ok program: SliceOf/MapOf the way encoding/json/v2 does
// (docs/design/05, 09 M9). Types are in B; results are the one-step universe.
func main() {
	m := map[int]string{1: "a"}
	t := reflect.TypeOf(m)
	_ = reflect.SliceOf(t.Key())
	_ = reflect.SliceOf(t.Elem())
	_ = reflect.PointerTo(t)
	empty := reflect.TypeFor[struct{}]()
	_ = reflect.MapOf(t.Key(), empty)
}
