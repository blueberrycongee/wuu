package agent_test

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"testing"
	"time"
	"unicode/utf8"

	"github.com/blueberrycongee/wuu/internal/providers"
	"github.com/blueberrycongee/wuu/internal/providers/anthropic"
	"github.com/blueberrycongee/wuu/internal/providers/openai"
	"github.com/blueberrycongee/wuu/internal/toolresult"
	"github.com/blueberrycongee/wuu/internal/tools"
)

// Exercise each real serializer, not only shared preparation. In particular,
// Responses must also replay historical settlements with explicit empty text.
func TestSettledResultProviderHTTP(t *testing.T) {
	t.Setenv("WUU_TOOL_RESULT_PROJECTION", "active")
	for _, api := range []string{"chat", "responses", "anthropic"} {
		for _, zero := range []bool{false, true} {
			t.Run(fmt.Sprintf("%s/zero=%v", api, zero), func(t *testing.T) {
				large := toolresult.FromText(strings.Repeat("界🙂", 30000))
				large.IsError = true
				large.Meta = json.RawMessage(`{"private":"synthetic-private-metadata"}`)
				large.StructuredContent = json.RawMessage(`{"continuation":{"next":"synthetic-recovery-cursor"}}`)
				large.Content = append(large.Content, toolresult.ContentPart{Type: "image", MIMEType: "image/png", Data: "aW1hZ2U="})
				small := toolresult.FromText("untrimmed")
				if zero {
					small = toolresult.FromText(strings.Repeat("b", 200000))
				} else {
					kit, err := tools.New(t.TempDir())
					if err != nil {
						t.Fatal(err)
					}
					small = toolresult.FromText(`{"action":"run","output":"started\nwarning: deprecated\nFAIL: assertion\n","stdout_tail":"started\n","stderr_tail":"warning: deprecated\nFAIL: assertion\n","stdout_tail_truncated":false,"stderr_tail_truncated":false,"exit_code":1,"duration_ms":7}`)
					small.IsError = true
					small = kit.FinalizeToolResult(providers.ToolCall{ID: "call_1", Name: "bash"}, small)
					if small.ModelText == nil {
						t.Fatal("bash model text was not settled")
					}
				}
				smallText := small.TextProjection()
				small.Content = append(small.Content, toolresult.ContentPart{Type: "file", MIMEType: "application/pdf", Data: "cGRm", Name: "synthetic.pdf"})
				page := `{"content":"界🙂","continuation":{"has_more":true,"next":{"continuation":"synthetic-cursor"}}}`
				if zero {
					page = ""
				}
				large.ModelText = &page
				history := []providers.ChatMessage{
					{Role: "assistant", ToolCalls: []providers.ToolCall{
						{ID: "call_0", Name: "lookup", Arguments: `{}`},
						{ID: "call_1", Name: "lookup", Arguments: `{}`},
					}},
					{Role: "tool", ToolCallID: "call_0", Content: large.TextProjection(), ToolResult: &large},
					{Role: "tool", ToolCallID: "call_1", Content: small.TextProjection(), ToolResult: &small},
				}
				if !zero {
					history[0].ToolCalls[1].Name = "bash"
				}
				before := providers.CloneChatMessages(history)
				bodies := make(chan []byte, 1)
				server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
					raw, err := io.ReadAll(r.Body)
					if err != nil {
						t.Error(err)
						http.Error(w, "read error", 400)
						return
					}
					select {
					case bodies <- raw:
					default:
						t.Error("unexpected extra request")
						http.Error(w, "extra request", 400)
						return
					}
					w.Header().Set("Content-Type", "application/json")
					switch api {
					case "chat":
						io.WriteString(w, `{"choices":[{"message":{"role":"assistant","content":"done"},"finish_reason":"stop"}]}`)
					case "responses":
						io.WriteString(w, `{"id":"resp_test","status":"completed","output":[{"type":"message","role":"assistant","content":[{"type":"output_text","text":"done"}]}]}`)
					case "anthropic":
						io.WriteString(w, `{"id":"msg_test","role":"assistant","content":[{"type":"text","text":"done"}],"stop_reason":"end_turn"}`)
					}
				}))
				defer server.Close()
				var client interface {
					Chat(context.Context, providers.ChatRequest) (providers.ChatResponse, error)
				}
				var err error
				model := "gpt-4o"
				if api == "anthropic" {
					client, err = anthropic.New(anthropic.ClientConfig{BaseURL: server.URL, APIKey: "synthetic-key", HTTPClient: server.Client()})
					model = "claude-sonnet-4-6"
				} else {
					client, err = openai.New(openai.ClientConfig{BaseURL: server.URL, APIKey: "synthetic-key", WireAPI: api, HTTPClient: server.Client()})
				}
				if err != nil {
					t.Fatal(err)
				}
				ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
				defer cancel()
				if _, err := client.Chat(ctx, providers.ChatRequest{Model: model, Messages: history}); err != nil {
					t.Fatal(err)
				}
				raw := <-bodies
				if strings.Contains(string(raw), "synthetic-private-metadata") || strings.Contains(string(raw), "model_text") {
					t.Fatal("internal result data leaked onto the wire")
				}
				if !reflect.DeepEqual(history, before) {
					t.Fatal("provider mutated history")
				}
				var request struct {
					Messages []budgetWireMessage `json:"messages"`
					Input    []budgetWireMessage `json:"input"`
				}
				if err := json.Unmarshal(raw, &request); err != nil {
					t.Fatal(err)
				}
				messages := request.Messages
				if api == "responses" {
					messages = request.Input
				}
				var ids []string
				total, media := 0, 0
				checkTool := func(id string, rawText json.RawMessage) {
					t.Helper()
					// Missing and null outputs are not equivalent to an explicit
					// empty result in provider protocols.
					if len(rawText) == 0 || string(rawText) == "null" {
						t.Fatal("missing tool output")
					}
					var text string
					if err := json.Unmarshal(rawText, &text); err != nil {
						t.Fatal(err)
					}
					if !utf8.ValidString(text) || strings.ContainsRune(text, '\uFFFD') {
						t.Fatal("UTF-8 was damaged at the budget boundary")
					}
					index := len(ids)
					if index >= 2 {
						t.Fatal("extra tool output")
					}
					if index == 0 && text != history[1].Content {
						t.Errorf("budgeted text restored: wire=%d settled=%d", len(text), len(history[1].Content))
					}
					if index == 1 && text != smallText {
						t.Error("smaller output changed")
					}
					if index == 1 && !zero {
						if text != "started\nwarning: deprecated\nFAIL: assertion\nExit code 1" {
							t.Fatalf("wire restored the JSON envelope or lost failure evidence: %s", text)
						}
					}
					if zero && index == 0 && text != "" {
						t.Error("zero allocation was restored")
					}
					total += len(text)
					ids = append(ids, id)
				}
				for _, message := range messages {
					if message.Role == "tool" {
						checkTool(message.ToolCallID, message.Content)
						continue
					}
					if message.Type == "function_call_output" {
						checkTool(message.CallID, message.Output)
						continue
					}
					var blocks []budgetWireMessage
					if json.Unmarshal(message.Content, &blocks) != nil {
						continue
					}
					for _, block := range blocks {
						if block.Type == "tool_result" {
							checkTool(block.ToolUseID, block.Content)
							continue
						}
						switch block.Type {
						case "image_url", "file", "input_image", "input_file", "image", "document":
							if len(ids) != 2 {
								t.Error("observation interleaved with paired results")
							}
							media++
						}
					}
				}
				if total > 200000 || !reflect.DeepEqual(ids, []string{"call_0", "call_1"}) || media != 2 {
					t.Errorf("wire bytes=%d IDs=%v media=%d", total, ids, media)
				}
				t.Logf("wire_tool_bytes=%d IDs=%v media=%d", total, ids, media)
			})
		}
	}
}

type budgetWireMessage struct {
	Role       string          `json:"role"`
	Type       string          `json:"type"`
	Content    json.RawMessage `json:"content"`
	Output     json.RawMessage `json:"output"`
	ToolCallID string          `json:"tool_call_id"`
	ToolUseID  string          `json:"tool_use_id"`
	CallID     string          `json:"call_id"`
}
