package generativeui

import (
	"context"
	"crypto/sha256"
	_ "embed"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"strings"

	pluginapi "github.com/blueberrycongee/wuu/packages/plugin-go"
	"github.com/santhosh-tekuri/jsonschema/v6"
)

const (
	uiMIMEType     = "application/vnd.wuu.ui+json"
	maxInputBytes  = 128 * 1024
	maxErrorLength = 4000
)

//go:embed schema.json
var inputSchemaJSON []byte

func Handler() pluginapi.Handler {
	var inputSchema map[string]any
	schemaErr := json.Unmarshal(inputSchemaJSON, &inputSchema)
	compiler := jsonschema.NewCompiler()
	compiler.DefaultDraft(jsonschema.Draft2020)
	if schemaErr == nil {
		schemaErr = compiler.AddResource("generative-ui.json", inputSchema)
	}
	var schema *jsonschema.Schema
	if schemaErr == nil {
		schema, schemaErr = compiler.Compile("generative-ui.json")
	}
	return pluginapi.Handler{
		Definition: pluginapi.Definition{Tools: []pluginapi.Tool{{
			ID: "render_ui",
			// Presentation must remain a top-level result in PTC mode, like
			// present_artifact; nested programs do not forward UI resources.
			DirectOnly:  true,
			Description: "Render an experimental interactive UI in the conversation. Supply a version 1 declarative spec with a title, a useful plain-text fallback and 1–16 uniquely identified text, table, chart or form blocks. Tables support local search/sort; charts support local range selection; forms support local preview/copy/reset only. No HTML, scripts, styles, network requests, submission or host actions. Arguments must fit in 128 KiB.",
			InputSchema: inputSchema,
			Activity:    &pluginapi.ToolActivity{ReadOnly: true, ConcurrencySafe: true},
			Display:     &pluginapi.ToolDisplay{Kind: "generative-ui", Label: "Render UI", Text: "Rendering interactive UI", Capability: "generative-ui"},
		}}},
		Initialize: func(_ context.Context, _ pluginapi.Host, _ pluginapi.InitializeParams) error {
			return schemaErr
		},
		ExecuteTool: func(_ context.Context, _ pluginapi.Host, call pluginapi.ToolCall) (pluginapi.ToolResult, error) {
			if schemaErr != nil {
				return pluginapi.ToolResult{}, fmt.Errorf("load UI schema: %w", schemaErr)
			}
			return executeTool(schema, call)
		},
	}
}

func executeTool(schema *jsonschema.Schema, call pluginapi.ToolCall) (pluginapi.ToolResult, error) {
	if call.ToolID != "render_ui" {
		return pluginapi.ToolResult{}, fmt.Errorf("unknown generative UI tool %q", call.ToolID)
	}
	if len(call.Arguments) > maxInputBytes {
		return invalidSpec(fmt.Errorf("arguments exceed %d bytes", maxInputBytes)), nil
	}
	var input any
	if err := json.Unmarshal(call.Arguments, &input); err != nil {
		return invalidSpec(fmt.Errorf("invalid JSON: %w", err)), nil
	}
	// The advertised schema is also enforced at execution, including every
	// nested object. Model-generated data never selects executable behavior.
	if err := schema.Validate(input); err != nil {
		return invalidSpec(err), nil
	}
	spec := input.(map[string]any)["spec"].(map[string]any)
	if err := validateSemantics(spec); err != nil {
		return invalidSpec(err), nil
	}
	payload, err := json.Marshal(spec)
	if err != nil {
		return pluginapi.ToolResult{}, err
	}
	if len(payload) > maxInputBytes {
		return invalidSpec(fmt.Errorf("encoded UI exceeds %d bytes; shorten the content", maxInputBytes)), nil
	}
	digest := sha256.Sum256(payload)
	// Keep equal-looking cards from sharing identity across calls or threads.
	// These host-supplied identifiers are never accepted from the model spec.
	identity, err := json.Marshal([]string{call.SessionID, call.ThreadID, call.TurnID, call.CallID, call.ExecutionID, hex.EncodeToString(digest[:])})
	if err != nil {
		return pluginapi.ToolResult{}, err
	}
	scope := sha256.Sum256(identity)
	resource, err := json.Marshal(map[string]any{"mimeType": uiMIMEType, "text": string(payload)})
	if err != nil {
		return pluginapi.ToolResult{}, err
	}
	return pluginapi.ToolResult{Content: []pluginapi.ContentPart{
		{Type: "text", Text: spec["fallback"].(string)},
		{
			Type: "resource", MIMEType: uiMIMEType, Name: spec["title"].(string), Resource: resource,
			Artifact: &pluginapi.ArtifactPresentation{
				Placement: "inline", Ref: "ui-v1-" + hex.EncodeToString(scope[:]),
				SHA256: hex.EncodeToString(digest[:]), SizeBytes: int64(len(payload)),
			},
		},
	}}, nil
}

func invalidSpec(err error) pluginapi.ToolResult {
	message := []rune(err.Error())
	if len(message) > maxErrorLength {
		message = append(message[:maxErrorLength], '…')
	}
	result := pluginapi.TextResult("UI was not rendered: " + string(message) + ". Correct the version 1 spec and retry render_ui, or provide the answer as plain text.")
	result.IsError = true
	return result
}

// validateSemantics runs only after schema validation. It checks relationships
// across fields that cannot be expressed portably in tool JSON schemas.
func validateSemantics(spec map[string]any) error {
	if err := nonblank(spec, "spec", "title", "fallback"); err != nil {
		return err
	}
	ids := make(map[string]bool)
	for index, raw := range spec["blocks"].([]any) {
		block := raw.(map[string]any)
		id := block["id"].(string)
		path := fmt.Sprintf("spec.blocks[%d]", index)
		if ids[id] {
			return fmt.Errorf("%s.id %q duplicates another block", path, id)
		}
		ids[id] = true
		if err := nonblank(block, path, "title", "text", "xLabel", "yLabel"); err != nil {
			return err
		}
		switch block["type"] {
		case "table":
			columns := block["columns"].([]any)
			keys := make(map[string]bool)
			for columnIndex, rawColumn := range columns {
				column := rawColumn.(map[string]any)
				key := column["key"].(string)
				if keys[key] {
					return fmt.Errorf("%s.columns[%d].key %q duplicates another column", path, columnIndex, key)
				}
				keys[key] = true
				if err := nonblank(column, fmt.Sprintf("%s.columns[%d]", path, columnIndex), "label"); err != nil {
					return err
				}
			}
			for rowIndex, row := range block["rows"].([]any) {
				if len(row.([]any)) != len(columns) {
					return fmt.Errorf("%s.rows[%d] must contain exactly %d cells", path, rowIndex, len(columns))
				}
			}
		case "chart":
			for pointIndex, point := range block["points"].([]any) {
				if err := nonblank(point.(map[string]any), fmt.Sprintf("%s.points[%d]", path, pointIndex), "label"); err != nil {
					return err
				}
			}
		case "form":
			if err := validateFields(block["fields"].([]any), path); err != nil {
				return err
			}
		}
	}
	return nil
}

func validateFields(fields []any, blockPath string) error {
	ids := make(map[string]bool)
	for index, raw := range fields {
		field := raw.(map[string]any)
		id := field["id"].(string)
		path := fmt.Sprintf("%s.fields[%d]", blockPath, index)
		if ids[id] {
			return fmt.Errorf("%s.id %q duplicates another field", path, id)
		}
		ids[id] = true
		if err := nonblank(field, path, "label"); err != nil {
			return err
		}
		options, hasOptions := field["options"]
		kind := field["kind"].(string)
		if (kind == "select") != hasOptions {
			return fmt.Errorf("%s.options is required for select fields and forbidden for other kinds", path)
		}
		choices := make(map[string]bool)
		if hasOptions {
			for optionIndex, rawOption := range options.([]any) {
				option := rawOption.(string)
				if !hasText(option) || choices[option] {
					return fmt.Errorf("%s.options[%d] must be nonblank and unique", path, optionIndex)
				}
				choices[option] = true
			}
		}
		initial, exists := field["initial"]
		if !exists {
			continue
		}
		valid := false
		switch kind {
		case "text":
			_, valid = initial.(string)
		case "number":
			_, valid = initial.(float64)
		case "checkbox":
			_, valid = initial.(bool)
		case "select":
			value, ok := initial.(string)
			valid = ok && choices[value]
		}
		if !valid {
			return fmt.Errorf("%s.initial must match kind %q and its allowed options", path, kind)
		}
	}
	return nil
}

func nonblank(value map[string]any, path string, keys ...string) error {
	for _, key := range keys {
		if text, exists := value[key]; exists && !hasText(text.(string)) {
			return fmt.Errorf("%s.%s must not be blank", path, key)
		}
	}
	return nil
}

func hasText(value string) bool {
	// Match String.trim in the desktop validator, including a byte-order mark.
	return strings.Trim(value, "\t\n\v\f\r \u00a0\u1680\u2000\u2001\u2002\u2003\u2004\u2005\u2006\u2007\u2008\u2009\u200a\u2028\u2029\u202f\u205f\u3000\ufeff") != ""
}
