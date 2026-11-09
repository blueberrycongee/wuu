package providers

import (
	"strings"
	"testing"
)

func TestValidateToolDefinitionsForProviderRejectsOpenAIInvalidName(t *testing.T) {
	err := ValidateToolDefinitionsForProvider(ToolSurfaceValidationTarget{
		ProviderKind: "openai-compatible",
		ProviderName: "openai",
		Model:        "gpt-test",
	}, []ToolDefinition{{
		Name:        "mcp.docs.search",
		Description: "Search docs",
		InputSchema: map[string]any{"type": "object"},
	}})
	if err == nil || !strings.Contains(err.Error(), "^[a-zA-Z0-9_-]{1,64}$") {
		t.Fatalf("expected provider name validation error, got %v", err)
	}
}

func TestValidateToolDefinitionsForProviderRejectsReservedName(t *testing.T) {
	err := ValidateToolDefinitionsForProvider(ToolSurfaceValidationTarget{
		ProviderKind:      "openai-compatible",
		ProviderName:      "openai",
		Model:             "gpt-test",
		ReservedToolNames: []string{"browser"},
	}, []ToolDefinition{{
		Name:        "Browser",
		Description: "Drive a browser",
		InputSchema: map[string]any{"type": "object"},
	}})
	if err == nil || !strings.Contains(err.Error(), "reserved by this provider") {
		t.Fatalf("expected reserved provider name error, got %v", err)
	}
}

func TestValidateToolDefinitionsForProviderAllowsLocalGatewayNames(t *testing.T) {
	if err := ValidateToolDefinitionsForProvider(ToolSurfaceValidationTarget{
		ProviderKind: "local",
		ProviderName: "ollama",
		Model:        "local-model",
	}, []ToolDefinition{{
		Name:        "mcp.docs.search",
		Description: "Search docs",
		InputSchema: map[string]any{"type": "object"},
	}}); err != nil {
		t.Fatalf("local provider should allow gateway-specific tool names: %v", err)
	}
}

func TestValidateToolDefinitionsForProviderRejectsOversizedSchema(t *testing.T) {
	err := ValidateToolDefinitionsForProvider(ToolSurfaceValidationTarget{
		ProviderKind: "openai-compatible",
		ProviderName: "openai",
		Model:        "gpt-test",
	}, []ToolDefinition{{
		Name:        "huge_schema",
		Description: "Large schema",
		InputSchema: map[string]any{
			"type": "object",
			"properties": map[string]any{
				"payload": map[string]any{
					"type":        "string",
					"description": strings.Repeat("x", openAIToolSchemaMaxBytes),
				},
			},
		},
	}})
	if err == nil || !strings.Contains(err.Error(), "over provider surface budget") {
		t.Fatalf("expected schema budget error, got %v", err)
	}
}

func TestValidateToolDefinitionsForProviderRejectsNonSerializableSchema(t *testing.T) {
	err := ValidateToolDefinitionsForProvider(ToolSurfaceValidationTarget{
		ProviderKind: "anthropic",
		ProviderName: "anthropic",
		Model:        "claude-test",
	}, []ToolDefinition{{
		Name:        "bad_schema",
		Description: "Bad schema",
		InputSchema: map[string]any{
			"type": "object",
			"bad":  func() {},
		},
	}})
	if err == nil || !strings.Contains(err.Error(), "not JSON serializable") {
		t.Fatalf("expected serialization error, got %v", err)
	}
}
