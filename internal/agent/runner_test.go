package agent

import (
	"context"
	"errors"
	"testing"

	"github.com/blueberrycongee/wuu/internal/providers"
)

func TestRunner_RunSimple(t *testing.T) {
	client := &fakeClient{responses: []providers.ChatResponse{{Content: "done"}}}
	runner := Runner{Client: client, Model: "gpt-test", SystemPrompt: "sys"}

	answer, err := runner.Run(context.Background(), "hello")
	if err != nil {
		t.Fatalf("Run returned error: %v", err)
	}
	if answer != "done" {
		t.Fatalf("unexpected answer: %s", answer)
	}
}

func TestRunner_RunWithToolCall(t *testing.T) {
	client := &fakeClient{responses: []providers.ChatResponse{
		{
			ToolCalls: []providers.ToolCall{{ID: "call_1", Name: "run_shell", Arguments: `{"command":"echo hi"}`}},
		},
		{Content: "final answer"},
	}}
	tool := &fakeTools{}
	runner := Runner{Client: client, Tools: tool, Model: "gpt-test", SystemPrompt: "sys", MaxSteps: 4}

	answer, err := runner.Run(context.Background(), "task")
	if err != nil {
		t.Fatalf("Run returned error: %v", err)
	}
	if answer != "final answer" {
		t.Fatalf("unexpected answer: %s", answer)
	}
	if len(tool.calls) != 1 {
		t.Fatalf("expected one tool call, got %d", len(tool.calls))
	}

	lastReq := client.requests[len(client.requests)-1]
	foundToolMessage := false
	for _, msg := range lastReq.Messages {
		if msg.Role == "tool" && msg.ToolCallID == "call_1" {
			foundToolMessage = true
			break
		}
	}
	if !foundToolMessage {
		t.Fatal("expected tool message in follow-up request")
	}
}

func TestRunner_MaxStepsExceeded(t *testing.T) {
	client := &fakeClient{responses: []providers.ChatResponse{{ToolCalls: []providers.ToolCall{{ID: "c", Name: "run_shell", Arguments: `{}`}}}}}
	runner := Runner{Client: client, Tools: &fakeTools{}, Model: "gpt-test", MaxSteps: 1}

	_, err := runner.Run(context.Background(), "task")
	if err == nil {
		t.Fatal("expected max steps error")
	}
}

type fakeClient struct {
	responses []providers.ChatResponse
	requests  []providers.ChatRequest
	idx       int
}

func (f *fakeClient) Chat(_ context.Context, req providers.ChatRequest) (providers.ChatResponse, error) {
	f.requests = append(f.requests, req)
	if f.idx >= len(f.responses) {
		return providers.ChatResponse{}, errors.New("unexpected chat call")
	}
	resp := f.responses[f.idx]
	f.idx++
	return resp, nil
}

type fakeTools struct {
	calls []providers.ToolCall
}

func (f *fakeTools) Definitions() []providers.ToolDefinition {
	return []providers.ToolDefinition{{Name: "run_shell", InputSchema: map[string]any{"type": "object"}}}
}

func (f *fakeTools) Execute(_ context.Context, call providers.ToolCall) (string, error) {
	f.calls = append(f.calls, call)
	return `{"ok":true}`, nil
}
