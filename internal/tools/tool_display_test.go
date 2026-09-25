package tools

import (
	"testing"

	"github.com/blueberrycongee/wuu/internal/providers"
)

func TestToolkitToolDisplayLeavesMCPRaw(t *testing.T) {
	kit, err := New(t.TempDir())
	if err != nil {
		t.Fatalf("New: %v", err)
	}

	if got, ok := kit.ToolDisplay(providers.ToolCall{Name: "mcp_docs_search", Arguments: `{"query":"abc"}`}); ok {
		t.Fatalf("MCP tools should not get built-in display metadata, got %+v", got)
	}
}

func TestToolkitToolDisplayAddsCapabilityForActiveSurface(t *testing.T) {
	kit, err := New(t.TempDir())
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	kit.ConfigureSurfaceForProviderModel("openai", "gpt-5-codex", true)

	got, ok := kit.ToolDisplay(providers.ToolCall{Name: "bash", Arguments: `{"command":"npm test"}`})
	if !ok {
		t.Fatal("expected display metadata")
	}
	if got.Capability != "command.bash" {
		t.Fatalf("Capability = %q, want command.bash; display=%+v", got.Capability, got)
	}

	got, ok = kit.ToolDisplay(providers.ToolCall{Name: "bash", Arguments: `{"command":"npm run dev","run_in_background":true}`})
	if !ok {
		t.Fatal("expected display metadata")
	}
	if got.Capability != "command.background" {
		t.Fatalf("Capability = %q, want command.background; display=%+v", got.Capability, got)
	}

	got, ok = kit.ToolDisplay(providers.ToolCall{Name: "apply_patch", Arguments: `{}`})
	if !ok {
		t.Fatal("expected display metadata")
	}
	if got.Capability != "file.edit" {
		t.Fatalf("Capability = %q, want file.edit; display=%+v", got.Capability, got)
	}
}
