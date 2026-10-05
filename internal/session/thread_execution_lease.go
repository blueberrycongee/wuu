package session

import (
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"

	"github.com/blueberrycongee/wuu/internal/securefs"
)

const threadExecutionLeaseDirName = ".turn-locks"

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
	return TryAcquireThreadExecutionLeaseWithProjectLimit(sessDir, threadID, 0)
}

// ErrProjectWorkerCapacity means another managed worker must release execution
// ownership before a new worker can be admitted. The caller may retry.
var ErrProjectWorkerCapacity = errors.New("project worker admission capacity is full")

// TryAcquireThreadExecutionLeaseWithProjectLimit atomically admits execution
// within a project's worker capacity. Zero disables the project limit. Membership
// is read from durable storage; coordinator and legacy side sessions do not consume capacity.
// Existing leases (including mutations) conservatively occupy worker capacity.
func TryAcquireThreadExecutionLeaseWithProjectLimit(sessDir, threadID string, limit int) (*ThreadExecutionLease, bool, error) {
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
	// Hold the target before counting, so an already-owned target stays busy
	// rather than being misreported as project capacity. All execution contenders
	// serialize their membership snapshot, count and acquisition in this transaction.
	if limit > 0 {
		if err := checkProjectExecutionCapacity(tx, sessDir, threadID, limit); err != nil {
			_ = unlockThreadExecutionFile(file)
			_ = file.Close()
			_ = db.Close()
			return nil, false, err
		}
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

func checkProjectExecutionCapacity(tx *sql.Tx, sessDir, threadID string, limit int) error {
	var projectID string
	err := tx.QueryRow(`SELECT parent_id FROM sessions WHERE id=? AND source='project-session'
  AND project_role IN ('','worker','executor','technical_lead') AND parent_id<>'' AND archived_at IS NULL`, threadID).Scan(&projectID)
	if errors.Is(err, sql.ErrNoRows) {
		return nil
	}
	if err != nil {
		return fmt.Errorf("read project admission membership: %w", err)
	}
	rows, err := tx.Query(`SELECT id FROM sessions WHERE parent_id=? AND source='project-session'
  AND project_role IN ('','worker','executor','technical_lead') AND archived_at IS NULL AND id<>?`, projectID, threadID)
	if err != nil {
		return fmt.Errorf("list project admission members: %w", err)
	}
	defer rows.Close()
	occupied := 0
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			return err
		}
		active, err := ThreadExecutionActive(sessDir, id)
		if err != nil {
			return err
		}
		if active {
			occupied++
			if occupied >= limit {
				return fmt.Errorf("project %q worker limit %d reached: %w", projectID, limit, ErrProjectWorkerCapacity)
			}
		}
	}
	return rows.Err()
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

// ThreadExecutionActive reports whether any app-server currently owns the
// durable thread. It intentionally exposes only coarse availability, not owner
// identity or detailed execution phase.
func ThreadExecutionActive(sessDir, threadID string) (bool, error) {
	sessDir = strings.TrimSpace(sessDir)
	threadID = strings.TrimSpace(threadID)
	if sessDir == "" {
		return false, errors.New("session directory is required")
	}
	if threadID == "" {
		return false, errors.New("thread id is required")
	}
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
	if !acquired {
		_ = file.Close()
		return true, nil
	}
	releaseErr := errors.Join(unlockThreadExecutionFile(file), file.Close())
	if releaseErr != nil {
		return false, fmt.Errorf("release thread execution probe: %w", releaseErr)
	}
	return false, nil
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

func threadExecutionLeasePath(sessDir, threadID string) string {
	digest := sha256.Sum256([]byte(strings.TrimSpace(threadID)))
	return filepath.Join(sessDir, threadExecutionLeaseDirName, hex.EncodeToString(digest[:])+".lock")
}
