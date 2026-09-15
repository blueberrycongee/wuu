package claudeengine

import (
	"strings"
	"testing"

	"github.com/blueberrycongee/wuu/internal/providers"
)

func TestTerminalFailureDetails(t *testing.T) {
	for _, tc := range []struct {
		name, result, want string
	}{
		{"errors array", `{"type":"result","subtype":"error_during_execution","is_error":true,"errors":["quota exhausted"," ","retry tomorrow"],"usage":{"input_tokens":7,"output_tokens":3}}`, "quota exhausted\nretry tomorrow"},
		{"limit subtype", `{"type":"result","subtype":"error_max_turns","is_error":false,"errors":[]}`, "error_max_turns"},
		{"legacy detail", `{"type":"result","is_error":true,"error":{"message":"permission denied"}}`, "permission denied"},
		{"malformed result", `{"type":"result","is_error":false,"result":42}`, "invalid terminal result"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var events []providers.StreamEvent
			done := make(chan turnOutcome, 1)
			sub := newTurnSubscription(func(ev providers.StreamEvent) { events = append(events, ev) }, done)
			sub.handleLine(`{"type":"assistant","message":{"content":[{"type":"text","text":"partial"}]}}`)
			sub.handleLine(tc.result)
			out := <-done
			if out.err == nil || !strings.Contains(out.err.Error(), tc.want) || out.result.Result.FinishReason != providers.FinishReasonError {
				t.Fatalf("failure lost: %+v", out)
			}
			if out.result.Result.Content != "partial" {
				t.Fatalf("partial output lost: %+v", out.result)
			}
			if tc.name == "errors array" && (out.result.Result.InputTokens != 7 || out.result.Result.OutputTokens != 3) {
				t.Fatalf("failed turn usage lost: %+v", out.result)
			}
			failures := 0
			for _, ev := range events {
				if ev.Type == providers.EventDone {
					t.Fatal("failure emitted successful completion")
				}
				if ev.Type == providers.EventError {
					failures++
				}
			}
			if failures != 1 {
				t.Fatalf("error events = %d, want 1", failures)
			}
		})
	}
}
