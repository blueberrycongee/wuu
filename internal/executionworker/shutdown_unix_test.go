//go:build !windows

package executionworker

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/blueberrycongee/wuu/internal/executionenv"
	"github.com/blueberrycongee/wuu/internal/process"
)

// EOF must reap managed processes, not just session-lifecycle commands. Two
// workers deliberately share a registry: records alone never confer ownership.
func TestWorkerEOFReapsOnlyOwnedManagedProcesses(t *testing.T) {
	home, root := t.TempDir(), t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("WUU_HOME", t.TempDir())
	t.Setenv("WUU_EXECUTION_TEST_WORKER", "1")
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	newWorker := func() *executionenv.Client {
		t.Helper()
		client := executionenv.NewClient([]string{os.Args[0], "-test.run=^TestWorkerTransportProcess$"}, os.Environ())
		t.Cleanup(func() { _ = client.Close() })
		if _, err := client.Call(ctx, "initialize", executionenv.Init{Version: executionenv.ProtocolVersion, Root: root, Session: "shared-registry"}); err != nil {
			t.Fatal(err)
		}
		return client
	}
	start := func(client *executionenv.Client) process.Process {
		t.Helper()
		raw, err := client.Call(ctx, "process/start", executionenv.ProcessRequest{Actor: "actor", PermissionMode: "unconfined", Command: "trap '' TERM; printf READY; exec sleep 60"})
		if err != nil {
			t.Fatal(err)
		}
		var started process.Process
		if err := json.Unmarshal(raw, &started); err != nil {
			t.Fatal(err)
		}
		if started.PID <= 1 || started.PGID <= 1 || started.Lifecycle != process.LifecycleManaged {
			t.Fatalf("invalid managed process receipt: %+v", started)
		}
		t.Cleanup(func() {
			if errors.Is(syscall.Kill(started.PID, 0), syscall.ESRCH) {
				return
			}
			// A failed assertion still needs rescue cleanup, but a recycled PID
			// must never be signalled. Stop validates the persisted launch identity.
			manager, err := process.NewManager(root, filepath.Join(home, ".wuu-execution", "shared-registry", "runtime"))
			if err != nil {
				t.Errorf("open fixture cleanup manager: %v", err)
				return
			}
			if _, err := manager.Stop(started.ID); err != nil {
				t.Errorf("stop fixture process: %v", err)
			}
		})
		raw, err = client.Call(ctx, "process/read", map[string]any{"id": started.ID, "options": process.OutputReadOptions{Wait: 5 * time.Second}})
		if err != nil || !strings.Contains(string(raw), "READY") {
			t.Fatalf("process did not install TERM resistance: %s %v", raw, err)
		}
		return started
	}
	assertReaped := func(started process.Process) {
		t.Helper()
		if err := syscall.Kill(started.PID, 0); !errors.Is(err, syscall.ESRCH) {
			t.Errorf("process %s pid %d survived worker EOF or was not reaped: %v", started.ID, started.PID, err)
		}
		path := filepath.Join(home, ".wuu-execution", "shared-registry", "runtime", "processes", started.ID+".json")
		data, err := os.ReadFile(path)
		if err != nil {
			t.Fatal(err)
		}
		var record process.Process
		if err := json.Unmarshal(data, &record); err != nil {
			t.Fatal(err)
		}
		if record.Status != process.StatusStopped || record.StoppedAt.IsZero() || record.TerminalCause != process.EventCauseRequestedStop {
			t.Errorf("worker exited before terminal process receipt: %+v", record)
		}
		t.Logf("EOF receipt: process=%s pid=%d status=%s cause=%s", started.ID, started.PID, record.Status, record.TerminalCause)
	}
	first := newWorker()
	// Serial TERM grace windows would exceed the native client's shutdown
	// budget; multiple resistant commands must be stopped concurrently.
	owned := []process.Process{start(first), start(first), start(first), start(first)}
	second := newWorker()
	other := start(second)
	if err := first.Close(); err != nil {
		t.Fatalf("first worker EOF cleanup: %v", err)
	}
	for _, started := range owned {
		assertReaped(started)
	}
	if err := syscall.Kill(other.PID, 0); err != nil {
		t.Fatalf("first worker stopped another manager's live process: %v", err)
	}
	raw, err := second.Call(ctx, "process/get", map[string]string{"id": other.ID})
	var survivor process.Process
	if err != nil || json.Unmarshal(raw, &survivor) != nil || survivor.Status != process.StatusRunning {
		t.Fatalf("other owner's receipt changed on first worker EOF: %s %v", raw, err)
	}
	if err := second.Close(); err != nil {
		t.Fatalf("second worker EOF cleanup: %v", err)
	}
	assertReaped(other)
	if !t.Failed() {
		t.Log("verified: parallel TERM-resistant managed cleanup, physical reap, durable terminal receipts, shared-registry ownership isolation")
	}
}

func TestWorkerClosePreservesWorkspaceOnProcessCleanupFailure(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	t.Setenv("WUU_HOME", t.TempDir())
	root := t.TempDir()
	s := New()
	defer s.Close()
	data, _ := json.Marshal(executionenv.Init{Version: executionenv.ProtocolVersion, Root: root, Session: "cleanup-failure", RemoveWorkspaceOnExit: true})
	if _, err := s.Handle(context.Background(), "initialize", data); err != nil {
		t.Fatal(err)
	}
	started, err := s.processes.Start(context.Background(), process.StartOptions{Command: "exec sleep 60", OwnerKind: process.OwnerMainAgent, OwnerID: "actor", Lifecycle: process.LifecycleManaged})
	if err != nil {
		t.Fatal(err)
	}
	recordPath := filepath.Join(os.Getenv("HOME"), ".wuu-execution", "cleanup-failure", "runtime", "processes", started.ID+".json")
	t.Cleanup(func() {
		if errors.Is(syscall.Kill(started.PID, 0), syscall.ESRCH) {
			return
		}
		// Only this test corrupts this receipt. Restore its original identity
		// before asking the manager to safely stop/reap a failed fixture.
		record, _ := json.Marshal(started)
		if err := os.WriteFile(recordPath, record, 0600); err != nil {
			t.Errorf("restore fixture record: %v", err)
			return
		}
		if _, err := s.processes.Stop(started.ID); err != nil {
			t.Errorf("stop fixture process: %v", err)
		}
	})
	record, err := os.ReadFile(recordPath)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(recordPath, []byte("invalid registry record"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := s.Close(); err == nil {
		t.Error("worker hid failed process cleanup")
	}
	if _, err := os.Stat(root); err != nil {
		t.Errorf("worker removed workspace after failed cleanup: %v", err)
	}
	// Repair only this fixture's deliberately corrupted receipt so the normal
	// owned stop path can reap it even when the contract assertion failed.
	if err := os.WriteFile(recordPath, record, 0600); err != nil {
		t.Fatal(err)
	}
	if _, err := s.processes.Stop(started.ID); err != nil {
		t.Fatal(err)
	}
}
