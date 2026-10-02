package appserver

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"sync"
	"testing"

	"github.com/blueberrycongee/wuu/internal/agent"
	"github.com/blueberrycongee/wuu/internal/hooks"
	"github.com/blueberrycongee/wuu/internal/providers"
	"github.com/blueberrycongee/wuu/internal/providers/openai"
	"github.com/blueberrycongee/wuu/internal/runtime"
)

func TestServerInterruptSettlesPartialStreamItems(t *testing.T) {
	for _, tc := range []struct {
		name, wire, chunk, notification string
		itemType                        ThreadItemType
		text                            string
	}{
		{"text", "chat", `{"choices":[{"delta":{"content":"Partial answer"}}]}`, NotificationAgentMessageDelta, ThreadItemAgentMessage, "Partial answer"},
		{"reasoning", "chat", `{"choices":[{"delta":{"reasoning_content":"Partial reasoning"}}]}`, NotificationReasoningDelta, ThreadItemReasoning, "Partial reasoning"},
		{"responses_reasoning", "responses", `{"type":"response.reasoning_summary_text.delta","item_id":"reasoning-1","output_index":0,"summary_index":0,"delta":"Partial reasoning"}`, NotificationReasoningDelta, ThreadItemReasoning, "Partial reasoning"},
		{"tool_draft", "chat", `{"choices":[{"delta":{"tool_calls":[{"index":0,"id":"call_draft","type":"function","function":{"name":"echo_tool","arguments":"{\"path\":\"part"}}]}}]}`, NotificationToolCallDelta, ThreadItemToolCall, ""},
	} {
		t.Run(tc.name, func(t *testing.T) {
			endpoint := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.Header().Set("Content-Type", "text/event-stream")
				fmt.Fprintf(w, "data: %s\n\n", tc.chunk)
				w.(http.Flusher).Flush()
				<-r.Context().Done()
			}))
			defer endpoint.Close()
			client, err := openai.New(openai.ClientConfig{
				BaseURL: endpoint.URL, APIKey: "synthetic", WireAPI: tc.wire,
				ResponsesTransport: providers.StreamTransportSSE,
			})
			if err != nil {
				t.Fatal(err)
			}
			repo := t.TempDir()
			rt := &runtime.Session{
				ProviderName: "test-provider", Model: "test-model", RootDir: repo,
				ConfigPath: filepath.Join(repo, ".wuu.json"), ConfigLoadMode: runtime.ConfigLoadFile,
				SessionDir: filepath.Join(repo, "sessions"), HookDispatcher: hooks.NewDispatcher(nil),
				StreamRunner: &agent.StreamRunner{
					Client: client, Model: "test-model", SystemPrompt: "system", Tools: fastToolExecutor{},
				},
			}
			out := &lockedBuffer{}
			srv := New(rt, out)
			if err := srv.handleLine(context.Background(), []byte(`{"id":"start","method":"thread/start"}`)); err != nil {
				t.Fatal(err)
			}
			threadID := remarshal[ThreadStartResult](t, responseByID(t, parseOutput(t, out.String()), "start")["result"]).Thread.ID
			if err := srv.handleLine(context.Background(), []byte(fmt.Sprintf(`{"id":"turn","method":"turn/start","params":{"thread_id":%q,"prompt":"Inspect"}}`, threadID))); err != nil {
				t.Fatal(err)
			}
			waitForMethod(t, out, tc.notification)
			if err := srv.handleLine(context.Background(), []byte(fmt.Sprintf(`{"id":"stop","method":"turn/interrupt","params":{"thread_id":%q}}`, threadID))); err != nil {
				t.Fatal(err)
			}
			messages := waitForMethod(t, out, NotificationTurnError)
			for _, message := range messages {
				if message["method"] != NotificationTurnError {
					continue
				}
				turn := remarshal[Turn](t, message["params"].(map[string]any)["turn"])
				t.Logf("terminal snapshot: %+v", turn)
				if turn.Status != TurnStatusInterrupted {
					t.Fatalf("turn status = %s, want interrupted", turn.Status)
				}
				found := false
				for _, item := range turn.Items {
					if item.Status == ThreadItemStatusInProgress {
						t.Errorf("interrupted turn retains a live item: %+v", item)
					}
					if item.Type != tc.itemType {
						continue
					}
					found = true
					if tc.itemType == ThreadItemToolCall {
						t.Errorf("unexecuted tool draft should be discarded: %+v", item)
					} else if item.Text != tc.text || item.Status != ThreadItemStatusCompleted {
						t.Errorf("partial text should be preserved and settled: %+v", item)
					}
				}
				if !found && tc.itemType != ThreadItemToolCall {
					t.Errorf("interruption lost partial %s text", tc.itemType)
				}
			}
		})
	}
}

// multiStepStreamClient returns tool_calls on the first call, then blocks
// on the second call until ctx is cancelled.
type multiStepStreamClient struct {
	mu      sync.Mutex
	callIdx int
	started chan struct{}
	release chan struct{}
	once    sync.Once
}

func newMultiStepStreamClient() *multiStepStreamClient {
	return &multiStepStreamClient{
		started: make(chan struct{}),
		release: make(chan struct{}),
	}
}

func (m *multiStepStreamClient) Chat(ctx context.Context, req providers.ChatRequest) (providers.ChatResponse, error) {
	ch, err := m.StreamChat(ctx, req)
	if err != nil {
		return providers.ChatResponse{}, err
	}
	var resp providers.ChatResponse
	for ev := range ch {
		if ev.Type == providers.EventError && ev.Error != nil {
			return providers.ChatResponse{}, ev.Error
		}
		if ev.Type == providers.EventToolUseEnd && ev.ToolCall != nil {
			resp.ToolCalls = append(resp.ToolCalls, *ev.ToolCall)
		}
		if ev.Type == providers.EventContentDelta {
			resp.Content += ev.Content
		}
	}
	return resp, nil
}

func (m *multiStepStreamClient) StreamChat(ctx context.Context, req providers.ChatRequest) (<-chan providers.StreamEvent, error) {
	m.mu.Lock()
	idx := m.callIdx
	m.callIdx++
	m.mu.Unlock()
	if idx == 0 {
		ch := make(chan providers.StreamEvent, 4)
		toolCall := providers.ToolCall{
			ID:        "call_1",
			Name:      "echo_tool",
			Arguments: `{}`,
		}
		ch <- providers.StreamEvent{Type: providers.EventToolUseStart, ToolCall: &providers.ToolCall{ID: toolCall.ID, Name: toolCall.Name, Kind: toolCall.Kind}}
		ch <- providers.StreamEvent{Type: providers.EventToolUseEnd, ToolCall: &toolCall}
		ch <- providers.StreamEvent{Type: providers.EventDone, StopReason: "tool_use"}
		close(ch)
		return ch, nil
	}
	m.once.Do(func() { close(m.started) })
	ch := make(chan providers.StreamEvent, 1)
	go func() {
		defer close(ch)
		select {
		case <-ctx.Done():
			ch <- providers.StreamEvent{Type: providers.EventError, Error: ctx.Err()}
		case <-m.release:
			ch <- providers.StreamEvent{Type: providers.EventContentDelta, Content: "should not see this"}
			ch <- providers.StreamEvent{Type: providers.EventDone}
		}
	}()
	return ch, nil
}

type fastToolExecutor struct{}

func (fastToolExecutor) Definitions() []providers.ToolDefinition {
	return []providers.ToolDefinition{{
		Name:        "echo_tool",
		Description: "echo tool",
		InputSchema: map[string]any{
			"type":       "object",
			"properties": map[string]any{},
		},
	}}
}

func (fastToolExecutor) Execute(ctx context.Context, _ providers.ToolCall) (string, error) {
	return `{"echoed":"hello"}`, nil
}

func TestRepro_InterruptAfterToolCompletes_PersistsMessages(t *testing.T) {
	client := newMultiStepStreamClient()
	rt := &runtime.Session{
		ProviderName:   "fake-provider",
		Model:          "fake-model",
		RootDir:        retryingTempDir(t),
		SessionDir:     retryingTempDir(t) + "/.wuu-state/sessions",
		ConfigLoadMode: runtime.ConfigLoadFile,
		HookDispatcher: hooks.NewDispatcher(nil),
		StreamRunner: &agent.StreamRunner{
			Client:       providers.AdaptStreamClient(client),
			Model:        "fake-model",
			SystemPrompt: "system",
		},
	}
	rt.StreamRunner.Tools = fastToolExecutor{}
	out := &lockedBuffer{}
	srv := New(rt, out)

	if err := srv.handleLine(context.Background(), []byte(`{"id":"1","method":"thread/start"}`)); err != nil {
		t.Fatalf("thread/start: %v", err)
	}
	threadID := remarshal[ThreadStartResult](t, responseByID(t, parseOutput(t, out.String()), "1")["result"]).Thread.ID

	startReq := fmt.Sprintf(`{"id":"2","method":"turn/start","params":{"thread_id":%q,"prompt":"please"}}`, threadID)
	if err := srv.handleLine(context.Background(), []byte(startReq)); err != nil {
		t.Fatalf("turn/start: %v", err)
	}

	<-client.started

	interruptReq := fmt.Sprintf(`{"id":"3","method":"turn/interrupt","params":{"thread_id":%q}}`, threadID)
	if err := srv.handleLine(context.Background(), []byte(interruptReq)); err != nil {
		t.Fatalf("turn/interrupt: %v", err)
	}
	waitForMethod(t, out, NotificationTurnError)

	persisted, err := loadChatMessages(rt.SessionDir, threadID)
	if err != nil {
		t.Fatalf("load persisted history: %v", err)
	}
	var hasUser, hasAssistantToolCall, hasToolResult bool
	for _, msg := range persisted {
		switch msg.Role {
		case "user":
			if msg.Content == "please" {
				hasUser = true
			}
		case "assistant":
			for _, tc := range msg.ToolCalls {
				if tc.ID == "call_1" && tc.Name == "echo_tool" {
					hasAssistantToolCall = true
				}
			}
		case "tool":
			if msg.ToolCallID == "call_1" {
				hasToolResult = true
			}
		}
	}
	if !hasUser || !hasAssistantToolCall || !hasToolResult {
		t.Fatalf("interrupted turn dropped partial messages: user=%v assistant_toolcall=%v tool_result=%v; persisted=%+v",
			hasUser, hasAssistantToolCall, hasToolResult, persisted)
	}
}
