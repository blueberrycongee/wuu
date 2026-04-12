package agent

import (
	"context"
	"testing"

	"github.com/blueberrycongee/wuu/internal/providers"
)

func TestRunToolLoop_OnMessageReceivesAssistantAndToolMessages(t *testing.T) {
	step := &fakeStep{results: []StepResult{
		{ToolCalls: []providers.ToolCall{{ID: "c1", Name: "t", Arguments: `{}`}}},
		{Content: "done"},
	}}
	tools := &fakeLoopTools{
		defs: []providers.ToolDefinition{{Name: "t"}},
		results: map[string]string{
			"t": `{"ok":true}`,
		},
	}

	var seen []providers.ChatMessage
	_, err := RunToolLoop(context.Background(), []providers.ChatMessage{{Role: "user", Content: "hi"}}, LoopConfig{
		Model: "m",
		Tools: tools,
		OnMessage: func(msg providers.ChatMessage) {
			seen = append(seen, msg)
		},
	}, step)
	if err != nil {
		t.Fatalf("RunToolLoop: %v", err)
	}
	if len(seen) != 3 {
		t.Fatalf("expected assistant/tool/final assistant messages, got %d", len(seen))
	}
	if seen[0].Role != "assistant" || len(seen[0].ToolCalls) != 1 {
		t.Fatalf("unexpected first message: %+v", seen[0])
	}
	if seen[1].Role != "tool" || seen[1].ToolCallID != "c1" {
		t.Fatalf("unexpected tool message: %+v", seen[1])
	}
	if seen[2].Role != "assistant" || seen[2].Content != "done" {
		t.Fatalf("unexpected final assistant message: %+v", seen[2])
	}
}
