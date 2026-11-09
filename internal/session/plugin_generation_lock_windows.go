//go:build windows

package session

import (
	"errors"
	"os"

	"golang.org/x/sys/windows"
)

// Windows byte-range locks also restrict I/O by the lock owner. Keep the lock
// beyond the eight-byte epoch so catalog writers can advance it while holding
// a shared lease and watchers can read it while an exclusive lease is held.
const pluginGenerationLockOffset = 8

func tryLockPluginGenerationFile(file *os.File, exclusive bool) (bool, error) {
	flags := uint32(windows.LOCKFILE_FAIL_IMMEDIATELY)
	if exclusive {
		flags |= windows.LOCKFILE_EXCLUSIVE_LOCK
	}
	err := windows.LockFileEx(windows.Handle(file.Fd()), flags, 0, 1, 0, &windows.Overlapped{Offset: pluginGenerationLockOffset})
	if err == nil {
		return true, nil
	}
	if errors.Is(err, windows.ERROR_LOCK_VIOLATION) {
		return false, nil
	}
	return false, err
}

func unlockPluginGenerationFile(file *os.File) error {
	return windows.UnlockFileEx(windows.Handle(file.Fd()), 0, 1, 0, &windows.Overlapped{Offset: pluginGenerationLockOffset})
}
