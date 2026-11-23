package main

import "reflect"

func dead() {
	var fields []reflect.StructField
	_ = reflect.StructOf(fields)
}

func main() {}
