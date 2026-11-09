//go:build windows

package host

import (
	"errors"
	"os"

	"github.com/blueberrycongee/wuu/internal/securefs"
	"golang.org/x/sys/windows"
)

func lockHostStore(path string) (func(), error) {
	file, err := securefs.OpenFile(path+".host.lock", os.O_CREATE|os.O_RDWR, securefs.FileMode)
	if err != nil {
		return nil, err
	}
	overlapped := &windows.Overlapped{}
	err = windows.LockFileEx(windows.Handle(file.Fd()), windows.LOCKFILE_EXCLUSIVE_LOCK|windows.LOCKFILE_FAIL_IMMEDIATELY, 0, 1, 0, overlapped)
	if err != nil {
		_ = file.Close()
		if errors.Is(err, windows.ERROR_LOCK_VIOLATION) {
			return nil, ErrHostAlreadyRunning
		}
		return nil, err
	}
	return func() { _ = file.Close() }, nil
}
