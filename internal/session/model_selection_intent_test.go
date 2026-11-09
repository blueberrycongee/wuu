package session

import (
	"context"
	"errors"
	"fmt"
	"testing"

	"github.com/blueberrycongee/wuu/internal/pluginhost"
)

// Next-step writes are a distinct optional wire operation. CAS protects competing
// intent writers and scope changes; a fresh client restores only the winner.
func TestModelSelectionIntentPersistence(t *testing.T) {
	forEachSessionStore(t, func(t *testing.T, native *documentStore) {
		store := NewStore(context.Background(), native.documents)
		if _, err := store.Create("selection"); err != nil {
			t.Fatal(err)
		}
		first, err := store.SetRuntimeSelection("selection", RuntimeSelection{Provider: "a", Model: "one", PermissionMode: "default"})
		if err != nil {
			t.Fatal(err)
		}
		invoke := func(revision uint64, engine, model, permission string) (Session, error) {
			var result Session
			err := store.invoke("SetNextStepModelSelection", []any{"selection", map[string]any{
				"ExpectedRevision": revision, "ExpectedEngineID": engine,
				"Selection": RuntimeSelection{Provider: "b", Model: model, PermissionMode: permission},
			}}, []any{&result})
			return result, err
		}
		second, err := invoke(first.ModelSelectionRevision, "", "two", "default")
		if err != nil {
			t.Fatalf("optional next-step operation unavailable: %v", err)
		}
		if second.ModelSelectionRevision != first.ModelSelectionRevision+1 || second.ModelSelectionApplyAt != "next_step" || second.PermissionMode != "default" {
			t.Fatalf("lost durable intent or permissions: %+v", second)
		}
		for _, bad := range []struct {
			revision           uint64
			engine, permission string
		}{
			{first.ModelSelectionRevision, "", "default"},
			{second.ModelSelectionRevision, "other-engine", "default"},
			{second.ModelSelectionRevision, "", "bypassPermissions"},
		} {
			if _, err := invoke(bad.revision, bad.engine, "rejected", bad.permission); err == nil {
				t.Fatal("stale or scope-changing write accepted")
			}
		}
		restored, found, err := NewStore(context.Background(), native.documents).Find("selection")
		if err != nil || !found || restored.ModelSelectionRevision != second.ModelSelectionRevision || restored.Model != "two" {
			t.Fatalf("rejected write changed restored selection: %+v %v", restored, err)
		}
		start := make(chan struct{})
		type outcome struct {
			session Session
			err     error
		}
		results := make(chan outcome, 2)
		for i := 0; i < 2; i++ {
			go func(i int) {
				<-start
				s, err := invoke(second.ModelSelectionRevision, "", fmt.Sprintf("winner-%d", i), "default")
				results <- outcome{s, err}
			}(i)
		}
		close(start)
		var winner Session
		successes, conflicts := 0, 0
		for i := 0; i < 2; i++ {
			got := <-results
			if got.err == nil {
				successes++
				winner = got.session
				continue
			}
			var remote *pluginhost.HostServiceError
			if !errors.As(got.err, &remote) || remote.Code != "session_model_selection_changed" || !errors.Is(got.err, ErrModelSelectionChanged) || errors.Is(got.err, ErrOutcomeUnknown) {
				t.Fatalf("CAS error contract: %v", got.err)
			}
			conflicts++
		}
		restored, _, err = store.Find("selection")
		if err != nil || successes != 1 || conflicts != 1 || restored.Model != winner.Model || restored.ModelSelectionRevision != second.ModelSelectionRevision+1 {
			t.Fatalf("CAS winner not restored: success=%d conflicts=%d winner=%+v restored=%+v err=%v", successes, conflicts, winner, restored, err)
		}
		legacy, err := store.SetRuntimeSelection("selection", RuntimeSelection{Provider: "a", Model: "legacy", PermissionMode: "default"})
		if err != nil || legacy.ModelSelectionApplyAt != "" || legacy.ModelSelectionRevision != restored.ModelSelectionRevision+1 {
			t.Fatalf("legacy turn selection changed: %+v %v", legacy, err)
		}
	})
}
