//go:build !windows

package main

import (
	"os"
	"syscall"
)

func openDesktopAdmissionPipe(fd uintptr) (*os.File, error) {
	// Inherited Node pipes are blocking. Register them with Go's poller so closing
	// the file interrupts an admission read when its timeout or context expires.
	if err := syscall.SetNonblock(int(fd), true); err != nil {
		_ = syscall.Close(int(fd))
		return nil, err
	}
	return os.NewFile(fd, "desktop-parent-admission"), nil
}
