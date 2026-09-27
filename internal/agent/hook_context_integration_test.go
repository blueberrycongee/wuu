package agent_test

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/blueberrycongee/wuu/internal/agent"
	"github.com/blueberrycongee/wuu/internal/hooks"
	"github.com/blueberrycongee/wuu/internal/providers"
)

type hookContextStep struct {
	results  []agent.StepResult
	requests []providers.ChatRequest
	index    int
}

func (s *hookContextStep) Execute(_ context.Context, request providers.ChatRequest) (agent.StepResult, error) {
	s.requests = append(s.requests, providers.ChatRequest{
		Model:    request.Model,
		Messages: providers.CloneChatMessages(request.Messages),
	})
	if s.index >= len(s.results) {
		return agent.StepResult{}, errors.New("unexpected model request")
	}
	result := s.results[s.index]
	s.index++
	return result, nil
}

type concurrentHookTools struct{}

func (concurrentHookTools) Definitions() []providers.ToolDefinition {
	return []providers.ToolDefinition{{Name: "read_file"}}
}

func (concurrentHookTools) ToolMetadata(providers.ToolCall) (agent.ToolMetadata, bool) {
	return agent.ToolMetadata{ReadOnly: true, ConcurrencySafe: true}, true
}

func (concurrentHookTools) Execute(_ context.Context, call providers.ToolCall) (string, error) {
	return `{"path":` + call.Arguments + `}`, nil
}

func TestRunToolLoopPreservesPostToolContextForConcurrentCalls(t *testing.T) {
	registry := hooks.NewRegistry(map[hooks.Event][]hooks.HookConfig{
		hooks.PostToolUse: {{Command: `printf '%s' '{"additional_context":"shared check"}'`}, {
			Matcher: "read_file",
			Command: `input=$(cat); case "$input" in *one.txt*) printf '%s' '{"additional_context":"context for one"}';; *) printf '%s' '{"additional_context":"context for two"}';; esac`,
		}},
	})
	executor := hooks.NewHookedExecutor(concurrentHookTools{}, hooks.NewDispatcher(registry), "session", t.TempDir())
	step := &hookContextStep{results: []agent.StepResult{
		{ToolCalls: []providers.ToolCall{
			{ID: "call-one", Name: "read_file", Arguments: `{"path":"one.txt"}`},
			{ID: "call-two", Name: "read_file", Arguments: `{"path":"two.txt"}`},
		}},
		{Content: "done"},
	}}

	result, err := agent.RunToolLoop(
		context.Background(),
		[]providers.ChatMessage{{Role: "user", Content: "compare both files"}},
		agent.LoopConfig{Model: "test-model", Tools: executor},
		step,
	)
	if err != nil {
		t.Fatal(err)
	}
	if len(step.requests) != 2 {
		t.Fatalf("model requests = %d, want 2", len(step.requests))
	}
	secondRequest := step.requests[1].Messages
	if got := countMessageContent(secondRequest, "shared check"); got != 2 {
		t.Fatalf("first hook context must survive for both calls, got %d", got)
	}
	if got := countMessageContent(result.NewMessages, "shared check"); got != 0 {
		t.Fatalf("shared check must stay out of durable history, got %d", got)
	}
	for _, want := range []string{"context for one", "context for two"} {
		if countMessageContent(secondRequest, want) != 1 {
			t.Fatalf("second request should contain %q exactly once: %+v", want, secondRequest)
		}
		if countMessageContent(result.NewMessages, want) != 0 {
			t.Fatalf("durable history must not contain request-only context %q: %+v", want, result.NewMessages)
		}
	}
	if got := countMessageContent(secondRequest, "[ADDITIONAL_CONTEXT]"); got != 2 {
		t.Fatalf("additional context blocks = %d, want 2: %+v", got, secondRequest)
	}
}

func countMessageContent(messages []providers.ChatMessage, needle string) int {
	count := 0
	for _, message := range messages {
		if strings.Contains(message.Content, needle) {
			count++
		}
	}
	return count
}
