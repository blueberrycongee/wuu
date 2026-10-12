//go:build windows

package runtime

import (
	"context"
	"errors"
	"os"

	"golang.org/x/sys/windows"
)

func acquireOSFileLock(ctx context.Context, file *os.File) (func(), error) {
	overlapped := &windows.Overlapped{}
	handle := windows.Handle(file.Fd())
	for {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		err := windows.LockFileEx(handle, windows.LOCKFILE_EXCLUSIVE_LOCK|windows.LOCKFILE_FAIL_IMMEDIATELY, 0, 1, 0, overlapped)
		if err == nil {
			return func() { _ = windows.UnlockFileEx(handle, 0, 1, 0, overlapped) }, nil
		}
		if !errors.Is(err, windows.ERROR_LOCK_VIOLATION) {
			return nil, err
		}
		if err := waitFileLockRetry(ctx); err != nil {
			return nil, err
		}
	}
}
