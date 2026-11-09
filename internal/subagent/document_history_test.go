package subagent

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"

	"github.com/blueberrycongee/wuu/internal/persistence"
)

func TestSelectedWorkerHistoryPreservesMigrationAndPendingCheckpoint(t *testing.T) {
	docs, err := persistence.Open(context.Background(), filepath.Join(t.TempDir(), "documents"), "sqlite")
	if err != nil {
		t.Fatal(err)
	}
	defer docs.Close()
	path := filepath.Join(t.TempDir(), "worker.json")
	if err := writeWorkerRecord(path, historyRecord{ID: "worker", Version: ResumeSnapshotVersion, Result: "legacy", Status: string(StatusCompleted), DriverExecutionID: "old"}); err != nil {
		t.Fatal(err)
	}
	first, err := LoadPersistedRunFromDocuments(docs, "root", "worker", path)
	if err != nil || first.Result != "legacy" {
		t.Fatalf("migration: %+v %v", first, err)
	}
	if err := writeWorkerRecord(path, historyRecord{ID: "worker", Result: "stale"}); err != nil {
		t.Fatal(err)
	}
	worker := &SubAgent{ID: "worker", Status: StatusRunning, historyPath: path, historyDocuments: docs, historyScope: "root", driverExecutionID: "next"}
	if err := persistDriverStateLocked(worker); err != nil {
		t.Fatal(err)
	}
	captured, err := LoadPersistedRunFromDocuments(docs, "root", "worker", path)
	if err != nil || captured.Result != "legacy" || captured.Recovery == nil || captured.Recovery.DriverExecutionID != "next" {
		t.Fatalf("checkpoint overwrote terminal delivery: %+v %v", captured, err)
	}
	worker.Status = StatusCompleted
	worker.Result = "committed"
	if err := persistHistory(worker); err != nil {
		t.Fatal(err)
	}
	captured, err = LoadPersistedRunFromDocuments(docs, "root", "worker", path)
	if err != nil || captured.Result != "committed" || captured.Recovery != nil {
		t.Fatalf("terminal commit: %+v %v", captured, err)
	}
	if _, err := LoadPersistedRunFromDocuments(docs, "other", "worker", ""); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("scope leaked: %v", err)
	}
	if err := docs.Close(); err != nil {
		t.Fatal(err)
	}
	if _, err := LoadPersistedRunFromDocuments(docs, "root", "worker", path); !errors.Is(err, persistence.ErrClosed) {
		t.Fatalf("provider failure fell back to files: %v", err)
	}
}
