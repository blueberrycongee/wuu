package openai

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/blueberrycongee/wuu/internal/agent"
	wuucontext "github.com/blueberrycongee/wuu/internal/context"
	"github.com/blueberrycongee/wuu/internal/providers"
	"github.com/coder/websocket"
)

func immediateStreamRetryWait(context.Context, time.Duration) error { return nil }

func TestResponsesWebSocketEventSizeBoundary(t *testing.T) {
	for _, tc := range []struct {
		name       string
		extra      int
		afterEvent bool
	}{
		{name: "at-limit"},
		{name: "oversized-first-event", extra: 1},
		{name: "oversized-after-event", extra: 1024, afterEvent: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			const prefix = `{"type":"response.output_text.delta","delta":"`
			const suffix = `"}`
			payload := strings.Repeat("x", providers.MaxStreamEventBytes+tc.extra-len(prefix)-len(suffix))
			var requests, sseRequests atomic.Int32
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				requests.Add(1)
				if r.Header.Get("Upgrade") == "" {
					sseRequests.Add(1)
					w.Header().Set("Content-Type", "text/event-stream")
					fmt.Fprint(w, "data: [DONE]\n\n")
					return
				}
				conn, err := websocket.Accept(w, r, &websocket.AcceptOptions{InsecureSkipVerify: true})
				if err != nil {
					t.Error(err)
					return
				}
				defer conn.CloseNow()
				ctx, cancel := context.WithTimeout(r.Context(), 10*time.Second)
				defer cancel()
				if _, _, err := conn.Read(ctx); err != nil {
					t.Error(err)
					return
				}
				if tc.afterEvent {
					writeWSEvent(t, ctx, conn, `{"type":"response.created","response":{"id":"resp_large"}}`)
				}
				// Oversized messages may be rejected before the write finishes.
				if err := conn.Write(ctx, websocket.MessageText, []byte(prefix+payload+suffix)); err != nil && tc.extra == 0 {
					t.Error(err)
				}
				if tc.extra == 0 {
					writeWSEvent(t, ctx, conn, `{"type":"response.completed","response":{"id":"resp_large","status":"completed","output":[]}}`)
				}
			}))
			defer server.Close()
			store := false
			client, err := New(ClientConfig{BaseURL: server.URL, APIKey: "test", WireAPI: "responses", ResponsesStore: &store, ResponsesTransport: providers.StreamTransportAuto, ResponsesWebSocketCache: NewResponsesWebSocketCache()})
			if err != nil {
				t.Fatal(err)
			}
			reliable := providers.NewReliableStreamClient(client, nil, providers.WithStreamRetryWait(immediateStreamRetryWait))
			ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
			defer cancel()
			events, err := reliable.StreamChat(ctx, providers.ChatRequest{Model: "test", Messages: []providers.ChatMessage{{Role: "user", Content: "hello"}}, CacheHint: &providers.CacheHint{PromptCacheKey: tc.name}, Operation: providers.NewInferenceOperation(providers.InferenceOperationAgentRound, providers.InferenceProfileInteractive)})
			if err != nil {
				t.Fatal(err)
			}
			var content strings.Builder
			var terminal error
			done := 0
			for event := range events {
				switch event.Type {
				case providers.EventContentDelta:
					content.WriteString(event.Content)
				case providers.EventError:
					terminal = event.Error
				case providers.EventDone:
					done++
				}
			}
			if requests.Load() != 1 || sseRequests.Load() != 0 {
				t.Fatalf("size limit triggered replay/fallback: requests=%d SSE=%d", requests.Load(), sseRequests.Load())
			}
			if tc.extra == 0 {
				if terminal != nil || done != 1 || content.String() != payload {
					t.Fatalf("boundary event: done=%d content bytes=%d err=%v", done, content.Len(), terminal)
				}
				return
			}
			var limitErr *providers.StreamEventTooLargeError
			if done != 0 || content.Len() != 0 || !errors.As(terminal, &limitErr) || providers.NormalizeFailure(terminal).Category != providers.FailureResponseTooLarge {
				t.Fatalf("oversized event: done=%d content bytes=%d err=%v", done, content.Len(), terminal)
			}
		})
	}
}

func TestResolveCodexWebSocketURL(t *testing.T) {
	cases := []struct {
		in   string
		want string
		err  bool
	}{
		{"https://chatgpt.com/backend-api/codex", "wss://chatgpt.com/backend-api/codex/responses", false},
		{"https://chatgpt.com/backend-api/codex/", "wss://chatgpt.com/backend-api/codex/responses", false},
		{"https://chatgpt.com/backend-api/codex/responses", "wss://chatgpt.com/backend-api/codex/responses", false},
		{"https://chatgpt.com/backend-api/codex/responses/compact", "wss://chatgpt.com/backend-api/codex/responses/compact", false},
		{"http://localhost:8080/codex", "ws://localhost:8080/codex/responses", false},
		{"wss://chatgpt.com/x", "wss://chatgpt.com/x/responses", false},
		{"ws://localhost:8080/x", "ws://localhost:8080/x/responses", false},
		{"", "", true},
		{"ftp://chatgpt.com/x", "", true},
	}
	for _, tc := range cases {
		got, err := resolveCodexWebSocketURL(tc.in)
		if tc.err {
			if err == nil {
				t.Errorf("resolveCodexWebSocketURL(%q): expected error, got %q", tc.in, got)
			}
			continue
		}
		if err != nil {
			t.Errorf("resolveCodexWebSocketURL(%q): unexpected error %v", tc.in, err)
			continue
		}
		if got != tc.want {
			t.Errorf("resolveCodexWebSocketURL(%q) = %q, want %q", tc.in, got, tc.want)
		}
	}
}

func TestDialCodexWebSocket_HappyPath(t *testing.T) {
	var seenHeaders http.Header
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		seenHeaders = r.Header.Clone()
		conn, err := websocket.Accept(w, r, &websocket.AcceptOptions{
			InsecureSkipVerify: true,
		})
		if err != nil {
			t.Errorf("server-side accept: %v", err)
			return
		}
		defer conn.Close(websocket.StatusNormalClosure, "")
		// Echo a single text message so the client can confirm the WS is live.
		ctx, cancel := context.WithTimeout(r.Context(), 2*time.Second)
		defer cancel()
		if err := conn.Write(ctx, websocket.MessageText, []byte("hello")); err != nil {
			t.Errorf("server-side write: %v", err)
			return
		}
		<-r.Context().Done()
	}))
	defer server.Close()

	wsURL := strings.Replace(server.URL, "http://", "ws://", 1)
	headers := http.Header{}
	headers.Set("Authorization", "Bearer test-token")
	headers.Set("chatgpt-account-id", "acct_abc")
	headers.Set("session-id", "thread-1")
	headers.Set("x-client-request-id", "thread-1")

	conn, err := (CodexWebSocketDialer{}).dialCodexWebSocket(context.Background(), wsURL, headers)
	if err != nil {
		t.Fatalf("dialCodexWebSocket: %v", err)
	}
	defer conn.Close(websocket.StatusNormalClosure, "")

	// Handshake headers from the upgrade request the server received.
	if got := seenHeaders.Get("Authorization"); got != "Bearer test-token" {
		t.Errorf("Authorization = %q", got)
	}
	if got := seenHeaders.Get("chatgpt-account-id"); got != "acct_abc" {
		t.Errorf("chatgpt-account-id = %q", got)
	}
	if got := seenHeaders.Get("OpenAI-Beta"); got != CodexWebSocketBetaTag {
		t.Errorf("OpenAI-Beta = %q, want %q", got, CodexWebSocketBetaTag)
	}
	if got := seenHeaders.Get("session-id"); got != "thread-1" {
		t.Errorf("session-id = %q", got)
	}
	if got := seenHeaders.Get("x-client-request-id"); got != "thread-1" {
		t.Errorf("x-client-request-id = %q", got)
	}

	// Confirm a message roundtrip works through the upgraded connection.
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	typ, data, err := conn.Read(ctx)
	if err != nil {
		t.Fatalf("client read: %v", err)
	}
	if typ != websocket.MessageText {
		t.Errorf("message type = %v, want MessageText", typ)
	}
	if string(data) != "hello" {
		t.Errorf("echoed message = %q, want hello", string(data))
	}
}

func TestDialCodexWebSocket_AllowsResponsesMessagesAboveDefaultReadLimit(t *testing.T) {
	largeMessage := strings.Repeat("x", 64*1024)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		conn, err := websocket.Accept(w, r, &websocket.AcceptOptions{
			InsecureSkipVerify: true,
		})
		if err != nil {
			t.Errorf("server-side accept: %v", err)
			return
		}
		defer conn.Close(websocket.StatusNormalClosure, "")
		ctx, cancel := context.WithTimeout(r.Context(), 2*time.Second)
		defer cancel()
		if err := conn.Write(ctx, websocket.MessageText, []byte(largeMessage)); err != nil {
			t.Errorf("server-side write: %v", err)
			return
		}
		<-r.Context().Done()
	}))
	defer server.Close()

	wsURL := strings.Replace(server.URL, "http://", "ws://", 1)
	conn, err := (CodexWebSocketDialer{}).dialCodexWebSocket(context.Background(), wsURL, http.Header{})
	if err != nil {
		t.Fatalf("dialCodexWebSocket: %v", err)
	}
	defer conn.Close(websocket.StatusNormalClosure, "")

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	typ, data, err := conn.Read(ctx)
	if err != nil {
		t.Fatalf("client read: %v", err)
	}
	if typ != websocket.MessageText {
		t.Errorf("message type = %v, want MessageText", typ)
	}
	if string(data) != largeMessage {
		t.Fatalf("large message length = %d, want %d", len(data), len(largeMessage))
	}
}

func TestDialCodexWebSocket_InjectsBetaTagWhenAbsent(t *testing.T) {
	var seenBeta string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		seenBeta = r.Header.Get("OpenAI-Beta")
		conn, err := websocket.Accept(w, r, &websocket.AcceptOptions{InsecureSkipVerify: true})
		if err != nil {
			return
		}
		_ = conn.Close(websocket.StatusNormalClosure, "")
	}))
	defer server.Close()

	wsURL := strings.Replace(server.URL, "http://", "ws://", 1)
	conn, err := (CodexWebSocketDialer{}).dialCodexWebSocket(context.Background(), wsURL, http.Header{})
	if err != nil {
		t.Fatalf("dial: %v", err)
	}
	defer conn.Close(websocket.StatusNormalClosure, "")

	if seenBeta != CodexWebSocketBetaTag {
		t.Errorf("OpenAI-Beta = %q, want %q", seenBeta, CodexWebSocketBetaTag)
	}
}

func TestDialCodexWebSocket_PreservesCallerBetaTag(t *testing.T) {
	var seenBeta string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		seenBeta = r.Header.Get("OpenAI-Beta")
		conn, err := websocket.Accept(w, r, &websocket.AcceptOptions{InsecureSkipVerify: true})
		if err != nil {
			return
		}
		_ = conn.Close(websocket.StatusNormalClosure, "")
	}))
	defer server.Close()

	wsURL := strings.Replace(server.URL, "http://", "ws://", 1)
	headers := http.Header{}
	headers.Set("OpenAI-Beta", "responses=experimental")
	conn, err := (CodexWebSocketDialer{}).dialCodexWebSocket(context.Background(), wsURL, headers)
	if err != nil {
		t.Fatalf("dial: %v", err)
	}
	defer conn.Close(websocket.StatusNormalClosure, "")

	if seenBeta != "responses=experimental" {
		t.Errorf("OpenAI-Beta = %q, want %q (caller override)", seenBeta, "responses=experimental")
	}
}

func TestDialCodexWebSocket_RejectsEmptyURL(t *testing.T) {
	if _, err := (CodexWebSocketDialer{}).dialCodexWebSocket(context.Background(), "", http.Header{}); err == nil {
		t.Fatal("expected error for empty URL")
	}
}

func TestDialCodexWebSocket_RespectsContextCancel(t *testing.T) {
	// Block the upgrade response so the client cancel races.
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		<-r.Context().Done()
	}))
	defer server.Close()

	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	wsURL := strings.Replace(server.URL, "http://", "ws://", 1)
	if _, err := (CodexWebSocketDialer{}).dialCodexWebSocket(ctx, wsURL, http.Header{}); err == nil {
		t.Fatal("expected dial to fail when context is already canceled")
	}
}

func TestDialCodexWebSocket_PreservesUpgradeStatus(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		http.Error(w, "forbidden", http.StatusForbidden)
	}))
	defer server.Close()

	wsURL := strings.Replace(server.URL, "http://", "ws://", 1)
	_, err := (CodexWebSocketDialer{}).dialCodexWebSocket(context.Background(), wsURL, http.Header{})
	if err == nil {
		t.Fatal("expected websocket upgrade to fail")
	}
	var dialErr *CodexWebSocketDialError
	if !errors.As(err, &dialErr) {
		t.Fatalf("dial error type = %T, want *CodexWebSocketDialError", err)
	}
	if dialErr.StatusCode != http.StatusForbidden {
		t.Fatalf("upgrade status = %d, want %d", dialErr.StatusCode, http.StatusForbidden)
	}
}

func TestResponsesWebSocketFallbackTTLClassification(t *testing.T) {
	dialStatus := func(status int) error {
		return &CodexWebSocketDialError{StatusCode: status, Err: errors.New("upgrade failed")}
	}
	tests := []struct {
		name   string
		reason string
		err    error
		want   time.Duration
	}{
		{name: "write failure", reason: "websocket_write_failed", err: errors.New("broken pipe"), want: responsesWebSocketTransientFallbackTTL},
		{name: "network setup failure", reason: "websocket_setup_failed", err: dialStatus(0), want: responsesWebSocketTransientFallbackTTL},
		{name: "server setup failure", reason: "websocket_setup_failed", err: dialStatus(http.StatusServiceUnavailable), want: responsesWebSocketTransientFallbackTTL},
		{name: "connection limit", reason: responsesWebSocketConnectionLimitCode, err: errors.New("limit"), want: responsesWebSocketPressureFallbackTTL},
		{name: "upgrade rate limit", reason: "websocket_setup_failed", err: dialStatus(http.StatusTooManyRequests), want: responsesWebSocketPressureFallbackTTL},
		{name: "auth rejection", reason: "websocket_setup_failed", err: dialStatus(http.StatusUnauthorized), want: responsesWebSocketLongFallbackTTL},
		{name: "missing endpoint", reason: "websocket_setup_failed", err: dialStatus(http.StatusNotFound), want: responsesWebSocketLongFallbackTTL},
		{name: "unsupported upgrade", reason: "websocket_setup_failed", err: dialStatus(http.StatusUpgradeRequired), want: responsesWebSocketLongFallbackTTL},
		{name: "status text is not parsed", reason: "websocket_setup_failed", err: errors.New("status=401"), want: responsesWebSocketTransientFallbackTTL},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			if got := responsesWebSocketFallbackTTL(test.reason, test.err); got != test.want {
				t.Fatalf("fallback TTL = %s, want %s", got, test.want)
			}
		})
	}
}

func TestPrewarmResponsesWebSocket_FailedDialPinsFirstRequestToSSE(t *testing.T) {
	var websocketAttempts atomic.Int32
	var sseRequests atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.EqualFold(r.Header.Get("Upgrade"), "websocket") {
			websocketAttempts.Add(1)
			http.Error(w, "websocket unavailable", http.StatusServiceUnavailable)
			return
		}
		sseRequests.Add(1)
		w.Header().Set("Content-Type", "text/event-stream")
		_, _ = w.Write([]byte(`data: {"type":"response.completed","response":{"id":"resp_sse","status":"completed","output":[],"usage":{"input_tokens":1,"output_tokens":1}}}` + "\n\n"))
	}))
	defer server.Close()

	client, err := New(ClientConfig{
		BaseURL:                 server.URL,
		WireAPI:                 "responses",
		APIKey:                  "test-key",
		ResponsesTransport:      providers.StreamTransportAuto,
		ResponsesWebSocketCache: NewResponsesWebSocketCache(),
	})
	if err != nil {
		t.Fatalf("New: %v", err)
	}

	const sessionID = "thread-failed-prewarm"
	if err := client.PrewarmResponsesWebSocket(context.Background(), sessionID); err == nil {
		t.Fatal("prewarm should report the failed websocket upgrade")
	}
	ch, err := client.StreamChat(context.Background(), providers.ChatRequest{
		Model:     "gpt-test",
		Messages:  []providers.ChatMessage{{Role: "user", Content: "hello"}},
		CacheHint: &providers.CacheHint{PromptCacheKey: sessionID},
	})
	if err != nil {
		t.Fatalf("StreamChat: %v", err)
	}
	states, err := drainStreamProviderStates(ch)
	if err != nil {
		t.Fatalf("drain SSE fallback: %v", err)
	}

	if got := websocketAttempts.Load(); got != 1 {
		t.Fatalf("websocket attempts = %d, want only the prewarm attempt", got)
	}
	if got := sseRequests.Load(); got != 1 {
		t.Fatalf("SSE requests = %d, want 1", got)
	}
	if len(states) != 1 || states[0].Transport != "http" ||
		!states[0].FallbackActive || states[0].FallbackReason != "websocket_setup_failed" ||
		states[0].FallbackPinStatus != responsesWebSocketFallbackPinReused {
		t.Fatalf("unexpected fallback provider state: %+v", states)
	}
}

func TestResponsesStreamChatWebSocket_CanceledSetupDoesNotPinFallback(t *testing.T) {
	requestStarted := make(chan struct{}, 1)
	var requests atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(_ http.ResponseWriter, r *http.Request) {
		requests.Add(1)
		select {
		case requestStarted <- struct{}{}:
		default:
		}
		<-r.Context().Done()
	}))
	defer server.Close()

	cache := NewResponsesWebSocketCache()
	client, err := New(ClientConfig{
		BaseURL:                 server.URL,
		WireAPI:                 "responses",
		APIKey:                  "test-key",
		ResponsesTransport:      providers.StreamTransportAuto,
		ResponsesWebSocketCache: cache,
	})
	if err != nil {
		t.Fatalf("New: %v", err)
	}

	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() {
		_, err := client.StreamChat(ctx, providers.ChatRequest{
			Model:     "gpt-test",
			Messages:  []providers.ChatMessage{{Role: "user", Content: "hello"}},
			CacheHint: &providers.CacheHint{PromptCacheKey: "thread-canceled-setup"},
		})
		done <- err
	}()

	select {
	case <-requestStarted:
	case <-time.After(5 * time.Second):
		t.Fatal("websocket setup did not reach the server")
	}
	cancel()
	select {
	case err := <-done:
		if !errors.Is(err, context.Canceled) {
			t.Fatalf("StreamChat error = %v, want context.Canceled", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("canceled websocket setup did not return")
	}

	if got := requests.Load(); got != 1 {
		t.Fatalf("requests = %d, want websocket attempt only", got)
	}
	cache.mu.Lock()
	_, exists := cache.sessions["thread-canceled-setup"]
	cache.mu.Unlock()
	if exists {
		t.Fatal("caller cancellation must not retain an idle fallback session")
	}
}

func TestResponsesStreamChatWebSocket_UsesPreviousResponseIDDelta(t *testing.T) {
	requests := make(chan map[string]any, 2)
	betas := make(chan string, 1)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		betas <- r.Header.Get("OpenAI-Beta")
		conn, err := websocket.Accept(w, r, &websocket.AcceptOptions{InsecureSkipVerify: true})
		if err != nil {
			t.Errorf("accept websocket: %v", err)
			return
		}
		defer conn.Close(websocket.StatusNormalClosure, "")

		ctx, cancel := context.WithTimeout(r.Context(), 5*time.Second)
		defer cancel()
		for i := 0; i < 2; i++ {
			typ, data, err := conn.Read(ctx)
			if err != nil {
				t.Errorf("read request %d: %v", i+1, err)
				return
			}
			if typ != websocket.MessageText {
				t.Errorf("request %d type = %v", i+1, typ)
				return
			}
			var body map[string]any
			if err := json.Unmarshal(data, &body); err != nil {
				t.Errorf("decode request %d: %v", i+1, err)
				return
			}
			requests <- body

			if i == 0 {
				writeWSEvent(t, ctx, conn, `{"type":"response.created","response":{"id":"resp_1","status":"in_progress"}}`)
				writeWSEvent(t, ctx, conn, `{"type":"response.output_item.added","item":{"id":"fc_1","type":"function_call","status":"in_progress","arguments":"","call_id":"call_1","name":"read_file"},"output_index":0}`)
				writeWSEvent(t, ctx, conn, `{"type":"response.function_call_arguments.done","arguments":"{\"path\":\"README.md\"}","item_id":"fc_1","output_index":0}`)
				writeWSEvent(t, ctx, conn, `{"type":"response.output_item.done","item":{"id":"fc_1","type":"function_call","status":"completed","arguments":"{\"path\":\"README.md\"}","call_id":"call_1","name":"read_file"},"output_index":0}`)
				writeWSEvent(t, ctx, conn, `{"type":"response.done","response":{"id":"resp_1","status":"completed","output":[],"usage":{"input_tokens":5,"output_tokens":2}}}`)
			} else {
				writeWSEvent(t, ctx, conn, `{"type":"response.created","response":{"id":"resp_2","status":"in_progress"}}`)
				writeWSEvent(t, ctx, conn, `{"type":"response.output_item.added","item":{"id":"msg_2","type":"message","role":"assistant","phase":"final_answer","status":"in_progress"},"output_index":0}`)
				writeWSEvent(t, ctx, conn, `{"type":"response.output_text.delta","delta":"done","item_id":"msg_2","output_index":0}`)
				writeWSEvent(t, ctx, conn, `{"type":"response.output_item.done","item":{"id":"msg_2","type":"message","role":"assistant","phase":"final_answer","status":"completed","content":[{"type":"output_text","text":"done"}]},"output_index":0}`)
				writeWSEvent(t, ctx, conn, `{"type":"response.completed","response":{"id":"resp_2","status":"completed","output":[],"usage":{"input_tokens":1,"output_tokens":1}}}`)
			}
		}
	}))
	defer server.Close()

	store := false
	client, err := New(ClientConfig{
		BaseURL:                 server.URL,
		WireAPI:                 "responses",
		APIKey:                  "test-key",
		Headers:                 map[string]string{"OpenAI-Beta": "responses=experimental"},
		ResponsesStore:          &store,
		ResponsesTransport:      providers.StreamTransportAuto,
		ResponsesWebSocketCache: NewResponsesWebSocketCache(),
	})
	if err != nil {
		t.Fatalf("New: %v", err)
	}

	tools := []providers.ToolDefinition{{Name: "read_file", Description: "read file", InputSchema: map[string]any{"type": "object"}}}
	cache := &providers.CacheHint{PromptCacheKey: "thread-1"}
	ch, err := client.StreamChat(context.Background(), providers.ChatRequest{
		Model:     "gpt-test",
		Messages:  []providers.ChatMessage{{Role: "user", Content: "read README"}},
		Tools:     tools,
		CacheHint: cache,
	})
	if err != nil {
		t.Fatalf("first StreamChat: %v", err)
	}
	firstStates, err := drainStreamProviderStates(ch)
	if err != nil {
		t.Fatalf("first stream: %v", err)
	}
	if len(firstStates) != 1 ||
		firstStates[0].ReplayMode != "full_request" ||
		firstStates[0].PreviousResponseIDUsed ||
		firstStates[0].InputItems != 1 ||
		firstStates[0].FullInputItems != 1 ||
		firstStates[0].DeltaInputItems != 0 {
		t.Fatalf("unexpected first provider state: %+v", firstStates)
	}

	reliable := providers.NewReliableStreamClient(client, nil, providers.WithStreamRetryWait(immediateStreamRetryWait))
	ch, err = reliable.StreamChat(context.Background(), providers.ChatRequest{
		Model: "gpt-test",
		Messages: []providers.ChatMessage{
			{Role: "user", Content: "read README"},
			{
				Role: "assistant",
				ToolCalls: []providers.ToolCall{{
					ID:                "call_1",
					ProviderItemID:    "fc_1",
					ProviderItemModel: "gpt-test",
					Name:              "read_file",
					Arguments:         `{"path":"README.md"}`,
				}},
			},
			{Role: "tool", ToolCallID: "call_1", Content: "contents"},
		},
		Tools:     tools,
		CacheHint: cache,
	})
	if err != nil {
		t.Fatalf("second StreamChat: %v", err)
	}
	secondStates, err := drainStreamProviderStates(ch)
	if err != nil {
		t.Fatalf("second stream: %v", err)
	}
	if len(secondStates) != 1 ||
		secondStates[0].ReplayMode != "previous_response_id" ||
		!secondStates[0].PreviousResponseIDUsed ||
		secondStates[0].InputItems != 1 ||
		secondStates[0].FullInputItems != 3 ||
		secondStates[0].DeltaInputItems != 1 {
		t.Fatalf("unexpected second provider state: %+v", secondStates)
	}

	if got := <-betas; got != CodexWebSocketBetaTag {
		t.Fatalf("OpenAI-Beta = %q, want %q", got, CodexWebSocketBetaTag)
	}
	first := <-requests
	if first["type"] != "response.create" {
		t.Fatalf("first request type = %#v", first["type"])
	}
	if _, exists := first["previous_response_id"]; exists {
		t.Fatalf("first request must be full context: %#v", first)
	}
	firstInput := first["input"].([]any)
	if len(firstInput) != 1 {
		t.Fatalf("first request input = %#v", firstInput)
	}

	second := <-requests
	if second["previous_response_id"] != "resp_1" {
		t.Fatalf("second request previous_response_id = %#v; body=%#v", second["previous_response_id"], second)
	}
	secondInput := second["input"].([]any)
	if len(secondInput) != 1 {
		t.Fatalf("second request should send only delta input, got %#v", secondInput)
	}
	output, ok := secondInput[0].(map[string]any)
	if !ok || output["type"] != "function_call_output" || output["call_id"] != "call_1" || output["output"] != "contents" {
		t.Fatalf("unexpected delta input: %#v", secondInput[0])
	}
}

func TestResponsesStreamChatWebSocket_WebSocketModeDoesNotUseCachedContext(t *testing.T) {
	requests := make(chan map[string]any, 2)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		conn, err := websocket.Accept(w, r, &websocket.AcceptOptions{InsecureSkipVerify: true})
		if err != nil {
			t.Errorf("accept websocket: %v", err)
			return
		}
		defer conn.Close(websocket.StatusNormalClosure, "")

		ctx, cancel := context.WithTimeout(r.Context(), 5*time.Second)
		defer cancel()
		for i := 0; i < 2; i++ {
			readWSRequest(t, ctx, conn, requests)
			if i == 0 {
				writeWSEvent(t, ctx, conn, `{"type":"response.created","response":{"id":"resp_1","status":"in_progress"}}`)
				writeWSEvent(t, ctx, conn, `{"type":"response.output_item.done","item":{"id":"msg_1","type":"message","role":"assistant","phase":"final_answer","status":"completed","content":[{"type":"output_text","text":"first answer"}]},"output_index":0}`)
				writeWSEvent(t, ctx, conn, `{"type":"response.completed","response":{"id":"resp_1","status":"completed","output":[],"usage":{"input_tokens":5,"output_tokens":2}}}`)
				continue
			}
			writeWSEvent(t, ctx, conn, `{"type":"response.created","response":{"id":"resp_2","status":"in_progress"}}`)
			writeWSEvent(t, ctx, conn, `{"type":"response.output_item.done","item":{"id":"msg_2","type":"message","role":"assistant","phase":"final_answer","status":"completed","content":[{"type":"output_text","text":"second answer"}]},"output_index":0}`)
			writeWSEvent(t, ctx, conn, `{"type":"response.completed","response":{"id":"resp_2","status":"completed","output":[],"usage":{"input_tokens":8,"output_tokens":2}}}`)
		}
	}))
	defer server.Close()

	store := false
	client, err := New(ClientConfig{
		BaseURL:            server.URL,
		WireAPI:            "responses",
		APIKey:             "test-key",
		ResponsesStore:     &store,
		ResponsesTransport: providers.StreamTransportWebSocket,
	})
	if err != nil {
		t.Fatalf("New: %v", err)
	}

	cache := &providers.CacheHint{PromptCacheKey: "thread-websocket-full"}
	ch, err := client.StreamChat(context.Background(), providers.ChatRequest{
		Model:     "gpt-test",
		Messages:  []providers.ChatMessage{{Role: "user", Content: "first"}},
		CacheHint: cache,
	})
	if err != nil {
		t.Fatalf("first StreamChat: %v", err)
	}
	firstStates, err := drainStreamProviderStates(ch)
	if err != nil {
		t.Fatalf("first stream: %v", err)
	}
	if len(firstStates) != 1 || firstStates[0].ConfiguredTransport != string(providers.StreamTransportWebSocket) {
		t.Fatalf("unexpected first provider state: %+v", firstStates)
	}

	ch, err = client.StreamChat(context.Background(), providers.ChatRequest{
		Model: "gpt-test",
		Messages: []providers.ChatMessage{
			{Role: "user", Content: "first"},
			{Role: "assistant", Content: "first answer", Phase: providers.MessagePhaseFinalAnswer, ProviderItemID: "msg_1", ProviderItemModel: "gpt-test"},
			{Role: "user", Content: "second"},
		},
		CacheHint: cache,
	})
	if err != nil {
		t.Fatalf("second StreamChat: %v", err)
	}
	secondStates, err := drainStreamProviderStates(ch)
	if err != nil {
		t.Fatalf("second stream: %v", err)
	}
	if len(secondStates) != 1 ||
		secondStates[0].ReplayMode != "full_request" ||
		secondStates[0].PreviousResponseIDUsed ||
		secondStates[0].InputItems != 3 ||
		secondStates[0].FullInputItems != 3 {
		t.Fatalf("unexpected second provider state: %+v", secondStates)
	}

	<-requests
	second := <-requests
	if _, exists := second["previous_response_id"]; exists {
		t.Fatalf("websocket transport must not use cached context: %#v", second)
	}
	if input := second["input"].([]any); len(input) != 3 {
		t.Fatalf("websocket transport should send full request input, got %#v", input)
	}
}

func TestResponsesStreamChatWebSocket_IdleTTLExpiresCachedConnection(t *testing.T) {
	requests := make(chan map[string]any, 2)
	var connections atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		conn, err := websocket.Accept(w, r, &websocket.AcceptOptions{InsecureSkipVerify: true})
		if err != nil {
			t.Errorf("accept websocket: %v", err)
			return
		}
		defer conn.Close(websocket.StatusNormalClosure, "")

		ctx, cancel := context.WithTimeout(r.Context(), 5*time.Second)
		defer cancel()
		connection := connections.Add(1)
		readWSRequest(t, ctx, conn, requests)
		responseID := "resp_1"
		messageID := "msg_1"
		answer := "first answer"
		if connection == 2 {
			responseID = "resp_2"
			messageID = "msg_2"
			answer = "second answer"
		}
		writeWSEvent(t, ctx, conn, `{"type":"response.created","response":{"id":"`+responseID+`","status":"in_progress"}}`)
		writeWSEvent(t, ctx, conn, `{"type":"response.output_item.done","item":{"id":"`+messageID+`","type":"message","role":"assistant","phase":"final_answer","status":"completed","content":[{"type":"output_text","text":"`+answer+`"}]},"output_index":0}`)
		writeWSEvent(t, ctx, conn, `{"type":"response.completed","response":{"id":"`+responseID+`","status":"completed","output":[],"usage":{"input_tokens":5,"output_tokens":2}}}`)
	}))
	defer server.Close()

	store := false
	client, err := New(ClientConfig{
		BaseURL:                 server.URL,
		WireAPI:                 "responses",
		APIKey:                  "test-key",
		ResponsesStore:          &store,
		ResponsesTransport:      providers.StreamTransportAuto,
		ResponsesWebSocketCache: NewResponsesWebSocketCacheWithTTL(5 * time.Millisecond),
	})
	if err != nil {
		t.Fatalf("New: %v", err)
	}

	cache := &providers.CacheHint{PromptCacheKey: "thread-idle-ttl"}
	ch, err := client.StreamChat(context.Background(), providers.ChatRequest{
		Model:     "gpt-test",
		Messages:  []providers.ChatMessage{{Role: "user", Content: "first"}},
		CacheHint: cache,
	})
	if err != nil {
		t.Fatalf("first StreamChat: %v", err)
	}
	if err := drainStream(ch); err != nil {
		t.Fatalf("first stream: %v", err)
	}
	time.Sleep(50 * time.Millisecond)

	ch, err = client.StreamChat(context.Background(), providers.ChatRequest{
		Model: "gpt-test",
		Messages: []providers.ChatMessage{
			{Role: "user", Content: "first"},
			{Role: "assistant", Content: "first answer", Phase: providers.MessagePhaseFinalAnswer, ProviderItemID: "msg_1", ProviderItemModel: "gpt-test"},
			{Role: "user", Content: "second"},
		},
		CacheHint: cache,
	})
	if err != nil {
		t.Fatalf("second StreamChat: %v", err)
	}
	states, err := drainStreamProviderStates(ch)
	if err != nil {
		t.Fatalf("second stream: %v", err)
	}
	if len(states) != 1 ||
		states[0].ConnectionReused ||
		states[0].ReplayMode != "full_request" ||
		states[0].PreviousResponseIDUsed ||
		states[0].InputItems != 3 {
		t.Fatalf("unexpected provider state after idle expiry: %+v", states)
	}

	<-requests
	second := <-requests
	if _, exists := second["previous_response_id"]; exists {
		t.Fatalf("expired websocket cache must not reuse previous_response_id: %#v", second)
	}
	if got := connections.Load(); got != 2 {
		t.Fatalf("expected a fresh websocket connection after idle expiry, got %d", got)
	}
}

func TestResponsesStreamChatWebSocket_UsesPreviousResponseIDDeltaAfterFinalAnswer(t *testing.T) {
	requests := make(chan map[string]any, 2)
	const reasoningItem = `{"id":"rs_1","type":"reasoning","content":[],"encrypted_content":"abc","summary":[]}`
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		conn, err := websocket.Accept(w, r, &websocket.AcceptOptions{InsecureSkipVerify: true})
		if err != nil {
			t.Errorf("accept websocket: %v", err)
			return
		}
		defer conn.Close(websocket.StatusNormalClosure, "")

		ctx, cancel := context.WithTimeout(r.Context(), 5*time.Second)
		defer cancel()
		for i := 0; i < 2; i++ {
			typ, data, err := conn.Read(ctx)
			if err != nil {
				t.Errorf("read request %d: %v", i+1, err)
				return
			}
			if typ != websocket.MessageText {
				t.Errorf("request %d type = %v", i+1, typ)
				return
			}
			var body map[string]any
			if err := json.Unmarshal(data, &body); err != nil {
				t.Errorf("decode request %d: %v", i+1, err)
				return
			}
			requests <- body

			if i == 0 {
				writeWSEvent(t, ctx, conn, `{"type":"response.created","response":{"id":"resp_1","status":"in_progress"}}`)
				writeWSEvent(t, ctx, conn, `{"type":"response.output_item.done","item":`+reasoningItem+`,"output_index":0}`)
				writeWSEvent(t, ctx, conn, `{"type":"response.output_item.added","item":{"id":"msg_1","type":"message","role":"assistant","phase":"final_answer","status":"in_progress"},"output_index":1}`)
				writeWSEvent(t, ctx, conn, `{"type":"response.output_text.delta","delta":"probe-one","item_id":"msg_1","output_index":1}`)
				writeWSEvent(t, ctx, conn, `{"type":"response.output_item.done","item":{"id":"msg_1","type":"message","role":"assistant","phase":"final_answer","status":"completed","content":[{"type":"output_text","text":"probe-one"}]},"output_index":1}`)
				writeWSEvent(t, ctx, conn, `{"type":"response.completed","response":{"id":"resp_1","status":"completed","output":[],"usage":{"input_tokens":5,"output_tokens":2}}}`)
			} else {
				writeWSEvent(t, ctx, conn, `{"type":"response.created","response":{"id":"resp_2","status":"in_progress"}}`)
				writeWSEvent(t, ctx, conn, `{"type":"response.output_item.added","item":{"id":"msg_2","type":"message","role":"assistant","phase":"final_answer","status":"in_progress"},"output_index":0}`)
				writeWSEvent(t, ctx, conn, `{"type":"response.output_text.delta","delta":"probe-two","item_id":"msg_2","output_index":0}`)
				writeWSEvent(t, ctx, conn, `{"type":"response.output_item.done","item":{"id":"msg_2","type":"message","role":"assistant","phase":"final_answer","status":"completed","content":[{"type":"output_text","text":"probe-two"}]},"output_index":0}`)
				writeWSEvent(t, ctx, conn, `{"type":"response.completed","response":{"id":"resp_2","status":"completed","output":[],"usage":{"input_tokens":1,"output_tokens":1}}}`)
			}
		}
	}))
	defer server.Close()

	store := false
	client, err := New(ClientConfig{
		BaseURL:            server.URL,
		WireAPI:            "responses",
		APIKey:             "test-key",
		ResponsesStore:     &store,
		ResponsesTransport: providers.StreamTransportAuto,
	})
	if err != nil {
		t.Fatalf("New: %v", err)
	}

	cache := &providers.CacheHint{PromptCacheKey: "thread-final-delta"}
	ch, err := client.StreamChat(context.Background(), providers.ChatRequest{
		Model:     "gpt-test",
		Messages:  []providers.ChatMessage{{Role: "user", Content: "first"}},
		CacheHint: cache,
	})
	if err != nil {
		t.Fatalf("first StreamChat: %v", err)
	}
	if err := drainStream(ch); err != nil {
		t.Fatalf("first stream: %v", err)
	}

	ch, err = client.StreamChat(context.Background(), providers.ChatRequest{
		Model: "gpt-test",
		Messages: []providers.ChatMessage{
			{Role: "user", Content: "first"},
			{
				Role:              "assistant",
				Content:           "probe-one",
				Phase:             providers.MessagePhaseFinalAnswer,
				ProviderItemID:    "msg_1",
				ProviderItemModel: "gpt-test",
				ReasoningBlocks: []providers.ReasoningBlock{{
					Type: "reasoning",
					Data: reasoningItem,
				}},
			},
			{Role: "user", Content: "second"},
		},
		CacheHint: cache,
	})
	if err != nil {
		t.Fatalf("second StreamChat: %v", err)
	}
	secondStates, err := drainStreamProviderStates(ch)
	if err != nil {
		t.Fatalf("second stream: %v", err)
	}
	if len(secondStates) != 1 ||
		secondStates[0].ReplayMode != "previous_response_id" ||
		!secondStates[0].PreviousResponseIDUsed ||
		secondStates[0].InputItems != 1 ||
		secondStates[0].FullInputItems != 4 ||
		secondStates[0].DeltaInputItems != 1 {
		t.Fatalf("unexpected final-answer provider state: %+v", secondStates)
	}

	<-requests
	second := <-requests
	if second["previous_response_id"] != "resp_1" {
		t.Fatalf("final-answer request previous_response_id = %#v; body=%#v", second["previous_response_id"], second)
	}
	secondInput := second["input"].([]any)
	if len(secondInput) != 1 {
		t.Fatalf("final-answer request should send only delta input, got %#v", secondInput)
	}
}

func TestResponsesStreamChatWebSocket_RunnerKeepsDeltaAcrossTurnsWithChangingContext(t *testing.T) {
	requests := make(chan map[string]any, 2)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		conn, err := websocket.Accept(w, r, &websocket.AcceptOptions{InsecureSkipVerify: true})
		if err != nil {
			t.Errorf("accept websocket: %v", err)
			return
		}
		defer conn.Close(websocket.StatusNormalClosure, "")
		ctx, cancel := context.WithTimeout(r.Context(), 5*time.Second)
		defer cancel()
		for i := 0; i < 2; i++ {
			readWSRequest(t, ctx, conn, requests)
			id := strconv.Itoa(i + 1)
			writeWSEvent(t, ctx, conn, `{"type":"response.created","response":{"id":"resp_`+id+`","status":"in_progress"}}`)
			writeWSEvent(t, ctx, conn, `{"type":"response.output_item.added","item":{"id":"msg_`+id+`","type":"message","role":"assistant","phase":"final_answer","status":"in_progress"},"output_index":0}`)
			writeWSEvent(t, ctx, conn, `{"type":"response.output_text.delta","delta":"answer-`+id+`","item_id":"msg_`+id+`","output_index":0}`)
			writeWSEvent(t, ctx, conn, `{"type":"response.output_item.done","item":{"id":"msg_`+id+`","type":"message","role":"assistant","phase":"final_answer","status":"completed","content":[{"type":"output_text","text":"answer-`+id+`"}]},"output_index":0}`)
			writeWSEvent(t, ctx, conn, `{"type":"response.completed","response":{"id":"resp_`+id+`","status":"completed","output":[],"usage":{"input_tokens":10,"output_tokens":2}}}`)
		}
	}))
	defer server.Close()

	store := false
	client, err := New(ClientConfig{
		BaseURL: server.URL, WireAPI: "responses", APIKey: "test-key",
		ResponsesStore: &store, ResponsesTransport: providers.StreamTransportAuto,
		ResponsesWebSocketCache: NewResponsesWebSocketCache(),
	})
	if err != nil {
		t.Fatal(err)
	}
	state := "alpha"
	runner := agent.StreamRunner{
		Client: client, Model: "gpt-test", PromptCacheKey: "runner-cross-turn-context",
		BeforeRequestContext: func() []agent.ContextSegment {
			return agent.RequestOnlyContextBlocks([]wuucontext.Block{{
				Kind: wuucontext.BlockToolResultSummary, Title: "Tool state", Source: "tool_telemetry", Content: "State: " + state,
			}})
		},
	}
	history1 := []providers.ChatMessage{{Role: "user", Content: "first ask"}}
	res1, err := runner.RunWithCallback(context.Background(), history1, nil)
	if err != nil {
		t.Fatalf("turn 1: %v", err)
	}
	state = "beta"
	history2 := append(append(providers.CloneChatMessages(history1), res1.NewMessages...), providers.ChatMessage{Role: "user", Content: "second ask"})
	var states []providers.ProviderStateSummary
	if _, err := runner.RunWithCallback(context.Background(), history2, func(event providers.StreamEvent) {
		if event.Type == providers.EventProviderState && event.ProviderState != nil {
			states = append(states, *event.ProviderState)
		}
	}); err != nil {
		t.Fatalf("turn 2: %v", err)
	}
	if len(states) != 1 || !states[0].PreviousResponseIDUsed || states[0].ReplayMode != "previous_response_id" {
		t.Fatalf("turn 2 did not use Responses delta continuation: %+v", states)
	}
	first := <-requests
	if _, ok := first["previous_response_id"]; ok {
		t.Fatalf("turn 1 unexpectedly used previous_response_id: %#v", first)
	}
	second := <-requests
	if second["previous_response_id"] != "resp_1" {
		t.Fatalf("turn 2 previous_response_id = %#v", second["previous_response_id"])
	}
	delta, err := json.Marshal(second["input"])
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Contains(delta, []byte("State: beta")) || !bytes.Contains(delta, []byte("second ask")) {
		t.Fatalf("turn 2 delta missing fresh context or user input: %s", delta)
	}
	if bytes.Contains(delta, []byte("State: alpha")) {
		t.Fatalf("turn 2 delta re-sent superseded context: %s", delta)
	}
}

func TestResponsesStreamChatWebSocket_PreservesDeltaWithHiddenModelContext(t *testing.T) {
	requests := make(chan map[string]any, 2)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		conn, err := websocket.Accept(w, r, &websocket.AcceptOptions{InsecureSkipVerify: true})
		if err != nil {
			t.Errorf("accept websocket: %v", err)
			return
		}
		defer conn.Close(websocket.StatusNormalClosure, "")

		ctx, cancel := context.WithTimeout(r.Context(), 5*time.Second)
		defer cancel()
		for i := 0; i < 2; i++ {
			typ, data, err := conn.Read(ctx)
			if err != nil {
				t.Errorf("read request %d: %v", i+1, err)
				return
			}
			if typ != websocket.MessageText {
				t.Errorf("request %d type = %v", i+1, typ)
				return
			}
			var body map[string]any
			if err := json.Unmarshal(data, &body); err != nil {
				t.Errorf("decode request %d: %v", i+1, err)
				return
			}
			requests <- body

			if i == 0 {
				writeWSEvent(t, ctx, conn, `{"type":"response.created","response":{"id":"resp_1","status":"in_progress"}}`)
				writeWSEvent(t, ctx, conn, `{"type":"response.output_item.added","item":{"id":"msg_1","type":"message","role":"assistant","phase":"final_answer","status":"in_progress"},"output_index":0}`)
				writeWSEvent(t, ctx, conn, `{"type":"response.output_text.delta","delta":"first answer","item_id":"msg_1","output_index":0}`)
				writeWSEvent(t, ctx, conn, `{"type":"response.output_item.done","item":{"id":"msg_1","type":"message","role":"assistant","phase":"final_answer","status":"completed","content":[{"type":"output_text","text":"first answer"}]},"output_index":0}`)
				writeWSEvent(t, ctx, conn, `{"type":"response.completed","response":{"id":"resp_1","status":"completed","output":[],"usage":{"input_tokens":8,"output_tokens":2}}}`)
			} else {
				writeWSEvent(t, ctx, conn, `{"type":"response.created","response":{"id":"resp_2","status":"in_progress"}}`)
				writeWSEvent(t, ctx, conn, `{"type":"response.output_item.added","item":{"id":"msg_2","type":"message","role":"assistant","phase":"final_answer","status":"in_progress"},"output_index":0}`)
				writeWSEvent(t, ctx, conn, `{"type":"response.output_text.delta","delta":"second answer","item_id":"msg_2","output_index":0}`)
				writeWSEvent(t, ctx, conn, `{"type":"response.output_item.done","item":{"id":"msg_2","type":"message","role":"assistant","phase":"final_answer","status":"completed","content":[{"type":"output_text","text":"second answer"}]},"output_index":0}`)
				writeWSEvent(t, ctx, conn, `{"type":"response.completed","response":{"id":"resp_2","status":"completed","output":[],"usage":{"input_tokens":1,"output_tokens":1}}}`)
			}
		}
	}))
	defer server.Close()

	store := false
	client, err := New(ClientConfig{
		BaseURL:                 server.URL,
		WireAPI:                 "responses",
		APIKey:                  "test-key",
		ResponsesStore:          &store,
		ResponsesTransport:      providers.StreamTransportAuto,
		ResponsesWebSocketCache: NewResponsesWebSocketCache(),
	})
	if err != nil {
		t.Fatalf("New: %v", err)
	}

	hiddenContext := providers.ChatMessage{
		Role:    "user",
		Name:    wuucontext.SystemReminderMessageName,
		Content: "<system-reminder>\n# Environment\n- CWD: /tmp/project\n</system-reminder>",
		Hidden:  true,
	}
	cache := &providers.CacheHint{PromptCacheKey: "thread-hidden-context"}
	ch, err := client.StreamChat(context.Background(), providers.ChatRequest{
		Model:     "gpt-test",
		Messages:  []providers.ChatMessage{{Role: "user", Content: "first"}, hiddenContext},
		CacheHint: cache,
	})
	if err != nil {
		t.Fatalf("first StreamChat: %v", err)
	}
	if err := drainStream(ch); err != nil {
		t.Fatalf("first stream: %v", err)
	}

	ch, err = client.StreamChat(context.Background(), providers.ChatRequest{
		Model: "gpt-test",
		Messages: []providers.ChatMessage{
			{Role: "user", Content: "first"},
			hiddenContext,
			{
				Role:              "assistant",
				Content:           "first answer",
				Phase:             providers.MessagePhaseFinalAnswer,
				ProviderItemID:    "msg_1",
				ProviderItemModel: "gpt-test",
			},
			{Role: "user", Content: "second"},
		},
		CacheHint: cache,
	})
	if err != nil {
		t.Fatalf("second StreamChat: %v", err)
	}
	if err := drainStream(ch); err != nil {
		t.Fatalf("second stream: %v", err)
	}

	first := <-requests
	firstInput := first["input"].([]any)
	if _, exists := first["previous_response_id"]; exists {
		t.Fatalf("first request must be full context: %#v", first)
	}
	if len(firstInput) != 2 {
		t.Fatalf("first request should include user input and hidden context, got %#v", firstInput)
	}
	hiddenInput, ok := firstInput[1].(map[string]any)
	if !ok || hiddenInput["role"] != "user" {
		t.Fatalf("hidden context serialized unexpectedly: %#v", firstInput[1])
	}

	second := <-requests
	if second["previous_response_id"] != "resp_1" {
		t.Fatalf("second request previous_response_id = %#v; body=%#v", second["previous_response_id"], second)
	}
	secondInput := second["input"].([]any)
	if len(secondInput) != 1 {
		t.Fatalf("second request should send only new user input, got %#v", secondInput)
	}
	delta, ok := secondInput[0].(map[string]any)
	if !ok || delta["role"] != "user" {
		t.Fatalf("unexpected delta input: %#v", secondInput[0])
	}
}

func TestResponsesStreamChatWebSocket_DeltaWithRefreshedHiddenContextAcrossTurns(t *testing.T) {
	requests := make(chan map[string]any, 2)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		conn, err := websocket.Accept(w, r, &websocket.AcceptOptions{InsecureSkipVerify: true})
		if err != nil {
			t.Errorf("accept websocket: %v", err)
			return
		}
		defer conn.Close(websocket.StatusNormalClosure, "")

		ctx, cancel := context.WithTimeout(r.Context(), 5*time.Second)
		defer cancel()
		for i := 0; i < 2; i++ {
			typ, data, err := conn.Read(ctx)
			if err != nil {
				t.Errorf("read request %d: %v", i+1, err)
				return
			}
			if typ != websocket.MessageText {
				t.Errorf("request %d type = %v", i+1, typ)
				return
			}
			var body map[string]any
			if err := json.Unmarshal(data, &body); err != nil {
				t.Errorf("decode request %d: %v", i+1, err)
				return
			}
			requests <- body

			if i == 0 {
				writeWSEvent(t, ctx, conn, `{"type":"response.created","response":{"id":"resp_1","status":"in_progress"}}`)
				writeWSEvent(t, ctx, conn, `{"type":"response.output_item.added","item":{"id":"msg_1","type":"message","role":"assistant","phase":"final_answer","status":"in_progress"},"output_index":0}`)
				writeWSEvent(t, ctx, conn, `{"type":"response.output_text.delta","delta":"first answer","item_id":"msg_1","output_index":0}`)
				writeWSEvent(t, ctx, conn, `{"type":"response.output_item.done","item":{"id":"msg_1","type":"message","role":"assistant","phase":"final_answer","status":"completed","content":[{"type":"output_text","text":"first answer"}]},"output_index":0}`)
				writeWSEvent(t, ctx, conn, `{"type":"response.completed","response":{"id":"resp_1","status":"completed","output":[],"usage":{"input_tokens":8,"output_tokens":2}}}`)
			} else {
				writeWSEvent(t, ctx, conn, `{"type":"response.created","response":{"id":"resp_2","status":"in_progress"}}`)
				writeWSEvent(t, ctx, conn, `{"type":"response.output_item.added","item":{"id":"msg_2","type":"message","role":"assistant","phase":"final_answer","status":"in_progress"},"output_index":0}`)
				writeWSEvent(t, ctx, conn, `{"type":"response.output_text.delta","delta":"second answer","item_id":"msg_2","output_index":0}`)
				writeWSEvent(t, ctx, conn, `{"type":"response.output_item.done","item":{"id":"msg_2","type":"message","role":"assistant","phase":"final_answer","status":"completed","content":[{"type":"output_text","text":"second answer"}]},"output_index":0}`)
				writeWSEvent(t, ctx, conn, `{"type":"response.completed","response":{"id":"resp_2","status":"completed","output":[],"usage":{"input_tokens":1,"output_tokens":1}}}`)
			}
		}
	}))
	defer server.Close()

	store := false
	client, err := New(ClientConfig{
		BaseURL:                 server.URL,
		WireAPI:                 "responses",
		APIKey:                  "test-key",
		ResponsesStore:          &store,
		ResponsesTransport:      providers.StreamTransportAuto,
		ResponsesWebSocketCache: NewResponsesWebSocketCache(),
	})
	if err != nil {
		t.Fatalf("New: %v", err)
	}

	oldContext := providers.ChatMessage{
		Role:    "user",
		Name:    wuucontext.SystemReminderMessageName,
		Content: "<system-reminder>\n# Environment\nState: old\n</system-reminder>",
		Hidden:  true,
	}
	newContext := providers.ChatMessage{
		Role:    "user",
		Name:    wuucontext.SystemReminderMessageName,
		Content: "<system-reminder>\n# Environment\nState: new\n</system-reminder>",
		Hidden:  true,
	}
	cache := &providers.CacheHint{PromptCacheKey: "thread-refresh-hidden-context"}
	ch, err := client.StreamChat(context.Background(), providers.ChatRequest{
		Model:     "gpt-test",
		Messages:  []providers.ChatMessage{{Role: "user", Content: "first"}, oldContext},
		CacheHint: cache,
	})
	if err != nil {
		t.Fatalf("first StreamChat: %v", err)
	}
	if err := drainStream(ch); err != nil {
		t.Fatalf("first stream: %v", err)
	}

	ch, err = client.StreamChat(context.Background(), providers.ChatRequest{
		Model: "gpt-test",
		Messages: []providers.ChatMessage{
			{Role: "user", Content: "first"},
			{
				Role:              "assistant",
				Content:           "first answer",
				Phase:             providers.MessagePhaseFinalAnswer,
				ProviderItemID:    "msg_1",
				ProviderItemModel: "gpt-test",
			},
			{Role: "user", Content: "second"},
			newContext,
		},
		CacheHint: cache,
	})
	if err != nil {
		t.Fatalf("second StreamChat: %v", err)
	}
	if err := drainStream(ch); err != nil {
		t.Fatalf("second stream: %v", err)
	}

	first := <-requests
	if _, exists := first["previous_response_id"]; exists {
		t.Fatalf("first request must be full context: %#v", first)
	}

	second := <-requests
	if second["previous_response_id"] != "resp_1" {
		t.Fatalf("second request previous_response_id = %#v; body=%#v", second["previous_response_id"], second)
	}
	secondInput := second["input"].([]any)
	if len(secondInput) != 2 {
		t.Fatalf("second request should send only new user input and refreshed context, got %#v", secondInput)
	}
	rawSecondInput, err := json.Marshal(secondInput)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(rawSecondInput), "second") || !strings.Contains(string(rawSecondInput), "State: new") {
		t.Fatalf("second delta missing new turn or refreshed context: %s", rawSecondInput)
	}
	if strings.Contains(string(rawSecondInput), "State: old") || strings.Contains(string(rawSecondInput), "first answer") {
		t.Fatalf("second delta should not resend old context or assistant answer: %s", rawSecondInput)
	}
}

func TestResponsesStreamChatWebSocket_AgentLoopPreservesDeltaWithChangingHiddenContext(t *testing.T) {
	requests := make(chan map[string]any, 2)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		conn, err := websocket.Accept(w, r, &websocket.AcceptOptions{InsecureSkipVerify: true})
		if err != nil {
			t.Errorf("accept websocket: %v", err)
			return
		}
		defer conn.Close(websocket.StatusNormalClosure, "")

		ctx, cancel := context.WithTimeout(r.Context(), 5*time.Second)
		defer cancel()
		for i := 0; i < 2; i++ {
			typ, data, err := conn.Read(ctx)
			if err != nil {
				t.Errorf("read request %d: %v", i+1, err)
				return
			}
			if typ != websocket.MessageText {
				t.Errorf("request %d type = %v", i+1, typ)
				return
			}
			var body map[string]any
			if err := json.Unmarshal(data, &body); err != nil {
				t.Errorf("decode request %d: %v", i+1, err)
				return
			}
			requests <- body

			if i == 0 {
				writeWSEvent(t, ctx, conn, `{"type":"response.created","response":{"id":"resp_1","status":"in_progress"}}`)
				writeWSEvent(t, ctx, conn, `{"type":"response.output_item.added","item":{"id":"fc_1","type":"function_call","status":"in_progress","arguments":"","call_id":"call_1","name":"read_file"},"output_index":0}`)
				writeWSEvent(t, ctx, conn, `{"type":"response.function_call_arguments.done","arguments":"{\"path\":\"README.md\"}","item_id":"fc_1","output_index":0}`)
				writeWSEvent(t, ctx, conn, `{"type":"response.output_item.done","item":{"id":"fc_1","type":"function_call","status":"completed","arguments":"{\"path\":\"README.md\"}","call_id":"call_1","name":"read_file"},"output_index":0}`)
				writeWSEvent(t, ctx, conn, `{"type":"response.done","response":{"id":"resp_1","status":"completed","output":[],"usage":{"input_tokens":10,"output_tokens":2}}}`)
			} else {
				writeWSEvent(t, ctx, conn, `{"type":"response.created","response":{"id":"resp_2","status":"in_progress"}}`)
				writeWSEvent(t, ctx, conn, `{"type":"response.output_item.added","item":{"id":"msg_2","type":"message","role":"assistant","phase":"final_answer","status":"in_progress"},"output_index":0}`)
				writeWSEvent(t, ctx, conn, `{"type":"response.output_text.delta","delta":"done","item_id":"msg_2","output_index":0}`)
				writeWSEvent(t, ctx, conn, `{"type":"response.output_item.done","item":{"id":"msg_2","type":"message","role":"assistant","phase":"final_answer","status":"completed","content":[{"type":"output_text","text":"done"}]},"output_index":0}`)
				writeWSEvent(t, ctx, conn, `{"type":"response.completed","response":{"id":"resp_2","status":"completed","output":[],"usage":{"input_tokens":2,"output_tokens":1}}}`)
			}
		}
	}))
	defer server.Close()

	store := false
	client, err := New(ClientConfig{
		BaseURL:                 server.URL,
		WireAPI:                 "responses",
		APIKey:                  "test-key",
		Headers:                 map[string]string{"OpenAI-Beta": "responses=experimental"},
		ResponsesStore:          &store,
		ResponsesTransport:      providers.StreamTransportAuto,
		ResponsesWebSocketCache: NewResponsesWebSocketCache(),
	})
	if err != nil {
		t.Fatalf("New: %v", err)
	}

	contextCalls := 0
	runner := agent.StreamRunner{
		Client:         client,
		Model:          "gpt-test",
		Tools:          &webSocketAgentLoopTools{},
		PromptCacheKey: "thread-agent-hidden-context",
		BeforeRequestContext: func() []agent.ContextSegment {
			contextCalls++
			env := wuucontext.Block{
				Kind:    wuucontext.BlockEnvironment,
				Title:   "Runtime environment",
				Source:  "runtime.snapshot",
				Content: "State: step " + strconv.Itoa(contextCalls),
			}
			return agent.RequestOnlyContextMessages([]providers.ChatMessage{
				hiddenReminderForWebSocketAgentTest(env),
			})
		},
	}
	res, err := runner.RunWithCallback(context.Background(), []providers.ChatMessage{{Role: "user", Content: "read README"}}, nil)
	if err != nil {
		t.Fatalf("RunWithCallback: %v", err)
	}
	if res.Content != "done" {
		t.Fatalf("unexpected final content %q", res.Content)
	}

	first := <-requests
	if _, exists := first["previous_response_id"]; exists {
		t.Fatalf("first request must be full context: %#v", first)
	}
	firstInputRaw, err := json.Marshal(first["input"])
	if err != nil {
		t.Fatal(err)
	}
	firstInput := string(firstInputRaw)
	if !strings.Contains(firstInput, "State: step 1") {
		t.Fatalf("first request missing request-only context: %s", firstInput)
	}

	second := <-requests
	if second["previous_response_id"] != "resp_1" {
		t.Fatalf("second request previous_response_id = %#v; body=%#v", second["previous_response_id"], second)
	}
	secondInputRaw, err := json.Marshal(second["input"])
	if err != nil {
		t.Fatal(err)
	}
	secondInput := string(secondInputRaw)
	if !strings.Contains(secondInput, "function_call_output") {
		t.Fatalf("second delta missing tool result: %s", secondInput)
	}
	if !strings.Contains(secondInput, "State: step 2") {
		t.Fatalf("second delta missing changed environment context: %s", secondInput)
	}
	if strings.Contains(secondInput, "State: step 1") {
		t.Fatalf("second delta re-sent stale request-only context: %s", secondInput)
	}
}

func TestResponsesStreamChatWebSocket_FallsBackToSSEAfterTransportCloseBeforeFirstEvent(t *testing.T) {
	wsRequests := make(chan map[string]any, 2)
	sseRequests := make(chan map[string]any, 1)
	var connections atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !strings.EqualFold(r.Header.Get("Upgrade"), "websocket") {
			var body map[string]any
			if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
				t.Errorf("decode SSE request: %v", err)
				http.Error(w, err.Error(), http.StatusBadRequest)
				return
			}
			sseRequests <- body
			w.Header().Set("Content-Type", "text/event-stream")
			_, _ = w.Write([]byte(`data: {"type":"response.completed","response":{"id":"resp_sse","status":"completed","output":[],"usage":{"input_tokens":9,"output_tokens":1}}}` + "\n\n"))
			return
		}

		conn, err := websocket.Accept(w, r, &websocket.AcceptOptions{InsecureSkipVerify: true})
		if err != nil {
			t.Errorf("accept websocket: %v", err)
			return
		}
		defer conn.Close(websocket.StatusNormalClosure, "")

		ctx, cancel := context.WithTimeout(r.Context(), 5*time.Second)
		defer cancel()
		switch connections.Add(1) {
		case 1:
			readWSRequest(t, ctx, conn, wsRequests)
			writeWSEvent(t, ctx, conn, `{"type":"response.created","response":{"id":"resp_1","status":"in_progress"}}`)
			writeWSEvent(t, ctx, conn, `{"type":"response.output_item.done","item":{"id":"msg_1","type":"message","role":"assistant","phase":"final_answer","status":"completed","content":[{"type":"output_text","text":"first answer"}]},"output_index":0}`)
			writeWSEvent(t, ctx, conn, `{"type":"response.completed","response":{"id":"resp_1","status":"completed","output":[],"usage":{"input_tokens":5,"output_tokens":2}}}`)

			readWSRequest(t, ctx, conn, wsRequests)
			_ = conn.Close(websocket.StatusInternalError, "keepalive ping timeout")
		default:
			t.Errorf("unexpected extra websocket connection")
		}
	}))
	defer server.Close()

	store := false
	client, err := New(ClientConfig{
		BaseURL:                 server.URL,
		WireAPI:                 "responses",
		APIKey:                  "test-key",
		ResponsesStore:          &store,
		ResponsesTransport:      providers.StreamTransportAuto,
		ResponsesWebSocketCache: NewResponsesWebSocketCache(),
	})
	if err != nil {
		t.Fatalf("New: %v", err)
	}

	cache := &providers.CacheHint{PromptCacheKey: "thread-transport-close"}
	ch, err := client.StreamChat(context.Background(), providers.ChatRequest{
		Model:     "gpt-test",
		Messages:  []providers.ChatMessage{{Role: "user", Content: "first"}},
		CacheHint: cache,
	})
	if err != nil {
		t.Fatalf("first StreamChat: %v", err)
	}
	if err := drainStream(ch); err != nil {
		t.Fatalf("first stream: %v", err)
	}

	followUp := []providers.ChatMessage{
		{Role: "user", Content: "first"},
		{
			Role:              "assistant",
			Content:           "first answer",
			Phase:             providers.MessagePhaseFinalAnswer,
			ProviderItemID:    "msg_1",
			ProviderItemModel: "gpt-test",
		},
		{Role: "user", Content: "second"},
	}
	reliable := providers.NewReliableStreamClient(client, nil, providers.WithStreamRetryWait(immediateStreamRetryWait))
	ch, err = reliable.StreamChat(context.Background(), providers.ChatRequest{
		Model:     "gpt-test",
		Messages:  followUp,
		CacheHint: cache,
	})
	if err != nil {
		t.Fatalf("second StreamChat: %v", err)
	}
	secondStates, err := drainStreamProviderStates(ch)
	if err != nil {
		t.Fatalf("second stream should fall back to SSE: %v", err)
	}
	if len(secondStates) != 3 {
		t.Fatalf("second stream should report websocket attempt and SSE fallback, got %+v", secondStates)
	}
	if secondStates[0].Transport != "websocket" ||
		secondStates[0].ReplayMode != "previous_response_id" ||
		!secondStates[0].PreviousResponseIDUsed ||
		secondStates[0].InputItems != 1 ||
		secondStates[0].FullInputItems != 3 ||
		secondStates[0].DeltaInputItems != 1 {
		t.Fatalf("unexpected websocket attempt state: %+v", secondStates[0])
	}
	if secondStates[1].Transport != "websocket" || secondStates[1].Diagnostic != "provider_transport_failure" {
		t.Fatalf("unexpected websocket failure state: %+v", secondStates[1])
	}
	if secondStates[2].Transport != "http" ||
		secondStates[2].ReplayMode != "full_request" ||
		secondStates[2].PreviousResponseIDUsed ||
		secondStates[2].Diagnostic != "provider_transport_failure" ||
		secondStates[2].TransportFailurePhase != "before_message_stream_start" ||
		secondStates[2].FallbackTransport != "http" ||
		secondStates[2].EventsEmitted ||
		!secondStates[2].FallbackActive ||
		secondStates[2].FallbackReason != "websocket_failed_before_first_event" ||
		secondStates[2].InputItems != 3 ||
		secondStates[2].FullInputItems != 3 {
		t.Fatalf("unexpected SSE fallback state: %+v", secondStates[2])
	}

	<-wsRequests
	second := <-wsRequests
	if second["previous_response_id"] != "resp_1" {
		t.Fatalf("second request previous_response_id = %#v; body=%#v", second["previous_response_id"], second)
	}
	fallback := <-sseRequests
	if _, exists := fallback["previous_response_id"]; exists {
		t.Fatalf("SSE fallback must not reuse websocket previous_response_id: %#v", fallback)
	}
	fallbackInput := fallback["input"].([]any)
	if len(fallbackInput) != 3 {
		t.Fatalf("SSE fallback should send the full request input, got %#v", fallbackInput)
	}
	if got := connections.Load(); got != 1 {
		t.Fatalf("unexpected websocket connection count %d", got)
	}
}

func TestResponsesStreamChatWebSocket_BusySessionUsesTransientWebSocket(t *testing.T) {
	requests := make(chan map[string]any, 3)
	sseRequests := make(chan map[string]any, 1)
	firstReady := make(chan struct{})
	firstRelease := make(chan struct{})
	var connections atomic.Int32

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Upgrade") == "" {
			var body map[string]any
			if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
				t.Errorf("decode SSE request: %v", err)
				return
			}
			sseRequests <- body
			w.Header().Set("Content-Type", "text/event-stream")
			_, _ = w.Write([]byte(`data: {"type":"response.completed","response":{"id":"resp_sse","status":"completed","output":[],"usage":{"input_tokens":1,"output_tokens":1}}}` + "\n\n"))
			return
		}

		conn, err := websocket.Accept(w, r, &websocket.AcceptOptions{InsecureSkipVerify: true})
		if err != nil {
			t.Errorf("accept websocket: %v", err)
			return
		}
		defer conn.Close(websocket.StatusNormalClosure, "")

		ctx, cancel := context.WithTimeout(r.Context(), 5*time.Second)
		defer cancel()
		switch connections.Add(1) {
		case 1:
			readWSRequest(t, ctx, conn, requests)
			close(firstReady)
			select {
			case <-firstRelease:
			case <-ctx.Done():
				t.Errorf("waiting to release first websocket: %v", ctx.Err())
				return
			}
			writeWSEvent(t, ctx, conn, `{"type":"response.created","response":{"id":"resp_1","status":"in_progress"}}`)
			writeWSEvent(t, ctx, conn, `{"type":"response.output_item.done","item":{"id":"msg_1","type":"message","role":"assistant","phase":"final_answer","status":"completed","content":[{"type":"output_text","text":"first answer"}]},"output_index":0}`)
			writeWSEvent(t, ctx, conn, `{"type":"response.completed","response":{"id":"resp_1","status":"completed","output":[],"usage":{"input_tokens":5,"output_tokens":2}}}`)
		case 2:
			readWSRequest(t, ctx, conn, requests)
			writeWSEvent(t, ctx, conn, `{"type":"response.created","response":{"id":"resp_2","status":"in_progress"}}`)
			writeWSEvent(t, ctx, conn, `{"type":"response.output_item.done","item":{"id":"msg_2","type":"message","role":"assistant","phase":"final_answer","status":"completed","content":[{"type":"output_text","text":"second answer"}]},"output_index":0}`)
			writeWSEvent(t, ctx, conn, `{"type":"response.completed","response":{"id":"resp_2","status":"completed","output":[],"usage":{"input_tokens":6,"output_tokens":2}}}`)
		default:
			t.Errorf("unexpected extra websocket connection")
		}
	}))
	defer server.Close()

	store := false
	client, err := New(ClientConfig{
		BaseURL:                 server.URL,
		WireAPI:                 "responses",
		APIKey:                  "test-key",
		ResponsesStore:          &store,
		ResponsesTransport:      providers.StreamTransportAuto,
		ResponsesWebSocketCache: NewResponsesWebSocketCache(),
	})
	if err != nil {
		t.Fatalf("New: %v", err)
	}

	cache := &providers.CacheHint{PromptCacheKey: "thread-busy-transient"}
	firstCh, err := client.StreamChat(context.Background(), providers.ChatRequest{
		Model:     "gpt-test",
		Messages:  []providers.ChatMessage{{Role: "user", Content: "first"}},
		CacheHint: cache,
	})
	if err != nil {
		t.Fatalf("first StreamChat: %v", err)
	}
	select {
	case <-firstReady:
	case <-time.After(2 * time.Second):
		t.Fatal("first websocket request did not start")
	}

	secondCh, err := client.StreamChat(context.Background(), providers.ChatRequest{
		Model:     "gpt-test",
		Messages:  []providers.ChatMessage{{Role: "user", Content: "second"}},
		CacheHint: cache,
	})
	if err != nil {
		t.Fatalf("second StreamChat: %v", err)
	}
	secondStates, err := drainStreamProviderStates(secondCh)
	if err != nil {
		t.Fatalf("second stream should use transient websocket: %v", err)
	}
	if len(secondStates) != 1 ||
		secondStates[0].Transport != "websocket" ||
		secondStates[0].ReplayMode != "full_request" ||
		secondStates[0].PreviousResponseIDUsed ||
		secondStates[0].ConnectionReused {
		t.Fatalf("unexpected transient websocket provider state: %+v", secondStates)
	}
	select {
	case body := <-sseRequests:
		t.Fatalf("busy websocket should not fall back to SSE, got request %#v", body)
	default:
	}
	close(firstRelease)
	if err := drainStream(firstCh); err != nil {
		t.Fatalf("first stream: %v", err)
	}
	if got := connections.Load(); got != 2 {
		t.Fatalf("websocket connections = %d, want 2", got)
	}
	<-requests
	secondReq := <-requests
	if _, exists := secondReq["previous_response_id"]; exists {
		t.Fatalf("transient websocket request must not use cached previous_response_id: %#v", secondReq)
	}
}

func TestResponsesStreamChatWebSocket_SwitchesToSSEOnConnectionLimit(t *testing.T) {
	requests := make(chan map[string]any, 2)
	sseRequests := make(chan map[string]any, 1)
	var connections atomic.Int32

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Upgrade") == "" {
			var body map[string]any
			if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
				t.Errorf("decode SSE request: %v", err)
				return
			}
			sseRequests <- body
			w.Header().Set("Content-Type", "text/event-stream")
			_, _ = w.Write([]byte(`data: {"type":"response.completed","response":{"id":"resp_sse","status":"completed","output":[],"usage":{"input_tokens":1,"output_tokens":1}}}` + "\n\n"))
			return
		}

		conn, err := websocket.Accept(w, r, &websocket.AcceptOptions{InsecureSkipVerify: true})
		if err != nil {
			t.Errorf("accept websocket: %v", err)
			return
		}
		defer conn.Close(websocket.StatusNormalClosure, "")

		ctx, cancel := context.WithTimeout(r.Context(), 5*time.Second)
		defer cancel()
		readWSRequest(t, ctx, conn, requests)
		switch connections.Add(1) {
		case 1:
			writeWSEvent(t, ctx, conn, `{"type":"error","error":{"code":"websocket_connection_limit_reached","message":"try again"}}`)
		default:
			t.Errorf("unexpected extra websocket connection")
		}
	}))
	defer server.Close()

	store := false
	client, err := New(ClientConfig{
		BaseURL:                 server.URL,
		WireAPI:                 "responses",
		APIKey:                  "test-key",
		ResponsesStore:          &store,
		ResponsesTransport:      providers.StreamTransportAuto,
		ResponsesWebSocketCache: NewResponsesWebSocketCache(),
	})
	if err != nil {
		t.Fatalf("New: %v", err)
	}

	req, err := providers.EnsureInferenceExecutionContext(context.Background(), providers.ChatRequest{
		Model:     "gpt-test",
		Messages:  []providers.ChatMessage{{Role: "user", Content: "hello"}},
		CacheHint: &providers.CacheHint{PromptCacheKey: "thread-connection-limit"},
	}, providers.InferenceOperationAgentRound, providers.InferenceProfileInteractive)
	if err != nil {
		t.Fatal(err)
	}
	reliable := providers.NewReliableStreamClient(client, nil, providers.WithStreamRetryWait(immediateStreamRetryWait))
	ch, err := reliable.StreamChat(context.Background(), req)
	if err != nil {
		t.Fatalf("StreamChat: %v", err)
	}
	states, err := drainStreamProviderStates(ch)
	if err != nil {
		t.Fatalf("stream should switch transport after websocket connection limit: %v", err)
	}
	if got := connections.Load(); got != 1 {
		t.Fatalf("websocket connections = %d, want 1", got)
	}
	if len(states) != 3 ||
		states[0].Transport != "websocket" ||
		states[1].Transport != "websocket" ||
		states[1].Diagnostic != "provider_transport_failure" ||
		states[2].Transport != "http" ||
		!states[2].FallbackActive ||
		states[2].FallbackReason != responsesWebSocketConnectionLimitCode {
		t.Fatalf("unexpected provider states after transport switch: %+v", states)
	}
	select {
	case body := <-sseRequests:
		if _, exists := body["previous_response_id"]; exists {
			t.Fatalf("SSE fallback must send full payload: %#v", body)
		}
	default:
		t.Fatal("expected SSE fallback request")
	}
	firstReq := <-requests
	if _, exists := firstReq["previous_response_id"]; exists {
		t.Fatalf("first request must be full payload: %#v", firstReq)
	}
	ledger := req.Execution.Snapshot()
	if ledger.Attempts != 2 || len(ledger.Submissions) != 2 {
		t.Fatalf("inference ledger = %+v, want two attempts and two submissions", ledger)
	}
	if ledger.Submissions[0].Transport != "websocket" || ledger.Submissions[1].Transport != "http" || ledger.Submissions[1].Reason != responsesWebSocketConnectionLimitCode {
		t.Fatalf("unexpected transport switch submissions: %+v", ledger.Submissions)
	}
}

func TestResponsesStreamChatWebSocket_WaitsForSlowConsumerWithoutDroppingFrames(t *testing.T) {
	const deltaCount = 256
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		conn, err := websocket.Accept(w, r, &websocket.AcceptOptions{InsecureSkipVerify: true})
		if err != nil {
			t.Errorf("accept websocket: %v", err)
			return
		}
		defer conn.Close(websocket.StatusNormalClosure, "")

		ctx, cancel := context.WithTimeout(r.Context(), 5*time.Second)
		defer cancel()
		requests := make(chan map[string]any, 1)
		readWSRequest(t, ctx, conn, requests)
		for i := 0; i < deltaCount; i++ {
			writeWSEvent(t, ctx, conn, `{"type":"response.output_text.delta","delta":"x","item_id":"msg_1"}`)
		}
		writeWSEvent(t, ctx, conn, `{"type":"response.completed","response":{"id":"resp_slow","status":"completed","output":[],"usage":{"input_tokens":1,"output_tokens":256}}}`)
	}))
	defer server.Close()

	store := false
	client, err := New(ClientConfig{
		BaseURL:                 server.URL,
		WireAPI:                 "responses",
		APIKey:                  "test-key",
		ResponsesStore:          &store,
		ResponsesTransport:      providers.StreamTransportWebSocket,
		ResponsesWebSocketCache: NewResponsesWebSocketCache(),
	})
	if err != nil {
		t.Fatalf("New: %v", err)
	}

	stream, err := client.StreamChat(context.Background(), providers.ChatRequest{
		Model:     "gpt-test",
		Messages:  []providers.ChatMessage{{Role: "user", Content: "hello"}},
		CacheHint: &providers.CacheHint{PromptCacheKey: "thread-slow-consumer"},
	})
	if err != nil {
		t.Fatalf("StreamChat: %v", err)
	}

	// Let both bounded stages fill before consuming. A full queue must apply
	// transport backpressure instead of turning local scheduling into a stream
	// failure.
	time.Sleep(100 * time.Millisecond)
	var deltas int
	var sawDone bool
	for event := range stream {
		switch event.Type {
		case providers.EventContentDelta:
			if event.Content != "" {
				deltas++
			}
		case providers.EventDone:
			sawDone = true
		case providers.EventError:
			t.Fatalf("slow consumer failed stream: %v", event.Error)
		}
	}
	if deltas != deltaCount || !sawDone {
		t.Fatalf("stream deltas/done = %d/%v, want %d/true", deltas, sawDone, deltaCount)
	}
}

func TestResponsesStreamChatWebSocket_CancelUnblocksSaturatedReader(t *testing.T) {
	serverDone := make(chan struct{})
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		defer close(serverDone)
		conn, err := websocket.Accept(w, r, &websocket.AcceptOptions{InsecureSkipVerify: true})
		if err != nil {
			t.Errorf("accept websocket: %v", err)
			return
		}
		defer conn.Close(websocket.StatusNormalClosure, "")

		ctx, cancel := context.WithTimeout(r.Context(), 5*time.Second)
		defer cancel()
		requests := make(chan map[string]any, 1)
		readWSRequest(t, ctx, conn, requests)
		frame := []byte(`{"type":"response.output_text.delta","delta":"x","item_id":"msg_1"}`)
		for {
			if err := conn.Write(ctx, websocket.MessageText, frame); err != nil {
				return
			}
		}
	}))
	defer server.Close()

	store := false
	client, err := New(ClientConfig{
		BaseURL:                 server.URL,
		WireAPI:                 "responses",
		APIKey:                  "test-key",
		ResponsesStore:          &store,
		ResponsesTransport:      providers.StreamTransportWebSocket,
		ResponsesWebSocketCache: NewResponsesWebSocketCache(),
	})
	if err != nil {
		t.Fatalf("New: %v", err)
	}

	ctx, cancel := context.WithCancel(context.Background())
	stream, err := client.StreamChat(ctx, providers.ChatRequest{
		Model:     "gpt-test",
		Messages:  []providers.ChatMessage{{Role: "user", Content: "hello"}},
		CacheHint: &providers.CacheHint{PromptCacheKey: "thread-cancel-saturated"},
	})
	if err != nil {
		t.Fatalf("StreamChat: %v", err)
	}
	time.Sleep(100 * time.Millisecond)
	cancel()

	drained := make(chan struct{})
	go func() {
		for range stream {
		}
		close(drained)
	}()
	select {
	case <-drained:
	case <-time.After(2 * time.Second):
		t.Fatal("cancel did not close the saturated stream")
	}
	select {
	case <-serverDone:
	case <-time.After(2 * time.Second):
		t.Fatal("cancel did not close the saturated websocket")
	}
}

func TestResponsesStreamChatWebSocket_PersistsSSEFallbackAfterConnectionLimit(t *testing.T) {
	requests := make(chan map[string]any, 3)
	sseRequests := make(chan map[string]any, 2)
	var connections atomic.Int32
	var sseResponses atomic.Int32

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Upgrade") == "" {
			var body map[string]any
			if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
				t.Errorf("decode SSE request: %v", err)
				return
			}
			sseRequests <- body
			responseID := fmt.Sprintf("resp_sse_%d", sseResponses.Add(1))
			w.Header().Set("Content-Type", "text/event-stream")
			_, _ = w.Write([]byte(`data: {"type":"response.completed","response":{"id":"` + responseID + `","status":"completed","output":[],"usage":{"input_tokens":1,"output_tokens":1}}}` + "\n\n"))
			return
		}

		conn, err := websocket.Accept(w, r, &websocket.AcceptOptions{InsecureSkipVerify: true})
		if err != nil {
			t.Errorf("accept websocket: %v", err)
			return
		}
		defer conn.Close(websocket.StatusNormalClosure, "")

		ctx, cancel := context.WithTimeout(r.Context(), 5*time.Second)
		defer cancel()
		readWSRequest(t, ctx, conn, requests)
		switch connections.Add(1) {
		case 1:
			writeWSEvent(t, ctx, conn, `{"type":"error","error":{"code":"websocket_connection_limit_reached","message":"try again"}}`)
		default:
			t.Errorf("unexpected extra websocket connection")
		}
	}))
	defer server.Close()

	store := false
	client, err := New(ClientConfig{
		BaseURL:                 server.URL,
		WireAPI:                 "responses",
		APIKey:                  "test-key",
		ResponsesStore:          &store,
		ResponsesTransport:      providers.StreamTransportAuto,
		ResponsesWebSocketCache: NewResponsesWebSocketCache(),
	})
	if err != nil {
		t.Fatalf("New: %v", err)
	}

	cache := &providers.CacheHint{PromptCacheKey: "thread-connection-limit-fallback"}
	req, err := providers.EnsureInferenceExecutionContext(context.Background(), providers.ChatRequest{
		Model:     "gpt-test",
		Messages:  []providers.ChatMessage{{Role: "user", Content: "hello"}},
		CacheHint: cache,
	}, providers.InferenceOperationAgentRound, providers.InferenceProfileInteractive)
	if err != nil {
		t.Fatal(err)
	}
	reliable := providers.NewReliableStreamClient(client, nil, providers.WithStreamRetryWait(immediateStreamRetryWait))
	ch, err := reliable.StreamChat(context.Background(), req)
	if err != nil {
		t.Fatalf("StreamChat: %v", err)
	}
	states, err := drainStreamProviderStates(ch)
	if err != nil {
		t.Fatalf("stream should fall back to SSE after websocket limit: %v", err)
	}
	if got := connections.Load(); got != 1 {
		t.Fatalf("websocket connections = %d, want 1", got)
	}
	if len(states) != 3 ||
		states[0].Transport != "websocket" ||
		states[1].Transport != "websocket" ||
		states[1].Diagnostic != "provider_transport_failure" ||
		states[1].FallbackPinStatus != responsesWebSocketFallbackPinCreated ||
		states[1].FallbackTTLMS != responsesWebSocketPressureFallbackTTL.Milliseconds() ||
		states[1].FallbackRetryAfterMS <= 0 ||
		states[2].Transport != "http" ||
		states[2].Diagnostic != "provider_transport_failure" ||
		states[2].TransportFailurePhase != "before_message_stream_start" ||
		states[2].FallbackTransport != "http" ||
		states[2].EventsEmitted ||
		!states[2].FallbackActive ||
		states[2].FallbackReason != "websocket_connection_limit_reached" ||
		states[2].FallbackPinStatus != responsesWebSocketFallbackPinReused ||
		states[2].FallbackTTLMS != responsesWebSocketPressureFallbackTTL.Milliseconds() ||
		states[2].FallbackRetryAfterMS <= 0 {
		t.Fatalf("unexpected provider states after SSE fallback: %+v", states)
	}
	firstFallbackReq := <-sseRequests
	if _, exists := firstFallbackReq["previous_response_id"]; exists {
		t.Fatalf("SSE fallback must send full payload: %#v", firstFallbackReq)
	}
	ledger := req.Execution.Snapshot()
	if ledger.Attempts != 2 || len(ledger.Submissions) != 2 {
		t.Fatalf("inference ledger = %+v, want websocket plus SSE on separate attempts", ledger)
	}
	if ledger.Submissions[1].Transport != "http" || ledger.Submissions[1].Mode != "fallback" || ledger.Submissions[1].Reason != responsesWebSocketConnectionLimitCode {
		t.Fatalf("unexpected SSE fallback submission: %+v", ledger.Submissions[1])
	}

	laterCh, err := client.StreamChat(context.Background(), providers.ChatRequest{
		Model:     "gpt-test",
		Messages:  []providers.ChatMessage{{Role: "user", Content: "later"}},
		CacheHint: cache,
	})
	if err != nil {
		t.Fatalf("later StreamChat: %v", err)
	}
	laterStates, err := drainStreamProviderStates(laterCh)
	if err != nil {
		t.Fatalf("later stream should stay on SSE: %v", err)
	}
	if got := connections.Load(); got != 1 {
		t.Fatalf("later stream should not open websocket, got %d connections", got)
	}
	if len(laterStates) != 1 ||
		laterStates[0].Transport != "http" ||
		!laterStates[0].FallbackActive ||
		laterStates[0].FallbackReason != "websocket_connection_limit_reached" ||
		laterStates[0].FallbackPinStatus != responsesWebSocketFallbackPinReused ||
		laterStates[0].FallbackTTLMS != responsesWebSocketPressureFallbackTTL.Milliseconds() ||
		laterStates[0].FallbackRetryAfterMS <= 0 {
		t.Fatalf("unexpected later provider states: %+v", laterStates)
	}
	laterFallbackReq := <-sseRequests
	if _, exists := laterFallbackReq["previous_response_id"]; exists {
		t.Fatalf("later SSE request must send full payload: %#v", laterFallbackReq)
	}
}

func TestResponsesStreamChatWebSocket_DoesNotAutoRetryAfterProviderEvent(t *testing.T) {
	requests := make(chan map[string]any, 2)
	sseRequests := make(chan map[string]any, 1)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Upgrade") == "" {
			var body map[string]any
			if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
				t.Errorf("decode SSE fallback request: %v", err)
				return
			}
			sseRequests <- body
			w.Header().Set("Content-Type", "text/event-stream")
			_, _ = w.Write([]byte(`data: {"type":"response.completed","response":{"id":"resp_sse_after_failure","status":"completed","output":[],"usage":{"input_tokens":11,"output_tokens":1}}}` + "\n\n"))
			return
		}

		conn, err := websocket.Accept(w, r, &websocket.AcceptOptions{InsecureSkipVerify: true})
		if err != nil {
			t.Errorf("accept websocket: %v", err)
			return
		}
		defer conn.Close(websocket.StatusNormalClosure, "")

		ctx, cancel := context.WithTimeout(r.Context(), 5*time.Second)
		defer cancel()
		readWSRequest(t, ctx, conn, requests)
		writeWSEvent(t, ctx, conn, `{"type":"response.created","response":{"id":"resp_1","status":"in_progress"}}`)
		writeWSEvent(t, ctx, conn, `{"type":"response.output_item.done","item":{"id":"msg_1","type":"message","role":"assistant","phase":"final_answer","status":"completed","content":[{"type":"output_text","text":"first answer"}]},"output_index":0}`)
		writeWSEvent(t, ctx, conn, `{"type":"response.completed","response":{"id":"resp_1","status":"completed","output":[],"usage":{"input_tokens":5,"output_tokens":2}}}`)

		readWSRequest(t, ctx, conn, requests)
		writeWSEvent(t, ctx, conn, `{"type":"response.created","response":{"id":"resp_2","status":"in_progress"}}`)
		_ = conn.Close(websocket.StatusInternalError, "closed after start")
	}))
	defer server.Close()

	store := false
	client, err := New(ClientConfig{
		BaseURL:                 server.URL,
		WireAPI:                 "responses",
		APIKey:                  "test-key",
		ResponsesStore:          &store,
		ResponsesTransport:      providers.StreamTransportAuto,
		ResponsesWebSocketCache: NewResponsesWebSocketCache(),
	})
	if err != nil {
		t.Fatalf("New: %v", err)
	}

	cache := &providers.CacheHint{PromptCacheKey: "thread-after-provider-event"}
	ch, err := client.StreamChat(context.Background(), providers.ChatRequest{
		Model:     "gpt-test",
		Messages:  []providers.ChatMessage{{Role: "user", Content: "first"}},
		CacheHint: cache,
	})
	if err != nil {
		t.Fatalf("first StreamChat: %v", err)
	}
	if err := drainStream(ch); err != nil {
		t.Fatalf("first stream: %v", err)
	}

	ch, err = client.StreamChat(context.Background(), providers.ChatRequest{
		Model: "gpt-test",
		Messages: []providers.ChatMessage{
			{Role: "user", Content: "first"},
			{
				Role:              "assistant",
				Content:           "first answer",
				Phase:             providers.MessagePhaseFinalAnswer,
				ProviderItemID:    "msg_1",
				ProviderItemModel: "gpt-test",
			},
			{Role: "user", Content: "second"},
		},
		CacheHint: cache,
	})
	if err != nil {
		t.Fatalf("second StreamChat: %v", err)
	}
	states, streamErr := drainStreamProviderStates(ch)
	if streamErr == nil {
		t.Fatal("second stream should fail after provider event")
	}
	if !providers.IsRetryable(streamErr) {
		t.Fatalf("stream error after provider event should be retryable: %v", streamErr)
	}
	if len(states) != 2 ||
		states[0].Transport != "websocket" ||
		states[0].ReplayMode != "previous_response_id" ||
		!states[0].PreviousResponseIDUsed {
		t.Fatalf("unexpected second provider state: %+v", states)
	}
	if states[1].Transport != "websocket" ||
		states[1].ReplayMode != "previous_response_id" ||
		!states[1].PreviousResponseIDUsed ||
		states[1].Diagnostic != "provider_transport_failure" ||
		states[1].TransportFailurePhase != "after_message_stream_start" ||
		states[1].FallbackTransport != "" ||
		!states[1].EventsEmitted ||
		!states[1].FallbackActive ||
		states[1].FallbackReason != "stream_error_after_provider_event" {
		t.Fatalf("unexpected second diagnostic provider state: %+v", states)
	}

	ch, err = client.StreamChat(context.Background(), providers.ChatRequest{
		Model: "gpt-test",
		Messages: []providers.ChatMessage{
			{Role: "user", Content: "first"},
			{
				Role:              "assistant",
				Content:           "first answer",
				Phase:             providers.MessagePhaseFinalAnswer,
				ProviderItemID:    "msg_1",
				ProviderItemModel: "gpt-test",
			},
			{Role: "user", Content: "second"},
			{Role: "user", Content: "third"},
		},
		CacheHint: cache,
	})
	if err != nil {
		t.Fatalf("third StreamChat: %v", err)
	}
	thirdStates, err := drainStreamProviderStates(ch)
	if err != nil {
		t.Fatalf("third stream should use SSE fallback: %v", err)
	}
	if len(thirdStates) != 1 ||
		thirdStates[0].Transport != "http" ||
		thirdStates[0].ReplayMode != "full_request" ||
		thirdStates[0].Diagnostic != "provider_transport_failure" ||
		thirdStates[0].TransportFailurePhase != "after_message_stream_start" ||
		thirdStates[0].FallbackTransport != "http" ||
		!thirdStates[0].EventsEmitted ||
		!thirdStates[0].FallbackActive ||
		thirdStates[0].FallbackReason != "stream_error_after_provider_event" ||
		thirdStates[0].PreviousResponseIDUsed ||
		thirdStates[0].InputItems != 4 ||
		thirdStates[0].FullInputItems != 4 {
		t.Fatalf("unexpected third provider state: %+v", thirdStates)
	}
	fallback := <-sseRequests
	if _, exists := fallback["previous_response_id"]; exists {
		t.Fatalf("SSE fallback must not reuse websocket previous_response_id: %#v", fallback)
	}
	fallbackInput := fallback["input"].([]any)
	if len(fallbackInput) != 4 {
		t.Fatalf("SSE fallback should send full request input, got %#v", fallbackInput)
	}
}

type webSocketAgentLoopTools struct{}

func (t *webSocketAgentLoopTools) Definitions() []providers.ToolDefinition {
	return []providers.ToolDefinition{{
		Name:        "read_file",
		Description: "read file",
		InputSchema: map[string]any{"type": "object"},
	}}
}

func (t *webSocketAgentLoopTools) Execute(_ context.Context, call providers.ToolCall) (string, error) {
	return "contents", nil
}

func hiddenReminderForWebSocketAgentTest(block wuucontext.Block) providers.ChatMessage {
	return providers.ChatMessage{
		Role:    "user",
		Name:    wuucontext.SystemReminderBlockMessageName(block, 0),
		Content: wuucontext.FormatSystemReminderBlocks(block),
		Hidden:  true,
	}
}

func writeWSEvent(t *testing.T, ctx context.Context, conn *websocket.Conn, data string) {
	t.Helper()
	if err := conn.Write(ctx, websocket.MessageText, []byte(data)); err != nil {
		t.Fatalf("write websocket event: %v", err)
	}
}

func readWSRequest(t *testing.T, ctx context.Context, conn *websocket.Conn, requests chan<- map[string]any) {
	t.Helper()
	typ, data, err := conn.Read(ctx)
	if err != nil {
		t.Fatalf("read websocket request: %v", err)
	}
	if typ != websocket.MessageText {
		t.Fatalf("websocket request type = %v", typ)
	}
	var body map[string]any
	if err := json.Unmarshal(data, &body); err != nil {
		t.Fatalf("decode websocket request: %v", err)
	}
	requests <- body
}

func drainStream(ch <-chan providers.StreamEvent) error {
	for ev := range ch {
		if ev.Type == providers.EventError {
			if ev.Error != nil {
				return ev.Error
			}
			return context.Canceled
		}
	}
	return nil
}

func drainStreamProviderStates(ch <-chan providers.StreamEvent) ([]providers.ProviderStateSummary, error) {
	var states []providers.ProviderStateSummary
	for ev := range ch {
		if ev.Type == providers.EventProviderState && ev.ProviderState != nil {
			states = append(states, *ev.ProviderState)
			continue
		}
		if ev.Type == providers.EventError {
			if ev.Error != nil {
				return states, ev.Error
			}
			return states, context.Canceled
		}
	}
	return states, nil
}

func TestResponsesStreamChatWebSocket_IdleWatchdogAbortsSilentStream(t *testing.T) {
	connClosed := make(chan struct{})
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		conn, err := websocket.Accept(w, r, &websocket.AcceptOptions{InsecureSkipVerify: true})
		if err != nil {
			t.Errorf("accept websocket: %v", err)
			return
		}
		defer close(connClosed)
		defer conn.CloseNow()

		ctx, cancel := context.WithTimeout(r.Context(), 10*time.Second)
		defer cancel()
		readWSRequest(t, ctx, conn, make(chan map[string]any, 1))
		writeWSEvent(t, ctx, conn, `{"type":"response.created","response":{"id":"resp_1","status":"in_progress"}}`)
		// Go silent: never send another frame, never close. The client-side
		// idle watchdog must abort; without it this stream hangs forever.
		// Keep reading only so the server observes the close frame immediately;
		// waiting on the handler context would turn a 150 ms watchdog check into
		// a fixed 10 second test.
		_, _, _ = conn.Read(ctx)
	}))
	defer server.Close()

	client, err := New(ClientConfig{
		BaseURL:            server.URL,
		WireAPI:            "responses",
		APIKey:             "test-key",
		ResponsesTransport: providers.StreamTransportWebSocket,
		StreamConfig:       &providers.StreamTransportConfig{IdleTimeout: 150 * time.Millisecond},
	})
	if err != nil {
		t.Fatalf("New: %v", err)
	}

	ch, err := client.StreamChat(context.Background(), providers.ChatRequest{
		Model:     "gpt-test",
		Messages:  []providers.ChatMessage{{Role: "user", Content: "hello"}},
		CacheHint: &providers.CacheHint{PromptCacheKey: "thread-idle-watchdog"},
	})
	if err != nil {
		t.Fatalf("StreamChat: %v", err)
	}

	type outcome struct {
		events []providers.StreamEvent
	}
	done := make(chan outcome, 1)
	go func() {
		var events []providers.StreamEvent
		for ev := range ch {
			events = append(events, ev)
		}
		done <- outcome{events: events}
	}()

	select {
	case result := <-done:
		if len(result.events) == 0 {
			t.Fatal("no events received")
		}
		final := result.events[len(result.events)-1]
		if final.Type != providers.EventError || final.Error == nil {
			t.Fatalf("final event = %+v, want EventError from idle watchdog", final)
		}
		if !strings.Contains(final.Error.Error(), "idle timeout") {
			t.Fatalf("error = %v, want websocket idle timeout", final.Error)
		}
	case <-time.After(10 * time.Second):
		t.Fatal("websocket stream hung on a silent connection despite idle watchdog")
	}

	select {
	case <-connClosed:
	case <-time.After(5 * time.Second):
		t.Fatal("dead connection was not closed after watchdog fired")
	}
}

func TestResponsesStreamChatWebSocket_BoundsTailAfterCompletedFinalAnswer(t *testing.T) {
	connClosed := make(chan struct{})
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		conn, err := websocket.Accept(w, r, &websocket.AcceptOptions{InsecureSkipVerify: true})
		if err != nil {
			t.Errorf("accept websocket: %v", err)
			return
		}
		defer close(connClosed)
		defer conn.CloseNow()

		ctx, cancel := context.WithTimeout(r.Context(), 10*time.Second)
		defer cancel()
		readWSRequest(t, ctx, conn, make(chan map[string]any, 1))
		writeWSEvent(t, ctx, conn, `{"type":"response.created","response":{"id":"resp_1","status":"in_progress"}}`)
		writeWSEvent(t, ctx, conn, `{"type":"response.output_item.added","item":{"id":"msg_1","type":"message","role":"assistant","phase":"final_answer","status":"in_progress"},"output_index":0}`)
		writeWSEvent(t, ctx, conn, `{"type":"response.output_text.delta","delta":"done","item_id":"msg_1","output_index":0}`)
		writeWSEvent(t, ctx, conn, `{"type":"response.output_item.done","item":{"id":"msg_1","type":"message","role":"assistant","phase":"final_answer","status":"completed","content":[{"type":"output_text","text":"done"}]},"output_index":0}`)
		// Reproduce a provider that never sends response.completed after the final
		// answer item itself is explicitly complete.
		_, _, _ = conn.Read(ctx)
	}))
	defer server.Close()

	client, err := New(ClientConfig{
		BaseURL:            server.URL,
		WireAPI:            "responses",
		APIKey:             "test-key",
		ResponsesTransport: providers.StreamTransportWebSocket,
		StreamConfig:       &providers.StreamTransportConfig{IdleTimeout: 100 * time.Millisecond},
	})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	ch, err := client.StreamChat(ctx, providers.ChatRequest{
		Model:     "gpt-test",
		Messages:  []providers.ChatMessage{{Role: "user", Content: "hello"}},
		CacheHint: &providers.CacheHint{PromptCacheKey: "thread-final-answer-tail"},
	})
	if err != nil {
		t.Fatalf("StreamChat: %v", err)
	}
	started := time.Now()
	var events []providers.StreamEvent
	for event := range ch {
		events = append(events, event)
	}
	if elapsed := time.Since(started); elapsed >= time.Second {
		t.Fatalf("final-answer tail took %s, want less than 1s", elapsed)
	}
	if len(events) == 0 {
		t.Fatal("no events received")
	}
	final := events[len(events)-1]
	if final.Type != providers.EventDone || final.FinishReason != providers.FinishReasonStop || final.StopReason != "completed" || final.ProviderEventType != "response.output_item.done" {
		t.Fatalf("final event = %+v, want inferred completed EventDone", final)
	}
	select {
	case <-connClosed:
	case <-time.After(5 * time.Second):
		t.Fatal("websocket connection was not closed after final-answer tail grace")
	}
}

func TestResponsesWebSocketFallbackPinExpires(t *testing.T) {
	client, err := New(ClientConfig{
		BaseURL:            "https://example.test/v1",
		WireAPI:            "responses",
		APIKey:             "test-key",
		ResponsesTransport: providers.StreamTransportWebSocket,
	})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	cache := client.responsesWSCache
	session := cache.session("thread-pin")

	created := client.responsesWebSocketMarkFallback(session, "websocket_setup_failed", errors.New("temporary dial failure"))
	if created.pinStatus != responsesWebSocketFallbackPinCreated ||
		created.ttl != responsesWebSocketTransientFallbackTTL ||
		created.retryAfter <= 0 {
		t.Fatalf("created pin telemetry = %+v", created)
	}

	session.mu.Lock()
	now := time.Now()
	if !session.fallbackActiveLocked(now) {
		session.mu.Unlock()
		t.Fatal("fresh fallback pin must be active")
	}
	reused := session.responsesWebSocketFallbackMetaLocked(now, responsesWebSocketFallbackPinReused)
	state := &providers.ProviderStateSummary{}
	applyResponsesWebSocketFallbackMeta(state, reused)
	if state.FallbackPinStatus != responsesWebSocketFallbackPinReused ||
		state.FallbackTTLMS != responsesWebSocketTransientFallbackTTL.Milliseconds() ||
		state.FallbackRetryAfterMS <= 0 {
		session.mu.Unlock()
		t.Fatalf("reused pin telemetry = %+v", state)
	}
	if session.fallback.until.IsZero() {
		session.mu.Unlock()
		t.Fatal("fallback pin must carry an expiry")
	}
	// Simulate the pin lapsing: the next check clears it so the websocket is
	// retried instead of the session being degraded for the process lifetime.
	session.fallback.until = time.Now().Add(-time.Second)
	if session.fallbackActiveLocked(time.Now()) {
		session.mu.Unlock()
		t.Fatal("expired fallback pin must clear")
	}
	if session.fallback.active {
		session.mu.Unlock()
		t.Fatal("expired pin state must be reset")
	}
	session.mu.Unlock()

	// The reclaim timer expires the entry once the pin lapses.
	cache.expireIdleSession("thread-pin", session)
	cache.mu.Lock()
	_, exists := cache.sessions["thread-pin"]
	cache.mu.Unlock()
	if exists {
		t.Fatal("expired fallback session entry must be reclaimed")
	}
}

func TestResponsesWebSocketFallbackPinReuseKeepsExpiryTimer(t *testing.T) {
	client, err := New(ClientConfig{
		BaseURL:            "https://example.test/v1",
		WireAPI:            "responses",
		APIKey:             "test-key",
		ResponsesTransport: providers.StreamTransportWebSocket,
	})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	cache := client.responsesWSCache
	session := cache.session("thread-pin-reuse")
	session.mu.Lock()
	session.fallback = responsesWebSocketFallbackState{
		active: true,
		reason: "websocket_setup_failed",
		until:  time.Now().Add(40 * time.Millisecond),
		ttl:    40 * time.Millisecond,
	}
	session.idleTimer = time.AfterFunc(40*time.Millisecond, func() {
		cache.expireIdleSession("thread-pin-reuse", session)
	})
	session.mu.Unlock()

	_, err = client.responsesStreamChatWebSocketAttempt(context.Background(), responsesRequest{
		Model:          "gpt-test",
		PromptCacheKey: "thread-pin-reuse",
	}, providers.StreamTransportWebSocket)
	var fallbackErr *responsesWebSocketFallbackError
	if !errors.As(err, &fallbackErr) || fallbackErr.fallback.pinStatus != responsesWebSocketFallbackPinReused {
		t.Fatalf("fallback error = %#v, want reused pin", err)
	}

	deadline := time.Now().Add(time.Second)
	for {
		cache.mu.Lock()
		_, exists := cache.sessions["thread-pin-reuse"]
		cache.mu.Unlock()
		if !exists {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("reused fallback pin did not expire from the session cache")
		}
		time.Sleep(10 * time.Millisecond)
	}
}

func TestResponsesWebSocketFallbackPinKeepsStrongestExpiry(t *testing.T) {
	client, err := New(ClientConfig{
		BaseURL:            "https://example.test/v1",
		WireAPI:            "responses",
		APIKey:             "test-key",
		ResponsesTransport: providers.StreamTransportWebSocket,
	})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	session := client.responsesWSCache.session("thread-pin-strength")
	t.Cleanup(func() {
		session.mu.Lock()
		client.responsesWebSocketCancelIdleTimerLocked(session)
		session.mu.Unlock()
	})

	created := client.responsesWebSocketMarkFallback(session, "websocket_setup_failed", &CodexWebSocketDialError{
		StatusCode: http.StatusUnauthorized,
		Err:        errors.New("unauthorized"),
	})
	if created.pinStatus != responsesWebSocketFallbackPinCreated || created.ttl != responsesWebSocketLongFallbackTTL {
		t.Fatalf("long pin metadata = %+v", created)
	}
	session.mu.Lock()
	longUntil := session.fallback.until
	session.mu.Unlock()

	reused := client.responsesWebSocketMarkFallback(session, "websocket_write_failed", errors.New("broken pipe"))
	if reused.pinStatus != responsesWebSocketFallbackPinReused || reused.ttl != responsesWebSocketLongFallbackTTL {
		t.Fatalf("weaker pin metadata = %+v", reused)
	}
	session.mu.Lock()
	if session.fallback.reason != "websocket_setup_failed" || session.fallback.ttl != responsesWebSocketLongFallbackTTL || session.fallback.until != longUntil {
		t.Fatalf("weaker failure replaced stronger pin: %+v", session.fallback)
	}
	session.mu.Unlock()

	extendedSession := client.responsesWSCache.session("thread-pin-extended")
	t.Cleanup(func() {
		extendedSession.mu.Lock()
		client.responsesWebSocketCancelIdleTimerLocked(extendedSession)
		extendedSession.mu.Unlock()
	})
	client.responsesWebSocketMarkFallback(extendedSession, "websocket_write_failed", errors.New("broken pipe"))
	extended := client.responsesWebSocketMarkFallback(extendedSession, responsesWebSocketConnectionLimitCode, errors.New("limit"))
	if extended.pinStatus != responsesWebSocketFallbackPinExtended || extended.ttl != responsesWebSocketPressureFallbackTTL {
		t.Fatalf("extended pin metadata = %+v", extended)
	}
}

func TestResponsesOutputItemReplayInput_MessageOmitsStatus(t *testing.T) {
	item := responsesOutputItem{
		ID:      "msg_1",
		Type:    "message",
		Phase:   "final_answer",
		Content: json.RawMessage(`[{"type":"output_text","text":"done"}]`),
	}
	replayed, ok := responsesOutputItemReplayInput(item)
	if !ok {
		t.Fatalf("expected message item to replay")
	}
	if replayed.Status != "" {
		t.Fatalf("message replay must not set status: %+v", replayed)
	}
}

func TestResponsesOutputItemReplayInput_ReasoningStripsStatus(t *testing.T) {
	item := responsesOutputItem{
		Raw:  json.RawMessage(`{"id":"rs_1","type":"reasoning","status":"completed","summary":[{"type":"summary_text","text":"inspect first"}],"encrypted_content":"enc_123"}`),
		Type: "reasoning",
	}
	replayed, ok := responsesOutputItemReplayInput(item)
	if !ok {
		t.Fatalf("expected reasoning item to replay")
	}
	var decoded map[string]any
	if err := json.Unmarshal(replayed.Raw, &decoded); err != nil {
		t.Fatalf("decode replayed reasoning: %v", err)
	}
	if _, exists := decoded["status"]; exists {
		t.Fatalf("reasoning replay must strip status: %s", replayed.Raw)
	}
	if decoded["encrypted_content"] != "enc_123" {
		t.Fatalf("reasoning replay lost encrypted_content: %s", replayed.Raw)
	}
}

func TestResponsesOutputItemReplayInput_FunctionCallDropsNonFCPrefixID(t *testing.T) {
	item := responsesOutputItem{
		ID:        "ctc_1",
		Type:      "function_call",
		CallID:    "call_1",
		Name:      "read_file",
		Arguments: json.RawMessage(`"{\"path\":\"README.md\"}"`),
	}
	replayed, ok := responsesOutputItemReplayInput(item)
	if !ok {
		t.Fatalf("expected function_call item to replay")
	}
	if replayed.ID != "" {
		t.Fatalf("non-fc_ id must be dropped: %+v", replayed)
	}
}
