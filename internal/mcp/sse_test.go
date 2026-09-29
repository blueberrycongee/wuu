package mcp

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"
	"time"
)

func TestSSETransportConnectHonorsContext(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		<-r.Context().Done()
	}))
	defer server.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()
	transport, err := newSSETransport(ctx, server.URL, nil)
	if transport != nil {
		t.Fatal("newSSETransport returned a transport after the context deadline")
	}
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("newSSETransport error = %v, want context deadline exceeded", err)
	}
}

func TestSSETransportCloseCancelsActiveSend(t *testing.T) {
	messageStarted := make(chan struct{})
	messageRelease := make(chan struct{})
	var messageOnce sync.Once
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/sse":
			w.Header().Set("Content-Type", "text/event-stream")
			w.WriteHeader(http.StatusOK)
			fmt.Fprint(w, "event: endpoint\ndata: /message\n\n")
			w.(http.Flusher).Flush()
			<-r.Context().Done()
		case "/message":
			messageOnce.Do(func() { close(messageStarted) })
			select {
			case <-r.Context().Done():
			case <-messageRelease:
			}
		default:
			http.NotFound(w, r)
		}
	}))
	defer func() {
		close(messageRelease)
		server.Close()
	}()

	transport, err := newSSETransport(context.Background(), server.URL+"/sse", nil)
	if err != nil {
		t.Fatalf("newSSETransport: %v", err)
	}
	client := &Client{name: "server", transport: transport, inFlight: newInFlight()}
	client.readLoop = newReadLoop(transport, client.inFlight, client.handleNotification, client.handleRequest, client.handleReadLoopExit)
	client.readLoop.Start()

	callDone := make(chan error, 1)
	go func() {
		_, err := client.CallTool(context.Background(), "slow", nil)
		callDone <- err
	}()
	select {
	case <-messageStarted:
	case <-time.After(2 * time.Second):
		t.Fatal("SSE message request did not start")
	}
	if err := client.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}
	select {
	case err := <-callDone:
		if !errors.Is(err, context.Canceled) {
			t.Fatalf("CallTool error = %v, want context canceled", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("CallTool did not return after Close")
	}
}

func TestSSEHTTPClientDoesNotSetWholeRequestTimeout(t *testing.T) {
	client := newSSEHTTPClient()
	if client.Timeout != 0 {
		t.Fatalf("http.Client.Timeout = %v, want 0 for a long-lived SSE response", client.Timeout)
	}
	transport, ok := client.Transport.(*http.Transport)
	if !ok {
		t.Fatalf("http client transport = %T, want *http.Transport", client.Transport)
	}
	if transport.ResponseHeaderTimeout <= 0 {
		t.Fatalf("ResponseHeaderTimeout = %v, want a bounded connection handshake", transport.ResponseHeaderTimeout)
	}
}
