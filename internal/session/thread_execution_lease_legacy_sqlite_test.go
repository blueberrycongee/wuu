// Legacy SQLite fixture code is test-only. Production sessions use Store;
// explicit conversion reads legacy databases through legacysqlite.
package session

import (
	"database/sql"
	"errors"
	"fmt"
	"os"
	"strings"
	"sync"

	"github.com/blueberrycongee/wuu/internal/securefs"
)

// ThreadExecutionLease owns execution of one durable thread while its file
// descriptor holds an operating-system advisory lock. The sidecar remains in
// place after release so every contender continues locking the same inode.
type ThreadExecutionLease struct {
	mu              sync.Mutex
	file            *os.File
	db              *sql.DB
	threadID        string
	resetGeneration int64
}

// TryAcquireThreadExecutionLease attempts to become the sole executor of a
// durable thread across app-server instances and processes. A false acquired
// result means another live owner currently holds the lease.
func TryAcquireThreadExecutionLease(sessDir, threadID string) (*ThreadExecutionLease, bool, error) {
	sessDir = strings.TrimSpace(sessDir)
	threadID = strings.TrimSpace(threadID)
	if sessDir == "" {
		return nil, false, errors.New("session directory is required")
	}
	if threadID == "" {
		return nil, false, errors.New("thread id is required")
	}

	db, err := OpenStore(sessDir)
	if err != nil {
		return nil, false, fmt.Errorf("open thread execution control: %w", err)
	}
	tx, err := db.Begin()
	if err != nil {
		_ = db.Close()
		return nil, false, fmt.Errorf("begin thread execution admission: %w", err)
	}
	defer tx.Rollback()
	resetGeneration, err := threadExecutionResetGeneration(tx, threadID)
	if err != nil {
		_ = db.Close()
		return nil, false, err
	}

	path := threadExecutionLeasePath(sessDir, threadID)
	file, err := securefs.OpenFile(path, os.O_CREATE|os.O_RDWR, securefs.FileMode)
	if err != nil {
		_ = db.Close()
		return nil, false, fmt.Errorf("open thread execution lease: %w", err)
	}
	acquired, err := tryLockThreadExecutionFile(file)
	if err != nil {
		_ = file.Close()
		_ = db.Close()
		return nil, false, fmt.Errorf("lock thread execution lease: %w", err)
	}
	if !acquired {
		_ = file.Close()
		_ = db.Close()
		return nil, false, nil
	}

	if err := tx.Commit(); err != nil {
		_ = unlockThreadExecutionFile(file)
		_ = file.Close()
		_ = db.Close()
		return nil, false, fmt.Errorf("commit thread execution admission: %w", err)
	}
	return &ThreadExecutionLease{
		file: file, db: db, threadID: threadID, resetGeneration: resetGeneration,
	}, true, nil
}

// RequestThreadExecutionReset asks the live owner of threadID to interrupt its
// current Turn. It returns false when the thread is already idle. Admission and
// reset publication share a SQLite write transaction, so a request cannot race
// forward and cancel a later owner.
func RequestThreadExecutionReset(sessDir, threadID string) (bool, error) {
	sessDir = strings.TrimSpace(sessDir)
	threadID = strings.TrimSpace(threadID)
	if sessDir == "" {
		return false, errors.New("session directory is required")
	}
	if threadID == "" {
		return false, errors.New("thread id is required")
	}
	db, err := OpenStore(sessDir)
	if err != nil {
		return false, fmt.Errorf("open thread execution control: %w", err)
	}
	defer db.Close()
	tx, err := db.Begin()
	if err != nil {
		return false, fmt.Errorf("begin thread execution reset: %w", err)
	}
	defer tx.Rollback()

	path := threadExecutionLeasePath(sessDir, threadID)
	file, err := securefs.OpenFile(path, os.O_CREATE|os.O_RDWR, securefs.FileMode)
	if err != nil {
		return false, fmt.Errorf("open thread execution lease: %w", err)
	}
	acquired, err := tryLockThreadExecutionFile(file)
	if err != nil {
		_ = file.Close()
		return false, fmt.Errorf("probe thread execution lease: %w", err)
	}
	if acquired {
		releaseErr := errors.Join(unlockThreadExecutionFile(file), file.Close())
		if releaseErr != nil {
			return false, fmt.Errorf("release idle thread execution probe: %w", releaseErr)
		}
		if err := tx.Commit(); err != nil {
			return false, fmt.Errorf("commit idle thread execution probe: %w", err)
		}
		return false, nil
	}
	_ = file.Close()
	if _, err := tx.Exec(`
		INSERT INTO thread_execution_resets (thread_id, generation, requested_at)
		VALUES (?, 1, unixepoch('subsec') * 1000)
		ON CONFLICT(thread_id) DO UPDATE SET
			generation = thread_execution_resets.generation + 1,
			requested_at = excluded.requested_at`, threadID); err != nil {
		return false, fmt.Errorf("publish thread execution reset: %w", err)
	}
	if err := tx.Commit(); err != nil {
		return false, fmt.Errorf("commit thread execution reset: %w", err)
	}
	return true, nil
}

// ResetRequested reports whether a reset was published after this lease was
// admitted. A later owner snapshots the newer generation and is unaffected.
func (l *ThreadExecutionLease) ResetRequested() (bool, error) {
	if l == nil {
		return false, nil
	}
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.file == nil || l.db == nil {
		return false, nil
	}
	generation, err := threadExecutionResetGeneration(l.db, l.threadID)
	if err != nil {
		return false, err
	}
	return generation > l.resetGeneration, nil
}

type threadExecutionResetQuery interface {
	QueryRow(query string, args ...any) *sql.Row
}

func threadExecutionResetGeneration(q threadExecutionResetQuery, threadID string) (int64, error) {
	var generation int64
	err := q.QueryRow(`SELECT generation FROM thread_execution_resets WHERE thread_id = ?`, threadID).Scan(&generation)
	if errors.Is(err, sql.ErrNoRows) {
		return 0, nil
	}
	if err != nil {
		return 0, fmt.Errorf("read thread execution reset generation: %w", err)
	}
	return generation, nil
}

// Release relinquishes the lease. It is safe to call more than once.
func (l *ThreadExecutionLease) Release() error {
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
	db := l.db
	l.db = nil
	unlockErr := unlockThreadExecutionFile(file)
	closeErr := file.Close()
	var dbErr error
	if db != nil {
		dbErr = db.Close()
	}
	return errors.Join(unlockErr, closeErr, dbErr)
}
