package main

import (
	"fmt"
	"math"
	"strconv"
)

func main() {
	fmt.Println(3.14, 1e21, -0.0, math.NaN(), math.Inf(1))

	var f64 float64 = 3.141592653589793
	var f32 float32 = 3.1415927
	fmt.Printf("%f %.3f %e %g %8.2f %x\n", f64, f64, f64, f64, f64, f64)
	fmt.Printf("%f %.3f %e %g %8.2f %x\n", f32, f32, f32, f32, f32, f32)

	for _, s := range []string{"1e-320", "0.1", "3.14", "-0", "1e21", "Inf", "NaN"} {
		v, err := strconv.ParseFloat(s, 64)
		if err != nil {
			fmt.Println("parse", s, err)
			continue
		}
		fmt.Println(strconv.FormatFloat(v, 'g', -1, 64))
	}

	// Non-constant format: verb prune must stay conservative.
	verb := "%g"
	fmt.Printf(verb+"\n", 2.5)

	type S struct{ X float64 }
	fmt.Println(fmt.Sprint(S{1.5}))
}
