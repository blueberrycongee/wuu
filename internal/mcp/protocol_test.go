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
	for _, test := range []struct {
		name string
		id   string
	}{
		{name: "nonempty", id: "server-request"},
		{name: "empty", id: ""},
	} {
		t.Run(test.name, func(t *testing.T) {
			wireID, err := json.Marshal(test.id)
			if err != nil {
				t.Fatal(err)
			}
			incomingWire := []byte(`{"jsonrpc":"2.0","id":` + string(wireID) + `,"method":"sampling/create","params":{}}`)
			var incoming Response
			if err := json.Unmarshal(incomingWire, &incoming); err != nil {
				t.Fatal(err)
			}
			if incoming.StringID != test.id || !incoming.hasID {
				t.Fatalf("string request ID was not preserved: %+v", incoming)
			}
			out, err := json.Marshal(Request{JSONRPC: "2.0", StringID: incoming.StringID, stringID: incoming.stringID, Result: json.RawMessage(`{"ok":true}`), hasID: incoming.hasID})
			if err != nil {
				t.Fatal(err)
			}
			var response map[string]json.RawMessage
			if err := json.Unmarshal(out, &response); err != nil {
				t.Fatal(err)
			}
			if string(response["id"]) != string(wireID) {
				t.Fatalf("server request response changed its ID: %s", out)
			}
		})
	}
}

func TestJSONRPCPreservesNumericZeroAndOmittedIDs(t *testing.T) {
	var incoming Response
	if err := json.Unmarshal([]byte(`{"jsonrpc":"2.0","id":0,"result":{}}`), &incoming); err != nil {
		t.Fatal(err)
	}
	if incoming.ID != 0 || !incoming.hasID {
		t.Fatalf("numeric zero ID was not preserved: %+v", incoming)
	}
	out, err := json.Marshal(Request{JSONRPC: "2.0", ID: incoming.ID, hasID: incoming.hasID, Result: json.RawMessage(`{}`)})
	if err != nil {
		t.Fatal(err)
	}
	var response map[string]json.RawMessage
	if err := json.Unmarshal(out, &response); err != nil {
		t.Fatal(err)
	}
	if string(response["id"]) != "0" {
		t.Fatalf("numeric zero ID changed: %s", out)
	}

	notification, err := json.Marshal(Response{JSONRPC: "2.0", Method: "notifications/initialized"})
	if err != nil {
		t.Fatal(err)
	}
	response = nil
	if err := json.Unmarshal(notification, &response); err != nil {
		t.Fatal(err)
	}
	if _, ok := response["id"]; ok {
		t.Fatalf("notification unexpectedly gained an ID: %s", notification)
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
