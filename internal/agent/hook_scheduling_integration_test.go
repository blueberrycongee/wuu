package agent_test

import (
	"context"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/blueberrycongee/wuu/internal/agent"
	"github.com/blueberrycongee/wuu/internal/hooks"
	"github.com/blueberrycongee/wuu/internal/providers"
	"github.com/blueberrycongee/wuu/internal/toolledger"
)

type rewriteAwareTools struct{}

func (rewriteAwareTools) Definitions() []providers.ToolDefinition {
	return []providers.ToolDefinition{{Name: "bash"}}
}

func (rewriteAwareTools) ToolMetadata(call providers.ToolCall) (agent.ToolMetadata, bool) {
	readOnly := !strings.Contains(call.Arguments, "write")
	return agent.ToolMetadata{ReadOnly: readOnly, ConcurrencySafe: readOnly}, true
}

func (rewriteAwareTools) Execute(_ context.Context, call providers.ToolCall) (string, error) {
	return call.Arguments, nil
}

func TestToolHooksDoNotSpeculativelyExecuteRewrittenCalls(t *testing.T) {
	for _, tc := range []struct {
		name    string
		event   hooks.Event
		matcher string
		rewrite bool
	}{
		{"matching pre-hook", hooks.PreToolUse, "BASH", true},
		{"unrelated pre-hook", hooks.PreToolUse, "read_file", false},
		{"post-hook only", hooks.PostToolUse, "bash", false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			ctx := context.Background()
			dir := t.TempDir()
			ledger, err := toolledger.New(agentTestDocuments(t, dir), "hook-scheduling")
			if err != nil {
				t.Fatal(err)
			}
			d := hooks.NewDispatcher(hooks.NewRegistry(map[hooks.Event][]hooks.HookConfig{
				tc.event: {{Matcher: tc.matcher, Command: `printf '%s' '{"updated_input":{"command":"write"}}'`}},
			}))
			executor := hooks.NewHookedExecutor(rewriteAwareTools{}, d, "session", dir)
			runtime := agent.NewTurnToolRuntime(agent.ToolRuntimeConfig{Executor: executor, Ledger: ledger, OperationID: "operation-1"})
			defer runtime.Cancel()
			call := providers.ToolCall{ID: "call-1", Name: "bash", Arguments: `{"command":"read"}`}
			if err := runtime.ObserveStreamEvent(ctx, providers.StreamEvent{Type: providers.EventToolUseEnd, ToolCall: &call}); err != nil {
				t.Fatal(err)
			}
			// The ledger records starts before ObserveStreamEvent returns. This
			// checks early execution without racing the tool goroutine or sleeping.
			decision, err := runtime.ReplayDecision(ctx)
			if err != nil {
				t.Fatal(err)
			}
			if (decision.Action == toolledger.ReplayAllow) != tc.rewrite {
				t.Errorf("unexpected streaming execution state: %+v", decision)
			}
			if meta, ok := executor.ToolMetadata(call); !ok || meta.ConcurrencySafe == tc.rewrite {
				t.Errorf("unsafe concurrency classification: %+v (found=%v)", meta, ok)
			}
			messages, err := runtime.ExecuteFinalCalls(ctx, []providers.ToolCall{call}, nil)
			want := call.Arguments
			if tc.rewrite {
				want = `{"command":"write"}`
			}
			if err != nil || len(messages) != 1 || messages[0].Content != want {
				t.Fatalf("final rewritten execution: messages=%+v err=%v", messages, err)
			}
		})
	}
}

type legacyHookContextTools struct {
	rewriteAwareTools
	reads atomic.Int32
}

func (e *legacyHookContextTools) LastAdditionalContext() string {
	e.reads.Add(1)
	return "unattributed inner context"
}

func TestToolHooksDoNotAttributeLegacySharedContextToCalls(t *testing.T) {
	inner := &legacyHookContextTools{}
	executor := hooks.NewHookedExecutor(inner, hooks.NewDispatcher(hooks.NewRegistry(nil)), "session", t.TempDir())
	runtime := agent.NewTurnToolRuntime(agent.ToolRuntimeConfig{Executor: executor})
	defer runtime.Close()
	_, err := runtime.ExecuteFinalCalls(context.Background(), []providers.ToolCall{
		{ID: "first", Name: "bash", Arguments: `{}`},
		{ID: "second", Name: "bash", Arguments: `{}`},
	}, nil)
	if err != nil {
		t.Fatal(err)
	}
	if inner.reads.Load() != 0 || len(runtime.TakeRequestContextSegments()) != 0 {
		t.Fatal("per-call hook decoration consumed an inner shared context slot")
	}
	// Direct legacy serial consumers retain their explicit API; the per-call
	// adapter cannot infer which invocation owns an inner last-result value.
	if got := executor.LastAdditionalContext(); got != "unattributed inner context" || inner.reads.Load() != 1 {
		t.Fatalf("legacy serial access changed: context=%q reads=%d", got, inner.reads.Load())
	}
}

type replayDeclaredHookTools struct {
	rewriteAwareTools
	safe bool
}

func (t replayDeclaredHookTools) ToolMetadata(call providers.ToolCall) (agent.ToolMetadata, bool) {
	metadata, found := t.rewriteAwareTools.ToolMetadata(call)
	metadata.ReplaySafe = t.safe
	return metadata, found
}

func TestToolHooksDoNotInheritInnerReplayPermission(t *testing.T) {
	for _, event := range []hooks.Event{hooks.PreToolUse, hooks.PostToolUse, hooks.PostToolUseFailure} {
		for _, matcher := range []string{"BASH", "read_file", ""} {
			for _, safe := range []bool{false, true} {
				dispatcher := hooks.NewDispatcher(hooks.NewRegistry(map[hooks.Event][]hooks.HookConfig{
					event: {{Matcher: matcher, Command: `printf '%s' '{"updated_input":{"command":"write"}}'`}},
				}))
				executor := hooks.NewHookedExecutor(replayDeclaredHookTools{safe: safe}, dispatcher, "session", t.TempDir())
				metadata, found := executor.ToolMetadata(providers.ToolCall{Name: "bash", Arguments: `{"command":"read"}`})
				want := safe && matcher == "read_file"
				if !found || metadata.ReplaySafe != want {
					t.Fatalf("event=%s matcher=%q inner_safe=%v: metadata=%+v found=%v, want replay_safe=%v", event, matcher, safe, metadata, found, want)
				}
			}
		}
	}
	for _, dispatcher := range []*hooks.Dispatcher{nil, hooks.NewDispatcher(hooks.NewRegistry(nil))} {
		executor := hooks.NewHookedExecutor(replayDeclaredHookTools{safe: true}, dispatcher, "session", t.TempDir())
		if metadata, found := executor.ToolMetadata(providers.ToolCall{Name: "bash"}); !found || !metadata.ReplaySafe {
			t.Fatalf("no-hook declaration lost: %+v, %v", metadata, found)
		}
	}
}
