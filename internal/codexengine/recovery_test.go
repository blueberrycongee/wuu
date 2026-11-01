package codexengine

import (
	"encoding/json"
	"os"
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
			retries, connected, failed, errors := 0, 0, 0, 0
			var lastReconnect, lastFailed providers.StreamLifecycle
			for _, ev := range events {
				if ev.Type == providers.EventError {
					errors++
				}
				if ev.Lifecycle != nil {
					switch ev.Lifecycle.Phase {
					case providers.StreamPhaseReconnecting:
						retries++
						lastReconnect = *ev.Lifecycle
						if ev.Lifecycle.ResetPartial {
							t.Fatal("retry must preserve prior output")
						}
					case providers.StreamPhaseConnected:
						connected++
					case providers.StreamPhaseFailed:
						failed++
						lastFailed = *ev.Lifecycle
					}
				}
			}
			if retries != 2 || lastReconnect.RetryCount != 2 || lastReconnect.MaxRetries != 5 {
				t.Fatalf("retries = %d (last=%+v), want 2 with RetryCount=2 MaxRetries=5", retries, lastReconnect)
			}
			if tc.wantError == "" && (connected != 1 || failed != 0 || errors != 0) {
				t.Fatalf("connected=%d failed=%d errors=%d", connected, failed, errors)
			}
			if tc.wantError != "" && (connected != 0 || failed != 1 || lastFailed.RetryCount != 2 || !strings.Contains(lastFailed.Reason, tc.wantError)) {
				t.Fatalf("failed lifecycle: connected=%d failed=%d lastFailed=%+v, want failed=1 with %q", connected, failed, lastFailed, tc.wantError)
			}
		})
	}
}

func TestCodexTransportStderrPreservedOnExit(t *testing.T) {
	if os.Getenv("WUU_CODEX_STDERR_HELPER") == "1" {
		_, _ = os.Stderr.WriteString("codex fatal: invalid auth token\n")
		os.Exit(1)
	}
	exe, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	transport, err := NewTransport(TransportOptions{
		BinaryPath: exe,
		ExtraArgs:  []string{"-test.run=^TestCodexTransportStderrPreservedOnExit$"},
		Env:        append(os.Environ(), "WUU_CODEX_STDERR_HELPER=1"),
	})
	if err != nil {
		t.Fatal(err)
	}
	defer transport.Close()
	done := make(chan string, 1)
	transport.OnClose(func(reason string) {
		done <- reason
	})
	reason := <-done
	if !strings.Contains(reason, "invalid auth token") {
		t.Fatalf("close reason = %q, want stderr diagnostic preserved", reason)
	}
}
