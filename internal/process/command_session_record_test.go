package process

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
)

func startedRecordOnDisk(t *testing.T, m *Manager, id string) Process {
	t.Helper()
	path := filepath.Join(m.registryDir, id+".json")
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read registry record: %v", err)
	}
	var p Process
	if err := json.Unmarshal(raw, &p); err != nil {
		t.Fatalf("decode registry record: %v", err)
	}
	return p
}

func newTestManager(t *testing.T) *Manager {
	t.Helper()
	root := t.TempDir()
	m, err := NewManager(root, t.TempDir())
	if err != nil {
		t.Fatalf("NewManager: %v", err)
	}
	return m
}

// The owner of a command has to survive into the registry, because once the
// thread or subagent is gone there is nothing left to scan to recover it.
func TestStartPersistsOwnerAndHostGeneration(t *testing.T) {
	m := newTestManager(t)
	t.Cleanup(func() { _ = m.CleanupSession() })

	p, err := m.Start(context.Background(), StartOptions{
		Command:      "true",
		OwnerKind:    OwnerSubagent,
		OwnerID:      "worker-7",
		RootThreadID: "thread-42",
	})
	if err != nil {
		t.Fatalf("Start: %v", err)
	}

	if p.RootThreadID != "thread-42" {
		t.Fatalf("RootThreadID = %q, want thread-42", p.RootThreadID)
	}
	if p.HostGenerationID != m.HostGenerationID() {
		t.Fatalf("HostGenerationID = %q, want %q", p.HostGenerationID, m.HostGenerationID())
	}

	stored := startedRecordOnDisk(t, m, p.ID)
	if stored.RootThreadID != "thread-42" || stored.OwnerKind != OwnerSubagent || stored.OwnerID != "worker-7" {
		t.Fatalf("registry record lost owner fields: %+v", stored)
	}
	if stored.HostGenerationID != m.HostGenerationID() {
		t.Fatalf("registry record lost host generation: %+v", stored)
	}
}

func TestTopLevelManagersHaveDistinctHostGenerations(t *testing.T) {
	first := newTestManager(t)
	second := newTestManager(t)

	if first.HostGenerationID() == "" {
		t.Fatal("host generation id must not be empty")
	}
	if first.HostGenerationID() == second.HostGenerationID() {
		t.Fatalf("two hosts share a generation id: %q", first.HostGenerationID())
	}
}

// Existing registry files predate both fields. They must keep loading, and the
// deprecated lifecycle value must round-trip untouched so this step stays a
// pure data-plane change.
func TestLegacyRecordWithoutNewFieldsStillLoads(t *testing.T) {
	m := newTestManager(t)

	legacy := `{"id":"legacy-1","owner_kind":"main_agent","owner_id":"main","lifecycle":"managed","status":"stopped","pid":4242,"command":"sleep 1","cwd":"/tmp","exit_code":0}`
	path := filepath.Join(m.registryDir, "legacy-1.json")
	if err := os.WriteFile(path, []byte(legacy), 0o644); err != nil {
		t.Fatalf("write legacy record: %v", err)
	}

	list, err := m.List()
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	var found *Process
	for i := range list {
		if list[i].ID == "legacy-1" {
			found = &list[i]
			break
		}
	}
	if found == nil {
		t.Fatalf("legacy record disappeared from List: %+v", list)
	}
	if found.RootThreadID != "" || found.HostGenerationID != "" {
		t.Fatalf("legacy record must not invent owner fields: %+v", found)
	}
	if found.Lifecycle != LifecycleManaged {
		t.Fatalf("deprecated lifecycle must round-trip, got %q", found.Lifecycle)
	}
}
