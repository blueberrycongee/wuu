//go:build windows

package session

import (
	"errors"
	"os"

	"golang.org/x/sys/windows"
)

func lockAppServerStartupGate(file *os.File) error {
	return lockAppServerPresence(file, true, false)
}

func lockAppServerPresenceShared(file *os.File) error {
	return lockAppServerPresence(file, false, false)
}

func tryLockAppServerPresenceExclusive(file *os.File) (bool, error) {
	err := lockAppServerPresence(file, true, true)
	if err == nil {
		return true, nil
	}
	if errors.Is(err, windows.ERROR_LOCK_VIOLATION) {
		return false, nil
	}
	return false, err
}

func lockAppServerPresence(file *os.File, exclusive, immediate bool) error {
	flags := uint32(0)
	if exclusive {
		flags |= windows.LOCKFILE_EXCLUSIVE_LOCK
	}
	if immediate {
		flags |= windows.LOCKFILE_FAIL_IMMEDIATELY
	}
	return windows.LockFileEx(windows.Handle(file.Fd()), flags, 0, 1, 0, &windows.Overlapped{})
}

func unlockAppServerPresenceFile(file *os.File) error {
	return windows.UnlockFileEx(windows.Handle(file.Fd()), 0, 1, 0, &windows.Overlapped{})
}
