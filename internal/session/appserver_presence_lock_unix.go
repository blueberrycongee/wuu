//go:build !windows

package session

import (
	"errors"
	"os"
	"syscall"
)

func lockAppServerStartupGate(file *os.File) error {
	return lockAppServerPresenceFile(file, syscall.LOCK_EX)
}

func lockAppServerPresenceShared(file *os.File) error {
	return lockAppServerPresenceFile(file, syscall.LOCK_SH)
}

func lockAppServerPresenceFile(file *os.File, mode int) error {
	for {
		err := syscall.Flock(int(file.Fd()), mode)
		if !errors.Is(err, syscall.EINTR) {
			return err
		}
	}
}

func tryLockAppServerPresenceExclusive(file *os.File) (bool, error) {
	for {
		err := syscall.Flock(int(file.Fd()), syscall.LOCK_EX|syscall.LOCK_NB)
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

func unlockAppServerPresenceFile(file *os.File) error {
	for {
		err := syscall.Flock(int(file.Fd()), syscall.LOCK_UN)
		if !errors.Is(err, syscall.EINTR) {
			return err
		}
	}
}
