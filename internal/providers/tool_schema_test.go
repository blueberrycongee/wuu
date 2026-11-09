package providers

import "testing"

func TestToolInputSchemaForModel_SanitizesGeminiSchema(t *testing.T) {
	original := map[string]any{
		"type": "object",
		"properties": map[string]any{
			"mode": map[string]any{
				"type": "integer",
				"enum": []any{1, 2},
			},
			"tags": map[string]any{
				"type": "array",
			},
			"note": map[string]any{
				"type": "string",
				"properties": map[string]any{
					"nested": map[string]any{"type": "string"},
				},
				"required": []any{"nested"},
			},
		},
		"required": []any{"mode", "missing"},
	}

	got := ToolInputSchemaForModel("google/gemini-3-pro", original)
	props := got["properties"].(map[string]any)
	mode := props["mode"].(map[string]any)
	if mode["type"] != "string" {
		t.Fatalf("expected integer enum type converted to string, got %#v", mode)
	}
	enum := mode["enum"].([]any)
	if enum[0] != "1" || enum[1] != "2" {
		t.Fatalf("expected string enum values, got %#v", enum)
	}
	tags := props["tags"].(map[string]any)
	items := tags["items"].(map[string]any)
	if items["type"] != "string" {
		t.Fatalf("expected empty array items to default to string, got %#v", tags)
	}
	note := props["note"].(map[string]any)
	if _, ok := note["properties"]; ok {
		t.Fatalf("expected properties removed from non-object schema, got %#v", note)
	}
	required := got["required"].([]any)
	if len(required) != 1 || required[0] != "mode" {
		t.Fatalf("expected required fields filtered to existing properties, got %#v", required)
	}

	originalProps := original["properties"].(map[string]any)
	if _, mutated := originalProps["tags"].(map[string]any)["items"]; mutated {
		t.Fatal("expected original schema to remain unchanged")
	}
}

func TestToolInputSchemaForModel_SanitizesKimiSchema(t *testing.T) {
	original := assertKimiSchemaSanitized(t, "moonshotai/kimi-k2")

	originalRef := original["properties"].(map[string]any)["ref"].(map[string]any)
	if originalRef["description"] != "must be dropped" {
		t.Fatal("expected original schema to remain unchanged")
	}
}

func TestToolInputSchemaForModel_SanitizesKimiSchemaOutsideMoonshotProvider(t *testing.T) {
	assertKimiSchemaSanitized(t, "custom-provider/kimi-k2")
}

func assertKimiSchemaSanitized(t *testing.T, model string) map[string]any {
	t.Helper()
	original := map[string]any{
		"type": "object",
		"properties": map[string]any{
			"ref": map[string]any{
				"$ref":        "#/$defs/Target",
				"description": "must be dropped",
			},
			"tuple": map[string]any{
				"type": "array",
				"items": []any{
					map[string]any{"type": "string"},
					map[string]any{"type": "number"},
				},
			},
		},
	}

	got := ToolInputSchemaForModel(model, original)
	props := got["properties"].(map[string]any)
	ref := props["ref"].(map[string]any)
	if len(ref) != 1 || ref["$ref"] != "#/$defs/Target" {
		t.Fatalf("expected $ref sibling keywords dropped, got %#v", ref)
	}
	tuple := props["tuple"].(map[string]any)
	items := tuple["items"].(map[string]any)
	if items["type"] != "string" {
		t.Fatalf("expected tuple items collapsed to first schema, got %#v", tuple["items"])
	}
	return original
}
