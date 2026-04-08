package hooks

import (
	"context"
	"encoding/json"
	"errors"
	"testing"

	"github.com/blueberrycongee/wuu/internal/providers"
)

// stubExecutor is a fake ToolExecutor for testing.
type stubExecutor struct {
	result string
	err    error
	calls  []providers.ToolCall
}

func (s *stubExecutor) Definitions() []providers.ToolDefinition { return nil }
func (s *stubExecutor) Execute(_ context.Context, call providers.ToolCall) (string, error) {
	s.calls = append(s.calls, call)
	return s.result, s.err
}

func TestHookedExecutor_PassThrough(t *testing.T) {
	inner := &stubExecutor{result: `{"ok":true}`}
	d := NewDispatcher(NewRegistry(nil))
	exec := NewHookedExecutor(inner, d, "sess-1", "/tmp")

	result, err := exec.Execute(context.Background(), providers.ToolCall{
		Name: "read_file", Arguments: `{"path":"foo.txt"}`,
	})
	if err != nil {
		t.Fatal(err)
	}
	if result != `{"ok":true}` {
		t.Fatalf("unexpected result: %s", result)
	}
	if len(inner.calls) != 1 {
		t.Fatal("inner should be called once")
	}
}

func TestHookedExecutor_PreToolBlock(t *testing.T) {
	inner := &stubExecutor{result: `{"ok":true}`}
	r := NewRegistry(map[Event][]HookConfig{
		PreToolUse: {{Matcher: "run_shell", Command: "exit 2"}},
	})
	exec := NewHookedExecutor(inner, NewDispatcher(r), "sess-1", "/tmp")

	_, err := exec.Execute(context.Background(), providers.ToolCall{
		Name: "run_shell", Arguments: `{"command":"rm -rf /"}`,
	})
	if err == nil {
		t.Fatal("expected error")
	}
	if !IsBlocked(err) {
		t.Fatalf("expected blocked, got: %v", err)
	}
	if len(inner.calls) != 0 {
		t.Fatal("inner should not be called when blocked")
	}
}

func TestHookedExecutor_PreToolBlock_NonMatching(t *testing.T) {
	inner := &stubExecutor{result: `ok`}
	r := NewRegistry(map[Event][]HookConfig{
		PreToolUse: {{Matcher: "run_shell", Command: "exit 2"}},
	})
	exec := NewHookedExecutor(inner, NewDispatcher(r), "sess-1", "/tmp")

	// read_file should not be blocked.
	_, err := exec.Execute(context.Background(), providers.ToolCall{
		Name: "read_file", Arguments: `{}`,
	})
	if err != nil {
		t.Fatalf("read_file should not be blocked, got: %v", err)
	}
	if len(inner.calls) != 1 {
		t.Fatal("inner should be called for non-matching tool")
	}
}

func TestHookedExecutor_UpdatedInput(t *testing.T) {
	inner := &stubExecutor{result: `{}`}
	r := NewRegistry(map[Event][]HookConfig{
		PreToolUse: {{Matcher: "*", Command: `echo '{"updated_input":{"command":"echo safe"}}'`}},
	})
	exec := NewHookedExecutor(inner, NewDispatcher(r), "sess-1", "/tmp")

	_, err := exec.Execute(context.Background(), providers.ToolCall{
		Name: "run_shell", Arguments: `{"command":"rm -rf /"}`,
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(inner.calls) != 1 {
		t.Fatal("expected inner to be called once")
	}
	var m map[string]any
	if err := json.Unmarshal([]byte(inner.calls[0].Arguments), &m); err != nil {
		t.Fatal(err)
	}
	if m["command"] != "echo safe" {
		t.Fatalf("expected updated args, got %v", m)
	}
}

func TestHookedExecutor_PostToolUseFailureFires(t *testing.T) {
	inner := &stubExecutor{err: errors.New("disk full")}
	r := NewRegistry(map[Event][]HookConfig{
		PostToolUseFailure: {{Matcher: "*", Command: "true"}},
	})
	exec := NewHookedExecutor(inner, NewDispatcher(r), "sess-1", "/tmp")

	_, err := exec.Execute(context.Background(), providers.ToolCall{
		Name: "write_file", Arguments: `{"path":"x"}`,
	})
	// Original error should propagate.
	if err == nil || err.Error() != "disk full" {
		t.Fatalf("expected 'disk full' error, got: %v", err)
	}
}

func TestHookedExecutor_PostToolSuccessFires(t *testing.T) {
	inner := &stubExecutor{result: `ok`}
	r := NewRegistry(map[Event][]HookConfig{
		PostToolUse: {{Matcher: "*", Command: "true"}},
	})
	exec := NewHookedExecutor(inner, NewDispatcher(r), "sess-1", "/tmp")

	result, err := exec.Execute(context.Background(), providers.ToolCall{
		Name: "read_file", Arguments: `{}`,
	})
	if err != nil {
		t.Fatal(err)
	}
	if result != "ok" {
		t.Fatalf("expected ok, got %s", result)
	}
}

func TestHookedExecutor_DefinitionsDelegates(t *testing.T) {
	inner := &stubExecutor{}
	exec := NewHookedExecutor(inner, NewDispatcher(nil), "", "")
	defs := exec.Definitions()
	if defs != nil {
		t.Fatal("expected nil definitions from stub")
	}
}
