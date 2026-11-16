package main

import (
	"fmt"
	"os"
)

func main() {
	size, err := measure("dist/chart")
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
