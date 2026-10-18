package main

import (
	"fmt"
	"os"

	"github.com/blueberrycongee/wuu/internal/cualinux"
)

func main() {
	if err := cualinux.Run(os.Args[1:], os.Stdin, os.Stdout); err != nil {
		fmt.Fprintln(os.Stderr, "wuu-cua-linux:", err)
		os.Exit(1)
	}
}
