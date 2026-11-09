package openai

import (
	"context"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"

	"github.com/blueberrycongee/wuu/internal/providers"
	"github.com/coder/websocket"
)

func coordinatedResponsesWebSocketClient(t *testing.T, server *httptest.Server, coordinator *providers.ProviderCoordinator, cache *ResponsesWebSocketCache) *Client {
	t.Helper()
	client, err := New(ClientConfig{
		BaseURL:                 server.URL,
		WireAPI:                 "responses",
		APIKey:                  "coordinator-test-key",
		HTTPClient:              server.Client(),
		Coordinator:             coordinator,
		ResponsesTransport:      providers.StreamTransportWebSocket,
		ResponsesWebSocketCache: cache,
	})
	if err != nil {
		t.Fatal(err)
	}
	return client
}

func coordinatedResponsesRequest(sessionID string) providers.ChatRequest {
	return providers.ChatRequest{
		Model:     "gpt-test",
		Messages:  []providers.ChatMessage{{Role: "user", Content: "hello"}},
		CacheHint: &providers.CacheHint{PromptCacheKey: sessionID},
	}
}

func drainCoordinatedResponsesStream(ch <-chan providers.StreamEvent) error {
	for event := range ch {
		if event.Type == providers.EventError {
			return event.Error
		}
	}
	return nil
}

func TestCoordinatorWaitsBeforeResponsesWebSocketSessionMutex(t *testing.T) {
	server := httptest.NewServer(http.NotFoundHandler())
	defer server.Close()
	coordinator := providers.NewProviderCoordinator(providers.ProviderCoordinatorConfig{MaxInFlight: 1})
	cache := NewResponsesWebSocketCache()
	client := coordinatedResponsesWebSocketClient(t, server, coordinator, cache)
	held, err := coordinator.Acquire(context.Background(), client.providerScope, providers.ProviderPriorityInteractive)
	if err != nil {
		t.Fatal(err)
	}
	session := cache.session("mutex-wait")

	ctx, cancel := context.WithCancel(context.Background())
	result := make(chan error, 1)
	go func() {
		_, streamErr := client.StreamChat(ctx, coordinatedResponsesRequest("mutex-wait"))
		result <- streamErr
	}()
	waitForOpenAICoordinator(t, func() bool {
		return coordinator.Snapshot(client.providerScope).Waiting == 1
	}, "websocket request to wait for provider admission")

	mutexAvailable := make(chan struct{})
	go func() {
		session.mu.Lock()
		session.mu.Unlock()
		close(mutexAvailable)
	}()
	select {
	case <-mutexAvailable:
	case <-time.After(100 * time.Millisecond):
		cancel()
		held.Release()
		t.Fatal("provider admission wait held the websocket session mutex")
	}

	cancel()
	held.Release()
	select {
	case err := <-result:
		if err == nil {
			t.Fatal("canceled websocket admission returned nil")
		}
	case <-time.After(time.Second):
		t.Fatal("timed out canceling websocket admission")
	}
}

func TestCoordinatorReleasesWebSocketLeaseBeforeSSEFallback(t *testing.T) {
	var websocketRequests atomic.Int32
	var httpRequests atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Upgrade") == "websocket" {
			websocketRequests.Add(1)
			conn, err := websocket.Accept(w, r, &websocket.AcceptOptions{InsecureSkipVerify: true})
			if err != nil {
				return
			}
			ctx, cancel := context.WithTimeout(r.Context(), time.Second)
			defer cancel()
			_, _, _ = conn.Read(ctx)
			_ = conn.Close(websocket.StatusInternalError, "drop before first event")
			return
		}
		httpRequests.Add(1)
		w.Header().Set("Content-Type", "text/event-stream")
		_, _ = w.Write([]byte("data: {\"type\":\"response.completed\",\"response\":{\"status\":\"completed\",\"output\":[]}}\n\n"))
	}))
	defer server.Close()

	coordinator := providers.NewProviderCoordinator(providers.ProviderCoordinatorConfig{
		MaxInFlight:             1,
		CircuitFailureThreshold: 1,
		CircuitOpenDuration:     500 * time.Millisecond,
	})
	client := coordinatedResponsesWebSocketClient(t, server, coordinator, NewResponsesWebSocketCache())
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	reliable := providers.NewReliableStreamClient(client, nil, providers.WithStreamRetryWait(immediateStreamRetryWait))
	stream, err := reliable.StreamChat(ctx, coordinatedResponsesRequest("fallback-no-deadlock"))
	if err != nil {
		t.Fatal(err)
	}
	if err := drainCoordinatedResponsesStream(stream); err != nil {
		t.Fatal(err)
	}
	if websocketRequests.Load() != 1 || httpRequests.Load() != 1 {
		t.Fatalf("submissions websocket=%d http=%d", websocketRequests.Load(), httpRequests.Load())
	}
	if snapshot := coordinator.Snapshot(client.providerScope); snapshot.InFlight != 0 || snapshot.Waiting != 0 || snapshot.ConsecutiveFailure != 0 || !snapshot.CircuitUntil.IsZero() {
		t.Fatalf("fallback polluted provider capacity state: %+v", snapshot)
	}
}

func TestCoordinatorHoldsResponsesWebSocketUntilTerminalEvent(t *testing.T) {
	websocketStarted := make(chan struct{})
	releaseWebSocket := make(chan struct{})
	httpReachedProvider := make(chan struct{}, 1)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Upgrade") == "websocket" {
			conn, err := websocket.Accept(w, r, &websocket.AcceptOptions{InsecureSkipVerify: true})
			if err != nil {
				return
			}
			defer conn.Close(websocket.StatusNormalClosure, "")
			ctx, cancel := context.WithTimeout(r.Context(), 2*time.Second)
			defer cancel()
			if _, _, err := conn.Read(ctx); err != nil {
				return
			}
			close(websocketStarted)
			<-releaseWebSocket
			_ = conn.Write(ctx, websocket.MessageText, []byte(`{"type":"response.completed","response":{"id":"resp_ws","status":"completed","output":[],"usage":{"input_tokens":1,"output_tokens":1}}}`))
			return
		}
		httpReachedProvider <- struct{}{}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"status":"completed","output":[{"type":"message","role":"assistant","content":[{"type":"output_text","text":"ok"}]}]}`))
	}))
	defer server.Close()

	coordinator := providers.NewProviderCoordinator(providers.ProviderCoordinatorConfig{MaxInFlight: 1})
	client := coordinatedResponsesWebSocketClient(t, server, coordinator, NewResponsesWebSocketCache())
	stream, err := client.StreamChat(context.Background(), coordinatedResponsesRequest("hold-until-terminal"))
	if err != nil {
		t.Fatal(err)
	}
	streamDone := make(chan error, 1)
	go func() { streamDone <- drainCoordinatedResponsesStream(stream) }()
	select {
	case <-websocketStarted:
	case <-time.After(time.Second):
		t.Fatal("websocket request did not start")
	}

	unaryDone := make(chan error, 1)
	go func() {
		_, chatErr := client.Chat(context.Background(), coordinatedResponsesRequest("unary-after-websocket"))
		unaryDone <- chatErr
	}()
	waitForOpenAICoordinator(t, func() bool {
		return coordinator.Snapshot(client.providerScope).Waiting == 1
	}, "HTTP request to wait behind websocket")
	select {
	case <-httpReachedProvider:
		t.Fatal("HTTP request reached provider before websocket terminal event")
	case <-time.After(20 * time.Millisecond):
	}

	close(releaseWebSocket)
	select {
	case err := <-streamDone:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for websocket completion")
	}
	select {
	case err := <-unaryDone:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for HTTP request")
	}
}

func TestCoordinatorDoesNotOpenAccountCircuitForWebSocketOnlyDrop(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		conn, err := websocket.Accept(w, r, &websocket.AcceptOptions{InsecureSkipVerify: true})
		if err != nil {
			return
		}
		ctx, cancel := context.WithTimeout(r.Context(), time.Second)
		defer cancel()
		if _, _, err := conn.Read(ctx); err != nil {
			return
		}
		_ = conn.Write(ctx, websocket.MessageText, []byte(`{"type":"response.created","response":{"id":"resp_partial","status":"in_progress"}}`))
		_ = conn.Close(websocket.StatusInternalError, "drop after first event")
	}))
	defer server.Close()

	coordinator := providers.NewProviderCoordinator(providers.ProviderCoordinatorConfig{
		MaxInFlight:             1,
		CircuitFailureThreshold: 1,
		CircuitOpenDuration:     500 * time.Millisecond,
	})
	client := coordinatedResponsesWebSocketClient(t, server, coordinator, NewResponsesWebSocketCache())
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	stream, err := client.StreamChat(ctx, coordinatedResponsesRequest("ws-drop-is-transport-local"))
	if err != nil {
		t.Fatal(err)
	}
	if err := drainCoordinatedResponsesStream(stream); err == nil {
		t.Fatal("websocket drop after provider event returned success")
	}
	snapshot := coordinator.Snapshot(client.providerScope)
	if snapshot.ConsecutiveFailure != 0 || !snapshot.CircuitUntil.IsZero() || snapshot.InFlight != 0 {
		t.Fatalf("websocket-only drop polluted account circuit: %+v", snapshot)
	}
}
