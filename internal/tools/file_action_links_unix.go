//go:build unix

package tools

import (
	"os"
	"syscall"
)

func singleFileActionLink(info os.FileInfo) bool {
	stat, ok := info.Sys().(*syscall.Stat_t)
	return ok && stat.Nlink == 1
}
