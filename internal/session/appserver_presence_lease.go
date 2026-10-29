package session

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"

	"github.com/blueberrycongee/wuu/internal/securefs"
)

const (
	appServerStartupGateFile = ".appserver-startup.lock"
	appServerPresenceFile    = ".appserver-presence.lock"
)

// AppServerPresenceLease holds a shared presence lock for one live app-server.
// Startup is serialized by a separate gate so the first process can settle
// crash leftovers under an exclusive presence lock before any peer becomes
// visible.
type AppServerPresenceLease struct {
	mu        sync.Mutex
	gate      *os.File
	presence  *os.File
	first     bool
	finalized bool
	directory string
}

// AcquireAppServerPresence enters the serialized startup section. first is
// true only when no finalized app-server currently holds shared presence. The
// caller must perform first-process recovery, then call FinalizeStartup before
// serving requests.
func AcquireAppServerPresence(sessDir string) (*AppServerPresenceLease, bool, error) {
	sessDir = strings.TrimSpace(sessDir)
	if sessDir == "" {
		return nil, false, errors.New("session directory is required")
	}
	resolved, err := filepath.Abs(sessDir)
	if err != nil {
		return nil, false, err
	}
	if err := securefs.Mkdir(resolved); err != nil {
		return nil, false, err
	}
	resolved, err = filepath.EvalSymlinks(resolved)
	if err != nil {
		return nil, false, err
	}
	sessDir = resolved
	gate, err := securefs.OpenFile(filepath.Join(sessDir, appServerStartupGateFile), os.O_CREATE|os.O_RDWR, securefs.FileMode)
	if err != nil {
		return nil, false, fmt.Errorf("open app-server startup gate: %w", err)
	}
	if err := lockAppServerStartupGate(gate); err != nil {
		_ = gate.Close()
		return nil, false, fmt.Errorf("lock app-server startup gate: %w", err)
	}
	presence, err := securefs.OpenFile(filepath.Join(sessDir, appServerPresenceFile), os.O_CREATE|os.O_RDWR, securefs.FileMode)
	if err != nil {
		_ = unlockAppServerPresenceFile(gate)
		_ = gate.Close()
		return nil, false, fmt.Errorf("open app-server presence: %w", err)
	}
	first, err := tryLockAppServerPresenceExclusive(presence)
	if err != nil {
		_ = presence.Close()
		_ = unlockAppServerPresenceFile(gate)
		_ = gate.Close()
		return nil, false, fmt.Errorf("inspect app-server presence: %w", err)
	}
	if !first {
		if err := lockAppServerPresenceShared(presence); err != nil {
			_ = presence.Close()
			_ = unlockAppServerPresenceFile(gate)
			_ = gate.Close()
			return nil, false, fmt.Errorf("join app-server presence: %w", err)
		}
	}
	return &AppServerPresenceLease{gate: gate, presence: presence, first: first, directory: resolved}, first, nil
}

// WithExclusiveStartup reuses this exact unfinalized first-host capability
// during nested initialization. It neither downgrades nor releases presence.
// Release and FinalizeStartup wait until use returns; use must not call them.
// Directory and inode checks reject a moved/rebound lock path, not merely a
// caller-provided claim that writers were stopped.
func (l *AppServerPresenceLease) WithExclusiveStartup(sessDir string, use func() error) error {
	if l == nil || use == nil {
		return errors.New("exclusive startup capability is required")
	}
	l.mu.Lock()
	defer l.mu.Unlock()
	if !l.first || l.finalized || l.gate == nil || l.presence == nil {
		return errors.New("exclusive startup capability is not active")
	}
	if strings.TrimSpace(sessDir) == "" {
		return errors.New("exclusive startup directory is required")
	}
	resolved, err := filepath.Abs(strings.TrimSpace(sessDir))
	if err != nil {
		return err
	}
	resolved, err = filepath.EvalSymlinks(resolved)
	if err != nil {
		return err
	}
	if resolved != l.directory {
		return errors.New("exclusive startup capability belongs to another directory")
	}
	for _, locked := range []struct {
		file *os.File
		name string
	}{{l.gate, appServerStartupGateFile}, {l.presence, appServerPresenceFile}} {
		held, err := locked.file.Stat()
		if err != nil {
			return err
		}
		current, err := os.Stat(filepath.Join(resolved, locked.name))
		if err != nil {
			return err
		}
		if !os.SameFile(held, current) {
			return errors.New("exclusive startup lock path was rebound")
		}
	}
	return use()
}

// FinalizeStartup converts the first process's exclusive lock to shared
// presence and releases the startup gate. The gate makes the unlock/relock
// conversion safe on platforms without an atomic lock downgrade.
func (l *AppServerPresenceLease) FinalizeStartup() error {
	if l == nil {
		return nil
	}
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.finalized {
		return nil
	}
	if l.presence == nil || l.gate == nil {
		return errors.New("app-server presence startup is not active")
	}
	if l.first {
		if err := unlockAppServerPresenceFile(l.presence); err != nil {
			return fmt.Errorf("downgrade app-server presence: %w", err)
		}
		if err := lockAppServerPresenceShared(l.presence); err != nil {
			return fmt.Errorf("acquire shared app-server presence: %w", err)
		}
	}
	gate := l.gate
	l.gate = nil
	if err := unlockAppServerPresenceFile(gate); err != nil {
		_ = gate.Close()
		return fmt.Errorf("release app-server startup gate: %w", err)
	}
	if err := gate.Close(); err != nil {
		return fmt.Errorf("close app-server startup gate: %w", err)
	}
	l.finalized = true
	l.first = false
	return nil
}

// Release leaves live presence. It is safe to call more than once and also
// cleans up a startup lease whose FinalizeStartup failed.
func (l *AppServerPresenceLease) Release() error {
	if l == nil {
		return nil
	}
	l.mu.Lock()
	defer l.mu.Unlock()
	var releaseErr error
	if l.presence != nil {
		presence := l.presence
		l.presence = nil
		releaseErr = errors.Join(releaseErr, unlockAppServerPresenceFile(presence), presence.Close())
	}
	if l.gate != nil {
		gate := l.gate
		l.gate = nil
		releaseErr = errors.Join(releaseErr, unlockAppServerPresenceFile(gate), gate.Close())
	}
	return releaseErr
}
