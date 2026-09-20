package agent

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/blueberrycongee/wuu/internal/providers"
)

func TestRunToolLoop_ContinuationLimit(t *testing.T) {
	for _, maxSteps := range []int{0, 2} {
		t.Run(map[int]string{0: "unlimited steps", 2: "step budget"}[maxSteps], func(t *testing.T) {
			step := &fakeStep{}
			for i := 0; i < maxConsecutiveContinuations+2; i++ {
				step.results = append(step.results, StepResult{
					Content: "still working", FinishReason: providers.FinishReasonContinue,
					Usage: &providers.TokenUsage{InputTokens: 7, OutputTokens: 3},
				})
			}
			result, err := RunToolLoop(context.Background(), []providers.ChatMessage{userMsg("hello")}, LoopConfig{Model: "test", MaxSteps: maxSteps}, step)
			wantCalls, wantError := maxConsecutiveContinuations+1, "continuation limit exceeded"
			if maxSteps > 0 {
				wantCalls, wantError = maxSteps, "max steps exceeded"
			}
			if err == nil || !strings.Contains(err.Error(), wantError) || len(step.calls) != wantCalls {
				t.Fatalf("err=%v calls=%d, want %s after %d requests", err, len(step.calls), wantError, wantCalls)
			}
			if result.InputTokens != wantCalls*7 || result.OutputTokens != wantCalls*3 || len(visibleMessagesForTest(result.NewMessages)) != wantCalls {
				t.Fatalf("lost usage or partial history on limit: %+v", result)
			}
		})
	}
}

func TestRunToolLoop_ToolProgressResetsContinuationLimit(t *testing.T) {
	step := &fakeStep{}
	for i := 0; i < maxConsecutiveContinuations; i++ {
		step.results = append(step.results, StepResult{Content: "working", FinishReason: providers.FinishReasonContinue})
	}
	// A simultaneous continuation flag must not skip client tool execution.
	step.results = append(step.results, StepResult{FinishReason: providers.FinishReasonContinue, ToolCalls: []providers.ToolCall{{ID: "c1", Name: "t", Arguments: `{}`}}})
	step.results = append(step.results,
		StepResult{Content: "working again", FinishReason: providers.FinishReasonContinue},
		StepResult{Content: "done", FinishReason: providers.FinishReasonStop},
	)
	tools := &fakeLoopTools{defs: []providers.ToolDefinition{{Name: "t"}}}
	result, err := RunToolLoop(context.Background(), []providers.ChatMessage{userMsg("hello")}, LoopConfig{Model: "test", Tools: tools}, step)
	if err != nil || result.Content != "done" || len(tools.recordedCalls()) != 1 {
		t.Fatalf("result=%+v calls=%v err=%v", result, tools.recordedCalls(), err)
	}
	for _, msg := range step.calls[maxConsecutiveContinuations+1].Messages {
		if msg.Role == "tool" && msg.ToolCallID == "c1" {
			return
		}
	}
	t.Fatal("continuation did not replay tool result")
}

func TestStreamRunner_CancelBetweenContinuations(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	client := &mockStreamClient{attempts: []mockStreamAttempt{{events: []providers.StreamEvent{
		{Type: providers.EventContentDelta, Content: "working"},
		{Type: providers.EventDone, FinishReason: providers.FinishReasonContinue},
	}}}}
	steps := 0
	runner := &StreamRunner{Model: "test", Client: client, BeforeStep: func() []providers.ChatMessage {
		steps++
		if steps == 2 {
			cancel()
		}
		return nil
	}}
	_, err := runner.Run(ctx, "hello")
	if !errors.Is(err, context.Canceled) || client.callCount != 1 {
		t.Fatalf("err=%v calls=%d, want cancellation without another request", err, client.callCount)
	}
}
