package appserver

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"slices"
	"sync/atomic"
	"testing"
	"time"

	"github.com/blueberrycongee/wuu/internal/agent"
	"github.com/blueberrycongee/wuu/internal/providers"
	"github.com/blueberrycongee/wuu/internal/providers/openai"
)

func TestResponsesFinalTextAfterToolBoundary(t *testing.T) {
	for _, tc := range []struct {
		name       string
		finalText  string
		secondText string
	}{
		{name: "correction", finalText: "Checking corrected target."},
		{name: "missing_suffix", finalText: "Checking old target. Then the new target."},
		{name: "cleared_message"},
		{name: "multiple_messages", finalText: "Checking corrected target.", secondText: " Additional context."},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var requests atomic.Int32
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.Header().Set("Content-Type", "text/event-stream")
				if requests.Add(1) != 1 {
					fmt.Fprint(w, "data: {\"type\":\"response.output_text.delta\",\"delta\":\"Done.\",\"item_id\":\"answer\",\"output_index\":0}\n\n")
					fmt.Fprint(w, "data: {\"type\":\"response.completed\",\"response\":{\"id\":\"resp_2\",\"status\":\"completed\",\"output\":[]}}\n\n")
					return
				}
				tool := `{"id":"fc_1","type":"function_call","call_id":"call_1","name":"echo_tool","arguments":"{}"}`
				for _, event := range []string{
					`{"type":"response.output_item.added","output_index":0,"item":{"id":"msg_1","type":"message","phase":"commentary"}}`,
					`{"type":"response.output_text.delta","item_id":"msg_1","output_index":0,"delta":"Checking old target."}`,
					`{"type":"response.output_item.done","output_index":0,"item":{"id":"msg_1","type":"message","phase":"commentary","content":[{"type":"output_text","text":"Checking old target."}]}}`,
					`{"type":"response.output_item.added","output_index":1,"item":{"id":"fc_1","type":"function_call","call_id":"call_1","name":"echo_tool","arguments":""}}`,
					`{"type":"response.output_item.done","output_index":1,"item":` + tool + `}`,
				} {
					fmt.Fprintf(w, "data: %s\n\n", event)
				}
				output := fmt.Sprintf(`{"id":"msg_1","type":"message","phase":"commentary","content":[{"type":"output_text","text":%q}]},%s`, tc.finalText, tool)
				if tc.secondText != "" {
					fmt.Fprintf(w, "data: %s\n\n", fmt.Sprintf(`{"type":"response.output_text.delta","item_id":"msg_2","output_index":2,"delta":%q}`, tc.secondText))
					output += fmt.Sprintf(`,{"id":"msg_2","type":"message","phase":"commentary","content":[{"type":"output_text","text":%q}]}`, tc.secondText)
				}
				fmt.Fprintf(w, "data: %s\n\n", `{"type":"response.completed","response":{"id":"resp_1","status":"completed","output":[`+output+`]}}`)
			}))
			defer server.Close()
			store := false
			client, err := openai.New(openai.ClientConfig{
				BaseURL: server.URL, APIKey: "test", WireAPI: "responses", ResponsesStore: &store,
				ResponsesTransport: providers.StreamTransportSSE,
			})
			if err != nil {
				t.Fatal(err)
			}
			ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			defer cancel()
			now := time.Unix(0, 0).UTC()
			th := newThreadState("thread", nil, "openai", "gpt-test", "/repo", false, now)
			user := providers.ChatMessage{Role: "user", Content: "inspect"}
			th.startTurnLocked("turn", user, now)
			runner := agent.StreamRunner{Client: client, Model: "gpt-test", Tools: fastToolExecutor{}, MaxSteps: 3}
			result, err := runner.RunWithCallback(ctx, []providers.ChatMessage{user}, func(event providers.StreamEvent) {
				th.applyStreamEventLocked("turn", event, now)
			})
			if err != nil {
				t.Fatal(err)
			}
			var persisted, visible []string
			for _, msg := range result.NewMessages {
				if msg.Role == "assistant" && msg.Content != "" {
					persisted = append(persisted, msg.Content)
				}
			}
			toolCount := 0
			for _, item := range th.ensureTurnLocked("turn", now).Items {
				if item.Type == ThreadItemAgentMessage {
					visible = append(visible, item.Text)
				}
				if item.Type == ThreadItemToolCall {
					toolCount++
					if item.Status != ThreadItemStatusCompleted || item.Result == "" {
						t.Fatalf("text reconciliation lost tool completion: %+v", item)
					}
				}
			}
			want := []string{"Done."}
			if text := tc.finalText + tc.secondText; text != "" {
				want = append([]string{text}, want...)
			}
			if !slices.Equal(visible, want) || !slices.Equal(persisted, want) {
				t.Fatalf("visible=%q persisted=%q, want %q", visible, persisted, want)
			}
			if toolCount != 1 || requests.Load() != 2 {
				t.Fatalf("tools=%d requests=%d, want one tool and two model steps", toolCount, requests.Load())
			}
		})
	}
}

func TestThreadStateReconcilesOnlyCurrentOperationAcrossRetry(t *testing.T) {
	now := time.Unix(0, 0).UTC()
	th := newThreadState("thread", nil, "provider", "model", "/repo", false, now)
	th.startTurnLocked("turn", providers.ChatMessage{Role: "user", Content: "inspect"}, now)
	apply := func(ev providers.StreamEvent) []outboundNotification {
		return th.applyStreamEventLocked("turn", ev, now)
	}
	connecting := func(id string) {
		apply(providers.StreamEvent{Type: providers.EventLifecycle, Lifecycle: &providers.StreamLifecycle{
			Phase: providers.StreamPhaseConnecting, OperationID: id,
		}})
	}
	connecting("previous")
	apply(providers.StreamEvent{Type: providers.EventContentDelta, Content: "Previous message."})
	apply(providers.StreamEvent{Type: providers.EventMessage, Message: &providers.ChatMessage{
		Role: "assistant", Content: "Previous message.", Phase: providers.MessagePhaseCommentary,
	}})
	connecting("current")
	apply(providers.StreamEvent{Type: providers.EventContentDelta, Content: "Stale prefix."})
	prefixID := th.activeAgentItemID
	apply(providers.StreamEvent{Type: providers.EventToolUseStart, ToolCall: &providers.ToolCall{ID: "call_1", Name: "inspect"}})
	apply(providers.StreamEvent{Type: providers.EventContentDelta, Content: "Stale suffix."})
	suffixID := th.activeAgentItemID
	out := apply(providers.StreamEvent{Type: providers.EventContentReplace, Content: "Corrected."})
	var replaced, removed bool
	for _, notification := range out {
		switch params := notification.params.(type) {
		case AgentMessageReplaceNotification:
			replaced = params.ItemID == prefixID && params.Text == "Corrected."
		case ItemRemovedNotification:
			removed = params.ItemID == suffixID
		}
	}
	if !replaced || !removed {
		t.Fatalf("replacement must update the original row and remove superseded rows: %+v", out)
	}

	// The runner clears text before reporting a replay-safe reconnect. Closing
	// the text row for a tool must not prevent that reset from reaching it.
	apply(providers.StreamEvent{Type: providers.EventToolUseStart, ToolCall: &providers.ToolCall{ID: "call_2", Name: "inspect"}})
	apply(providers.StreamEvent{Type: providers.EventContentReplace})
	apply(providers.StreamEvent{Type: providers.EventLifecycle, Lifecycle: &providers.StreamLifecycle{
		Phase: providers.StreamPhaseReconnecting, OperationID: "current", ResetPartial: true,
	}})
	connecting("current")
	apply(providers.StreamEvent{Type: providers.EventContentDelta, Content: "Fresh answer."})
	apply(providers.StreamEvent{Type: providers.EventMessage, Message: &providers.ChatMessage{
		Role: "assistant", Content: "Fresh answer.", Phase: providers.MessagePhaseFinalAnswer,
	}})
	var visible []string
	for _, item := range th.ensureTurnLocked("turn", now).Items {
		if item.Type == ThreadItemAgentMessage {
			visible = append(visible, item.Text)
		}
		if item.Type == ThreadItemToolCall {
			t.Fatalf("retry retained an abandoned tool: %+v", item)
		}
	}
	if !slices.Equal(visible, []string{"Previous message.", "Fresh answer."}) {
		t.Fatalf("retry altered another operation or retained stale text: %q", visible)
	}
}
