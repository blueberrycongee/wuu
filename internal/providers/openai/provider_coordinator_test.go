package openai

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

const coordinatedChatResponse = `{"choices":[{"message":{"role":"assistant","content":"ok"},"finish_reason":"stop"}]}`

func coordinatedOpenAIClient(t *testing.T, server *httptest.Server, coordinator *providers.ProviderCoordinator) *Client {
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

func coordinatedChatRequest() providers.ChatRequest {
	return providers.ChatRequest{
		Model:    "test-model",
		Messages: []providers.ChatMessage{{Role: "user", Content: "hello"}},
	}
}

func waitForOpenAICoordinator(t *testing.T, condition func() bool, description string) {
	t.Helper()
	deadline := time.Now().Add(time.Second)
	for !condition() {
		if time.Now().After(deadline) {
			t.Fatalf("timed out waiting for %s", description)
		}
		time.Sleep(time.Millisecond)
	}
}

func TestCoordinatorHoldsUnaryLeaseUntilBodyIsConsumed(t *testing.T) {
	firstHeaders := make(chan struct{})
	releaseFirstBody := make(chan struct{})
	var requests atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		ordinal := requests.Add(1)
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		if flusher, ok := w.(http.Flusher); ok {
			flusher.Flush()
		}
		if ordinal == 1 {
			close(firstHeaders)
			<-releaseFirstBody
		}
		_, _ = w.Write([]byte(coordinatedChatResponse))
	}))
	defer server.Close()

	coordinator := providers.NewProviderCoordinator(providers.ProviderCoordinatorConfig{MaxInFlight: 1})
	client := coordinatedOpenAIClient(t, server, coordinator)
	results := make(chan error, 2)
	go func() {
		_, err := client.Chat(context.Background(), coordinatedChatRequest())
		results <- err
	}()
	select {
	case <-firstHeaders:
	case <-time.After(time.Second):
		t.Fatal("first request did not reach response headers")
	}
	go func() {
		_, err := client.Chat(context.Background(), coordinatedChatRequest())
		results <- err
	}()
	waitForOpenAICoordinator(t, func() bool {
		return coordinator.Snapshot(client.providerScope).Waiting == 1
	}, "second unary request to wait")
	if got := requests.Load(); got != 1 {
		t.Fatalf("provider saw %d requests before the first body completed", got)
	}

	close(releaseFirstBody)
	for range 2 {
		select {
		case err := <-results:
			if err != nil {
				t.Fatal(err)
			}
		case <-time.After(time.Second):
			t.Fatal("timed out waiting for coordinated unary request")
		}
	}
}

func TestCoordinatorHoldsStreamLeaseUntilTerminalEvent(t *testing.T) {
	streamStarted := make(chan struct{})
	releaseStream := make(chan struct{})
	secondReachedProvider := make(chan struct{}, 1)
	var requests atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		ordinal := requests.Add(1)
		if ordinal == 1 {
			w.Header().Set("Content-Type", "text/event-stream")
			_, _ = fmt.Fprint(w, "data: {\"choices\":[{\"delta\":{\"content\":\"partial\"}}]}\n\n")
			if flusher, ok := w.(http.Flusher); ok {
				flusher.Flush()
			}
			close(streamStarted)
			<-releaseStream
			_, _ = fmt.Fprint(w, "data: [DONE]\n\n")
			return
		}
		secondReachedProvider <- struct{}{}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(coordinatedChatResponse))
	}))
	defer server.Close()

	coordinator := providers.NewProviderCoordinator(providers.ProviderCoordinatorConfig{MaxInFlight: 1})
	client := coordinatedOpenAIClient(t, server, coordinator)
	stream, err := client.StreamChat(context.Background(), coordinatedChatRequest())
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
		t.Fatal("stream did not start")
	}

	unaryDone := make(chan error, 1)
	go func() {
		_, chatErr := client.Chat(context.Background(), coordinatedChatRequest())
		unaryDone <- chatErr
	}()
	waitForOpenAICoordinator(t, func() bool {
		return coordinator.Snapshot(client.providerScope).Waiting == 1
	}, "unary request to wait behind live stream")
	select {
	case <-secondReachedProvider:
		t.Fatal("unary request reached provider before stream terminal event")
	case <-time.After(20 * time.Millisecond):
	}

	close(releaseStream)
	select {
	case err := <-streamDone:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for stream completion")
	}
	select {
	case err := <-unaryDone:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for unary request after stream")
	}
}

func TestCoordinatorObservesOpenAIRateLimit(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		http.Error(w, `{"error":{"message":"rate limited"}}`, http.StatusTooManyRequests)
	}))
	defer server.Close()
	coordinator := providers.NewProviderCoordinator(providers.ProviderCoordinatorConfig{
		MaxInFlight:           1,
		DefaultRateLimitDelay: 100 * time.Millisecond,
	})
	client := coordinatedOpenAIClient(t, server, coordinator)
	_, err := client.Chat(context.Background(), coordinatedChatRequest())
	if err == nil || !strings.Contains(err.Error(), "429") {
		t.Fatalf("rate-limit error = %v", err)
	}
	snapshot := coordinator.Snapshot(client.providerScope)
	if !snapshot.CooldownUntil.After(time.Now().Add(50 * time.Millisecond)) {
		t.Fatalf("rate limit did not update shared cooldown: %+v", snapshot)
	}
}
