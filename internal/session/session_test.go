package session

import (
	"os"
	"path/filepath"
	"sync"
	"testing"
)

func TestCreateAndList(t *testing.T) {
	dir := t.TempDir()
	s1, err := Create(dir)
	if err != nil {
		t.Fatal(err)
	}
	s2, err := Create(dir)
	if err != nil {
		t.Fatal(err)
	}
	if s1.ID == s2.ID {
		t.Fatalf("expected unique IDs, got %q twice", s1.ID)
	}

	sessions, err := List(dir, 0)
	if err != nil {
		t.Fatal(err)
	}
	if len(sessions) != 2 {
		t.Fatalf("expected 2 sessions, got %d", len(sessions))
	}
}

func TestUpdateIndex(t *testing.T) {
	dir := t.TempDir()
	s, err := Create(dir)
	if err != nil {
		t.Fatal(err)
	}
	if err := UpdateIndex(dir, s.ID, 42, "hello"); err != nil {
		t.Fatal(err)
	}
	sessions, err := List(dir, 0)
	if err != nil {
		t.Fatal(err)
	}
	if len(sessions) != 1 || sessions[0].Entries != 42 || sessions[0].Summary != "hello" {
		t.Fatalf("update not persisted: %+v", sessions)
	}
}

// TestConcurrentCreateAndUpdate exercises the race fixed by withIndexLock:
// before the fix, UpdateIndex's read-modify-rewrite could clobber a Create
// that happened between the read and the rewrite.
func TestConcurrentCreateAndUpdate(t *testing.T) {
	dir := t.TempDir()

	// Seed with one session that UpdateIndex will target.
	seed, err := Create(dir)
	if err != nil {
		t.Fatal(err)
	}

	const newSessions = 50
	const updates = 50
	var wg sync.WaitGroup
	wg.Add(2)

	go func() {
		defer wg.Done()
		for i := 0; i < newSessions; i++ {
			if _, err := Create(dir); err != nil {
				t.Errorf("Create: %v", err)
				return
			}
		}
	}()

	go func() {
		defer wg.Done()
		for i := 0; i < updates; i++ {
			if err := UpdateIndex(dir, seed.ID, i, ""); err != nil {
				t.Errorf("UpdateIndex: %v", err)
				return
			}
		}
	}()

	wg.Wait()

	sessions, err := List(dir, 0)
	if err != nil {
		t.Fatal(err)
	}
	expected := newSessions + 1 // seed + creates
	if len(sessions) != expected {
		t.Fatalf("expected %d sessions after concurrent work, got %d", expected, len(sessions))
	}

	// Verify no duplicate IDs (a symptom of torn writes).
	seen := make(map[string]bool, len(sessions))
	for _, s := range sessions {
		if seen[s.ID] {
			t.Errorf("duplicate session ID in index: %s", s.ID)
		}
		seen[s.ID] = true
	}
}

func TestLockFileIsCreated(t *testing.T) {
	dir := t.TempDir()
	if _, err := Create(dir); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(dir, ".index.lock")); err != nil {
		t.Fatalf("expected lock file to exist: %v", err)
	}
}
