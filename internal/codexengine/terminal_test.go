package codexengine

import (
	"context"
	"encoding/json"
	"errors"
	"runtime"
	"strings"
	"testing"

	"github.com/blueberrycongee/wuu/internal/providers"
)

func TestTurnTerminalDoesNotOvertakeAcceptedNotifications(t *testing.T) {
	// Start the consumer and producer together so the queue can become nonempty
	// while next is deciding whether to deliver a separately stored terminal.
	// Repeat without sleeps: this exercises the real enqueue/dequeue paths rather
	// than adding a scheduler hook to the production subscription.
	for attempt := 0; attempt < 20000; attempt++ {
		sub := &turnSubscription{
			threadID: "thread", turnID: "turn", events: make(chan turnNotification, 512),
			wake: make(chan struct{}, 1), nativeStopped: make(chan struct{}),
		}
		start := make(chan struct{})
		result := make(chan []string, 1)
		go func() {
			<-start
			var methods []string
			for {
				notification, err := sub.next()
				if err != nil {
					methods = append(methods, err.Error())
					break
				}
				methods = append(methods, notification.method)
				if notification.method == NotifyTurnCompleted {
					break
				}
			}
			result <- methods
		}()
		close(start)
		runtime.Gosched()
		sub.enqueue(turnNotification{NotifyAgentMessageDelta, json.RawMessage(`{"threadId":"thread","turnId":"turn","delta":"complete answer"}`)})
		sub.enqueue(turnNotification{NotifyItemCompleted, json.RawMessage(`{"threadId":"thread","turnId":"turn","item":{"id":"message","type":"agentMessage","phase":"final_answer","text":"complete answer"}}`)})
		sub.enqueue(turnNotification{NotifyTokenUsageUpdated, json.RawMessage(`{"threadId":"thread","turnId":"turn","tokenUsage":{"total":{"totalTokens":3},"last":{"inputTokens":1,"outputTokens":2}}}`)})
		sub.enqueue(turnNotification{NotifyTurnCompleted, json.RawMessage(`{"threadId":"thread","turn":{"id":"turn","status":"completed"}}`)})
		methods := <-result
		want := strings.Join([]string{NotifyAgentMessageDelta, NotifyItemCompleted, NotifyTokenUsageUpdated, NotifyTurnCompleted}, ",")
		if got := strings.Join(methods, ","); got != want {
			t.Fatalf("attempt %d: terminal overtook accepted output: got %s, want %s", attempt, got, want)
		}
	}
}

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
