package codexengine

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/blueberrycongee/wuu/internal/providers"
)

func TestTurnRecoveryNotifications(t *testing.T) {
	for _, tc := range []struct {
		name        string
		ending      []turnNotification
		wantError   string
		wantContent string
	}{
		{
			name: "recovers and preserves output",
			ending: []turnNotification{
				{NotifyAgentMessageDelta, json.RawMessage(`{"turnId":"turn","delta":"after"}`)},
				{NotifyTurnCompleted, json.RawMessage(`{"turn":{"id":"turn","status":"completed"}}`)},
			},
			wantContent: "before after",
		},
		{
			name:        "completes without more output",
			ending:      []turnNotification{{NotifyTurnCompleted, json.RawMessage(`{"turn":{"id":"turn","status":"completed"}}`)}},
			wantContent: "before",
		},
		{
			name:      "retry exhausted",
			ending:    []turnNotification{{NotifyError, json.RawMessage(`{"turnId":"turn","willRetry":false,"error":{"message":"connection reset"}}`)}},
			wantError: "connection reset",
		},
		{
			name:      "legacy terminal error",
			ending:    []turnNotification{{NotifyError, json.RawMessage(`{"turnId":"turn","error":{"message":"permission denied"}}`)}},
			wantError: "permission denied",
		},
		{
			name:      "transport closes during retry",
			wantError: "stream closed before completion",
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var events []providers.StreamEvent
			sub := &turnSubscription{
				turnID: "turn",
				done:   make(chan turnOutcome, 1),
				events: make(chan turnNotification, 10),
				sink:   func(ev providers.StreamEvent) { events = append(events, ev) },
			}
			sub.events <- turnNotification{NotifyAgentMessageDelta, json.RawMessage(`{"turnId":"turn","delta":"before "}`)}
			// Human text is not the retry contract.
			sub.events <- turnNotification{NotifyError, json.RawMessage(`{"turnId":"turn","willRetry":true,"error":{"message":"temporary upstream failure"}}`)}
			sub.events <- turnNotification{NotifyError, json.RawMessage(`{"turnId":"other","willRetry":false,"error":{"message":"unrelated failure"}}`)}
			sub.events <- turnNotification{NotifyError, json.RawMessage(`{"turnId":"turn","willRetry":true,"error":{"message":"Reconnecting... 2/5"}}`)}
			for _, n := range tc.ending {
				sub.events <- n
			}
			close(sub.events)
			sub.run()
			out := <-sub.done
			if tc.wantError != "" {
				if out.err == nil || !strings.Contains(out.err.Error(), tc.wantError) {
					t.Fatalf("error = %v, want %q", out.err, tc.wantError)
				}
			} else if out.err != nil || out.result.Result.Content != tc.wantContent || out.result.Result.StopReason != "completed" {
				t.Fatalf("outcome = %+v", out)
			}
			retries, connected, errors := 0, 0, 0
			for _, ev := range events {
				if ev.Type == providers.EventError {
					errors++
				}
				if ev.Lifecycle != nil {
					switch ev.Lifecycle.Phase {
					case providers.StreamPhaseReconnecting:
						retries++
						if ev.Lifecycle.ResetPartial {
							t.Fatal("retry must preserve prior output")
						}
					case providers.StreamPhaseConnected:
						connected++
					}
				}
			}
			if retries != 2 {
				t.Fatalf("retries = %d, want 2", retries)
			}
			if tc.wantError == "" && (connected != 1 || errors != 0) {
				t.Fatalf("connected=%d errors=%d", connected, errors)
			}
			if tc.wantError != "" && connected != 0 {
				t.Fatal("reported recovery before failure")
			}
		})
	}
}
