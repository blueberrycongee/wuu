package agent

import (
	"context"
	"errors"
	"reflect"
	"strings"
	"testing"

	"github.com/blueberrycongee/wuu/internal/providers"
)

type windowFailureTransform struct{ transform RequestTransform }

func (p windowFailureTransform) TransformKey() string        { return "window-test" }
func (p windowFailureTransform) TransformPriority() int      { return 0 }
func (p windowFailureTransform) Transform() RequestTransform { return p.transform }

func recoveryWindowConfig() LoopConfig {
	return LoopConfig{
		Model: "test", MaxSteps: 8, FreshContextTokens: 4000, CompactThresholdTokens: 1_000_000,
		ArchiveHistory: func(context.Context, []providers.ChatMessage) (HistoryArchive, error) {
			return HistoryArchive{HeadSeq: 100}, nil
		},
		FreshContext: func(_ context.Context, messages []providers.ChatMessage, head, fixed, target int) ([]providers.ChatMessage, error) {
			return providers.CloneChatMessages(messages[:2]), nil
		},
	}
}

func TestFreshContextFailureRetainsOriginalWindow(t *testing.T) {
	for _, stage := range []string{"before-request", "before-request-invalid", "chain", "chain-invalid", "budget", "commit"} {
		t.Run(stage, func(t *testing.T) {
			history := fallbackHistory()
			retained := []RetainedContextMessage{{AfterDurable: len(history), Message: contextWindowReminder("previous request context")}}
			cfg := recoveryWindowConfig()
			cfg.CompactThresholdTokens = 1
			cfg.RetainedRequestContext = buildRetainedRequestContextState(retained, history)
			commits, successes, failures := 0, 0, 0
			cfg.AcceptFreshContext = func(_ context.Context, messages []providers.ChatMessage, head int) ([]providers.ChatMessage, int, error) {
				commits++
				return nil, head, errors.New("commit unavailable")
			}
			cfg.OnCompact = func(CompactInfo) { successes++ }
			cfg.OnCompactAttempt = func(info CompactAttemptInfo) {
				if info.Status == CompactAttemptFailed {
					failures++
				}
			}
			transform := func(_ context.Context, req *providers.ChatRequest) error {
				if strings.HasSuffix(stage, "invalid") {
					req.Model = ""
					return nil
				}
				if stage == "budget" {
					req.Messages = append(req.Messages, providers.ChatMessage{Role: "system", Content: strings.Repeat("large transform ", 10000)})
					return nil
				}
				return errors.New("transform unavailable")
			}
			switch stage {
			case "chain", "chain-invalid":
				cfg.RequestTransforms = NewRequestTransformChain()
				cfg.RequestTransforms.Add(windowFailureTransform{transform})
			case "commit":
			default:
				cfg.BeforeRequest = transform
			}
			step := &fakeStep{}
			result, err := RunToolLoop(context.Background(), history, cfg, step)
			if err == nil {
				t.Fatal("expected pre-commit failure")
			}
			if result.HistoryRewritten || len(result.NewMessages) != 0 || len(step.calls) != 0 || successes != 0 || failures != 1 {
				t.Fatalf("failed window escaped: rewritten=%v new=%d requests=%d successes=%d failures=%d", result.HistoryRewritten, len(result.NewMessages), len(step.calls), successes, failures)
			}
			wantCommits := 0
			if stage == "commit" {
				wantCommits = 1
			}
			if commits != wantCommits {
				t.Fatalf("commits=%d want=%d", commits, wantCommits)
			}
			if !reflect.DeepEqual(result.RetainedRequestContext, cfg.RetainedRequestContext) {
				t.Fatal("failed window changed retained request context")
			}
		})
	}
}

type recoveryProgressTools struct{}

func (recoveryProgressTools) Definitions() []providers.ToolDefinition {
	return []providers.ToolDefinition{{Name: "read", InputSchema: map[string]any{"type": "object"}}}
}
func (recoveryProgressTools) Execute(context.Context, providers.ToolCall) (string, error) {
	return strings.Repeat("progress ", 3000), nil
}

func TestFreshContextOverflowRecoveryResetsOnlyAfterSuccess(t *testing.T) {
	for _, progress := range []bool{false, true} {
		t.Run(map[bool]string{false: "consecutive-overflows", true: "later-window-overflow"}[progress], func(t *testing.T) {
			cfg := recoveryWindowConfig()
			cfg.Tools = recoveryProgressTools{}
			overflow := providers.NewProviderStreamError("context_length_exceeded", "")
			step := &fakeStep{results: []StepResult{{}, {}}, errs: []error{overflow, overflow}}
			if progress {
				step.results = []StepResult{{}, {ToolCalls: []providers.ToolCall{{ID: "read-progress", Name: "read", Arguments: `{}`}}}, {}, {Content: "done", StopReason: "stop"}}
				step.errs = []error{overflow, nil, overflow, nil}
			}
			result, err := RunToolLoop(context.Background(), fallbackHistory(), cfg, step)
			if progress {
				if err != nil || len(step.calls) != 4 || !result.HistoryRewritten {
					t.Fatalf("later window failed: calls=%d err=%v", len(step.calls), err)
				}
			} else if !providers.IsContextOverflow(err) || len(step.calls) != 2 {
				t.Fatalf("consecutive failure retried: calls=%d err=%v", len(step.calls), err)
			}
		})
	}
}

func TestFreshContextOverflowForceTrimsWhenWindowUnchanged(t *testing.T) {
	cfg := recoveryWindowConfig()
	cfg.FreshContext = func(_ context.Context, messages []providers.ChatMessage, _, _, _ int) ([]providers.ChatMessage, error) {
		return providers.CloneChatMessages(messages), nil
	}
	overflow := providers.NewProviderStreamError("context_length_exceeded", "")
	step := &fakeStep{results: []StepResult{{}, {}, {Content: "ok", StopReason: "stop"}}, errs: []error{overflow, overflow, nil}}
	history := []providers.ChatMessage{
		{Role: "system", Content: "instructions"},
		{Role: "user", Content: "old task"},
		{Role: "assistant", Content: "old answer"},
		{Role: "user", Content: "latest task"},
	}

	result, err := RunToolLoop(context.Background(), history, cfg, step)
	if err != nil {
		t.Fatalf("expected force-trim recovery after unchanged fresh context, got %v", err)
	}
	if len(step.calls) != 3 {
		t.Fatalf("expected overflow, unchanged window retry, then trimmed retry, got %d calls", len(step.calls))
	}
	if !result.HistoryRewritten {
		t.Fatal("expected force-trim to rewrite history")
	}
	if len(step.calls[2].Messages) >= len(history) {
		t.Fatalf("trimmed retry still had %d messages", len(step.calls[2].Messages))
	}
}

func TestFreshContextOverflowDoesNotRetryUnchangedPayload(t *testing.T) {
	cfg := recoveryWindowConfig()
	cfg.FreshContext = func(_ context.Context, messages []providers.ChatMessage, _, _, _ int) ([]providers.ChatMessage, error) {
		return providers.CloneChatMessages(messages), nil
	}
	overflow := providers.NewProviderStreamError("context_length_exceeded", "")
	step := &fakeStep{results: []StepResult{{}, {}}, errs: []error{overflow, overflow}}
	history := []providers.ChatMessage{
		{Role: "system", Content: "instructions"},
		{Role: "user", Content: "oversized fresh prompt"},
	}

	_, err := RunToolLoop(context.Background(), history, cfg, step)
	if err == nil || !providers.IsContextOverflow(err) {
		t.Fatalf("expected original context overflow, got %v", err)
	}
	if len(step.calls) != 2 {
		t.Fatalf("unchanged overflow recovery should stop after the no-op window retry, got %d calls", len(step.calls))
	}
}
