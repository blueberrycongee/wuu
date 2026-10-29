//go:build !unix

package tools

import "os"

// Existing targets are unsupported when this host cannot prove link isolation.
func singleFileActionLink(os.FileInfo) bool { return false }
