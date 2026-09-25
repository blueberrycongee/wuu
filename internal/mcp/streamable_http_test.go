package mcp

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"
)

func initializeResultJSON(id int64, version string) string {
	return fmt.Sprintf(`{"jsonrpc":"2.0","id":%d,"result":{"protocolVersion":%q,"capabilities":{},"serverInfo":{"name":"fake","version":"0"}}}`, id, version)
}

func toolsListResultJSON(id int64, names ...string) string {
	tools := make([]string, 0, len(names))
	for _, name := range names {
		tools = append(tools, fmt.Sprintf(`{"name":%q,"inputSchema":{"type":"object"}}`, name))
	}
	return fmt.Sprintf(`{"jsonrpc":"2.0","id":%d,"result":{"tools":[%s]}}`, id, strings.Join(tools, ","))
}

// TestStreamableHTTPJSONResponseSessionAndVersionHeaders covers the plain
// single-JSON-response shape plus the header contract: Accept advertises both
// content types on every POST, the Mcp-Session-Id from the initialize
// response is echoed on all subsequent requests, and MCP-Protocol-Version
// carries the server-negotiated version.
func TestStreamableHTTPJSONResponseSessionAndVersionHeaders(t *testing.T) {
	var mu sync.Mutex
	headersByMethod := map[string]http.Header{}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.Method {
		case http.MethodGet:
			w.WriteHeader(http.StatusMethodNotAllowed) // no server-notification stream offered
			return
		case http.MethodDelete:
			w.WriteHeader(http.StatusOK)
			return
		}
		var req Request
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		mu.Lock()
		headersByMethod[req.Method] = r.Header.Clone()
		mu.Unlock()
		switch req.Method {
		case "initialize":
			w.Header().Set("Mcp-Session-Id", "sess-1")
			w.Header().Set("Content-Type", "application/json")
			fmt.Fprint(w, initializeResultJSON(req.ID, "2025-06-18"))
		case "notifications/initialized":
			w.WriteHeader(http.StatusAccepted)
		case "tools/list":
			w.Header().Set("Content-Type", "application/json")
			fmt.Fprint(w, toolsListResultJSON(req.ID, "echo"))
		default:
			w.WriteHeader(http.StatusAccepted)
		}
	}))
	defer srv.Close()

	client, err := ConnectStreamableHTTP(context.Background(), ServerConfig{Name: "s", URL: srv.URL})
	if err != nil {
		t.Fatalf("ConnectStreamableHTTP: %v", err)
	}
	defer client.Close()
	tools, err := client.DiscoverTools(context.Background())
	if err != nil {
		t.Fatalf("DiscoverTools: %v", err)
	}
	if len(tools) != 1 || tools[0].Name != "echo" {
		t.Fatalf("unexpected tools: %+v", tools)
	}

	mu.Lock()
	defer mu.Unlock()
	init := headersByMethod["initialize"]
	if init == nil {
		t.Fatal("no initialize request seen")
	}
	if accept := init.Get("Accept"); !strings.Contains(accept, "application/json") || !strings.Contains(accept, "text/event-stream") {
		t.Fatalf("initialize Accept must list both content types, got %q", accept)
	}
	if got := init.Get("Mcp-Session-Id"); got != "" {
		t.Fatalf("initialize must not carry a session id, got %q", got)
	}
	for _, method := range []string{"notifications/initialized", "tools/list"} {
		h := headersByMethod[method]
		if h == nil {
			t.Fatalf("no %s request seen", method)
		}
		if got := h.Get("Mcp-Session-Id"); got != "sess-1" {
			t.Fatalf("%s should echo session id, got %q", method, got)
		}
		if got := h.Get("MCP-Protocol-Version"); got != "2025-06-18" {
			t.Fatalf("%s should carry the negotiated protocol version, got %q", method, got)
		}
		if accept := h.Get("Accept"); !strings.Contains(accept, "application/json") || !strings.Contains(accept, "text/event-stream") {
			t.Fatalf("%s Accept must list both content types, got %q", method, accept)
		}
	}
}

// TestStreamableHTTPSSEStreamResponse covers the second response shape: the
// server answers POSTs with a text/event-stream carrying an interleaved
// notification and then the JSON-RPC response (split across two data lines).
func TestStreamableHTTPSSEStreamResponse(t *testing.T) {
	var mu sync.Mutex
	headersByMethod := map[string]http.Header{}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			w.WriteHeader(http.StatusMethodNotAllowed)
			return
		}
		var req Request
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		mu.Lock()
		headersByMethod[req.Method] = r.Header.Clone()
		mu.Unlock()
		switch req.Method {
		case "initialize":
			w.Header().Set("Mcp-Session-Id", "sess-sse")
			w.Header().Set("Content-Type", "text/event-stream")
			fmt.Fprint(w, ": stream open\n\n")
			fmt.Fprintf(w, "id: ev-1\ndata: %s\n\n", initializeResultJSON(req.ID, "2025-03-26"))
		case "notifications/initialized":
			w.WriteHeader(http.StatusAccepted)
		case "tools/list":
			w.Header().Set("Content-Type", "text/event-stream")
			fmt.Fprint(w, "data: {\"jsonrpc\":\"2.0\",\"method\":\"notifications/message\",\"params\":{}}\n\n")
			// Response split across two data lines: parser must join with \n.
			fmt.Fprintf(w, "data: {\"jsonrpc\":\"2.0\",\"id\":%d,\ndata: \"result\":{\"tools\":[{\"name\":\"sse-tool\",\"inputSchema\":{\"type\":\"object\"}}]}}\n\n", req.ID)
		default:
			w.WriteHeader(http.StatusAccepted)
		}
	}))
	defer srv.Close()

	client, err := ConnectStreamableHTTP(context.Background(), ServerConfig{Name: "s", URL: srv.URL})
	if err != nil {
		t.Fatalf("ConnectStreamableHTTP: %v", err)
	}
	defer client.Close()
	tools, err := client.DiscoverTools(context.Background())
	if err != nil {
		t.Fatalf("DiscoverTools: %v", err)
	}
	if len(tools) != 1 || tools[0].Name != "sse-tool" {
		t.Fatalf("unexpected tools: %+v", tools)
	}

	mu.Lock()
	defer mu.Unlock()
	list := headersByMethod["tools/list"]
	if list == nil {
		t.Fatal("no tools/list request seen")
	}
	if got := list.Get("Mcp-Session-Id"); got != "sess-sse" {
		t.Fatalf("tools/list should echo session id from SSE initialize, got %q", got)
	}
	if got := list.Get("MCP-Protocol-Version"); got != "2025-03-26" {
		t.Fatalf("tools/list should carry the version sniffed from the SSE initialize response, got %q", got)
	}
}

// TestStreamableHTTPSessionExpiryReinitializesAndRetries covers spec Session
// Management #3-4: a 404 for a request carrying the session ID makes the
// client re-initialize (a fresh InitializeRequest without a session ID) and
// retry the original request once against the new session.
func TestStreamableHTTPSessionExpiryReinitializesAndRetries(t *testing.T) {
	var mu sync.Mutex
	initCount := 0
	currentSession := ""
	reinitCarriedSession := false
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			w.WriteHeader(http.StatusMethodNotAllowed)
			return
		}
		var req Request
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		switch req.Method {
		case "initialize":
			mu.Lock()
			initCount++
			if initCount > 1 && r.Header.Get("Mcp-Session-Id") != "" {
				reinitCarriedSession = true
			}
			currentSession = fmt.Sprintf("sess-%d", initCount)
			session := currentSession
			mu.Unlock()
			w.Header().Set("Mcp-Session-Id", session)
			w.Header().Set("Content-Type", "application/json")
			fmt.Fprint(w, initializeResultJSON(req.ID, "2025-06-18"))
		case "notifications/initialized":
			w.WriteHeader(http.StatusAccepted)
		case "tools/call":
			mu.Lock()
			valid := r.Header.Get("Mcp-Session-Id") == currentSession
			mu.Unlock()
			if !valid {
				w.WriteHeader(http.StatusNotFound) // session terminated
				return
			}
			w.Header().Set("Content-Type", "application/json")
			fmt.Fprintf(w, `{"jsonrpc":"2.0","id":%d,"result":{"content":[{"type":"text","text":"ok"}]}}`, req.ID)
		default:
			w.WriteHeader(http.StatusAccepted)
		}
	}))
	defer srv.Close()

	client, err := ConnectStreamableHTTP(context.Background(), ServerConfig{Name: "s", URL: srv.URL})
	if err != nil {
		t.Fatalf("ConnectStreamableHTTP: %v", err)
	}
	defer client.Close()

	// Expire the session server-side: the client's sess-1 no longer matches.
	mu.Lock()
	currentSession = "rotated-away"
	mu.Unlock()

	result, err := client.CallTool(context.Background(), "echo", nil)
	if err != nil {
		t.Fatalf("CallTool after session expiry should transparently retry: %v", err)
	}
	if len(result.Content) != 1 || result.Content[0].Text != "ok" {
		t.Fatalf("unexpected tool result: %+v", result)
	}

	mu.Lock()
	defer mu.Unlock()
	if initCount != 2 {
		t.Fatalf("expected exactly one re-initialize (2 total), got %d", initCount)
	}
	if reinitCarriedSession {
		t.Fatal("re-initialize must start a new session without a session ID attached")
	}
}

// TestStreamableHTTPDeleteOnClose covers spec Session Management #5: closing
// the client sends a best-effort HTTP DELETE carrying the session ID.
func TestStreamableHTTPDeleteOnClose(t *testing.T) {
	deleted := make(chan string, 1)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.Method {
		case http.MethodDelete:
			select {
			case deleted <- r.Header.Get("Mcp-Session-Id"):
			default:
			}
			w.WriteHeader(http.StatusOK)
			return
		case http.MethodGet:
			w.WriteHeader(http.StatusMethodNotAllowed)
			return
		}
		var req Request
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		switch req.Method {
		case "initialize":
			w.Header().Set("Mcp-Session-Id", "sess-del")
			w.Header().Set("Content-Type", "application/json")
			fmt.Fprint(w, initializeResultJSON(req.ID, "2025-06-18"))
		default:
			w.WriteHeader(http.StatusAccepted)
		}
	}))
	defer srv.Close()

	client, err := ConnectStreamableHTTP(context.Background(), ServerConfig{Name: "s", URL: srv.URL})
	if err != nil {
		t.Fatalf("ConnectStreamableHTTP: %v", err)
	}
	if err := client.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}
	select {
	case session := <-deleted:
		if session != "sess-del" {
			t.Fatalf("DELETE should carry the session id, got %q", session)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("Close did not send a session-terminating DELETE")
	}
}

// TestStreamableHTTPServerNotificationsViaGETStream covers the optional GET
// listening stream: server-initiated notifications/tools/list_changed arrives
// on the GET SSE stream and triggers a tools refresh in the client.
func TestStreamableHTTPServerNotificationsViaGETStream(t *testing.T) {
	notify := make(chan string, 4)
	var mu sync.Mutex
	listCalls := 0
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.Method {
		case http.MethodGet:
			flusher := w.(http.Flusher)
			w.Header().Set("Content-Type", "text/event-stream")
			w.WriteHeader(http.StatusOK)
			flusher.Flush()
			for {
				select {
				case msg := <-notify:
					fmt.Fprintf(w, "id: n-1\ndata: %s\n\n", msg)
					flusher.Flush()
				case <-r.Context().Done():
					return
				}
			}
		case http.MethodDelete:
			w.WriteHeader(http.StatusOK)
			return
		}
		var req Request
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		switch req.Method {
		case "initialize":
			w.Header().Set("Mcp-Session-Id", "sess-get")
			w.Header().Set("Content-Type", "application/json")
			fmt.Fprint(w, initializeResultJSON(req.ID, "2025-06-18"))
		case "notifications/initialized":
			w.WriteHeader(http.StatusAccepted)
		case "tools/list":
			mu.Lock()
			listCalls++
			call := listCalls
			mu.Unlock()
			w.Header().Set("Content-Type", "application/json")
			if call > 1 {
				fmt.Fprint(w, toolsListResultJSON(req.ID, "initial", "refreshed"))
			} else {
				fmt.Fprint(w, toolsListResultJSON(req.ID, "initial"))
			}
		default:
			w.WriteHeader(http.StatusAccepted)
		}
	}))
	defer srv.Close()

	client, err := ConnectStreamableHTTP(context.Background(), ServerConfig{Name: "s", URL: srv.URL})
	if err != nil {
		t.Fatalf("ConnectStreamableHTTP: %v", err)
	}
	defer client.Close()
	tools, err := client.DiscoverTools(context.Background())
	if err != nil {
		t.Fatalf("DiscoverTools: %v", err)
	}
	if len(tools) != 1 || tools[0].Name != "initial" {
		t.Fatalf("unexpected initial tools: %+v", tools)
	}

	notify <- `{"jsonrpc":"2.0","method":"notifications/tools/list_changed"}`
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		tools = client.Tools()
		if len(tools) == 2 && tools[1].Name == "refreshed" {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatalf("tools did not refresh after GET-stream notification: %+v", client.Tools())
}

// legacySSEServer emulates the deprecated HTTP+SSE transport the way wuu's
// SSETransport expects it: GET <base>/sse opens the event stream, JSON-RPC
// messages are POSTed to <base>/message, and responses come back as events.
// POSTs to the /sse endpoint itself (a streamable HTTP attempt) return 405.
type legacySSEServer struct {
	srv    *httptest.Server
	events chan string
	mu     sync.Mutex
	posts  int // streamable HTTP POSTs to /sse
	gets   int // SSE stream connections
}

func newLegacySSEServer(t *testing.T) *legacySSEServer {
	t.Helper()
	ls := &legacySSEServer{events: make(chan string, 16)}
	mux := http.NewServeMux()
	mux.HandleFunc("/sse", func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet {
			ls.mu.Lock()
			ls.posts++
			ls.mu.Unlock()
			w.WriteHeader(http.StatusMethodNotAllowed)
			return
		}
		ls.mu.Lock()
		ls.gets++
		ls.mu.Unlock()
		flusher := w.(http.Flusher)
		w.Header().Set("Content-Type", "text/event-stream")
		w.WriteHeader(http.StatusOK)
		flusher.Flush()
		for {
			select {
			case msg := <-ls.events:
				fmt.Fprintf(w, "data: %s\n\n", msg)
				flusher.Flush()
			case <-r.Context().Done():
				return
			}
		}
	})
	mux.HandleFunc("/message", func(w http.ResponseWriter, r *http.Request) {
		var req Request
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		switch req.Method {
		case "initialize":
			ls.events <- initializeResultJSON(req.ID, "2024-11-05")
		case "tools/list":
			ls.events <- toolsListResultJSON(req.ID, "legacy-tool")
		}
		w.WriteHeader(http.StatusAccepted)
	})
	ls.srv = httptest.NewServer(mux)
	t.Cleanup(ls.srv.Close)
	return ls
}

func (ls *legacySSEServer) counts() (posts, gets int) {
	ls.mu.Lock()
	defer ls.mu.Unlock()
	return ls.posts, ls.gets
}

// TestConnectRemoteAutoFallsBackToSSE covers the spec's client
// backwards-compatibility strategy: with no explicit transport, the
// streamable HTTP initialize POST is attempted first, its 405 triggers the
// SSE fallback, and the legacy server works end to end. This is the
// zero-breakage path for existing URL-only (pure SSE) configurations.
func TestConnectRemoteAutoFallsBackToSSE(t *testing.T) {
	ls := newLegacySSEServer(t)
	client, err := ConnectRemote(context.Background(), ServerConfig{Name: "legacy", URL: ls.srv.URL + "/sse"})
	if err != nil {
		t.Fatalf("ConnectRemote should fall back to SSE: %v", err)
	}
	defer client.Close()
	tools, err := client.DiscoverTools(context.Background())
	if err != nil {
		t.Fatalf("DiscoverTools over fallback SSE: %v", err)
	}
	if len(tools) != 1 || tools[0].Name != "legacy-tool" {
		t.Fatalf("unexpected tools: %+v", tools)
	}
	posts, gets := ls.counts()
	if posts == 0 {
		t.Fatal("auto mode should attempt a streamable HTTP initialize POST first")
	}
	if gets == 0 {
		t.Fatal("auto mode should have fallen back to the SSE stream")
	}
}

// TestConnectRemoteExplicitHTTPDoesNotFallBack: when the user pins
// transport "http", a rejected initialize POST is a hard, diagnosable error
// (status, endpoint, and the no-fallback decision are all in the message).
func TestConnectRemoteExplicitHTTPDoesNotFallBack(t *testing.T) {
	ls := newLegacySSEServer(t)
	_, err := ConnectRemote(context.Background(), ServerConfig{Name: "legacy", URL: ls.srv.URL + "/sse", Transport: "http"})
	if err == nil {
		t.Fatal("explicit http transport against a legacy SSE server should fail")
	}
	msg := err.Error()
	for _, want := range []string{"405", ls.srv.URL + "/sse", "not falling back to SSE", "streamable HTTP"} {
		if !strings.Contains(msg, want) {
			t.Fatalf("error should mention %q, got: %v", want, err)
		}
	}
	if _, gets := ls.counts(); gets != 0 {
		t.Fatal("explicit http transport must not attempt the SSE fallback")
	}
}

// TestConnectRemoteExplicitSSESkipsStreamableHTTP: transport "sse" preserves
// the pre-existing behavior exactly — no streamable HTTP probe at all.
func TestConnectRemoteExplicitSSESkipsStreamableHTTP(t *testing.T) {
	ls := newLegacySSEServer(t)
	client, err := ConnectRemote(context.Background(), ServerConfig{Name: "legacy", URL: ls.srv.URL + "/sse", Transport: "sse"})
	if err != nil {
		t.Fatalf("ConnectRemote with explicit sse: %v", err)
	}
	defer client.Close()
	if posts, _ := ls.counts(); posts != 0 {
		t.Fatal("explicit sse transport must not probe the endpoint with a streamable HTTP POST")
	}
}

// TestConnectRemoteStreamableHTTPPreferred: a server that answers the
// initialize POST directly is used as streamable HTTP; no SSE fallback runs.
func TestConnectRemoteStreamableHTTPPreferred(t *testing.T) {
	var mu sync.Mutex
	getCount := 0
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.Method {
		case http.MethodGet:
			mu.Lock()
			getCount++
			mu.Unlock()
			w.WriteHeader(http.StatusMethodNotAllowed)
			return
		case http.MethodDelete:
			w.WriteHeader(http.StatusOK)
			return
		}
		var req Request
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		switch req.Method {
		case "initialize":
			w.Header().Set("Content-Type", "application/json")
			fmt.Fprint(w, initializeResultJSON(req.ID, "2025-06-18"))
		case "tools/list":
			w.Header().Set("Content-Type", "application/json")
			fmt.Fprint(w, toolsListResultJSON(req.ID, "modern-tool"))
		default:
			w.WriteHeader(http.StatusAccepted)
		}
	}))
	defer srv.Close()

	client, err := ConnectRemote(context.Background(), ServerConfig{Name: "modern", URL: srv.URL})
	if err != nil {
		t.Fatalf("ConnectRemote: %v", err)
	}
	defer client.Close()
	tools, err := client.DiscoverTools(context.Background())
	if err != nil {
		t.Fatalf("DiscoverTools: %v", err)
	}
	if len(tools) != 1 || tools[0].Name != "modern-tool" {
		t.Fatalf("unexpected tools: %+v", tools)
	}
}

// TestConnectRemoteAutoNoFallbackOnNetworkError: dial failures are not
// "endpoint speaks the old transport" evidence, so auto mode reports them
// directly instead of doubling the failure with an SSE attempt.
func TestConnectRemoteAutoNoFallbackOnNetworkError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {}))
	url := srv.URL
	srv.Close() // now unreachable

	_, err := ConnectRemote(context.Background(), ServerConfig{Name: "dead", URL: url})
	if err == nil {
		t.Fatal("expected connection error")
	}
	if strings.Contains(err.Error(), "SSE fallback") {
		t.Fatalf("network errors must not trigger the SSE fallback: %v", err)
	}
}

// TestNormalizeTransportAliases pins the accepted spellings.
func TestNormalizeTransportAliases(t *testing.T) {
	for input, want := range map[string]string{
		"":                TransportAuto,
		"sse":             TransportSSE,
		"SSE":             TransportSSE,
		"http":            TransportStreamableHTTP,
		"streamable-http": TransportStreamableHTTP,
		"streamable_http": TransportStreamableHTTP,
		"StreamableHTTP":  TransportStreamableHTTP,
	} {
		got, err := normalizeTransport(input)
		if err != nil || got != want {
			t.Fatalf("normalizeTransport(%q) = %q, %v; want %q", input, got, err, want)
		}
	}
	if _, err := normalizeTransport("ws"); err == nil {
		t.Fatal("normalizeTransport should reject unknown names")
	}
}
