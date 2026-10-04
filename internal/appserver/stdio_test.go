package appserver

import (
	"bytes"
	"context"
	"fmt"
	"strings"
	"testing"
)

func TestRunStdioScannerAcceptsAttachmentSizedRequests(t *testing.T) {
	const payloadBytes = 5 * 1024 * 1024

	var out bytes.Buffer
	server := &Server{out: &out}
	input := fmt.Sprintf(
		"{\"id\":\"large\",\"method\":\"test/unknown\",\"params\":{\"data\":\"%s\"}}\n",
		strings.Repeat("a", payloadBytes),
	)

	if err := runStdioScanner(context.Background(), server, strings.NewReader(input)); err != nil {
		t.Fatalf("runStdioScanner rejected attachment-sized request: %v", err)
	}
	if !strings.Contains(out.String(), `"id":"large"`) {
		t.Fatalf("response did not preserve request id: %s", out.String())
	}
}

func TestRunStdioScannerAcceptsFullFrameBudget(t *testing.T) {
	var out bytes.Buffer
	server := &Server{out: &out}
	const prefix = `{"id":"boundary","method":"test/unknown","params":{"data":"`
	const suffix = "\"}}\n"
	input := prefix + strings.Repeat("a", appServerMaxStdioRequestBytes-len(prefix)-len(suffix)) + suffix
	if err := runStdioScanner(context.Background(), server, strings.NewReader(input)); err != nil {
		t.Fatalf("full-budget frame was rejected: %v", err)
	}
	if !strings.Contains(out.String(), `"id":"boundary"`) {
		t.Fatalf("response did not preserve request id: %s", out.String())
	}
}
