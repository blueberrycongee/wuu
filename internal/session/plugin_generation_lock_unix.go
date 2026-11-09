//go:build !windows

package session

import (
	"errors"
	"os"
	"syscall"
)

func tryLockPluginGenerationFile(file *os.File, exclusive bool) (bool, error) {
	mode := syscall.LOCK_SH | syscall.LOCK_NB
	if exclusive {
		mode = syscall.LOCK_EX | syscall.LOCK_NB
	}
	for {
		err := syscall.Flock(int(file.Fd()), mode)
		switch {
		case err == nil:
			return true, nil
		case errors.Is(err, syscall.EINTR):
			continue
		case errors.Is(err, syscall.EWOULDBLOCK), errors.Is(err, syscall.EAGAIN):
			return false, nil
		default:
			return false, err
		}
	}
}

func unlockPluginGenerationFile(file *os.File) error {
	for {
		err := syscall.Flock(int(file.Fd()), syscall.LOCK_UN)
		if !errors.Is(err, syscall.EINTR) {
			return err
		}
	}
}
