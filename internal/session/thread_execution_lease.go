package session

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/blueberrycongee/wuu/internal/securefs"
)

const threadExecutionLeaseDirName = ".turn-locks"

// ErrProjectWorkerCapacity means another managed worker must release execution
// ownership before a new worker can be admitted. The caller may retry.
var ErrProjectWorkerCapacity = errors.New("project worker admission capacity is full")

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

func threadExecutionLeasePath(sessDir, threadID string) string {
	digest := sha256.Sum256([]byte(strings.TrimSpace(threadID)))
	return filepath.Join(sessDir, threadExecutionLeaseDirName, hex.EncodeToString(digest[:])+".lock")
}
