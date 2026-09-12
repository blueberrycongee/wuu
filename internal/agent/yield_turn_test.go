package agent

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/blueberrycongee/wuu/internal/providers"
)

func TestEmptyAnswerRecoveryRequiresExplicitCompletion(t *testing.T) {
	for _, stop := range []string{"completed", "end_turn", "stop"} {
		for _, resolution := range []string{"reply", "yield", "empty"} {
			t.Run(stop+"/"+resolution, func(t *testing.T) {
				tools := &fakeLoopTools{defs: []providers.ToolDefinition{{Name: yieldTurnToolName}}, results: map[string]string{"yield": `{"yielded":true,"reason":"No action required"}`}}
				final := StepResult{StopReason: stop}
				switch resolution {
				case "reply":
					final.Content = "The task is complete."
				case "yield":
					final.ToolCalls = []providers.ToolCall{{ID: "yield", Name: yieldTurnToolName, Arguments: `{"reason":"No action required"}`}}
				}
				step := &fakeStep{results: []StepResult{{StopReason: stop, Usage: &providers.TokenUsage{OutputTokens: 4}}, final}}
				res, err := RunToolLoop(context.Background(), []providers.ChatMessage{userMsg("Review this delivery")}, LoopConfig{Model: "m", Tools: tools}, step)
				if resolution == "empty" {
					if !IsEmptyAnswer(err) {
						t.Fatalf("repeated empty reply must fail: %v", err)
					}
				} else if err != nil {
					t.Fatal(err)
				}
				if len(step.calls) != 2 || res.OutputTokens != 4 {
					t.Fatalf("recovery calls/usage = %d/%d", len(step.calls), res.OutputTokens)
				}
				if resolution == "reply" && res.Content != final.Content {
					t.Fatalf("lost recovered reply: %q", res.Content)
				}
				if resolution == "yield" && (res.Content != "" || res.FinishReason != providers.FinishReasonStop || len(res.NewMessages) != 2) {
					t.Fatalf("yield result = %+v", res)
				}
				for _, message := range res.DurableNewMessages {
					if strings.Contains(message.Content, "empty-response recovery") {
						t.Fatal("recovery instruction leaked into durable conversation")
					}
				}
			})
		}
	}
}

func TestTurnYieldDoesNotHideFailedOrSiblingTools(t *testing.T) {
	for _, mode := range []string{"failed", "sibling", "missing_reason"} {
		t.Run(mode, func(t *testing.T) {
			tools := &fakeLoopTools{defs: []providers.ToolDefinition{{Name: yieldTurnToolName}, {Name: "read_file"}}, results: map[string]string{"yield": `{"yielded":true,"reason":"done"}`}}
			calls := []providers.ToolCall{{ID: "yield", Name: yieldTurnToolName, Arguments: `{"reason":"done"}`}}
			switch mode {
			case "failed":
				tools.err = errors.New("yield failed")
			case "sibling":
				calls = append(calls, providers.ToolCall{ID: "read", Name: "read_file", Arguments: `{}`})
			case "missing_reason":
				tools.results["yield"] = `{"yielded":true}`
			}
			step := &fakeStep{results: []StepResult{{ToolCalls: calls}, {Content: "Reviewed the tool results."}}}
			res, err := RunToolLoop(context.Background(), []providers.ChatMessage{userMsg("work")}, LoopConfig{Model: "m", Tools: tools}, step)
			if err != nil || len(step.calls) != 2 || res.Content == "" {
				t.Fatalf("tool results were skipped: %+v, %v", res, err)
			}
		})
	}
}

func TestEmptyAnswerRecoveryHonorsStepLimitAndTruncation(t *testing.T) {
	tools := &fakeLoopTools{defs: []providers.ToolDefinition{{Name: yieldTurnToolName}}}
	for _, truncated := range []bool{false, true} {
		result := StepResult{StopReason: "completed"}
		if truncated {
			result.FinishReason, result.StopReason, result.Truncated = providers.FinishReasonLength, "max_tokens", true
		}
		step := &fakeStep{results: []StepResult{result}}
		_, err := RunToolLoop(context.Background(), []providers.ChatMessage{userMsg("work")}, LoopConfig{Model: "m", Tools: tools, MaxSteps: 1}, step)
		if truncated && err != nil || !truncated && !IsEmptyAnswer(err) || len(step.calls) != 1 {
			t.Fatalf("limit/truncation result = %v, calls=%d", err, len(step.calls))
		}
	}
}
