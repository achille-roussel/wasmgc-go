package main

import (
	"encoding/json"
	"fmt"
)

type Person struct {
	Name string `json:"name"`
	Age  int    `json:"age"`
}

func main() {
	p := Person{Name: "alice", Age: 30}
	b, err := json.Marshal(p)
	if err != nil {
		panic(err)
	}
	fmt.Println(string(b))

	var q Person
	if err := json.Unmarshal(b, &q); err != nil {
		panic(err)
	}
	fmt.Println(q.Name)
	fmt.Println(q.Age)
}
