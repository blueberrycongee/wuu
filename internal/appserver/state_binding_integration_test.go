package appserver

import (
	"context"
	"fmt"
	"testing"

	"github.com/blueberrycongee/wuu/internal/execution"
	"github.com/blueberrycongee/wuu/internal/persistence"
	"github.com/blueberrycongee/wuu/internal/providers"
	"github.com/blueberrycongee/wuu/internal/session"
)

func TestServerResumeUsesSelectedAuthorityInsteadOfCachedTranscript(t *testing.T) {
	rt := newTestRuntime(t, &fakeClient{})
	old := rt.Sessions
	const id = "authority-switch-transcript"
	if _, err := old.CreateWithMetadata(id, rt.RootDir); err != nil {
		t.Fatal(err)
	}
	if _, err := appendChatMessage(old, id, providers.ChatMessage{Role: "user", Content: "original authority"}); err != nil {
		t.Fatal(err)
	}
	out := &lockedBuffer{}
	srv := New(rt, out)
	defer srv.Close()
	if err := srv.handleLine(context.Background(), []byte(fmt.Sprintf(`{"id":"before","method":"thread/resume","params":{"session_id":%q}}`, id))); err != nil {
		t.Fatal(err)
	}
	before := responseByID(t, parseOutput(t, out.String()), "before")
	if before["error"] != nil {
		t.Fatalf("initial resume: %+v", before)
	}
	documents, err := persistence.Open(context.Background(), t.TempDir(), "jsonl")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = documents.Close() })
	selected := session.NewStore(context.Background(), documents)
	if _, err := selected.CreateWithMetadata(id, rt.RootDir); err != nil {
		t.Fatal(err)
	}
	if _, err := appendChatMessage(selected, id, providers.ChatMessage{Role: "user", Content: "selected authority"}); err != nil {
		t.Fatal(err)
	}
	// This embedded runtime publishes an explicit replacement; production uses
	// the same State capture through its generation publication lock.
	rt.Documents, rt.Sessions = documents, selected
	if err := srv.handleLine(context.Background(), []byte(fmt.Sprintf(`{"id":"after","method":"thread/resume","params":{"session_id":%q}}`, id))); err != nil {
		t.Fatal(err)
	}
	after := responseByID(t, parseOutput(t, out.String()), "after")
	if after["error"] != nil {
		t.Fatalf("replacement resume: %+v", after)
	}
	result := remarshal[ThreadResumeResult](t, after["result"])
	found := false
	for _, turn := range result.Thread.Turns {
		for _, item := range turn.Items {
			if item.Text == "selected authority" {
				found = true
			}
			if item.Text == "original authority" {
				t.Fatalf("served stale cached transcript: %+v", result.Thread)
			}
		}
	}
	if !found {
		t.Fatalf("selected transcript missing: %+v", result.Thread)
	}
	original, err := loadChatMessages(old, id)
	if err != nil || len(original) != 1 || original[0].Content != "original authority" {
		t.Fatalf("original authority changed: %+v, %v", original, err)
	}
}

func TestAttachedRunKeepsCapturedStoreAfterRequestCloseAndReplacement(t *testing.T) {
	rt := newTestRuntime(t, &fakeClient{})
	const id = "captured-run-thread"
	if _, err := rt.Sessions.CreateWithMetadata(id, rt.RootDir); err != nil {
		t.Fatal(err)
	}
	srv := New(rt, &lockedBuffer{})
	defer srv.Close()
	binding, err := rt.RetainState()
	if err != nil {
		t.Fatal(err)
	}
	store, err := execution.NewStore(binding.Documents)
	if err != nil {
		t.Fatal(err)
	}
	run, err := store.Create(context.Background(), execution.CreateParams{RuntimeID: "captured-runtime", ThreadID: id, Request: execution.Request{Mode: execution.ModeStart}, Runtime: execution.RuntimeManifest{Resolved: execution.Selection{Provider: "test", Model: "test"}, ProtocolVersion: ProtocolVersion}, Workspace: execution.WorkspaceRef{Root: rt.RootDir}})
	if err != nil {
		t.Fatal(err)
	}
	srv.registerExecutionRun(binding, store, run, nil)
	if err := binding.Close(); err != nil {
		t.Fatal(err)
	}
	replacement, err := persistence.Open(context.Background(), t.TempDir(), "jsonl")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = replacement.Close() })
	rt.Documents, rt.Sessions = replacement, session.NewStore(context.Background(), replacement)
	current := testRuntimeState(t, rt)
	view, err := srv.readExecutionRun(current, context.Background(), run.ID)
	if err != nil || view.Run.ID != run.ID || !view.Attached {
		t.Fatalf("attached run switched authority: %+v, %v", view, err)
	}
	settled, err := srv.failAndDetachExecutionRun(run.ID, execution.StatusInterrupted, "test_stop", "cancelled", context.Canceled)
	if err != nil || settled.Status != execution.StatusInterrupted {
		t.Fatalf("settle original run: %+v, %v", settled, err)
	}
	original, err := store.Get(context.Background(), run.ID)
	if err != nil || original.Status != execution.StatusInterrupted {
		t.Fatalf("original run was not settled: %+v, %v", original, err)
	}
	replacementStore, err := execution.NewStore(replacement)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := replacementStore.Get(context.Background(), run.ID); err == nil {
		t.Fatal("run was copied into replacement authority")
	}
}
