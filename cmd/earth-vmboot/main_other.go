//go:build !linux

// Command earth-vmboot is PID 1 inside a Firecracker microVM. It is a Linux
// guest and builds nowhere else; this exists so the package still compiles when
// the tree is built for another platform.
package main

import (
	"fmt"
	"os"
)

func main() {
	fmt.Fprintln(os.Stderr, "earth-vmboot runs as PID 1 inside a Linux microVM")
	os.Exit(1)
}
