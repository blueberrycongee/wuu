package agentthread

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestEnsureRootRepairsLegacyEventAfterFailure(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "events.jsonl")
	if err := os.Mkdir(path, 0700); err != nil {
		t.Fatal(err)
	}
	store := NewStore(dir)
	now := time.Now().UTC()
	meta := Metadata{ID: "id", SessionID: "id", Path: RootPath, TaskName: "root", Role: "root", CWD: "/work", Status: StatusRunning, CreatedAt: now, UpdatedAt: now}
	if _, err := store.EnsureRoot(meta); err == nil {
		t.Fatal("expected append failure")
	}
	if err := os.Remove(path); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(`{"type":`), 0600); err != nil {
		t.Fatal(err)
	}
	if _, err := store.EnsureRoot(meta); err != nil {
		t.Fatal(err)
	}
	events, err := store.ReadEvents()
	if err != nil {
		t.Fatal(err)
	}
	if len(events) != 1 {
		t.Fatalf("successful retry left %d root events; index was committed before append failure", len(events))
	}
	if _, err := store.EnsureRoot(meta); err != nil {
		t.Fatal(err)
	}
	events, err = store.ReadEvents()
	if err != nil || len(events) != 1 {
		t.Fatalf("repeated retry duplicated event: %+v %v", events, err)
	}
	backup := path + ".backup"
	if err := os.Rename(path, backup); err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(path, 0700); err != nil {
		t.Fatal(err)
	}
	meta.CWD = "/new"
	meta.UpdatedAt = meta.UpdatedAt.Add(time.Hour)
	if _, err := store.EnsureRoot(meta); err == nil {
		t.Fatal("expected workspace update append failure")
	}
	if err := os.Remove(path); err != nil {
		t.Fatal(err)
	}
	if err := os.Rename(backup, path); err != nil {
		t.Fatal(err)
	}
	if _, err := store.EnsureRoot(meta); err != nil {
		t.Fatal(err)
	}
	events, err = store.ReadEvents()
	if err != nil || len(events) != 2 || events[1].Metadata == nil || events[1].Metadata.CWD != "/new" {
		t.Fatalf("workspace event not recovered: %+v %v", events, err)
	}
}
