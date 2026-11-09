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

func TestFreshContextHooksProtectWindowReplacement(t *testing.T) {
	for _, trigger := range []string{"manual", "threshold", "overflow", "overflow-trim"} {
		for _, blockedHook := range []string{"pre", "post", "none"} {
			t.Run(trigger+"/"+blockedHook, func(t *testing.T) {
				history := []providers.ChatMessage{
					{Role: "system", Content: "instructions"},
					{Role: "user", Content: strings.Repeat("original task ", 2000)},
					{Role: "assistant", Content: "completed progress"},
					{Role: "user", Content: "continue"},
				}
				cfg := recoveryWindowConfig()
				overflow := providers.NewProviderStreamError("context_length_exceeded", "")
				step := &fakeStep{results: []StepResult{{Content: "done"}}}
				wantRequests, blockAttempt := 0, 1
				switch trigger {
				case "manual":
					cfg.ForceInitialCompact, cfg.CompactOnly = true, true
				case "threshold":
					cfg.CompactThresholdTokens = 1
				case "overflow":
					step.results = []StepResult{{}, {Content: "done"}}
					step.errs = []error{overflow, nil}
					wantRequests = 1
				case "overflow-trim":
					step.results = []StepResult{{}, {}, {Content: "done"}}
					step.errs = []error{overflow, overflow, nil}
					wantRequests, blockAttempt = 2, 2
				}
				before, after, archives, commits, successes := 0, 0, 0, 0, 0
				hookErr := errors.New("checkpoint must remain active")
				cfg.BeforeCompact = func(context.Context, CompactReason) error {
					before++
					if blockedHook == "pre" && before == blockAttempt {
						return hookErr
					}
					return nil
				}
				cfg.AfterCompact = func(_ context.Context, _ CompactReason, err error) error {
					after++
					if err != nil {
						t.Fatalf("unexpected window failure: %v", err)
					}
					if blockedHook == "post" && after == blockAttempt {
						return hookErr
					}
					return nil
				}
				cfg.ArchiveHistory = func(context.Context, []providers.ChatMessage) (HistoryArchive, error) {
					archives++
					return HistoryArchive{HeadSeq: 100}, nil
				}
				cfg.FreshContext = func(_ context.Context, messages []providers.ChatMessage, _, _, _ int) ([]providers.ChatMessage, error) {
					if trigger == "overflow-trim" {
						return providers.CloneChatMessages(messages), nil
					}
					return []providers.ChatMessage{messages[0], messages[len(messages)-1]}, nil
				}
				cfg.AcceptFreshContext = func(_ context.Context, messages []providers.ChatMessage, head int) ([]providers.ChatMessage, int, error) {
					commits++
					return messages, head, nil
				}
				cfg.OnCompact = func(CompactInfo) { successes++ }
				result, err := RunToolLoop(context.Background(), history, cfg, step)
				wantAfter, wantCommits := blockAttempt, 0
				if blockedHook == "none" {
					wantCommits = 1
					if trigger != "manual" {
						wantRequests++
					}
					if err != nil || !result.HistoryRewritten {
						t.Fatalf("approved window failed: rewritten=%v err=%v", result.HistoryRewritten, err)
					}
					if trigger != "manual" {
						for _, message := range step.calls[len(step.calls)-1].Messages {
							if message.Content == history[1].Content {
								t.Error("approved retry still contains released history")
							}
						}
					}
				} else {
					if blockedHook == "pre" {
						wantAfter--
					}
					if !errors.Is(err, hookErr) || result.HistoryRewritten || len(result.NewMessages) != 0 {
						t.Errorf("rejected window escaped: rewritten=%v new=%d err=%v", result.HistoryRewritten, len(result.NewMessages), err)
					}
				}
				if before != blockAttempt || after != wantAfter || archives != wantAfter || commits != wantCommits || successes != wantCommits || len(step.calls) != wantRequests {
					t.Fatalf("pre=%d post=%d archives=%d commits=%d successes=%d requests=%d; want %d/%d/%d/%d/%d/%d", before, after, archives, commits, successes, len(step.calls), blockAttempt, wantAfter, wantAfter, wantCommits, wantCommits, wantRequests)
				}
			})
		}
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
	commits := 0
	cfg.AcceptFreshContext = func(_ context.Context, messages []providers.ChatMessage, head int) ([]providers.ChatMessage, int, error) {
		commits++
		return messages, head, nil
	}
	cfg.FreshContext = func(_ context.Context, messages []providers.ChatMessage, _, _, _ int) ([]providers.ChatMessage, error) {
		return providers.CloneChatMessages(messages), nil
	}
	overflow := providers.NewProviderStreamError("context_length_exceeded", "")
	step := &fakeStep{results: []StepResult{{}, {}, {Content: "ok", StopReason: "stop"}}, errs: []error{overflow, overflow, nil}}
	history := []providers.ChatMessage{
		{Role: "system", Content: "instructions"},
		{Role: "user", Content: strings.Repeat("old task ", 2000)},
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
	if commits != 1 {
		t.Fatalf("force-trim must commit the replacement before retrying, got %d commits", commits)
	}
	if len(step.calls[2].Messages) >= len(history) {
		t.Fatalf("trimmed retry still had %d messages", len(step.calls[2].Messages))
	}
}

func TestFreshContextOverflowTrimStopsOnPersistenceFailure(t *testing.T) {
	for _, stage := range []string{"archive", "commit"} {
		t.Run(stage, func(t *testing.T) {
			cfg := recoveryWindowConfig()
			cfg.FreshContext = func(_ context.Context, messages []providers.ChatMessage, _, _, _ int) ([]providers.ChatMessage, error) {
				return providers.CloneChatMessages(messages), nil
			}
			storageErr := errors.New("storage unavailable")
			archives := 0
			cfg.ArchiveHistory = func(context.Context, []providers.ChatMessage) (HistoryArchive, error) {
				archives++
				if stage == "archive" && archives == 2 {
					return HistoryArchive{}, storageErr
				}
				return HistoryArchive{HeadSeq: 100}, nil
			}
			cfg.AcceptFreshContext = func(_ context.Context, messages []providers.ChatMessage, head int) ([]providers.ChatMessage, int, error) {
				if stage == "commit" {
					return nil, head, storageErr
				}
				return messages, head, nil
			}
			overflow := providers.NewProviderStreamError("context_length_exceeded", "")
			step := &fakeStep{results: []StepResult{{}, {}, {Content: "must not run", StopReason: "stop"}}, errs: []error{overflow, overflow, nil}}
			history := []providers.ChatMessage{
				{Role: "system", Content: "instructions"},
				{Role: "user", Content: strings.Repeat("old task ", 2000)},
				{Role: "assistant", Content: "old answer"},
				{Role: "user", Content: "latest task"},
			}
			result, err := RunToolLoop(context.Background(), history, cfg, step)
			if !errors.Is(err, storageErr) || len(step.calls) != 2 || result.HistoryRewritten || len(result.NewMessages) != 0 {
				t.Fatalf("uncommitted trim escaped: err=%v calls=%d rewritten=%v new=%d", err, len(step.calls), result.HistoryRewritten, len(result.NewMessages))
			}
		})
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
