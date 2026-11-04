package appserver

import (
	"context"
	"fmt"
	"io"
	"sync"
	"testing"
	"time"

	"github.com/blueberrycongee/wuu/internal/persistence"
	"github.com/blueberrycongee/wuu/internal/providers"
	"github.com/blueberrycongee/wuu/internal/session"
)

// The recovery list is an offer to continue work interrupted by the last exit,
// not a replay of old background results or deliberately stopped turns.
func TestRestartRecoveryOnlyLastUnexpectedExit(t *testing.T) {
	rt := newTestRuntime(t, &fakeClient{})
	now := time.Now().UTC()
	previousBoot := now.Add(-2 * time.Hour)
	cases := []struct {
		id, state, reason string
		recordTurnID      string
		started           time.Time
		unexpected        bool
		terminal          string
		want              bool
	}{
		{id: "crashed-turn", state: "running", started: now.Add(-time.Hour), want: true},
		{id: "lost-parent-turn", state: "pending", reason: "interrupted", started: now.Add(-time.Hour), unexpected: true, want: true},
		{id: "older-crash", state: "pending", reason: "interrupted", started: now.Add(-3 * time.Hour), unexpected: true},
		{id: "old-background", state: "pending", reason: "background_result", started: now.Add(-time.Hour)},
		{id: "legacy-pending", state: "pending", reason: "interrupted", started: now.Add(-time.Hour)},
		{id: "user-stopped", state: "idle", started: now.Add(-time.Hour), terminal: "interrupted"},
		{id: "completed-before-exit", state: "running", started: now.Add(-time.Hour), terminal: "completed"},
		{id: "failed-before-exit", state: "running", started: now.Add(-time.Hour), terminal: "failed"},
		{id: "retried-completed", state: "running", recordTurnID: "retried-completed-turn-0002", started: now.Add(-time.Hour), terminal: "completed"},
	}
	for _, tc := range cases {
		if _, err := rt.Sessions.CreateWithMetadata(tc.id, rt.RootDir); err != nil {
			t.Fatal(err)
		}
		if _, err := appendChatMessage(rt.Sessions, tc.id, providers.ChatMessage{Role: "user", Content: "fixture"}); err != nil {
			t.Fatal(err)
		}
		if tc.terminal != "" {
			if err := rt.Sessions.AppendHistoryRecord(tc.id, session.HistoryRecord{
				Role: "meta", Content: "turn_terminal", ClientID: tc.id + "-turn-0001", StopReason: tc.terminal, At: now.Add(-time.Minute),
			}); err != nil {
				t.Fatal(err)
			}
		}
	}
	if err := persistence.Update(context.Background(), rt.Documents, func(tx *persistence.Tx) error {
		if err := tx.Put(restartRecoveryBootNamespace, "boot", previousBoot); err != nil {
			return err
		}
		for _, tc := range cases {
			turnID := tc.recordTurnID
			if turnID == "" {
				turnID = tc.id + "-turn-0001"
			}
			if err := tx.Put(restartRecoveryNamespace, tc.id, map[string]any{
				"thread_id": tc.id, "turn_id": turnID, "state": tc.state,
				"reason": tc.reason, "allowed_at": tc.started, "updated_at": tc.started,
				"unexpected_exit": tc.unexpected,
			}); err != nil {
				return err
			}
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	out := &lockedBuffer{}
	srv := New(rt, out)
	t.Cleanup(srv.Close)
	if srv.startupErr != nil {
		t.Fatal(srv.startupErr)
	}
	if err := srv.handleLine(context.Background(), []byte(`{"id":"recovery","method":"thread/recovery/list"}`)); err != nil {
		t.Fatal(err)
	}
	response := waitForResponseByID(t, out, "recovery")
	if response["error"] != nil {
		t.Fatalf("recovery list failed: %+v", response)
	}
	result := remarshal[struct {
		Items []RestartRecoveryItem `json:"items"`
	}](t, response["result"])
	got := map[string]bool{}
	for _, item := range result.Items {
		got[item.Thread.ID] = true
	}
	for _, tc := range cases {
		if got[tc.id] != tc.want {
			t.Errorf("recovery offer for %s = %t, want %t; all=%v", tc.id, got[tc.id], tc.want, got)
		}
	}
}

func TestRestartRecoveryExcludesDeliberateShutdown(t *testing.T) {
	client := newPartialBlockingStreamClient("partial")
	rt := newTestRuntime(t, &fakeClient{})
	rt.StreamRunner.Client = client
	attachTestModelGeneration(t, rt)
	out := &lockedBuffer{}
	srv := New(rt, out)
	if err := srv.handleLine(context.Background(), []byte(`{"id":"start","method":"thread/start"}`)); err != nil {
		t.Fatal(err)
	}
	threadID := remarshal[ThreadStartResult](t, responseByID(t, parseOutput(t, out.String()), "start")["result"]).Thread.ID
	request := fmt.Sprintf(`{"id":"turn","method":"turn/start","params":{"thread_id":%q,"prompt":"inspect"}}`, threadID)
	if err := srv.handleLine(context.Background(), []byte(request)); err != nil {
		t.Fatal(err)
	}
	select {
	case <-client.started:
	case <-time.After(5 * time.Second):
		t.Fatal("turn did not start")
	}
	if err := srv.handleLine(context.Background(), []byte(`{"id":"shutdown","method":"shutdown"}`)); err != errShutdown {
		t.Fatalf("deliberate shutdown: %v", err)
	}
	resumedOut := &lockedBuffer{}
	resumed := New(rt, resumedOut)
	t.Cleanup(resumed.Close)
	if err := resumed.handleLine(context.Background(), []byte(`{"id":"recovery","method":"thread/recovery/list"}`)); err != nil {
		t.Fatal(err)
	}
	response := waitForResponseByID(t, resumedOut, "recovery")
	result := remarshal[struct {
		Items []RestartRecoveryItem `json:"items"`
	}](t, response["result"])
	if len(result.Items) != 0 {
		t.Fatalf("deliberate shutdown was offered as an unexpected interruption: %+v", result.Items)
	}
}

type blockedRecoveryDocuments struct {
	persistence.Documents
	entered, release chan struct{}
	once             sync.Once
}

func (d *blockedRecoveryDocuments) Read(ctx context.Context, q persistence.Query) (persistence.View, error) {
	if q.Namespace == restartRecoveryNamespace && len(q.Keys) == 0 {
		d.once.Do(func() { close(d.entered) })
		select {
		case <-d.release:
		case <-ctx.Done():
			return persistence.View{}, ctx.Err()
		}
	}
	return d.Documents.Read(ctx, q)
}

func TestRestartRecoveryDoesNotBlockSkillList(t *testing.T) {
	rt := newTestRuntime(t, &fakeClient{})
	docs := &blockedRecoveryDocuments{Documents: rt.Documents, entered: make(chan struct{}), release: make(chan struct{})}
	rt.Documents = docs
	rt.Sessions = session.NewStore(context.Background(), docs)
	out := &lockedBuffer{}
	srv := New(rt, out)
	var release sync.Once
	t.Cleanup(func() { release.Do(func() { close(docs.release) }); srv.Close() })
	if srv.startupErr != nil {
		t.Fatal(srv.startupErr)
	}
	reader, writer := io.Pipe()
	t.Cleanup(func() { _ = writer.Close(); _ = reader.Close() })
	scannerDone := make(chan error, 1)
	go func() { scannerDone <- runStdioScanner(context.Background(), srv, reader) }()
	if _, err := fmt.Fprintln(writer, `{"id":"recovery","method":"thread/recovery/list"}`); err != nil {
		t.Fatal(err)
	}
	select {
	case <-docs.entered:
	case <-time.After(5 * time.Second):
		t.Fatal("recovery did not reach the storage barrier")
	}
	written := make(chan error, 1)
	go func() { _, err := fmt.Fprintln(writer, `{"id":"skills","method":"skill/list"}`); written <- err }()
	select {
	case err := <-written:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("recovery blocked the skill request in the stdio queue")
	}
	if response := waitForResponseByID(t, out, "skills"); response["error"] != nil {
		t.Fatalf("skill list failed while recovery was waiting: %+v", response)
	}
	release.Do(func() { close(docs.release) })
	if response := waitForResponseByID(t, out, "recovery"); response["error"] != nil {
		t.Fatalf("recovery failed after storage resumed: %+v", response)
	}
	_ = writer.Close()
	if err := <-scannerDone; err != nil {
		t.Fatal(err)
	}
}
