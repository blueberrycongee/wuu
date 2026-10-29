package agentcontrol

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/blueberrycongee/wuu/internal/subagent"
)

func TestWorkerExecutionProbeSeesTerminalCommittedBeforeLeaseTransfer(t *testing.T) {
	root := filepath.Join(t.TempDir(), "harness")
	const id = "worker"
	lease, acquired, err := tryAcquireWorkerExecutionLease(root, id)
	if err != nil || !acquired {
		t.Fatalf("worker lease: %v %v", acquired, err)
	}
	entered, resume := make(chan struct{}), make(chan struct{})
	type result struct {
		active bool
		err    error
	}
	done := make(chan result, 1)
	go func() {
		active, err := workerExecutionActive(root, id, func(dir, worker string) (*workerExecutionLease, bool, error) {
			close(entered)
			<-resume
			return tryAcquireWorkerExecutionLease(dir, worker)
		})
		done <- result{active, err}
	}()
	select {
	case <-entered:
	case <-time.After(5 * time.Second):
		t.Fatal("probe did not reach lock attempt")
	}
	control := &AgentControl{harnessDir: root}
	if err := control.persistWorkerTerminalFinalization(subagent.Notification{AgentID: id, Status: subagent.StatusCompleted, Snapshot: subagent.SubAgentSnapshot{ID: id, Status: subagent.StatusCompleted}}); err != nil {
		t.Fatal(err)
	}
	if err := lease.release(); err != nil {
		t.Fatal(err)
	}
	close(resume)
	select {
	case got := <-done:
		if got.err != nil || !got.active {
			t.Fatalf("probe missed terminal: %+v", got)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("probe did not complete")
	}
}

func TestWorkerExecutionProbeReportsAcquireInspectAndReleaseFailures(t *testing.T) {
	t.Run("acquire", func(t *testing.T) {
		sentinel := errors.New("acquire failure")
		active, err := workerExecutionActive(t.TempDir(), "worker", func(string, string) (*workerExecutionLease, bool, error) { return nil, false, sentinel })
		if active || !errors.Is(err, sentinel) {
			t.Fatalf("acquire failure lost: %v %v", active, err)
		}
	})
	t.Run("inspect and release", func(t *testing.T) {
		root := t.TempDir()
		file, err := os.CreateTemp(t.TempDir(), "closed")
		if err != nil {
			t.Fatal(err)
		}
		if err := file.Close(); err != nil {
			t.Fatal(err)
		}
		active, err := workerExecutionActive(root, "worker", func(string, string) (*workerExecutionLease, bool, error) {
			if err := os.WriteFile(filepath.Join(root, workerTerminalFinalizationDirName), []byte("not a directory"), 0600); err != nil {
				t.Fatal(err)
			}
			return newWorkerExecutionLease(file), true, nil
		})
		if active || err == nil || !strings.Contains(err.Error(), "inspect worker terminal ownership") || !strings.Contains(err.Error(), "release worker execution probe") {
			t.Fatalf("probe errors lost: %v %v", active, err)
		}
	})
}

func TestWorkerExecutionProbeExistingTerminalDoesNotRequireLockDirectory(t *testing.T) {
	root := filepath.Join(t.TempDir(), "harness")
	const id = "worker"
	c := &AgentControl{harnessDir: root}
	if err := c.persistWorkerTerminalFinalization(subagent.Notification{AgentID: id, Status: subagent.StatusCompleted, Snapshot: subagent.SubAgentSnapshot{ID: id, Status: subagent.StatusCompleted}}); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, workerExecutionLeaseDirName), []byte("not a directory"), 0600); err != nil {
		t.Fatal(err)
	}
	active, err := WorkerExecutionActive(root, id)
	if err != nil || !active {
		t.Fatalf("existing terminal probe: %v %v", active, err)
	}
}

func TestWorkerExecutionProbeOwnershipStates(t *testing.T) {
	root := filepath.Join(t.TempDir(), "harness")
	const id = "worker"
	active, err := WorkerExecutionActive(root, id)
	if err != nil || active {
		t.Fatalf("empty probe: %v %v", active, err)
	}
	lease, acquired, err := tryAcquireWorkerExecutionLease(root, id)
	if err != nil || !acquired {
		t.Fatalf("worker lease: %v %v", acquired, err)
	}
	active, err = WorkerExecutionActive(root, id)
	if err != nil || !active {
		t.Fatalf("held lease probe: %v %v", active, err)
	}
	if err := lease.release(); err != nil {
		t.Fatal(err)
	}
	c := &AgentControl{harnessDir: root}
	if err := c.persistWorkerTerminalFinalization(subagent.Notification{AgentID: id, Status: subagent.StatusCompleted, Snapshot: subagent.SubAgentSnapshot{ID: id, Status: subagent.StatusCompleted}}); err != nil {
		t.Fatal(err)
	}
	active, err = WorkerExecutionActive(root, id)
	if err != nil || !active {
		t.Fatalf("terminal probe: %v %v", active, err)
	}
	if err := c.acknowledgeWorkerTerminalFinalization(id); err != nil {
		t.Fatal(err)
	}
	active, err = WorkerExecutionActive(root, id)
	if err != nil || active {
		t.Fatalf("acknowledged probe: %v %v", active, err)
	}
}
