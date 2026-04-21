package cron

import (
	"encoding/json"
	"fmt"
	"os"
	"syscall"
	"time"
)

type LockFile struct {
	SessionID string `json:"sessionId"`
	PID       int    `json:"pid"`
	Acquired  int64  `json:"acquiredAt"`
}

type Lock struct {
	path      string
	sessionID string
}

func NewLock(path, sessionID string) *Lock {
	return &Lock{path: path, sessionID: sessionID}
}

func (l *Lock) TryAcquire() (bool, error) {
	body, _ := json.Marshal(LockFile{
		SessionID: l.sessionID,
		PID:       os.Getpid(),
		Acquired:  time.Now().UnixMilli(),
	})

	f, err := os.OpenFile(l.path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o644)
	if err == nil {
		_, err = f.Write(body)
		f.Close()
		return err == nil, err
	}
	if !os.IsExist(err) {
		return false, err
	}

	existing, readErr := l.readLock()
	if readErr != nil {
		os.Remove(l.path)
		return l.TryAcquire()
	}

	if existing.SessionID == l.sessionID {
		_ = os.WriteFile(l.path, body, 0o644)
		return true, nil
	}

	if existing.PID > 0 && isProcessRunning(existing.PID) {
		return false, nil
	}

	os.Remove(l.path)
	return l.TryAcquire()
}

func (l *Lock) Release() {
	existing, _ := l.readLock()
	if existing != nil && existing.SessionID == l.sessionID {
		os.Remove(l.path)
	}
}

func (l *Lock) readLock() (*LockFile, error) {
	data, err := os.ReadFile(l.path)
	if err != nil {
		return nil, err
	}
	var lf LockFile
	if err := json.Unmarshal(data, &lf); err != nil {
		return nil, err
	}
	return &lf, nil
}

func isProcessRunning(pid int) bool {
	data, err := os.ReadFile(fmt.Sprintf("/proc/%d/stat", pid))
	if err == nil && len(data) > 0 {
		return true
	}
	err = syscall.Kill(pid, 0)
	return err == nil
}
