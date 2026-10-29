package session

import (
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"testing"
)

func TestExclusiveStartupCapabilityRejectsStaleAndForeignPresence(t *testing.T) {
	for _, failure := range []string{"foreign-directory", "released", "finalized", "rebound-gate", "rebound-presence"} {
		t.Run(failure, func(t *testing.T) {
			dir := t.TempDir()
			lease, first, err := AcquireAppServerPresence(dir)
			if err != nil || !first {
				t.Fatal("acquire", err)
			}
			defer lease.Release()
			target := dir
			switch failure {
			case "foreign-directory":
				target = t.TempDir()
			case "released":
				if err := lease.Release(); err != nil {
					t.Fatal(err)
				}
			case "finalized":
				if err := lease.FinalizeStartup(); err != nil {
					t.Fatal(err)
				}
			case "rebound-gate", "rebound-presence":
				if runtime.GOOS == "windows" {
					t.Skip("Windows does not replace an open locked file")
				}
				name := appServerStartupGateFile
				if failure == "rebound-presence" {
					name = appServerPresenceFile
				}
				path := filepath.Join(dir, name)
				if err := os.Rename(path, path+".old"); err != nil {
					t.Fatal(err)
				}
				if err := os.WriteFile(path, nil, 0600); err != nil {
					t.Fatal(err)
				}
			}
			entered := false
			if err := lease.WithExclusiveStartup(target, func() error { entered = true; return nil }); err == nil || entered {
				t.Fatalf("invalid startup capability entered callback: %v", err)
			}
		})
	}
}

func TestExclusiveStartupCapabilityRetainsLockThroughCallbackAndCleanup(t *testing.T) {
	dir := t.TempDir()
	lease, first, err := AcquireAppServerPresence(dir)
	if err != nil || !first {
		t.Fatal("acquire", err)
	}
	defer lease.Release()
	expected := errors.New("fixture initialization failed")
	attempted, done := make(chan struct{}), make(chan error, 1)
	err = lease.WithExclusiveStartup(dir, func() error {
		go func() { close(attempted); done <- lease.Release() }()
		<-attempted
		select {
		case err := <-done:
			t.Fatalf("lease released during initialization: %v", err)
		default:
		}
		if _, err := lease.presence.Stat(); err != nil {
			t.Fatal("held presence closed during callback", err)
		}
		return expected
	})
	if !errors.Is(err, expected) {
		t.Fatalf("callback failure lost: %v", err)
	}
	if err := <-done; err != nil {
		t.Fatal(err)
	}
	if err := lease.WithExclusiveStartup(dir, func() error { t.Fatal("released capability reused"); return nil }); err == nil {
		t.Fatal("released capability accepted")
	}
}
