package tools

import (
	"strings"
	"testing"

	"github.com/blueberrycongee/wuu/internal/providers"
)

func TestToolkitValidateActiveToolSurfaceChecksDeferredTools(t *testing.T) {
	kit, err := New(t.TempDir())
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	kit.registry = NewRegistry(
		NewToolSearchTool(kit),
		&stubTool{name: "mcp.bad.name", def: providers.ToolDefinition{
			Name:        "mcp.bad.name",
			Description: "Bad MCP name",
			InputSchema: map[string]any{"type": "object"},
		}},
		&stubTool{name: "mcp_other_one", def: providers.ToolDefinition{Name: "mcp_other_one", Description: "Other MCP tool"}},
		&stubTool{name: "mcp_other_two", def: providers.ToolDefinition{Name: "mcp_other_two", Description: "Other MCP tool"}},
		&stubTool{name: "mcp_other_three", def: providers.ToolDefinition{Name: "mcp_other_three", Description: "Other MCP tool"}},
		&stubTool{name: "mcp_other_four", def: providers.ToolDefinition{Name: "mcp_other_four", Description: "Other MCP tool"}},
	)

	err = kit.ValidateActiveToolSurfaceForProvider(providers.ToolSurfaceValidationTarget{
		ProviderKind: "openai-compatible",
		ProviderName: "openai",
		Model:        "gpt-test",
	})
	if err == nil || !strings.Contains(err.Error(), "mcp.bad.name") {
		t.Fatalf("expected deferred provider validation failure, got %v", err)
	}
}
