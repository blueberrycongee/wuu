package cron

import (
	"os"
	"path/filepath"
	"testing"
)

func TestLock_AcquireAndRelease(t *testing.T) {
	dir := t.TempDir()
	lockPath := filepath.Join(dir, "sched.lock")

	lock := NewLock(lockPath, "session-a")
	acquired, err := lock.TryAcquire()
	if err != nil {
		t.Fatalf("TryAcquire error: %v", err)
	}
	if !acquired {
		t.Fatal("expected to acquire lock")
	}

	acquired2, err := lock.TryAcquire()
	if err != nil {
		t.Fatalf("idempotent TryAcquire error: %v", err)
	}
	if !acquired2 {
		t.Fatal("expected idempotent re-acquire to succeed")
	}

	lock2 := NewLock(lockPath, "session-b")
	acquired3, err := lock2.TryAcquire()
	if err != nil {
		t.Fatalf("second session TryAcquire error: %v", err)
	}
	if acquired3 {
		t.Fatal("expected second session to fail acquiring lock")
	}

	lock.Release()

	acquired4, err := lock2.TryAcquire()
	if err != nil {
		t.Fatalf("post-release TryAcquire error: %v", err)
	}
	if !acquired4 {
		t.Fatal("expected second session to acquire after release")
	}
}

func TestLock_StaleRecovery(t *testing.T) {
	dir := t.TempDir()
	lockPath := filepath.Join(dir, "sched.lock")

	os.WriteFile(lockPath, []byte(`{"sessionId":"old","pid":99999,"acquiredAt":1}`), 0o644)

	lock := NewLock(lockPath, "new-session")
	acquired, err := lock.TryAcquire()
	if err != nil {
		t.Fatalf("TryAcquire error: %v", err)
	}
	if !acquired {
		t.Fatal("expected to recover stale lock")
	}
}
