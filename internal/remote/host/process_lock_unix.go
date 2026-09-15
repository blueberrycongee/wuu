//go:build !windows

package host

import (
	"errors"
	"os"
	"syscall"

	"github.com/blueberrycongee/wuu/internal/securefs"
)

func lockHostStore(path string) (func(), error) {
	file, err := securefs.OpenFile(path+".host.lock", os.O_CREATE|os.O_RDWR, securefs.FileMode)
	if err != nil {
		return nil, err
	}
	if err := syscall.Flock(int(file.Fd()), syscall.LOCK_EX|syscall.LOCK_NB); err != nil {
		_ = file.Close()
		if errors.Is(err, syscall.EWOULDBLOCK) || errors.Is(err, syscall.EAGAIN) {
			return nil, ErrHostAlreadyRunning
		}
		return nil, err
	}
	// Keep the inode in place: unlinking it would let another process lock a
	// replacement while an existing owner still holds the original inode.
	return func() { _ = file.Close() }, nil
}
