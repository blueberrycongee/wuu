//go:build !linux

package main

import (
	"fmt"
	"os"
)

func main() {
	fmt.Fprintln(os.Stderr, "wuu-cua-linux requires a Linux X11 desktop")
	os.Exit(1)
}
