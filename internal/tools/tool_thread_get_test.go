package tools

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"

	"github.com/blueberrycongee/wuu/internal/persistence"
	"github.com/blueberrycongee/wuu/internal/providers"
	"github.com/blueberrycongee/wuu/internal/session"
)

func newToolSessionStore(t *testing.T, backend string) *session.Store {
	t.Helper()
	documents, err := persistence.Open(context.Background(), t.TempDir(), backend)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { documents.Close() })
	return session.NewStore(context.Background(), documents)
}

func TestThreadGetToolReturnsSessionAndHistory(t *testing.T) {
	store := newToolSessionStore(t, "jsonl")

	const id = "test-thread-happy"
	sess, err := store.CreateInitialized(session.Session{ID: id, CWD: "/tmp/workdir"}, nil)
	if err != nil {
		t.Fatalf("create session: %v", err)
	}
	if err := store.AppendHistoryRecord(sess.ID, session.HistoryRecord{
		Role:    "user",
		Content: "hello",
	}); err != nil {
		t.Fatalf("append history: %v", err)
	}
	if err := store.AppendHistoryRecord(sess.ID, session.HistoryRecord{
		Role:    "assistant",
		Content: "world",
	}); err != nil {
		t.Fatalf("append history: %v", err)
	}

	tool := NewThreadGetTool(store)
	out, err := tool.Execute(context.Background(), `{"thread_id":"`+id+`"}`)
	if err != nil {
		t.Fatalf("execute: %v", err)
	}

	var payload struct {
		ThreadID string                  `json:"thread_id"`
		Session  session.Session         `json:"session"`
		History  []session.HistoryRecord `json:"history"`
	}
	if err := json.Unmarshal([]byte(out), &payload); err != nil {
		t.Fatalf("parse output: %v\noutput: %s", err, out)
	}
	if payload.ThreadID != id {
		t.Errorf("expected thread_id %q, got %q", id, payload.ThreadID)
	}
	if payload.Session.ID != id {
		t.Errorf("expected session.id %q, got %q", id, payload.Session.ID)
	}
	if got, want := len(payload.History), 2; got != want {
		t.Fatalf("expected %d history records, got %d", want, got)
	}
	if payload.History[0].Content != "hello" || payload.History[1].Content != "world" {
		t.Errorf("unexpected history contents: %+v", payload.History)
	}
}

func TestThreadGetToolContinuationPinsHistorySnapshotAcrossAppends(t *testing.T) {
	store := newToolSessionStore(t, "jsonl")
	const id = "snapshot-thread"
	if _, err := store.CreateInitialized(session.Session{ID: id, CWD: "/tmp/workdir"}, nil); err != nil {
		t.Fatalf("create session: %v", err)
	}
	for _, record := range []session.HistoryRecord{
		{Role: "user", Content: "one"},
		{Role: "assistant", Content: "two"},
		{Role: "user", Content: "three"},
		{Role: "assistant", Content: "four"},
	} {
		if err := store.AppendHistoryRecord(id, record); err != nil {
			t.Fatalf("append history: %v", err)
		}
	}
	tool := NewThreadGetTool(store)
	firstRaw, err := tool.Execute(context.Background(), `{"thread_id":"snapshot-thread"}`)
	if err != nil {
		t.Fatalf("first execute: %v", err)
	}
	var first struct {
		SnapshotSeq   int                     `json:"snapshot_seq"`
		SnapshotToken string                  `json:"snapshot_token"`
		History       []session.HistoryRecord `json:"history"`
	}
	if err := json.Unmarshal([]byte(firstRaw), &first); err != nil {
		t.Fatalf("parse first result: %v", err)
	}
	if first.SnapshotSeq != 4 || first.SnapshotToken == "" {
		t.Fatalf("missing initial snapshot: %+v", first)
	}
	if err := store.AppendHistoryRecord(id, session.HistoryRecord{Role: "user", Content: "appended later"}); err != nil {
		t.Fatalf("append later record: %v", err)
	}
	args, _ := json.Marshal(map[string]any{
		"thread_id":         id,
		"before_seq":        4,
		"snapshot_seq":      first.SnapshotSeq,
		"expected_snapshot": first.SnapshotToken,
	})
	secondRaw, err := tool.Execute(context.Background(), string(args))
	if err != nil {
		t.Fatalf("continuation after append: %v", err)
	}
	var second struct {
		SnapshotToken string                  `json:"snapshot_token"`
		History       []session.HistoryRecord `json:"history"`
	}
	if err := json.Unmarshal([]byte(secondRaw), &second); err != nil {
		t.Fatalf("parse continuation: %v", err)
	}
	if second.SnapshotToken != first.SnapshotToken || len(second.History) != 4 {
		t.Fatalf("append changed pinned snapshot: first=%+v second=%+v", first, second)
	}
	for _, record := range second.History {
		if record.Content == "appended later" {
			t.Fatal("continuation leaked a record appended after snapshot_seq")
		}
	}
}

func TestThreadGetToolContinuationRequiresSnapshotBinding(t *testing.T) {
	tool := NewThreadGetTool(nil)
	_, err := tool.Execute(context.Background(), `{"thread_id":"x","before_seq":10}`)
	if err == nil || !strings.Contains(err.Error(), "snapshot_seq and expected_snapshot are required") {
		t.Fatalf("unbound continuation was not rejected: %v", err)
	}
}

func TestThreadGetToolReturnsErrSessionNotFound(t *testing.T) {
	store := newToolSessionStore(t, "jsonl")

	tool := NewThreadGetTool(store)
	_, err := tool.Execute(context.Background(), `{"thread_id":"missing-id"}`)
	if err == nil {
		t.Fatal("expected error for missing thread id")
	}
	if !errors.Is(err, session.ErrSessionNotFound) {
		t.Errorf("expected session.ErrSessionNotFound, got: %v", err)
	}
	if !strings.Contains(err.Error(), "missing-id") {
		t.Errorf("expected error to include the id %q, got: %v", "missing-id", err)
	}
}

// A template update must not change an existing consumer's captured provider,
// and an unavailable/canceled binding must fail instead of opening local state.
func TestSessionToolsRetainCapturedStore(t *testing.T) {
	for _, backend := range []string{"sqlite", "jsonl"} {
		t.Run(backend, func(t *testing.T) {
			original := newToolSessionStore(t, backend)
			next := newToolSessionStore(t, backend)
			for _, fixture := range []struct {
				store   *session.Store
				content string
			}{{original, "original"}, {next, "replacement"}} {
				if _, err := fixture.store.CreateInitialized(session.Session{ID: "same", CWD: t.TempDir()}, []session.HistoryRecord{{Role: "user", Content: fixture.content}}); err != nil {
					t.Fatal(err)
				}
			}
			kit, err := New(t.TempDir())
			if err != nil {
				t.Fatal(err)
			}
			kit.SetSessionID("same")
			kit.SetSessionStore(original)
			retained, err := kit.CloneForRoot(t.TempDir())
			if err != nil {
				t.Fatal(err)
			}
			kit.SetSessionStore(next)
			call := providers.ToolCall{Name: historyReadToolName, Arguments: `{"start_seq":1}`}
			got, err := retained.Execute(context.Background(), call)
			if err != nil || !strings.Contains(got, "original") {
				t.Fatalf("retained binding: %s, %v", got, err)
			}
			got, err = kit.Execute(context.Background(), call)
			if err != nil || !strings.Contains(got, "replacement") {
				t.Fatalf("new binding: %s, %v", got, err)
			}
			kit.SetSessionStore(nil)
			if _, err := kit.Execute(context.Background(), call); err == nil {
				t.Fatal("missing binding fell back")
			}
			ctx, cancel := context.WithCancel(context.Background())
			cancel()
			if _, err := NewThreadGetTool(original).Execute(ctx, `{"thread_id":"same"}`); !errors.Is(err, context.Canceled) {
				t.Fatalf("canceled read: %v", err)
			}
		})
	}
}
