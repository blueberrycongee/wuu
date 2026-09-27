package mcp

import (
	"encoding/json"
	"testing"
)

func TestToolProtocolTypesPreserveSchemasAnnotationsAndMetadata(t *testing.T) {
	raw := []byte(`{
  "name":"inspect",
  "title":"Inspect",
  "description":"Inspect state",
  "inputSchema":{"type":"object"},
  "outputSchema":{"type":"object","properties":{"ok":{"type":"boolean"}}},
  "annotations":{"title":"Safe inspect","readOnlyHint":true,"destructiveHint":false,"idempotentHint":true,"openWorldHint":false},
  "_meta":{"vendor":"kept"}
}`)
	var tool Tool
	if err := json.Unmarshal(raw, &tool); err != nil {
		t.Fatal(err)
	}
	if tool.Title != "Inspect" || len(tool.OutputSchema) == 0 || len(tool.Meta) == 0 || tool.Annotations.IdempotentHint == nil || !*tool.Annotations.IdempotentHint {
		t.Fatalf("tool protocol fields lost: %+v", tool)
	}
}
