package hooks

import (
	"encoding/json"
	"testing"
)

func TestInputMarshal(t *testing.T) {
	in := Input{
		Event:     PreToolUse,
		SessionID: "sess-1",
		CWD:       "/tmp",
		ToolName:  "run_shell",
		ToolInput: json.RawMessage(`{"command":"ls"}`),
	}
	data, err := json.Marshal(in)
	if err != nil {
		t.Fatal(err)
	}
	var decoded map[string]any
	if err := json.Unmarshal(data, &decoded); err != nil {
		t.Fatal(err)
	}
	if decoded["hook_event_name"] != "PreToolUse" {
		t.Fatalf("expected PreToolUse, got %v", decoded["hook_event_name"])
	}
	if decoded["tool_name"] != "run_shell" {
		t.Fatalf("expected run_shell, got %v", decoded["tool_name"])
	}
}

func TestOutputFallbackExitCodeZero(t *testing.T) {
	out, err := ParseOutput([]byte("some text\n"), 0)
	if err != nil {
		t.Fatal(err)
	}
	if out.Decision != "" {
		t.Fatal("expected empty decision for non-JSON output with exit 0")
	}
	if out.IsBlocked() {
		t.Fatal("should not be blocked")
	}
}

func TestOutputContinueFalse(t *testing.T) {
	raw := `{"continue":false,"reason":"stop here"}`
	out, err := ParseOutput([]byte(raw), 0)
	if err != nil {
		t.Fatal(err)
	}
	if !out.IsBlocked() {
		t.Fatal("continue=false should block")
	}
}
