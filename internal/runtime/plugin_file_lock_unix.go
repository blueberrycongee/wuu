//go:build !windows

package runtime

import (
	"context"
	"errors"
	"os"
	"syscall"
)

func acquireOSFileLock(ctx context.Context, file *os.File) (func(), error) {
	for {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		err := syscall.Flock(int(file.Fd()), syscall.LOCK_EX|syscall.LOCK_NB)
		if err == nil {
			return func() {
				for errors.Is(syscall.Flock(int(file.Fd()), syscall.LOCK_UN), syscall.EINTR) {
				}
			}, nil
		}
		if errors.Is(err, syscall.EINTR) {
			continue
		}
		if !errors.Is(err, syscall.EWOULDBLOCK) && !errors.Is(err, syscall.EAGAIN) {
			return nil, err
		}
		if err := waitFileLockRetry(ctx); err != nil {
			return nil, err
		}
	}
}
