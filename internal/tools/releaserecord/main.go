package main

import (
	"fmt"
	"os"
)

func main() {
	dir := "dist/chart"
	if len(os.Args) > 1 {
		dir = os.Args[1]
	}
	size, err := measure(dir)
	if err != nil {
		fmt.Fprintln(os.Stderr, "releaserecord:", err)
		os.Exit(1)
	}
	msg, ok := verdict(size)
	if !ok {
		fmt.Fprintln(os.Stderr, "releaserecord:", msg)
		os.Exit(1)
	}
	fmt.Println(msg)
}
