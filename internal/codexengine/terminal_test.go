package codexengine

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"

	"github.com/blueberrycongee/wuu/internal/providers"
)

func TestTerminalStatusSettlesWithoutSeparateErrorNotification(t *testing.T) {
	for _, status := range []string{"failed", "interrupted", "completed"} {
		t.Run(status, func(t *testing.T) {
			var visibleErrors []error
			sub := &turnSubscription{
				turnID: "turn", done: make(chan turnOutcome, 1), events: make(chan turnNotification, 2),
				sink: func(ev providers.StreamEvent) {
					if ev.Type == providers.EventError {
						visibleErrors = append(visibleErrors, ev.Error)
					}
				},
			}
			sub.events <- turnNotification{NotifyAgentMessageDelta, json.RawMessage(`{"turnId":"turn","delta":"partial"}`)}
			sub.events <- turnNotification{NotifyTurnCompleted, json.RawMessage(`{"turn":{"id":"turn","status":"` + status + `","error":{"message":"capacity exhausted"}}}`)}
			close(sub.events)
			sub.run()
			out := <-sub.done
			if out.result.Result.Content != "partial" {
				t.Fatalf("lost partial output: %+v", out.result)
			}
			switch status {
			case "failed":
				if out.err == nil || !strings.Contains(out.err.Error(), "capacity exhausted") || len(visibleErrors) != 1 || out.result.Result.FinishReason != providers.FinishReasonError {
					t.Fatalf("failure not propagated: %+v, events %v", out, visibleErrors)
				}
			case "interrupted":
				if !errors.Is(out.err, context.Canceled) || len(visibleErrors) != 0 {
					t.Fatalf("interruption not silent: %+v, events %v", out, visibleErrors)
				}
			default:
				if out.err != nil || len(visibleErrors) != 0 {
					t.Fatalf("successful turn failed: %+v", out)
				}
			}
		})
	}
}
