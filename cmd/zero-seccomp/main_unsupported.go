//go:build !linux

package main

import (
	"fmt"
	"os"
)

func main() {
	fmt.Fprintln(os.Stderr, "zero-seccomp is only supported on Linux")
	os.Exit(2)
}
