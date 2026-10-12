package providers

import (
	"fmt"
	"strings"
)

// ToolInputSchemaForModel returns a provider/model-compatible copy of schema.
// Some OpenAI-compatible gateways expose non-OpenAI models whose tool schema
// validators are stricter than OpenAI's. Keep this model-scoped so the normal
// GPT/Claude path stays unchanged.
func ToolInputSchemaForModel(model string, schema map[string]any) map[string]any {
	if schema == nil {
		return nil
	}
	lower := strings.ToLower(strings.TrimSpace(model))
	if strings.Contains(lower, "kimi") || strings.Contains(lower, "moonshot") {
		if sanitized, ok := sanitizeMoonshotToolSchema(cloneSchemaValue(schema)).(map[string]any); ok {
			schema = sanitized
		}
	}
	if strings.Contains(lower, "gemini") {
		if sanitized, ok := sanitizeGeminiToolSchema(cloneSchemaValue(schema)).(map[string]any); ok {
			schema = sanitized
		}
	}
	return schema
}

func sanitizeMoonshotToolSchema(value any) any {
	switch typed := value.(type) {
	case map[string]any:
		if ref, ok := typed["$ref"].(string); ok {
			return map[string]any{"$ref": ref}
		}
		out := make(map[string]any, len(typed))
		for key, child := range typed {
			out[key] = sanitizeMoonshotToolSchema(child)
		}
		if items, ok := out["items"].([]any); ok {
			if len(items) > 0 {
				out["items"] = items[0]
			} else {
				out["items"] = map[string]any{}
			}
		}
		return out
	case []any:
		out := make([]any, len(typed))
		for i, child := range typed {
			out[i] = sanitizeMoonshotToolSchema(child)
		}
		return out
	default:
		return typed
	}
}

func sanitizeGeminiToolSchema(value any) any {
	switch typed := value.(type) {
	case map[string]any:
		out := make(map[string]any, len(typed))
		for key, child := range typed {
			if key == "enum" {
				out[key] = stringifySchemaEnum(child)
				continue
			}
			out[key] = sanitizeGeminiToolSchema(child)
		}
		if _, ok := out["enum"]; ok {
			if schemaType(out) == "integer" || schemaType(out) == "number" {
				out["type"] = "string"
			}
		}
		if schemaType(out) == "object" {
			filterRequiredToProperties(out)
		}
		if schemaType(out) == "array" && !hasSchemaCombiner(out) {
			items, ok := out["items"]
			if !ok || items == nil {
				out["items"] = map[string]any{"type": "string"}
			} else if itemMap, ok := items.(map[string]any); ok && !hasSchemaIntent(itemMap) {
				itemMap["type"] = "string"
			}
		}
		if typ := schemaType(out); typ != "" && typ != "object" && !hasSchemaCombiner(out) {
			delete(out, "properties")
			delete(out, "required")
		}
		return out
	case []any:
		out := make([]any, len(typed))
		for i, child := range typed {
			out[i] = sanitizeGeminiToolSchema(child)
		}
		return out
	default:
		return typed
	}
}

// CloneRequestObject preserves JSON object identity, including nested empty
// schemas and provider options. Request round-trips must not turn {} into null.
func CloneRequestObject(value map[string]any) map[string]any {
	if value == nil {
		return nil
	}
	return cloneSchemaValue(value).(map[string]any)
}

func cloneSchemaValue(value any) any {
	switch typed := value.(type) {
	case map[string]any:
		out := make(map[string]any, len(typed))
		for key, child := range typed {
			out[key] = cloneSchemaValue(child)
		}
		return out
	case []any:
		out := make([]any, len(typed))
		for i, child := range typed {
			out[i] = cloneSchemaValue(child)
		}
		return out
	case []string:
		out := make([]string, len(typed))
		copy(out, typed)
		return out
	default:
		return typed
	}
}

func stringifySchemaEnum(value any) any {
	switch typed := value.(type) {
	case []any:
		out := make([]any, len(typed))
		for i, item := range typed {
			out[i] = stringifyEnumValue(item)
		}
		return out
	case []string:
		out := make([]any, len(typed))
		for i, item := range typed {
			out[i] = item
		}
		return out
	default:
		return value
	}
}

func stringifyEnumValue(value any) string {
	switch typed := value.(type) {
	case string:
		return typed
	default:
		return strings.TrimSpace(toString(typed))
	}
}

func toString(value any) string {
	return fmt.Sprint(value)
}

func schemaType(schema map[string]any) string {
	if value, ok := schema["type"].(string); ok {
		return strings.ToLower(strings.TrimSpace(value))
	}
	return ""
}

func filterRequiredToProperties(schema map[string]any) {
	props, ok := schema["properties"].(map[string]any)
	if !ok {
		return
	}
	required, ok := stringSlice(schema["required"])
	if !ok {
		return
	}
	out := make([]any, 0, len(required))
	for _, field := range required {
		if _, exists := props[field]; exists {
			out = append(out, field)
		}
	}
	schema["required"] = out
}

func stringSlice(value any) ([]string, bool) {
	switch typed := value.(type) {
	case []string:
		out := make([]string, len(typed))
		copy(out, typed)
		return out, true
	case []any:
		out := make([]string, 0, len(typed))
		for _, item := range typed {
			text, ok := item.(string)
			if !ok {
				continue
			}
			out = append(out, text)
		}
		return out, true
	default:
		return nil, false
	}
}

func hasSchemaCombiner(schema map[string]any) bool {
	for _, key := range []string{"anyOf", "oneOf", "allOf"} {
		if _, ok := schema[key].([]any); ok {
			return true
		}
	}
	return false
}

func hasSchemaIntent(schema map[string]any) bool {
	if hasSchemaCombiner(schema) {
		return true
	}
	for _, key := range []string{
		"type", "properties", "items", "prefixItems", "enum", "const", "$ref",
		"additionalProperties", "patternProperties", "required", "not", "if", "then", "else",
	} {
		if _, ok := schema[key]; ok {
			return true
		}
	}
	return false
}
