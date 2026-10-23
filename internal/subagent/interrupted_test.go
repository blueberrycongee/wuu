package subagent

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/blueberrycongee/wuu/internal/loopdriver"
	"github.com/blueberrycongee/wuu/internal/providers"
)

func writeSnapshotFile(t *testing.T, path, status, errText string, started, completed time.Time) {
	t.Helper()
	sa := &SubAgent{
		ID:          "wk-1",
		Type:        "general-purpose",
		Description: "test run",
		Status:      Status(status),
		StartedAt:   started,
		CompletedAt: completed,
		prompt:      "do it",
		model:       "fake-model",
		workerRoot:  filepath.Dir(path),
		historyPath: path,
		history: []providers.ChatMessage{
			{Role: "system", Content: "sys"},
			{Role: "user", Content: "do it"},
			{Role: "assistant", Content: "partial progress"},
		},
	}
	if errText != "" {
		sa.Error = errStr(errText)
	}
	if err := persistHistory(sa); err != nil {
		t.Fatalf("persistHistory: %v", err)
	}
}

type errStrType string

func (e errStrType) Error() string { return string(e) }

func errStr(s string) error { return errStrType(s) }

func TestMarkPersistedRunInterrupted_SettlesRunningSnapshot(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "wk-1.json")
	started := time.Now().Add(-time.Hour).UTC().Truncate(time.Second)
	writeSnapshotFile(t, path, string(StatusRunning), "", started, time.Time{})

	now := time.Now().UTC().Truncate(time.Second)
	changed, err := MarkPersistedRunInterrupted(path, "interrupted: process exited mid-run", now)
	if err != nil {
		t.Fatalf("MarkPersistedRunInterrupted: %v", err)
	}
	if !changed {
		t.Fatal("expected the running snapshot to be rewritten")
	}
	run, err := LoadPersistedRun(path)
	if err != nil {
		t.Fatalf("LoadPersistedRun: %v", err)
	}
	if run.Status != StatusInterrupted {
		t.Fatalf("expected interrupted, got %s", run.Status)
	}
	if !run.StartedAt.Equal(started) {
		t.Fatalf("StartedAt must be preserved: want %v got %v", started, run.StartedAt)
	}
	if !run.CompletedAt.Equal(now) {
		t.Fatalf("zero CompletedAt should be filled with the settle time, got %v", run.CompletedAt)
	}
	if !strings.Contains(run.Error, "interrupted") {
		t.Fatalf("reason should land on the empty error field, got %q", run.Error)
	}
	if len(run.Messages) != 3 {
		t.Fatalf("history must survive the rewrite, got %d messages", len(run.Messages))
	}
}

func TestMarkPersistedRunInterrupted_PreservesExistingError(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "wk-1.json")
	writeSnapshotFile(t, path, string(StatusRunning), "original failure", time.Now().UTC(), time.Time{})

	if _, err := MarkPersistedRunInterrupted(path, "interrupted: reconciled", time.Now().UTC()); err != nil {
		t.Fatalf("MarkPersistedRunInterrupted: %v", err)
	}
	run, err := LoadPersistedRun(path)
	if err != nil {
		t.Fatalf("LoadPersistedRun: %v", err)
	}
	if run.Error != "original failure" {
		t.Fatalf("existing error must win over the reconciliation reason, got %q", run.Error)
	}
}

func TestMarkPersistedRunInterrupted_LeavesTerminalAndMissingAlone(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "wk-1.json")
	completed := time.Now().UTC().Truncate(time.Second)
	writeSnapshotFile(t, path, string(StatusCompleted), "", completed.Add(-time.Minute), completed)

	changed, err := MarkPersistedRunInterrupted(path, "interrupted", time.Now().UTC())
	if err != nil {
		t.Fatalf("MarkPersistedRunInterrupted: %v", err)
	}
	if changed {
		t.Fatal("terminal snapshots must not be rewritten")
	}
	run, _ := LoadPersistedRun(path)
	if run.Status != StatusCompleted || !run.CompletedAt.Equal(completed) {
		t.Fatalf("terminal snapshot mutated: %+v", run)
	}

	changed, err = MarkPersistedRunInterrupted(filepath.Join(dir, "missing.json"), "interrupted", time.Now().UTC())
	if err != nil || changed {
		t.Fatalf("missing snapshot should be a quiet no-op, got changed=%v err=%v", changed, err)
	}
}

// TestRestore_NormalizesNonTerminalStatus locks the CountRunning fix: a
// restored run has no goroutine, so a snapshot that still claims running
// must register as interrupted — never as a phantom running entry that
// permanently occupies a max-parallel slot.
func TestRestore_NormalizesNonTerminalStatus(t *testing.T) {
	client := &fakeClient{response: providers.ChatResponse{Content: "revived"}}
	mgr := NewManager(client, "fake-model", loopdriver.DefaultDriver{})

	sa, err := mgr.Restore(RestoreOptions{
		Run: PersistedRun{
			Version: ResumeSnapshotVersion,
			ID:      "wk-phantom",
			Type:    "general-purpose",
			Status:  StatusRunning,
			Model:   "fake-model",
			CWD:     os.TempDir(),
			Messages: []providers.ChatMessage{
				{Role: "system", Content: "sys"},
				{Role: "user", Content: "do it"},
			},
		},
		Toolkit: fakeToolkit{},
	})
	if err != nil {
		t.Fatalf("Restore: %v", err)
	}
	if got := sa.Snapshot().Status; got != StatusInterrupted {
		t.Fatalf("non-terminal restore must normalize to interrupted, got %s", got)
	}
	if got := mgr.CountRunning(); got != 0 {
		t.Fatalf("restored phantom must not count as running, got %d", got)
	}

	// And it stays resumable like any terminal run.
	snap, err := mgr.Followup(context.Background(), "wk-phantom", "carry on")
	if err != nil {
		t.Fatalf("Followup: %v", err)
	}
	if snap.Status != StatusRunning {
		t.Fatalf("expected followup to start a turn, got %s", snap.Status)
	}
	final, err := mgr.Wait(context.Background(), "wk-phantom")
	if err != nil {
		t.Fatalf("Wait: %v", err)
	}
	if final.Status != StatusCompleted || final.Result != "revived" {
		t.Fatalf("expected revived completion, got %s result=%q", final.Status, final.Result)
	}
}
