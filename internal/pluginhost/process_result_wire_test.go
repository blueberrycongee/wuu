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
