package pluginhost

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"os"
	"testing"
	"time"
)

// Service result budgets describe the provider's actual JSON. Transport
// envelopes must not expand raw HTML or Unicode into a larger result payload.
func TestHostServiceResultPreservesBoundedRawJSON(t *testing.T) {
	reader, writer, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	defer reader.Close()
	defer writer.Close()
	client := &ProcessClient{stdin: writer, writeGate: make(chan struct{}, 1), processCtx: context.Background(), config: ProcessConfig{Timeout: time.Second}}
	raw := json.RawMessage("{\"text\":\"<>&\u2028\u2029\",\"nested\":{\"value\":true}}")
	if err := client.writeHostServiceResult(HostServiceResult{ID: "page", Result: raw}); err != nil {
		t.Fatal(err)
	}
	if err := writer.Close(); err != nil {
		t.Fatal(err)
	}
	frame, err := io.ReadAll(reader)
	if err != nil {
		t.Fatal(err)
	}
	var result HostServiceResult
	if err := json.Unmarshal(frame, &result); err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(result.Result, raw) {
		t.Fatalf("raw service result was expanded: got %s want %s", result.Result, raw)
	}
}

// A raw process may bypass SDK types, but cannot inject a different target or
// payload into the separately negotiated continuation gateway.
func TestServiceContinuationWireRejectsAuthorityOverrides(t *testing.T) {
	for _, field := range []string{"service", "method", "params", "caller", "caller_scope", "cleanup", "tool_scope"} {
		t.Run(field, func(t *testing.T) {
			reader, writer, err := os.Pipe()
			if err != nil {
				t.Fatal(err)
			}
			defer reader.Close()
			defer writer.Close()
			client := &ProcessClient{stdin: writer, writeGate: make(chan struct{}, 1), processCtx: context.Background(), config: ProcessConfig{Timeout: time.Second}, negotiated: map[HostServiceMethod]struct{}{ServiceContinueMethod: {}}}
			params, _ := json.Marshal(map[string]any{"execution_id": "exec-original", "continuation_id": "token", field: "override"})
			if err := client.dispatchHostService(context.Background(), HostServiceCall{ID: "override", Method: ServiceContinueMethod, Params: params}); err != nil {
				t.Fatal(err)
			}
			if err := writer.Close(); err != nil {
				t.Fatal(err)
			}
			frame, err := io.ReadAll(reader)
			if err != nil {
				t.Fatal(err)
			}
			var response HostServiceResult
			if err := json.Unmarshal(frame, &response); err != nil {
				t.Fatal(err)
			}
			if response.Error == nil || response.Error.Code != "invalid_request" {
				t.Fatalf("override was routed: %s", frame)
			}
		})
	}
}
