package agentthread

import (
	"strings"
	"testing"
	"time"
)

func TestEnsureRootAcceptsLargeLegacyEvents(t *testing.T) {
	store := NewStore(t.TempDir())
	now := time.Now().UTC()
	meta := Metadata{ID: "root", SessionID: "root", Path: RootPath, Role: "root", CWD: "/work", Status: StatusRunning, CreatedAt: now, UpdatedAt: now}
	if _, err := store.EnsureRoot(meta); err != nil {
		t.Fatal(err)
	}
	if err := store.RecordCommunication("root", NewInterAgentCommunication(RootAgentPath(), RootAgentPath(), strings.Repeat("a", 100000), false)); err != nil {
		t.Fatal(err)
	}
	if _, err := store.EnsureRoot(meta); err != nil {
		t.Fatalf("previously accepted large communication prevents reopening: %v", err)
	}
}
