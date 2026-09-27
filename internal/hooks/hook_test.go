package hooks

import (
	"context"
	"encoding/json"
	"testing"
	"time"
)

func TestCommandHook_BlockViaExitCode(t *testing.T) {
	h := &CommandHook{Command: "exit 2", Timeout: 5 * time.Second}
	_, err := h.Execute(context.Background(), &Input{Event: PreToolUse})
	if err == nil {
		t.Fatal("expected error for exit 2")
	}
	if !IsBlocked(err) {
		t.Fatalf("expected ErrBlocked, got: %v", err)
	}
}

func TestCommandHook_BlockViaJSON(t *testing.T) {
	h := &CommandHook{
		Command: `echo '{"decision":"block","reason":"test"}'`,
		Timeout: 5 * time.Second,
	}
	_, err := h.Execute(context.Background(), &Input{Event: PreToolUse})
	if !IsBlocked(err) {
		t.Fatalf("expected block from JSON output, got: %v", err)
	}
}

func TestCommandHook_UpdatedInput(t *testing.T) {
	h := &CommandHook{
		Command: `echo '{"updated_input":{"command":"echo safe"}}'`,
		Timeout: 5 * time.Second,
	}
	out, err := h.Execute(context.Background(), &Input{Event: PreToolUse})
	if err != nil {
		t.Fatal(err)
	}
	var m map[string]any
	if err := json.Unmarshal(out.UpdatedInput, &m); err != nil {
		t.Fatal(err)
	}
	if m["command"] != "echo safe" {
		t.Fatalf("expected updated command, got %v", m["command"])
	}
}

func TestCommandHook_StdinReceivesInput(t *testing.T) {
	// Hook reads stdin via sh utilities, asserts field presence, exits 0 on match.
	h := &CommandHook{
		Command: `read -r line; echo "$line" | grep -q '"hook_event_name":"PreToolUse"'`,
		Timeout: 5 * time.Second,
	}
	_, err := h.Execute(context.Background(), &Input{
		Event:    PreToolUse,
		ToolName: "run_shell",
	})
	if err != nil {
		t.Fatalf("hook should succeed reading stdin: %v", err)
	}
}

func TestCommandHook_Timeout(t *testing.T) {
	h := &CommandHook{Command: "sleep 10", Timeout: 100 * time.Millisecond}
	_, err := h.Execute(context.Background(), &Input{Event: PreToolUse})
	if err == nil {
		t.Fatal("expected timeout error")
	}
	if IsBlocked(err) {
		t.Fatal("timeout should not be classified as block")
	}
}

func TestCommandHook_NonZeroExitIsError(t *testing.T) {
	// Exit 1 (not 2) is a hook execution failure, not a block.
	h := &CommandHook{Command: "exit 1", Timeout: 5 * time.Second}
	_, err := h.Execute(context.Background(), &Input{Event: PreToolUse})
	if err == nil {
		t.Fatal("expected error for exit 1")
	}
	if IsBlocked(err) {
		t.Fatal("exit 1 should not be classified as block")
	}
}
