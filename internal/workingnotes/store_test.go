package workingnotes

import (
	"crypto/sha256"
	"fmt"
	"sync"
	"testing"

	"github.com/blueberrycongee/wuu/internal/pluginsettings"
)

func TestLegacyNotesSurviveMigrationAndRestart(t *testing.T) {
	home := t.TempDir()
	legacy := `{"work.md":"verified facts"}`
	_, err := pluginsettings.UpdateState(home, "", "plugin:bundled:note-compaction", pluginsettings.ScopeUser, func(values map[string]string) error {
		values[fmt.Sprintf("notes.v1.%x", sha256.Sum256([]byte("one")))] = legacy
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	store := Store{Home: home}
	value, err := store.Read("one")
	if err != nil || value == nil || *value != legacy {
		t.Fatalf("legacy read=%v %v", value, err)
	}
	next := `{"work.md":"verified facts and next steps"}`
	if err := store.CompareAndSwap("one", value, next); err != nil {
		t.Fatal(err)
	}
	value, err = (Store{Home: home}).Read("one")
	if err != nil || value == nil || *value != next {
		t.Fatalf("restart read=%v %v", value, err)
	}
	other, err := store.Read("two")
	if err != nil || other != nil {
		t.Fatalf("session leak=%v %v", other, err)
	}
}

func TestConcurrentNotesWritersRejectLostUpdates(t *testing.T) {
	store := Store{Home: t.TempDir()}
	start := make(chan struct{})
	results := make(chan error, 2)
	var wg sync.WaitGroup
	for _, value := range []string{`{"a":"one"}`, `{"a":"two"}`} {
		wg.Add(1)
		go func() { defer wg.Done(); <-start; results <- store.CompareAndSwap("one", nil, value) }()
	}
	close(start)
	wg.Wait()
	close(results)
	successes := 0
	for err := range results {
		if err == nil {
			successes++
		}
	}
	if successes != 1 {
		t.Fatalf("successful conflicting writes=%d", successes)
	}
}
