package main

import "reflect"

func main() {
	// MakeMap + SetMapIndex + MapIndex for map[string]int
	mt := reflect.TypeOf(map[string]int(nil))
	m := reflect.MakeMap(mt)
	k := reflect.New(mt.Key()).Elem()
	k.SetString("a")
	v := reflect.New(mt.Elem()).Elem()
	v.SetInt(7)
	m.SetMapIndex(k, v)
	got := m.MapIndex(k)
	println(got.Int())

	// MakeSlice + Index set (map[int]string sort path)
	st := reflect.SliceOf(reflect.TypeOf(0))
	s := reflect.MakeSlice(st, 2, 2)
	s.Index(0).SetInt(3)
	s.Index(1).SetInt(1)
	println(s.Index(0).Int())
	println(s.Index(1).Int())
}
