package appserver

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/blueberrycongee/wuu/internal/providers"
	"github.com/blueberrycongee/wuu/internal/providers/anthropic"
	"github.com/blueberrycongee/wuu/internal/providers/openai"
	"github.com/blueberrycongee/wuu/internal/session"
)

// Public RPC -> persisted history -> production adapter -> controlled HTTP.
// These are protocol fixtures, not evidence of live model acceptance.
func TestTurnContinueProtocolEndToEnd(t *testing.T) {
	for _, protocol := range []string{"responses", "chat_completions", "anthropic"} {
		t.Run(protocol, func(t *testing.T) {
			var requests atomic.Int32
			fake := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				requests.Add(1)
				body, err := io.ReadAll(r.Body)
				if err != nil {
					t.Error(err)
					return
				}
				text := string(body)
				if strings.Count(text, "original task") != 1 || !strings.Contains(text, "completed step") || !strings.Contains(text, "tool output saved") || strings.Contains(text, "discard fragment") || strings.Count(text, "call_saved") < 2 {
					t.Errorf("invalid continuation input: %s", text)
				}
				var payload map[string]any
				if err := json.Unmarshal(body, &payload); err != nil {
					t.Error(err)
					return
				}
				if protocol == "anthropic" {
					messages := payload["messages"].([]any)
					if messages[len(messages)-1].(map[string]any)["role"] != "user" || !strings.Contains(text, "signed-thought") {
						t.Errorf("illegal Messages tail/reasoning: %s", text)
					}
				} else if protocol == "chat_completions" {
					messages := payload["messages"].([]any)
					if messages[len(messages)-1].(map[string]any)["role"] != "assistant" {
						t.Errorf("Chat history tail changed: %s", text)
					}
				} else if !strings.Contains(text, "function_call_output") || !strings.Contains(text, "encrypted") {
					t.Errorf("Responses lost tool/reasoning state: %s", text)
				}
				t.Logf("synthetic %s request: %s", protocol, text)
				w.Header().Set("Content-Type", "text/event-stream")
				switch protocol {
				case "responses":
					fmt.Fprint(w, "data: {\"type\":\"response.output_text.delta\",\"delta\":\"resumed answer\"}\n\ndata: {\"type\":\"response.completed\",\"response\":{\"id\":\"resp_new\",\"status\":\"completed\",\"output\":[]}}\n\n")
				case "chat_completions":
					fmt.Fprint(w, "data: {\"choices\":[{\"index\":0,\"delta\":{\"content\":\"resumed answer\"},\"finish_reason\":\"stop\"}]}\n\ndata: [DONE]\n\n")
				case "anthropic":
					fmt.Fprint(w, "event: message_start\ndata: {\"message\":{\"usage\":{\"input_tokens\":3}}}\n\nevent: content_block_start\ndata: {\"index\":0,\"content_block\":{\"type\":\"text\",\"text\":\"\"}}\n\nevent: content_block_delta\ndata: {\"index\":0,\"delta\":{\"type\":\"text_delta\",\"text\":\"resumed answer\"}}\n\nevent: content_block_stop\ndata: {\"index\":0}\n\nevent: message_delta\ndata: {\"delta\":{\"stop_reason\":\"end_turn\"},\"usage\":{\"output_tokens\":2}}\n\nevent: message_stop\ndata: {}\n\n")
				}
			}))
			defer fake.Close()
			rt := newTestRuntime(t, &fakeClient{})
			if protocol == "anthropic" {
				client, err := anthropic.New(anthropic.ClientConfig{BaseURL: fake.URL, APIKey: "synthetic-only"})
				if err != nil {
					t.Fatal(err)
				}
				rt.StreamRunner.Client = client
				rt.StreamRunner.ProviderOptions = map[string]any{"thinking": map[string]any{"type": "enabled", "budget_tokens": 1024}}
			} else {
				wire := ""
				if protocol == "responses" {
					wire = "responses"
				}
				client, err := openai.New(openai.ClientConfig{BaseURL: fake.URL, APIKey: "synthetic-only", WireAPI: wire, ResponsesTransport: providers.StreamTransportSSE})
				if err != nil {
					t.Fatal(err)
				}
				rt.StreamRunner.Client = client
			}
			sess, err := session.CreateWithMetadata(rt.SessionDir, session.NewID(), rt.RootDir)
			if err != nil {
				t.Fatal(err)
			}
			reasoning := providers.ChatMessage{Role: "assistant", ProviderItemProvider: rt.ProviderName, ProviderItemModel: rt.Model, ToolCalls: []providers.ToolCall{{ID: "call_saved", Name: "read_file", Arguments: `{"path":"saved.txt"}`}}}
			switch protocol {
			case "responses":
				reasoning.ReasoningBlocks = []providers.ReasoningBlock{{Type: "reasoning", Data: `{"id":"rs_saved","type":"reasoning","encrypted_content":"encrypted","summary":[]}`}}
			case "anthropic":
				reasoning.ReasoningBlocks = []providers.ReasoningBlock{{Type: "thinking", Thinking: "completed reasoning", Signature: "signed-thought"}}
			case "chat_completions":
				reasoning.ReasoningContent = "completed reasoning"
			}
			if err := appendChatMessages(rt.SessionDir, sess.ID, []providers.ChatMessage{{Role: "user", Content: "original task"}, reasoning, {Role: "tool", Name: "read_file", ToolCallID: "call_saved", Content: "tool output saved"}, {Role: "assistant", Content: "completed step"}, {Role: "assistant", Content: "discard fragment", Cause: providers.IncompleteStreamFragment}}); err != nil {
				t.Fatal(err)
			}
			out := &lockedBuffer{}
			srv := New(rt, out)
			t.Cleanup(srv.Close)
			if err := srv.handleLine(context.Background(), []byte(fmt.Sprintf(`{"id":"continue","method":"turn/start","params":{"thread_id":%q,"resume_after_turn_id":%q}}`, sess.ID, sess.ID+"-turn-0001"))); err != nil {
				t.Fatal(err)
			}
			reply := responseByID(t, parseOutput(t, out.String()), "continue")
			if reply["error"] != nil {
				t.Fatalf("admission: %+v", reply)
			}
			waitForTurnCompletedForThread(t, out, sess.ID)
			waitForThreadLeaseRelease(t, rt.SessionDir, sess.ID)
			if requests.Load() != 1 {
				t.Fatalf("requests=%d", requests.Load())
			}
		})
	}
}
