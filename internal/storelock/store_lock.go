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

// Ordinary transactions should finish within acquireTimeout. Full-store
// verification uses AcquireUntilCanceled because its duration scales with data.
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
	mu      sync.Mutex
	file    *os.File
	waiters *lockWaiters
}

// Acquire blocks until it exclusively owns the durable store rooted at dir,
// or fails after acquireTimeout. Use AcquireUntilCanceled when the same fence
// also protects long-running verification or maintenance transactions.
func Acquire(dir string) (*Lock, error) {
	return AcquireContext(context.Background(), dir)
}

// AcquireContext observes cancellation while waiting for another writer. A
// successful acquisition belongs to the caller until Release, even if ctx ends.
func AcquireContext(ctx context.Context, dir string) (*Lock, error) {
	return acquire(ctx, dir, acquireTimeout)
}

// AcquireUntilCanceled waits for long transactions such as full-store admission
// verification. Only the caller's context limits the wait; elapsed time cannot
// distinguish a healthy large traversal from a stalled owner. The OS releases
// ownership if the owner exits, and cancellation never steals the lock.
func AcquireUntilCanceled(ctx context.Context, dir string) (*Lock, error) {
	return acquire(ctx, dir, 0)
}

func acquire(ctx context.Context, dir string, timeout time.Duration) (*Lock, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	dir = strings.TrimSpace(dir)
	if dir == "" {
		return nil, errors.New("store directory is required")
	}
	path, err := filepath.Abs(filepath.Join(dir, FileName))
	if err != nil {
		return nil, err
	}
	file, err := securefs.OpenFile(path, os.O_CREATE|os.O_RDWR, securefs.FileMode)
	if err != nil {
		return nil, fmt.Errorf("open store lock: %w", err)
	}
	waiters := retainLockWaiters(path)
	acquired := false
	defer func() {
		if !acquired {
			waiters.release(false)
		}
	}()
	deadline := time.Now().Add(timeout)
	for {
		if err := ctx.Err(); err != nil {
			_ = file.Close()
			return nil, err
		}
		// Subscribe before attempting the OS lock so a release between a failed
		// attempt and the wait cannot be lost.
		released := waiters.nextRelease()
		err := tryLockFile(file)
		if err == nil {
			acquired = true
			return &Lock{file: file, waiters: waiters}, nil
		}
		if !errors.Is(err, errLockHeld) {
			_ = file.Close()
			return nil, fmt.Errorf("lock store: %w", err)
		}
		if timeout > 0 && !time.Now().Before(deadline) {
			_ = file.Close()
			return nil, fmt.Errorf("lock store: %s held by another owner for over %s", path, timeout)
		}
		timer := time.NewTimer(acquireRetryDelay)
		select {
		case <-ctx.Done():
			timer.Stop()
			_ = file.Close()
			return nil, ctx.Err()
		case <-released:
			timer.Stop()
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
	err := errors.Join(unlockFile(file), file.Close())
	l.waiters.release(true)
	return err
}
