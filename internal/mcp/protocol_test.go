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

func TestJSONRPCAcceptsAndEchoesStringIDs(t *testing.T) {
	var incoming Response
	if err := json.Unmarshal([]byte(`{"jsonrpc":"2.0","id":"server-request","method":"sampling/create","params":{}}`), &incoming); err != nil {
		t.Fatal(err)
	}
	if incoming.StringID != "server-request" || !incoming.hasID {
		t.Fatalf("string request ID was not preserved: %+v", incoming)
	}
	out, err := json.Marshal(Request{JSONRPC: "2.0", StringID: incoming.StringID, Result: json.RawMessage(`{"ok":true}`), hasID: true})
	if err != nil {
		t.Fatal(err)
	}
	if string(out) != `{"id":"server-request","jsonrpc":"2.0","result":{"ok":true}}` {
		t.Fatalf("server request response changed its ID: %s", out)
	}
}

func TestResolveSSEEndpointPreservesAnnouncedQuery(t *testing.T) {
	got, err := resolveSSEEndpoint("https://example.test/api/sse?stream=1", "/api/message?session=abc")
	if err != nil {
		t.Fatal(err)
	}
	if got != "https://example.test/api/message?session=abc" {
		t.Fatalf("resolved endpoint = %q", got)
	}
}
