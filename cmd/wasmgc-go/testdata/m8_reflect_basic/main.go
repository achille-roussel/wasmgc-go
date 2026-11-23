package main

import "reflect"

type S struct{ A int }

func main() {
	var i any = 42
	t := reflect.TypeOf(i)
	v := reflect.ValueOf(i)
	println(t.Kind().String())
	println(t.String())
	println(v.Int())

	var s any = S{A: 3}
	sv := reflect.ValueOf(s)
	println(sv.NumField())
	println(sv.Field(0).Int())
	println(sv.Type().Field(0).Name)

	var str any = "hello"
	println(reflect.ValueOf(str).String())
}
