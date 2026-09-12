package channels

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"
)

func TestNotebookSharedDiscoveryPrivateScopeAndConcurrentCorrection(t *testing.T) {
	ctx := context.Background()
	s := openTestService(t, nil)
	owner, peer := createTestAgent(t, s, "Owner"), createTestAgent(t, s, "Peer")
	room := createTestRoom(t, s, owner, peer)
	a := flexibleTestSession(t, s, owner.Agent.ID, room.ID, "writer")
	b := flexibleTestSession(t, s, peer.Agent.ID, room.ID, "reader")
	p := NotebookParams{Action: "write", Scope: "room", Name: "preferences.md", Content: "Source: user message 42\nPrefer a Friday digest.", Revision: "missing"}
	first, err := a.Notebook(ctx, p)
	if err != nil {
		t.Fatal(err)
	}
	p.Action = "search"
	p.Query = "Friday"
	found, err := b.Notebook(ctx, p)
	if err != nil || len(found.Entries) != 1 {
		t.Fatalf("shared search = %+v, %v", found, err)
	}
	p.Action = "write"
	p.Revision = first.Entries[0].Revision
	p.Content = "Source: user message 51\nPrefer a Monday digest."
	if _, err = b.Notebook(ctx, p); err != nil {
		t.Fatal(err)
	}
	if _, err = a.Notebook(ctx, p); !errors.Is(err, ErrConflict) {
		t.Fatalf("stale overwrite = %v", err)
	}
	p = NotebookParams{Action: "write", Scope: "identity", Name: "private.md", Content: "Private preference", Revision: "missing"}
	if _, err = a.Notebook(ctx, p); err != nil {
		t.Fatal(err)
	}
	p.Action = "list"
	private, err := b.Notebook(ctx, p)
	if err != nil {
		t.Fatal(err)
	}
	for _, entry := range private.Entries {
		if entry.Name == "private.md" {
			t.Fatal("identity memory leaked")
		}
	}
	p.Action = "read"
	p.Name = "../token.md"
	if _, err = a.Notebook(ctx, p); err == nil {
		t.Fatal("traversal accepted")
	}
	outside := filepath.Join(t.TempDir(), "secret.md")
	if err = os.WriteFile(outside, []byte("secret"), 0600); err != nil {
		t.Fatal(err)
	}
	if err = os.Symlink(outside, filepath.Join(owner.Agent.MemoryDir, "escape.md")); err != nil {
		t.Fatal(err)
	}
	p.Name = "escape.md"
	if _, err = a.Notebook(ctx, p); err == nil {
		t.Fatal("symlink escaped notebook")
	}
}
