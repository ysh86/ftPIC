package main

import (
	"fmt"
)

func Mandelbrot() {
	for y := -12.0; y <= 12.0; y += 1.0 {
		for x := -39.0; x <= 39.0; x += 1.0 {
			ca := x * 0.0458
			cb := y * 0.08333
			a := ca
			b := cb
			chr := " "
			for i := 0; i < 16; i++ {
				t := a*a - b*b + ca
				b = 2.0*a*b + cb
				a = t
				if a*a+b*b > 4 {
					if i > 9 {
						i = i + 7
					}
					chr = string(rune(48 + i))
					break
				}
			}
			fmt.Print(chr)
		}
		fmt.Println("")
	}
}
