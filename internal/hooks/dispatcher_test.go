package hooks

import (
	"context"
	"encoding/json"
	"testing"
)

func TestDispatcher_NoHooks(t *testing.T) {
	r := NewRegistry(nil)
	d := NewDispatcher(r)
	out, err := d.Dispatch(context.Background(), PreToolUse, &Input{ToolName: "read_file"})
	if err != nil {
		t.Fatal(err)
	}
	if out.IsBlocked() {
		t.Fatal("should not be blocked with no hooks")
	}
}

func TestDispatcher_NilRegistry(t *testing.T) {
	d := NewDispatcher(nil)
	_, err := d.Dispatch(context.Background(), PreToolUse, &Input{})
	if err != nil {
		t.Fatalf("nil registry should be a no-op, got: %v", err)
	}
}

func TestDispatcher_PassThrough(t *testing.T) {
	r := NewRegistry(map[Event][]HookConfig{
		PreToolUse: {{Matcher: "*", Command: "true"}},
	})
	d := NewDispatcher(r)
	out, err := d.Dispatch(context.Background(), PreToolUse, &Input{ToolName: "read_file"})
	if err != nil {
		t.Fatal(err)
	}
	if out.IsBlocked() {
		t.Fatal("should not be blocked")
	}
}

func TestDispatcher_Block(t *testing.T) {
	r := NewRegistry(map[Event][]HookConfig{
		PreToolUse: {{Matcher: "*", Command: "exit 2"}},
	})
	d := NewDispatcher(r)
	_, err := d.Dispatch(context.Background(), PreToolUse, &Input{ToolName: "run_shell"})
	if !IsBlocked(err) {
		t.Fatalf("expected blocked, got: %v", err)
	}
}

func TestDispatcher_FirstBlockWins(t *testing.T) {
	r := NewRegistry(map[Event][]HookConfig{
		PreToolUse: {
			{Matcher: "*", Command: "exit 2"},
			{Matcher: "*", Command: "true"},
		},
	})
	d := NewDispatcher(r)
	_, err := d.Dispatch(context.Background(), PreToolUse, &Input{ToolName: "run_shell"})
	if !IsBlocked(err) {
		t.Fatal("first hook blocks, should propagate")
	}
}

func TestDispatcher_UpdatedInput(t *testing.T) {
	r := NewRegistry(map[Event][]HookConfig{
		PreToolUse: {
			{Matcher: "*", Command: `echo '{"updated_input":{"command":"safe"}}'`},
		},
	})
	d := NewDispatcher(r)
	out, err := d.Dispatch(context.Background(), PreToolUse, &Input{ToolName: "run_shell"})
	if err != nil {
		t.Fatal(err)
	}
	var m map[string]any
	if err := json.Unmarshal(out.UpdatedInput, &m); err != nil {
		t.Fatal(err)
	}
	if m["command"] != "safe" {
		t.Fatalf("expected updated command, got %v", m)
	}
}

func TestDispatcher_MergeLastWriterWins(t *testing.T) {
	r := NewRegistry(map[Event][]HookConfig{
		PreToolUse: {
			{Matcher: "*", Command: `echo '{"additional_context":"first"}'`},
			{Matcher: "*", Command: `echo '{"additional_context":"second"}'`},
		},
	})
	d := NewDispatcher(r)
	out, err := d.Dispatch(context.Background(), PreToolUse, &Input{ToolName: "x"})
	if err != nil {
		t.Fatal(err)
	}
	if out.Context != "second" {
		t.Fatalf("expected last context to win, got %s", out.Context)
	}
}

func TestDispatcher_EventFieldOverwritten(t *testing.T) {
	// Even if the caller passes a stale Event in input, Dispatch overwrites it.
	r := NewRegistry(map[Event][]HookConfig{
		Stop: {{Command: "true"}},
	})
	d := NewDispatcher(r)
	in := &Input{Event: PreToolUse}
	_, err := d.Dispatch(context.Background(), Stop, in)
	if err != nil {
		t.Fatal(err)
	}
	if in.Event != Stop {
		t.Fatalf("expected event to be overwritten to Stop, got %s", in.Event)
	}
}

func TestDispatcher_NilInput(t *testing.T) {
	r := NewRegistry(map[Event][]HookConfig{
		SessionStart: {{Command: "true"}},
	})
	d := NewDispatcher(r)
	_, err := d.Dispatch(context.Background(), SessionStart, nil)
	if err != nil {
		t.Fatalf("nil input should be tolerated, got: %v", err)
	}
}
