package claudeengine

import (
	"encoding/json"
	"os"
	"strings"
	"testing"

	"github.com/blueberrycongee/wuu/internal/providers"
)

func TestAssistantAPIErrorsStayOutOfReplyAndHistory(t *testing.T) {
	const diagnostic = `API Error: 400 {"error":{"message":"image exceeds size limit"}}`
	assistant := func(text, code, parent string) string {
		envelope := map[string]any{
			"type":    "assistant",
			"message": map[string]any{"content": []any{map[string]any{"type": "text", "text": text}}},
		}
		if code != "" {
			envelope["error"] = code
		}
		if parent != "" {
			envelope["parent_tool_use_id"] = parent
		}
		line, err := json.Marshal(envelope)
		if err != nil {
			t.Fatal(err)
		}
		return string(line)
	}
	failedResult, err := json.Marshal(map[string]any{"type": "result", "is_error": true, "result": diagnostic})
	if err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		name           string
		before         []string
		assistant      string
		result         string
		inputPending   bool
		transportClose bool
		wantBeforeText string
		wantText       string
		wantError      string
	}{
		{
			name: "error only", assistant: assistant(diagnostic, "invalid_request", ""),
			result: string(failedResult), wantError: diagnostic,
		},
		{
			name: "partial reply", before: []string{
				`{"type":"stream_event","event":{"type":"content_block_delta","index":0,"delta":{"type":"text_delta","text":"Useful partial answer."}}}`,
				assistant("Useful partial answer.", "", ""),
			},
			assistant: assistant(diagnostic, "invalid_request", ""), result: string(failedResult),
			wantBeforeText: "Useful partial answer.", wantText: "Useful partial answer.", wantError: diagnostic,
		},
		{
			name: "literal error quotation", assistant: assistant(diagnostic, "", ""),
			result: string(failedResult), wantBeforeText: diagnostic, wantText: diagnostic, wantError: diagnostic,
		},
		{
			name: "terminal omits detail", assistant: assistant(diagnostic, "invalid_request", ""),
			result:    `{"type":"result","subtype":"error_during_execution","is_error":true}`,
			wantError: diagnostic,
		},
		{
			name: "child error is ignored", assistant: assistant(diagnostic, "invalid_request", "child-tool"),
			result:    `{"type":"result","subtype":"error_max_turns","is_error":true}`,
			wantError: "engine turn failed: error_max_turns",
		},
		{
			name: "pending input ignores earlier error", assistant: assistant(diagnostic, "invalid_request", ""),
			inputPending: true, result: `{"type":"result","subtype":"error_max_turns","is_error":true}`,
			wantError: "engine turn failed: error_max_turns",
		},
		{
			name: "transport closes before result", before: []string{assistant("Useful partial answer.", "", "")},
			assistant: assistant(diagnostic, "invalid_request", ""), transportClose: true,
			wantBeforeText: "Useful partial answer.", wantText: "Useful partial answer.", wantError: diagnostic,
		},
		{
			name: "recovered error", assistant: assistant(diagnostic, "invalid_request", ""),
			result:   `{"type":"result","subtype":"success","is_error":false,"result":"Recovered answer."}`,
			wantText: "Recovered answer.",
		},
		{
			name: "new assistant output clears recovered diagnostic", before: []string{assistant(diagnostic, "invalid_request", "")},
			assistant: assistant("Useful partial answer.", "", ""), transportClose: true,
			wantBeforeText: "Useful partial answer.", wantText: "Useful partial answer.", wantError: "claude exited",
		},
		{
			name: "new stream clears recovered diagnostic", before: []string{
				assistant(diagnostic, "invalid_request", ""),
				`{"type":"stream_event","event":{"type":"message_start"}}`,
			},
			assistant:      `{"type":"stream_event","event":{"type":"content_block_delta","index":0,"delta":{"type":"text_delta","text":"Useful partial answer."}}}`,
			transportClose: true, wantBeforeText: "Useful partial answer.", wantText: "Useful partial answer.", wantError: "claude exited",
		},
		{
			name: "ignored notification cannot supply user failure detail", before: []string{assistant(diagnostic, "invalid_request", "")},
			assistant: `{"type":"result","subtype":"error_during_execution","is_error":true,"origin":{"kind":"task-notification"}}`,
			result:    `{"type":"result","subtype":"error_max_turns","is_error":true}`,
			wantError: "engine turn failed: error_max_turns",
		},
		{
			name: "ignored notification cannot supply transport failure detail", before: []string{assistant(diagnostic, "invalid_request", "")},
			assistant:      `{"type":"result","subtype":"error_during_execution","is_error":true,"origin":{"kind":"task-notification"}}`,
			transportClose: true, wantError: "claude exited",
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var events []providers.StreamEvent
			done := make(chan turnOutcome, 1)
			sub := newTurnSubscription(func(ev providers.StreamEvent) { events = append(events, ev) }, done)
			for _, line := range tc.before {
				sub.handleLine(line)
			}
			sub.inputPending = tc.inputPending
			sub.handleLine(tc.assistant)
			var beforeResult strings.Builder
			for _, ev := range events {
				if ev.Type == providers.EventError {
					t.Fatal("assistant API error terminated the turn before its result")
				}
				if ev.Type == providers.EventContentDelta {
					beforeResult.WriteString(ev.Content)
				}
			}
			if beforeResult.String() != tc.wantBeforeText {
				t.Fatalf("reply before terminal result = %q, want %q", beforeResult.String(), tc.wantBeforeText)
			}
			sub.inputPending = false
			if tc.transportClose {
				sub.handleTransportClose("claude exited")
			} else {
				sub.handleLine(tc.result)
			}
			out := <-done
			if tc.wantError == "" {
				if out.err != nil {
					t.Fatalf("recovered turn failed: %v", out.err)
				}
			} else if out.err == nil || out.err.Error() != tc.wantError || out.result.Result.FinishReason != providers.FinishReasonError {
				t.Fatalf("terminal error = %v, want %q", out.err, tc.wantError)
			}
			if out.result.Result.Content != tc.wantText {
				t.Fatalf("reply = %q, want %q", out.result.Result.Content, tc.wantText)
			}
			messages := out.result.Result.NewMessages
			if tc.wantText == "" {
				if len(messages) != 0 {
					t.Fatalf("API error leaked into durable history: %+v", messages)
				}
			} else if len(messages) != 1 || messages[0].Role != "assistant" || messages[0].Content != tc.wantText {
				t.Fatalf("durable assistant output = %+v, want %q", messages, tc.wantText)
			}
			var streamed strings.Builder
			failures, completions := 0, 0
			for _, ev := range events {
				switch ev.Type {
				case providers.EventContentDelta:
					streamed.WriteString(ev.Content)
				case providers.EventContentReplace:
					streamed.Reset()
					streamed.WriteString(ev.Content)
				case providers.EventError:
					failures++
					if ev.Error == nil || ev.Error.Error() != tc.wantError {
						t.Fatalf("notice error = %v, want %q", ev.Error, tc.wantError)
					}
				case providers.EventDone:
					completions++
				}
			}
			if streamed.String() != tc.wantText {
				t.Fatalf("streamed reply = %q, want %q", streamed.String(), tc.wantText)
			}
			wantFailures, wantCompletions := 1, 0
			if tc.wantError == "" {
				wantFailures, wantCompletions = 0, 1
			}
			if failures != wantFailures || completions != wantCompletions {
				t.Fatalf("terminal events = %d errors/%d completions, want %d/%d", failures, completions, wantFailures, wantCompletions)
			}
		})
	}
}

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

func TestSystemAPIErrorRetryLifecycle(t *testing.T) {
	t.Run("recovers after system api_error retry", func(t *testing.T) {
		var phases []providers.StreamLifecyclePhase
		done := make(chan turnOutcome, 1)
		sub := newTurnSubscription(func(ev providers.StreamEvent) {
			if ev.Type == providers.EventLifecycle && ev.Lifecycle != nil {
				phases = append(phases, ev.Lifecycle.Phase)
			}
		}, done)
		sub.handleLine(`{"type":"system","subtype":"api_error","attempt":1,"max_retries":5,"error":{"message":"529 Overloaded"}}`)
		sub.handleLine(`{"type":"system","subtype":"api_error","attempt":2,"max_retries":5,"error":{"message":"529 Overloaded"}}`)
		sub.handleLine(`{"type":"stream_event","event":{"type":"message_start"}}`)
		sub.handleLine(`{"type":"stream_event","event":{"type":"content_block_delta","index":0,"delta":{"type":"text_delta","text":"Recovered"}}}`)
		sub.handleLine(`{"type":"result","subtype":"success","is_error":false,"result":"Recovered"}`)
		out := <-done
		if out.err != nil || out.result.Result.Content != "Recovered" {
			t.Fatalf("outcome = %+v", out)
		}
		want := []providers.StreamLifecyclePhase{
			providers.StreamPhaseReconnecting,
			providers.StreamPhaseReconnecting,
			providers.StreamPhaseConnected,
		}
		if len(phases) != len(want) {
			t.Fatalf("phases = %v, want %v", phases, want)
		}
		for i := range want {
			if phases[i] != want[i] {
				t.Fatalf("phases = %v, want %v", phases, want)
			}
		}
	})

	t.Run("settles reconnect as failed when turn fails after retry", func(t *testing.T) {
		var lifecycles []providers.StreamLifecycle
		done := make(chan turnOutcome, 1)
		sub := newTurnSubscription(func(ev providers.StreamEvent) {
			if ev.Type == providers.EventLifecycle && ev.Lifecycle != nil {
				lifecycles = append(lifecycles, *ev.Lifecycle)
			}
		}, done)
		sub.handleLine(`{"type":"system","subtype":"api_error","attempt":3,"max_retries":3,"error":{"message":"529 Overloaded"}}`)
		sub.handleLine(`{"type":"result","subtype":"error_during_execution","is_error":true}`)
		out := <-done
		if out.err == nil || !strings.Contains(out.err.Error(), "529 Overloaded") {
			t.Fatalf("err = %v, want 529 Overloaded", out.err)
		}
		if len(lifecycles) != 2 || lifecycles[0].Phase != providers.StreamPhaseReconnecting || lifecycles[1].Phase != providers.StreamPhaseFailed || lifecycles[1].RetryCount != 3 {
			t.Fatalf("lifecycles = %+v, want reconnecting then failed (retryCount=3)", lifecycles)
		}
	})
}

func TestTransportStderrPreservedOnExit(t *testing.T) {
	if os.Getenv("WUU_CLAUDE_STDERR_HELPER") == "1" {
		_, _ = os.Stderr.WriteString("Error: Invalid API key. Please run /login\n")
		os.Exit(1)
	}
	exe, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	transport, err := NewTransport(TransportOptions{
		BinaryPath: exe,
		Args:       []string{"-test.run=^TestTransportStderrPreservedOnExit$"},
		Env:        append(os.Environ(), "WUU_CLAUDE_STDERR_HELPER=1"),
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
	if !strings.Contains(reason, "Invalid API key") {
		t.Fatalf("close reason = %q, want stderr diagnostic preserved", reason)
	}
}
