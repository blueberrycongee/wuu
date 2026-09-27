package hooks

import (
	"context"
	"testing"
)

func TestDispatcher_NilRegistry(t *testing.T) {
	d := NewDispatcher(nil)
	_, err := d.Dispatch(context.Background(), PreToolUse, &Input{})
	if err != nil {
		t.Fatalf("nil registry should be a no-op, got: %v", err)
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

func TestDispatcher_AccumulatesContext(t *testing.T) {
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
	if out.Context != "first\n\nsecond" {
		t.Fatalf("expected both contexts in order, got %s", out.Context)
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
