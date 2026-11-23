package main

import "reflect"

type T struct {
	Name string `json:"name"`
}

func main() {
	t := reflect.TypeOf(T{})
	tag := t.Field(0).Tag.Get("json")
	println(tag)

	p := reflect.New(t)
	e := p.Elem()
	f := e.Field(0)
	f.SetString("ok")
	println(f.String())
}
