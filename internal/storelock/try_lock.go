package storelock

import (
	"errors"
	"github.com/blueberrycongee/wuu/internal/securefs"
	"os"
	"path/filepath"
	"strings"
)

// TryAcquire checks ownership once without waiting. A held lock returns
// (nil,false,nil); only the OS lock decides liveness, never a timestamp or PID.
func TryAcquire(dir string) (*Lock, bool, error) {
	if strings.TrimSpace(dir) == "" {
		return nil, false, errors.New("store directory is required")
	}
	path, err := filepath.Abs(filepath.Join(dir, FileName))
	if err != nil {
		return nil, false, err
	}
	file, err := securefs.OpenFile(path, os.O_CREATE|os.O_RDWR, securefs.FileMode)
	if err != nil {
		return nil, false, err
	}
	waiters := retainLockWaiters(path)
	if err := tryLockFile(file); err != nil {
		file.Close()
		waiters.release(false)
		if errors.Is(err, errLockHeld) {
			return nil, false, nil
		}
		return nil, false, err
	}
	return &Lock{file: file, waiters: waiters}, true, nil
}
