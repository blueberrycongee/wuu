package tools

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"

	"github.com/blueberrycongee/wuu/internal/session"
	"github.com/blueberrycongee/wuu/internal/statepath"
)

// withTempWuuHome redirects statepath.Home("") at a fresh temp dir for the
// duration of the test, so the tool's "look up the user-level sessions db"
// call lands in an isolated SQLite file and does not pollute the real one.
func withTempWuuHome(t *testing.T) string {
	t.Helper()
	wuuHome := t.TempDir()
	t.Setenv("WUU_HOME", wuuHome)
	home, err := statepath.Home("")
	if err != nil {
		t.Fatalf("statepath.Home: %v", err)
	}
	if home != wuuHome {
		// WUU_HOME may not be the only signal the helper honours; if a
		// different value comes back the test will silently hit the real
		// user state dir, which we must not allow.
		t.Fatalf("statepath.Home did not honour WUU_HOME: got %q want %q", home, wuuHome)
	}
	return wuuHome
}

func TestThreadGetToolReturnsSessionAndHistory(t *testing.T) {
	withTempWuuHome(t)
	home, _ := statepath.Home("")
	sessDir := statepath.SessionsDir(home)
	if sessDir == "" {
		t.Fatal("statepath.SessionsDir returned empty path")
	}

	const id = "test-thread-happy"
	sess, err := session.CreateWithMetadata(sessDir, id, "/tmp/workdir")
	if err != nil {
		t.Fatalf("create session: %v", err)
	}
	if err := session.AppendHistoryRecord(sessDir, sess.ID, session.HistoryRecord{
		Role:    "user",
		Content: "hello",
	}); err != nil {
		t.Fatalf("append history: %v", err)
	}
	if err := session.AppendHistoryRecord(sessDir, sess.ID, session.HistoryRecord{
		Role:    "assistant",
		Content: "world",
	}); err != nil {
		t.Fatalf("append history: %v", err)
	}

	tool := NewThreadGetTool(&Env{})
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
	sessDir := t.TempDir()
	const id = "snapshot-thread"
	if _, err := session.CreateWithMetadata(sessDir, id, "/tmp/workdir"); err != nil {
		t.Fatalf("create session: %v", err)
	}
	for _, record := range []session.HistoryRecord{
		{Role: "user", Content: "one"},
		{Role: "assistant", Content: "two"},
		{Role: "user", Content: "three"},
		{Role: "assistant", Content: "four"},
	} {
		if err := session.AppendHistoryRecord(sessDir, id, record); err != nil {
			t.Fatalf("append history: %v", err)
		}
	}
	tool := NewThreadGetTool(&Env{SessionsDir: sessDir})
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
	if err := session.AppendHistoryRecord(sessDir, id, session.HistoryRecord{Role: "user", Content: "appended later"}); err != nil {
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
	tool := NewThreadGetTool(&Env{})
	_, err := tool.Execute(context.Background(), `{"thread_id":"x","before_seq":10}`)
	if err == nil || !strings.Contains(err.Error(), "snapshot_seq and expected_snapshot are required") {
		t.Fatalf("unbound continuation was not rejected: %v", err)
	}
}

func TestThreadGetToolReturnsErrSessionNotFound(t *testing.T) {
	withTempWuuHome(t)

	tool := NewThreadGetTool(&Env{})
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

func TestThreadGetToolUsesInjectedSessionsDir(t *testing.T) {
	sessDir := t.TempDir()
	const id = "injected-session"
	if _, err := session.CreateWithMetadata(sessDir, id, "/tmp/workdir"); err != nil {
		t.Fatalf("create session: %v", err)
	}
	if err := session.AppendHistoryRecord(sessDir, id, session.HistoryRecord{Role: "user", Content: "from injected store"}); err != nil {
		t.Fatalf("append history: %v", err)
	}

	tool := NewThreadGetTool(&Env{SessionsDir: sessDir})
	out, err := tool.Execute(context.Background(), `{"thread_id":"injected-session"}`)
	if err != nil {
		t.Fatalf("execute: %v", err)
	}
	if !strings.Contains(out, "from injected store") {
		t.Fatalf("thread_get ignored injected store: %s", out)
	}
}

func TestThreadGetToolRejectsEmptyThreadID(t *testing.T) {
	tool := NewThreadGetTool(&Env{})
	_, err := tool.Execute(context.Background(), `{"thread_id":""}`)
	if err == nil {
		t.Fatal("expected error for empty thread_id")
	}
	if !strings.Contains(err.Error(), "thread_id is required") {
		t.Errorf("expected required-error, got: %v", err)
	}
}
