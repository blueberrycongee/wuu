//go:build windows

package session

import (
	"os"

	"golang.org/x/sys/windows"
)

func lockThreadLifecycleFile(file *os.File) error {
	return windows.LockFileEx(
		windows.Handle(file.Fd()),
		windows.LOCKFILE_EXCLUSIVE_LOCK,
		0,
		1,
		0,
		&windows.Overlapped{},
	)
}

func unlockThreadLifecycleFile(file *os.File) error {
	return windows.UnlockFileEx(windows.Handle(file.Fd()), 0, 1, 0, &windows.Overlapped{})
}
