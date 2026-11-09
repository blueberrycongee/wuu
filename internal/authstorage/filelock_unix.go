//go:build !windows

package authstorage

import (
	"os"
	"syscall"

	"github.com/blueberrycongee/wuu/internal/securefs"
)

func lockFile(path string) (func(), error) {
	file, err := securefs.OpenFile(path, os.O_CREATE|os.O_RDWR, securefs.FileMode)
	if err != nil {
		return nil, err
	}
	if err := syscall.Flock(int(file.Fd()), syscall.LOCK_EX); err != nil {
		_ = file.Close()
		return nil, err
	}
	return func() {
		_ = syscall.Flock(int(file.Fd()), syscall.LOCK_UN)
		_ = file.Close()
	}, nil
}
