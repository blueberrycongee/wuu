package process

import (
	"bytes"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestCompactProcessLogKeepsNewestOutputWithinLimit(t *testing.T) {
	path := filepath.Join(t.TempDir(), "process.log")
	content := bytes.Repeat([]byte("0123456789"), 10)
	if err := os.WriteFile(path, content, 0o600); err != nil {
		t.Fatal(err)
	}
	limit := int64(len(processLogCompactionMarker) + 10)
	changed, discarded, err := compactProcessLog(path, limit)
	if err != nil {
		t.Fatal(err)
	}
	if !changed {
		t.Fatal("oversized process log was not compacted")
	}
	if discarded != int64(len(content))-limit {
		t.Fatalf("discarded bytes = %d, want %d", discarded, int64(len(content))-limit)
	}
	stored, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if int64(len(stored)) > limit || !bytes.HasPrefix(stored, processLogCompactionMarker) || !bytes.HasSuffix(stored, content[len(content)-10:]) {
		t.Fatalf("compacted process log = %q", stored)
	}
}

func TestCompactedProcessLogPreservesLogicalContinuationOffsets(t *testing.T) {
	path := filepath.Join(t.TempDir(), "process.log")
	content := bytes.Repeat([]byte("0123456789"), 10)
	if err := os.WriteFile(path, content, 0o600); err != nil {
		t.Fatal(err)
	}
	limit := int64(len(processLogCompactionMarker) + 20)
	_, base, err := compactProcessLog(path, limit)
	if err != nil {
		t.Fatal(err)
	}

	oldOffset := int64(10)
	output, _, start, end, total, err := readLogWindow(path, int(limit), &oldOffset, base)
	if err != nil {
		t.Fatal(err)
	}
	if start != base || end != int64(len(content)) || total != int64(len(content)) {
		t.Fatalf("logical window = %d:%d/%d, want %d:%d/%d", start, end, total, base, len(content), len(content))
	}
	if !bytes.HasPrefix([]byte(output), processLogCompactionMarker) || !bytes.HasSuffix([]byte(output), content[len(content)-20:]) {
		t.Fatalf("logical continuation did not expose compaction marker and tail: %q", output)
	}
}

func TestTerminalStorageExpiresOnlyCompletedObligations(t *testing.T) {
	root := t.TempDir()
	runtimeDir := filepath.Join(root, "runtime")
	manager, err := NewManager(root, runtimeDir)
	if err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC()
	old := now.Add(-terminalProcessRetention - time.Hour)
	seed := func(p Process) {
		t.Helper()
		p.LogPath = filepath.Join(manager.logDir, p.ID+".log")
		p.StartedAt = old
		p.UpdatedAt = old
		p.StoppedAt = old
		if err := os.WriteFile(p.LogPath, []byte("output"), 0o600); err != nil {
			t.Fatal(err)
		}
		if err := manager.save(&p); err != nil {
			t.Fatal(err)
		}
	}
	seed(Process{ID: "proc-delivered", Status: StatusStopped, TerminalCause: EventCauseNaturalExit, CompletionDeliveredAt: old})
	seed(Process{ID: "proc-pending", Status: StatusStopped, TerminalCause: EventCauseNaturalExit})
	seed(Process{ID: "proc-detached", Status: StatusFailed, TerminalCause: EventCauseNaturalExit, CompletionMode: CompletionModeDetached})

	if err := manager.maintainTerminalStorage(now); err != nil {
		t.Fatal(err)
	}
	for _, id := range []string{"proc-delivered", "proc-detached"} {
		if _, err := manager.Get(id); !os.IsNotExist(err) {
			t.Fatalf("expired process %q still has record: %v", id, err)
		}
		if _, err := os.Stat(filepath.Join(manager.logDir, id+".log")); !os.IsNotExist(err) {
			t.Fatalf("expired process %q still has log: %v", id, err)
		}
	}
	if pending, err := manager.Get("proc-pending"); err != nil || !processCompletionPending(*pending) {
		t.Fatalf("pending completion was removed: process=%+v err=%v", pending, err)
	}
}
