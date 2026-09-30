package mcp

import (
	"context"
	"encoding/json"
	"errors"
	"strconv"
	"strings"
	"testing"
	"time"
)

func TestStdioTransportCloseReapsProcessAndIsIdempotent(t *testing.T) {
	transport, err := NewStdioTransport("sh", "-c", "cat >/dev/null")
	if err != nil {
		t.Fatalf("NewStdioTransport: %v", err)
	}

	if err := transport.Close(); err != nil {
		t.Fatalf("first Close: %v", err)
	}
	if transport.cmd.ProcessState == nil || !transport.cmd.ProcessState.Exited() {
		t.Fatalf("Close did not reap the child process: %+v", transport.cmd.ProcessState)
	}
	if err := transport.Close(); err != nil {
		t.Fatalf("second Close: %v", err)
	}
}

// A server that stops reading stdin fills the pipe. The caller's deadline must
// still end Send, and because the frame may be half written the connection
// cannot carry another message afterwards.
func TestStdioSendHonorsContextWhenServerStopsReading(t *testing.T) {
	transport, err := NewStdioTransport("sh", "-c", "sleep 30")
	if err != nil {
		t.Fatalf("NewStdioTransport: %v", err)
	}
	defer func() {
		_ = killProcessTree(transport.cmd, transport.processGroup)
		_ = transport.Close()
	}()
	frame := Request{JSONRPC: "2.0", ID: 1, Method: "tools/call", Params: json.RawMessage(strconv.Quote(strings.Repeat("x", 4<<20)))}
	send := func(ctx context.Context) <-chan error {
		done := make(chan error, 1)
		go func() { done <- transport.Send(ctx, frame) }()
		return done
	}

	ctx, cancel := context.WithTimeout(context.Background(), 200*time.Millisecond)
	defer cancel()
	select {
	case err := <-send(ctx):
		if !errors.Is(err, context.DeadlineExceeded) {
			t.Fatalf("Send error = %v, want deadline exceeded", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("Send stayed blocked on a full stdin pipe")
	}
	select {
	case err := <-send(context.Background()):
		if err == nil {
			t.Fatal("a connection with a partial frame accepted another message")
		}
	case <-time.After(5 * time.Second):
		t.Fatal("later Send waited behind the abandoned write")
	}
}
