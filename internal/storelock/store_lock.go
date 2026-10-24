// Package storelock serializes durable file-store transactions across
// processes. Callers keep the returned lock for the complete write or
// read-modify-write transaction, including every related index and event-log
// mutation. Pure readers should not acquire it: the lock is exclusive, and
// Acquire creates the store directory and lock file on first use, so stores
// whose writers replace files atomically serve reads directly.
package storelock

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/blueberrycongee/wuu/internal/securefs"
)

// FileName is the retained advisory-lock inode in a store directory.
const FileName = ".store.lock"

// Healthy transactions hold the lock for milliseconds; an owner that keeps it
// past acquireTimeout is wedged, and waiting on it would wedge this process
// too, so Acquire surfaces an error instead.
const (
	acquireTimeout    = time.Minute
	acquireRetryDelay = 25 * time.Millisecond
)

// errLockHeld reports a lock currently owned elsewhere, distinguishing
// contention from I/O failures in the acquire loop.
var errLockHeld = errors.New("store lock is held by another owner")

// Lock owns an exclusive operating-system advisory lock. The sidecar remains
// in place after release so every contender continues locking the same inode.
type Lock struct {
	mu   sync.Mutex
	file *os.File
}

// Acquire blocks until it exclusively owns the durable store rooted at dir,
// or fails after acquireTimeout when another owner holds the lock for longer
// than any healthy transaction can.
func Acquire(dir string) (*Lock, error) {
	return AcquireContext(context.Background(), dir)
}

// AcquireContext observes cancellation while waiting for another writer. A
// successful acquisition belongs to the caller until Release, even if ctx ends.
func AcquireContext(ctx context.Context, dir string) (*Lock, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	dir = strings.TrimSpace(dir)
	if dir == "" {
		return nil, errors.New("store directory is required")
	}
	path := filepath.Join(dir, FileName)
	file, err := securefs.OpenFile(path, os.O_CREATE|os.O_RDWR, securefs.FileMode)
	if err != nil {
		return nil, fmt.Errorf("open store lock: %w", err)
	}
	deadline := time.Now().Add(acquireTimeout)
	for {
		if err := ctx.Err(); err != nil {
			_ = file.Close()
			return nil, err
		}
		err := tryLockFile(file)
		if err == nil {
			return &Lock{file: file}, nil
		}
		if !errors.Is(err, errLockHeld) {
			_ = file.Close()
			return nil, fmt.Errorf("lock store: %w", err)
		}
		if !time.Now().Before(deadline) {
			_ = file.Close()
			return nil, fmt.Errorf("lock store: %s held by another owner for over %s", path, acquireTimeout)
		}
		timer := time.NewTimer(acquireRetryDelay)
		select {
		case <-ctx.Done():
			timer.Stop()
			_ = file.Close()
			return nil, ctx.Err()
		case <-timer.C:
		}
	}
}

// Release relinquishes the lock. It is safe to call more than once.
func (l *Lock) Release() error {
	if l == nil {
		return nil
	}
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.file == nil {
		return nil
	}
	file := l.file
	l.file = nil
	return errors.Join(unlockFile(file), file.Close())
}
