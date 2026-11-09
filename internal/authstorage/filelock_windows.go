//go:build windows

package authstorage

import (
	"os"

	"github.com/blueberrycongee/wuu/internal/securefs"
	"golang.org/x/sys/windows"
)

func lockFile(path string) (func(), error) {
	file, err := securefs.OpenFile(path, os.O_CREATE|os.O_RDWR, securefs.FileMode)
	if err != nil {
		return nil, err
	}
	overlapped := &windows.Overlapped{}
	handle := windows.Handle(file.Fd())
	if err := windows.LockFileEx(handle, windows.LOCKFILE_EXCLUSIVE_LOCK, 0, 1, 0, overlapped); err != nil {
		_ = file.Close()
		return nil, err
	}
	return func() {
		_ = windows.UnlockFileEx(handle, 0, 1, 0, overlapped)
		_ = file.Close()
	}, nil
}
