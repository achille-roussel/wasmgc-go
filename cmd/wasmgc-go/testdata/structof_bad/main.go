package main

import "reflect"

func main() {
	var fields []reflect.StructField
	_ = reflect.StructOf(fields)
}
