package hooks

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/blueberrycongee/wuu/internal/providers"
)

func TestIntegration_FullToolHookFlow(t *testing.T) {
	inner := &stubExecutor{result: `{"content":"hello"}`}
	r := NewRegistry(map[Event][]HookConfig{
		PreToolUse: {
			{Matcher: "run_shell", Command: `echo '{"updated_input":{"command":"echo hello"}}'`},
		},
		PostToolUse: {
			{Matcher: "*", Command: "true"},
		},
	})
	d := NewDispatcher(r)
	exec := NewHookedExecutor(inner, d, "test-session", "/tmp")

	result, err := exec.Execute(context.Background(), providers.ToolCall{
		ID:        "tc-1",
		Name:      "run_shell",
		Arguments: `{"command":"rm -rf /"}`,
	})
	if err != nil {
		t.Fatal(err)
	}

	// Verify inner received updated arguments.
	if len(inner.calls) != 1 {
		t.Fatal("expected 1 inner call")
	}
	var args map[string]any
	if err := json.Unmarshal([]byte(inner.calls[0].Arguments), &args); err != nil {
		t.Fatal(err)
	}
	if args["command"] != "echo hello" {
		t.Fatalf("expected rewritten args, got %v", args)
	}
	if result != `{"content":"hello"}` {
		t.Fatalf("unexpected result: %s", result)
	}
}

func TestIntegration_BlockPreventsExecution(t *testing.T) {
	inner := &stubExecutor{result: `ok`}
	r := NewRegistry(map[Event][]HookConfig{
		PreToolUse: {
			{Matcher: "run_shell", Command: `echo '{"decision":"block","reason":"forbidden"}'`},
		},
	})
	d := NewDispatcher(r)
	exec := NewHookedExecutor(inner, d, "test-session", "/tmp")

	_, err := exec.Execute(context.Background(), providers.ToolCall{
		Name: "run_shell", Arguments: `{}`,
	})
	if !IsBlocked(err) {
		t.Fatalf("expected blocked, got: %v", err)
	}
	if len(inner.calls) != 0 {
		t.Fatal("inner should not be called when blocked")
	}
}

func TestIntegration_SelectiveBlockByTool(t *testing.T) {
	inner := &stubExecutor{result: `ok`}
	r := NewRegistry(map[Event][]HookConfig{
		PreToolUse: {
			{Matcher: "run_shell", Command: "exit 2"},
		},
	})
	d := NewDispatcher(r)
	exec := NewHookedExecutor(inner, d, "test-session", "/tmp")

	// run_shell should be blocked.
	_, err := exec.Execute(context.Background(), providers.ToolCall{
		Name: "run_shell", Arguments: `{}`,
	})
	if !IsBlocked(err) {
		t.Fatal("expected run_shell to be blocked")
	}

	// read_file should pass through.
	result, err := exec.Execute(context.Background(), providers.ToolCall{
		Name: "read_file", Arguments: `{"path":"x"}`,
	})
	if err != nil {
		t.Fatalf("read_file should not be blocked: %v", err)
	}
	if result != "ok" {
		t.Fatalf("expected ok, got %s", result)
	}
}

func TestIntegration_ChainedPreToolHooks(t *testing.T) {
	// Two hooks run in sequence; first adds context, second rewrites input.
	inner := &stubExecutor{result: `ok`}
	r := NewRegistry(map[Event][]HookConfig{
		PreToolUse: {
			{Matcher: "*", Command: `echo '{"additional_context":"logged"}'`},
			{Matcher: "*", Command: `echo '{"updated_input":{"command":"safe"}}'`},
		},
	})
	d := NewDispatcher(r)
	exec := NewHookedExecutor(inner, d, "s", "/tmp")

	_, err := exec.Execute(context.Background(), providers.ToolCall{
		Name: "run_shell", Arguments: `{"command":"dangerous"}`,
	})
	if err != nil {
		t.Fatal(err)
	}

	var args map[string]any
	json.Unmarshal([]byte(inner.calls[0].Arguments), &args)
	if args["command"] != "safe" {
		t.Fatalf("expected rewritten to safe, got %v", args)
	}
}
