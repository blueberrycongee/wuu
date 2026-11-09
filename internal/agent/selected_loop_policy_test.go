package agent

import (
	"context"
	"errors"
	"testing"

	"github.com/blueberrycongee/wuu/internal/loopdriver"
	"github.com/blueberrycongee/wuu/internal/pluginprotocol"
	"github.com/blueberrycongee/wuu/internal/providers"
)

// Policy failures must stop before another model dispatch; a replacement may
// also stop a provider continuation without replacing the entire loop.
func TestLoopSelectedContinuationPolicy(t *testing.T) {
	for _, fail := range []bool{false, true} {
		step := &fakeStep{results: []StepResult{{Content: "partial", FinishReason: providers.FinishReasonContinue}, {Content: "unexpected", FinishReason: providers.FinishReasonStop}}}
		cfg := LoopConfig{Model: "test", EvaluatePolicy: func(ctx context.Context, in pluginprotocol.AgentPolicyInput) (pluginprotocol.AgentPolicyDecision, error) {
			if in.Phase != "continue" {
				t.Fatalf("unexpected phase %q", in.Phase)
			}
			if fail {
				return pluginprotocol.AgentPolicyDecision{}, errors.New("policy offline")
			}
			return pluginprotocol.AgentPolicyDecision{Action: "stop"}, nil
		}}
		result, err := RunToolLoop(context.Background(), []providers.ChatMessage{userMsg("task")}, cfg, step)
		if (err != nil) != fail || len(step.calls) != 1 {
			t.Fatalf("fail=%v calls=%d result=%+v err=%v", fail, len(step.calls), result, err)
		}
	}
}

func TestLoopPolicyCannotReplayPartialOutput(t *testing.T) {
	step := &fakeStep{results: []StepResult{{Content: "already visible"}}, errs: []error{errors.New("connection lost")}}
	cfg := LoopConfig{Model: "test", EvaluatePolicy: func(context.Context, pluginprotocol.AgentPolicyInput) (pluginprotocol.AgentPolicyDecision, error) {
		return pluginprotocol.AgentPolicyDecision{Action: "retry"}, nil
	}}
	_, err := RunToolLoop(context.Background(), []providers.ChatMessage{userMsg("task")}, cfg, step)
	if err == nil || len(step.calls) != 1 {
		t.Fatalf("calls=%d err=%v", len(step.calls), err)
	}
}

func TestLoopPolicyRetryBudgetCannotBeRenewed(t *testing.T) {
	failure := &loopdriver.FailureError{Failure: pluginprotocol.DriverFailure{Message: "not submitted", Category: "connect", Recovery: &pluginprotocol.DriverModelRecovery{SubmissionCount: 0}}}
	step := &fakeStep{results: []StepResult{{Usage: &providers.TokenUsage{InputTokens: 2}}, {Usage: &providers.TokenUsage{InputTokens: 3}}, {}}, errs: []error{failure, failure, failure}}
	attempts := 0
	cfg := LoopConfig{Model: "test", EvaluatePolicy: func(context.Context, pluginprotocol.AgentPolicyInput) (pluginprotocol.AgentPolicyDecision, error) {
		attempts++
		return pluginprotocol.AgentPolicyDecision{Action: "retry", MaxAttempts: attempts + 1}, nil
	}}
	result, err := RunToolLoop(context.Background(), []providers.ChatMessage{userMsg("task")}, cfg, step)
	if err == nil || len(step.calls) != 2 || result.InputTokens != 5 {
		t.Fatalf("calls=%d result=%+v err=%v", len(step.calls), result, err)
	}
}

func TestLoopPolicyCannotRetryUnknownSubmission(t *testing.T) {
	step := &fakeStep{results: []StepResult{{}}, errs: []error{errors.New("connection lost, submission unknown")}}
	cfg := LoopConfig{Model: "test", EvaluatePolicy: func(context.Context, pluginprotocol.AgentPolicyInput) (pluginprotocol.AgentPolicyDecision, error) {
		return pluginprotocol.AgentPolicyDecision{Action: "retry", MaxAttempts: 2}, nil
	}}
	_, err := RunToolLoop(context.Background(), []providers.ChatMessage{userMsg("task")}, cfg, step)
	if err == nil || len(step.calls) != 1 {
		t.Fatalf("calls=%d err=%v", len(step.calls), err)
	}
}

func TestLoopCheckpointResumeRequiresSelectedPolicy(t *testing.T) {
	for _, action := range []string{"fail", "resume", "retry"} {
		t.Run(action, func(t *testing.T) {
			checkpoint := providers.ChatMessage{Role: "assistant", ReasoningBlocks: []providers.ReasoningBlock{{Thinking: "completed reasoning"}}}
			failure := &providers.StreamRecoveryError{Cause: &providers.ReplayBlockedError{Cause: errors.New("saved checkpoint")}, Recovery: providers.StreamRecoveryInfo{StopReason: "replay_unsafe", MaxAttempts: 3}}
			step := &fakeStep{results: []StepResult{{CompletedMessages: []providers.ChatMessage{checkpoint}}, {Content: "done", FinishReason: providers.FinishReasonStop}}, errs: []error{failure, nil}}
			seen := ""
			cfg := LoopConfig{Model: "test", EvaluatePolicy: func(_ context.Context, in pluginprotocol.AgentPolicyInput) (pluginprotocol.AgentPolicyDecision, error) {
				if in.Phase == "continue" {
					return pluginprotocol.AgentPolicyDecision{Action: "stop"}, nil
				}
				seen = in.Phase
				return pluginprotocol.AgentPolicyDecision{Action: action, MaxAttempts: 3}, nil
			}}
			result, err := RunToolLoop(context.Background(), []providers.ChatMessage{userMsg("task")}, cfg, step)
			if seen != "checkpoint_resume" {
				t.Fatalf("checkpoint evaluated as %q", seen)
			}
			want := 1
			if action == "resume" {
				want = 2
			}
			if len(step.calls) != want || (err == nil) != (action == "resume") {
				t.Fatalf("calls=%d result=%+v err=%v", len(step.calls), result, err)
			}
			if action == "resume" && !step.calls[1].ResumeSampling {
				t.Fatal("checkpoint was replayed instead of sampled")
			}
		})
	}
}

func TestLoopCheckpointResumeRetainsBudgetAndToolFences(t *testing.T) {
	for _, tool := range []bool{false, true} {
		failure := &providers.StreamRecoveryError{Cause: &providers.ReplayBlockedError{Cause: errors.New("checkpoint")}, Recovery: providers.StreamRecoveryInfo{StopReason: "replay_unsafe", MaxAttempts: 2}}
		partial := StepResult{CompletedMessages: []providers.ChatMessage{{Role: "assistant", Content: "saved"}}}
		if tool {
			partial.ToolCalls = []providers.ToolCall{{ID: "uncertain", Name: "write"}}
		}
		step := &fakeStep{results: []StepResult{partial, partial, partial}, errs: []error{failure, failure, failure}}
		cfg := LoopConfig{Model: "test", EvaluatePolicy: func(context.Context, pluginprotocol.AgentPolicyInput) (pluginprotocol.AgentPolicyDecision, error) {
			return pluginprotocol.AgentPolicyDecision{Action: "resume"}, nil
		}}
		_, err := RunToolLoop(context.Background(), []providers.ChatMessage{userMsg("task")}, cfg, step)
		want := 2
		if tool {
			want = 1
		}
		if err == nil || len(step.calls) != want {
			t.Fatalf("tool=%v calls=%d err=%v", tool, len(step.calls), err)
		}
	}
}
