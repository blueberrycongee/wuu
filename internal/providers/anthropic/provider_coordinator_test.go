package anthropic

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/blueberrycongee/wuu/internal/providers"
)

const coordinatedAnthropicResponse = `{"id":"msg_test","type":"message","role":"assistant","content":[{"type":"text","text":"ok"}],"stop_reason":"end_turn","usage":{"input_tokens":1,"output_tokens":1}}`

func coordinatedAnthropicClient(t *testing.T, server *httptest.Server, coordinator *providers.ProviderCoordinator) *Client {
	t.Helper()
	client, err := New(ClientConfig{
		BaseURL:     server.URL,
		APIKey:      "coordinator-test-key",
		HTTPClient:  server.Client(),
		Coordinator: coordinator,
	})
	if err != nil {
		t.Fatal(err)
	}
	return client
}

func coordinatedAnthropicRequest() providers.ChatRequest {
	return providers.ChatRequest{
		Model:    "claude-test",
		Messages: []providers.ChatMessage{{Role: "user", Content: "hello"}},
	}
}

func waitForAnthropicCoordinator(t *testing.T, condition func() bool, description string) {
	t.Helper()
	deadline := time.Now().Add(time.Second)
	for !condition() {
		if time.Now().After(deadline) {
			t.Fatalf("timed out waiting for %s", description)
		}
		time.Sleep(time.Millisecond)
	}
}

func TestCoordinatorHoldsAnthropicStreamUntilMessageStop(t *testing.T) {
	streamStarted := make(chan struct{})
	releaseStream := make(chan struct{})
	secondReachedProvider := make(chan struct{}, 1)
	var requests atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		if requests.Add(1) == 1 {
			w.Header().Set("Content-Type", "text/event-stream")
			_, _ = fmt.Fprint(w, "event: message_start\ndata: {\"message\":{\"usage\":{\"input_tokens\":1,\"output_tokens\":0}}}\n\n")
			if flusher, ok := w.(http.Flusher); ok {
				flusher.Flush()
			}
			close(streamStarted)
			<-releaseStream
			_, _ = fmt.Fprint(w, "event: message_stop\ndata: {}\n\n")
			return
		}
		secondReachedProvider <- struct{}{}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(coordinatedAnthropicResponse))
	}))
	defer server.Close()

	coordinator := providers.NewProviderCoordinator(providers.ProviderCoordinatorConfig{MaxInFlight: 1})
	client := coordinatedAnthropicClient(t, server, coordinator)
	stream, err := client.StreamChat(context.Background(), coordinatedAnthropicRequest())
	if err != nil {
		t.Fatal(err)
	}
	streamDone := make(chan error, 1)
	go func() {
		for event := range stream {
			if event.Type == providers.EventError {
				streamDone <- event.Error
				return
			}
		}
		streamDone <- nil
	}()
	select {
	case <-streamStarted:
	case <-time.After(time.Second):
		t.Fatal("Anthropic stream did not start")
	}

	unaryDone := make(chan error, 1)
	go func() {
		_, chatErr := client.Chat(context.Background(), coordinatedAnthropicRequest())
		unaryDone <- chatErr
	}()
	waitForAnthropicCoordinator(t, func() bool {
		return coordinator.Snapshot(client.providerScope).Waiting == 1
	}, "unary request to wait behind Anthropic stream")
	select {
	case <-secondReachedProvider:
		t.Fatal("unary request reached Anthropic before message_stop")
	case <-time.After(20 * time.Millisecond):
	}

	close(releaseStream)
	select {
	case err := <-streamDone:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for Anthropic stream")
	}
	select {
	case err := <-unaryDone:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for Anthropic unary request")
	}
}

func TestCoordinatorObservesAnthropicRateLimit(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		http.Error(w, `{"type":"error","error":{"type":"rate_limit_error","message":"rate limited"}}`, http.StatusTooManyRequests)
	}))
	defer server.Close()
	coordinator := providers.NewProviderCoordinator(providers.ProviderCoordinatorConfig{
		MaxInFlight:           1,
		DefaultRateLimitDelay: 100 * time.Millisecond,
	})
	client := coordinatedAnthropicClient(t, server, coordinator)
	_, err := client.Chat(context.Background(), coordinatedAnthropicRequest())
	if err == nil || !strings.Contains(err.Error(), "429") {
		t.Fatalf("rate-limit error = %v", err)
	}
	snapshot := coordinator.Snapshot(client.providerScope)
	if !snapshot.CooldownUntil.After(time.Now().Add(50 * time.Millisecond)) {
		t.Fatalf("rate limit did not update shared cooldown: %+v", snapshot)
	}
}
