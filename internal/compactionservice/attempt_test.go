package compactionservice

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/blueberrycongee/wuu/internal/pluginhost"
	"github.com/blueberrycongee/wuu/internal/providers"
)

func TestNoteAttemptExhaustionAndSelectedPlan(t *testing.T) {
	s := NewService()
	if err := s.Activate(context.Background()); err != nil {
		t.Fatal(err)
	}
	c := NewClient(func(ctx context.Context, method string, raw json.RawMessage) (json.RawMessage, error) {
		return s.InvokeService(ctx, pluginhost.ServiceInvokeParams{Service: ServiceName, Method: method, Params: raw})
	})
	out, err := c.NoteAttempt(context.Background(), NoteAttemptRequest{Plan: HandoffPlan{Prompt: "owned instructions", MaxBytes: 100}, Attempt: 3, PreviousFailure: "truncated"})
	if err != nil || out.Invoke {
		t.Fatalf("exhausted attempt admitted: %+v %v", out, err)
	}
	if _, err = c.NoteAttempt(context.Background(), NoteAttemptRequest{Attempt: -1}); err == nil {
		t.Fatal("negative attempt accepted")
	}
	calls := 0
	c = NewClient(func(_ context.Context, method string, _ json.RawMessage) (json.RawMessage, error) {
		calls++
		if method != "note-attempt" {
			t.Fatalf("unexpected %s", method)
		}
		return json.Marshal(NoteAttemptPlan{Messages: []providers.ChatMessage{{Role: "user", Content: "independent retry"}}, Invoke: true, Prompt: "independent retry", MaxBytes: 50})
	})
	out, err = c.NoteAttempt(context.Background(), NoteAttemptRequest{Plan: HandoffPlan{Prompt: "ignored"}})
	if err != nil || out.Prompt != "independent retry" || calls != 1 {
		t.Fatalf("selected plan overwritten: %+v %v", out, err)
	}
}
