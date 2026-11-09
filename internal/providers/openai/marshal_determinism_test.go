package openai

import (
	"bytes"
	"testing"
)

// Request bodies must serialize with one canonical key order regardless of
// whether provider options are present: automatic prefix caching matches raw
// bytes, so flipping between struct-order and sorted-map-order bodies when
// options appear or disappear would invalidate the cache for the entire
// request.

func TestMarshalChatCompletionsRequestKeyOrderIndependentOfOptions(t *testing.T) {
	payload := chatCompletionsRequest{
		Model: "gpt-x",
		Messages: []chatMessage{
			{Role: "system", Content: "sys"},
			{Role: "user", Content: "hello"},
		},
		Temperature: 0.5,
		Stream:      true,
	}
	without, err := marshalChatCompletionsRequest(payload)
	if err != nil {
		t.Fatal(err)
	}
	payload.Options = map[string]any{}
	withEmpty, err := marshalChatCompletionsRequest(payload)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(without, withEmpty) {
		t.Fatalf("body bytes must not depend on options presence:\nwithout=%s\nwith=%s", without, withEmpty)
	}
	// Sorted-map form: "messages" precedes "model".
	if bytes.Index(without, []byte(`"messages"`)) > bytes.Index(without, []byte(`"model"`)) {
		t.Fatalf("expected canonical sorted key order, got %s", without)
	}
}

func TestMarshalResponsesRequestKeyOrderIndependentOfOptions(t *testing.T) {
	payload := responsesRequest{
		Model:        "gpt-x",
		Instructions: "sys",
		Stream:       true,
	}
	without, err := marshalResponsesRequest(payload)
	if err != nil {
		t.Fatal(err)
	}
	payload.Options = map[string]any{}
	withEmpty, err := marshalResponsesRequest(payload)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(without, withEmpty) {
		t.Fatalf("body bytes must not depend on options presence:\nwithout=%s\nwith=%s", without, withEmpty)
	}
	if bytes.Index(without, []byte(`"instructions"`)) > bytes.Index(without, []byte(`"model"`)) {
		t.Fatalf("expected canonical sorted key order, got %s", without)
	}
}
