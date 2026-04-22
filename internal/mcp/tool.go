package mcp

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/blueberrycongee/wuu/internal/providers"
)

// MCPTool wraps an MCP server tool so it satisfies wuu's tools.Tool interface.
type MCPTool struct {
	client     *Client
	serverName string
	tool       Tool
}

// NewMCPTool creates a wuu-compatible tool from an MCP tool definition.
func NewMCPTool(client *Client, tool Tool) *MCPTool {
	return &MCPTool{
		client:     client,
		serverName: client.Name(),
		tool:       tool,
	}
}

// Name returns the fully-qualified tool name: "mcp:<server>:<tool>".
// This avoids collisions between tools from different MCP servers.
func (t *MCPTool) Name() string {
	return fmt.Sprintf("mcp_%s_%s", t.serverName, t.tool.Name)
}

// Definition returns the JSON-schema tool definition for the model.
func (t *MCPTool) Definition() providers.ToolDefinition {
	return providers.ToolDefinition{
		Name:        t.Name(),
		Description: fmt.Sprintf("[%s] %s", t.serverName, t.tool.Description),
		InputSchema: schemaToMap(t.tool.InputSchema),
	}
}

// Execute calls the MCP server tool.
func (t *MCPTool) Execute(ctx context.Context, args string) (string, error) {
	var rawArgs json.RawMessage
	if strings.TrimSpace(args) != "" {
		rawArgs = json.RawMessage(args)
	}
	result, err := t.client.CallTool(ctx, t.tool.Name, rawArgs)
	if err != nil {
		return "", err
	}
	if result.IsError {
		return formatToolResult(result), fmt.Errorf("mcp tool error")
	}
	return formatToolResult(result), nil
}

// IsReadOnly reports whether the tool never modifies state.
// MCP does not declare this explicitly; we conservatively return false.
func (t *MCPTool) IsReadOnly() bool { return false }

// IsConcurrencySafe reports whether multiple instances can run in parallel.
// Conservatively false unless proven otherwise.
func (t *MCPTool) IsConcurrencySafe() bool { return false }

func formatToolResult(result *CallToolResult) string {
	var parts []string
	for _, c := range result.Content {
		switch c.Type {
		case "text":
			parts = append(parts, c.Text)
		default:
			parts = append(parts, fmt.Sprintf("[%s content]", c.Type))
		}
	}
	return strings.Join(parts, "\n")
}

func schemaToMap(raw json.RawMessage) map[string]any {
	var m map[string]any
	if err := json.Unmarshal(raw, &m); err != nil {
		return map[string]any{"type": "object"}
	}
	return m
}
