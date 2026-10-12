package mcp

// Streamable HTTP client transport for MCP.
//
// Protocol semantics implemented here follow, in priority order:
//
//  1. MCP specification, revision 2025-06-18, "Transports" chapter
//     (https://modelcontextprotocol.io/specification/2025-06-18/basic/transports),
//     sections "Sending Messages to the Server", "Listening for Messages from
//     the Server", "Session Management", "Protocol Version Header",
//     "Resumability and Redelivery", and "Backwards Compatibility".
//  2. Claude Code's client (thirdparty/claude-code-sourcemap/src/services/mcp/
//     client.ts): config type "http" selects StreamableHTTPClientTransport,
//     every POST carries `Accept: application/json, text/event-stream`
//     (MCP_STREAMABLE_HTTP_ACCEPT), and HTTP 404 / JSON-RPC -32001 during a
//     call means the session expired and a fresh initialize is required.
//
// Wire behavior:
//
//   - Every JSON-RPC message is POSTed to the single MCP endpoint with
//     `Accept: application/json, text/event-stream`. The response is either
//     202 (accepted notification/response), a single `application/json`
//     JSON-RPC message, or a `text/event-stream` stream carrying one or more
//     JSON-RPC messages; both response shapes are handled.
//   - If the initialize response carries an `Mcp-Session-Id` header, it is
//     echoed on all subsequent requests. An HTTP 404 for a request that
//     carried the session ID means the session expired: the transport
//     re-initializes once (a new InitializeRequest without a session ID, per
//     spec) and retries the original message once.
//   - After initialization, every request carries `MCP-Protocol-Version` set
//     to the version the server returned in the InitializeResult.
//   - Close sends a best-effort HTTP DELETE with the session ID (servers MAY
//     reject it with 405; failures are ignored).
//   - A GET SSE listening stream is opened after initialize because wuu's
//     client consumes server-initiated notifications (client.go reacts to
//     notifications/tools/list_changed by re-running tools/list), and over
//     streamable HTTP unsolicited server->client messages only arrive on the
//     GET stream. Servers that do not offer the stream answer 405 (or another
//     4xx) and the listener stops for that session. Reconnects send
//     `Last-Event-ID` when the server tagged events with IDs.

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"mime"
	"net/http"
	"strings"
	"sync"
	"time"
)

const (
	headerSessionID       = "Mcp-Session-Id"
	headerProtocolVersion = "MCP-Protocol-Version"
	headerMethod          = "Mcp-Method"
	headerName            = "Mcp-Name"
	headerLastEventID     = "Last-Event-ID"

	// acceptStreamableHTTP is required on every POST by the spec ("the client
	// MUST include an Accept header, listing both application/json and
	// text/event-stream"). Claude Code hardcodes the same value.
	acceptStreamableHTTP = "application/json, text/event-stream"
)

// streamableHTTPError is a failed streamable HTTP round trip. It is a typed
// error so ConnectRemote can distinguish "this endpoint does not speak
// streamable HTTP" (fallback to SSE in auto mode) from network-level failures
// (no fallback: SSE against the same unreachable host would fail identically).
type streamableHTTPError struct {
	endpoint   string
	status     int    // HTTP status code; 0 when the failure is not status-shaped
	statusText string // e.g. "405 Method Not Allowed"
	body       string // trimmed response body excerpt, for diagnostics
	reason     string // non-empty for protocol mismatches (unexpected content type, non-JSON-RPC body)
	hadSession bool   // request carried an Mcp-Session-Id header
	generation uint64 // session owning the rejected request
}

func (e *streamableHTTPError) Error() string {
	if e.status == 0 {
		return fmt.Sprintf("streamable HTTP POST %s: %s", e.endpoint, e.reason)
	}
	msg := fmt.Sprintf("streamable HTTP POST %s: HTTP %s", e.endpoint, e.statusText)
	if e.reason != "" {
		msg += " (" + e.reason + ")"
	}
	if e.body != "" {
		msg += ": " + e.body
	}
	return msg
}

// fallbackToSSE reports whether the failure indicates the endpoint is not a
// streamable HTTP server. The spec's backwards-compatibility rule is "fails
// with an HTTP 4xx status code (e.g., 405 Method Not Allowed or 404 Not
// Found)"; we additionally treat a 2xx reply that is not a JSON-RPC message
// (reason != "") as fallback-worthy so legacy endpoints that answer POSTs
// with unrelated 200s keep working.
func (e *streamableHTTPError) fallbackToSSE() bool {
	return (e.status >= 400 && e.status < 500) || e.reason != ""
}

// StreamableHTTPTransport communicates with an MCP server over the
// streamable HTTP transport (MCP spec revision 2025-03-26 and later).
type StreamableHTTPTransport struct {
	endpoint     string
	client       *http.Client
	toolClient   *http.Client
	headers      map[string]string
	authProvider TokenProvider

	ctx    context.Context
	cancel context.CancelFunc
	inbox  chan httpInboundMessage
	failed chan struct{}

	mu              sync.Mutex
	sessionID       string
	protocolVersion string          // as returned by the server's InitializeResult
	initParams      json.RawMessage // params of the last initialize request, for session re-init
	generation      uint64
	listener        *httpListener
	recovery        *httpSessionRecovery
	listenerBackoff time.Duration
	closed          bool
	terminalErr     error

	wg sync.WaitGroup
}

// Notifications and reverse requests belong to their originating session,
// including while buffered. POST responses still belong to their original call.
type httpInboundMessage struct {
	response   Response
	generation uint64
}

// A listener and its resume cursor belong to one initialized session.
type httpListener struct {
	ctx              context.Context
	cancel           context.CancelFunc
	generation       uint64
	session, version string
	lastEventID      string // guarded by the transport mutex
	initialDelay     time.Duration
	unsupported      bool // a 405 disables GET only for this session
}

type httpSessionRecovery struct {
	generation uint64
	done       chan struct{}
	err        error // published by closing done
}

// NewStreamableHTTPTransport creates a transport for the given MCP endpoint.
// No network traffic happens until the first Send.
func NewStreamableHTTPTransport(endpoint string, headers map[string]string) *StreamableHTTPTransport {
	return NewStreamableHTTPTransportWithAuth(endpoint, headers, nil)
}

// NewStreamableHTTPTransportWithAuth creates a transport whose bearer token is
// resolved for each request instead of being captured at connection time.
func NewStreamableHTTPTransportWithAuth(endpoint string, headers map[string]string, authProvider TokenProvider) *StreamableHTTPTransport {
	ctx, cancel := context.WithCancel(context.Background())
	client := newStreamableHTTPClient()
	if len(headers) > 0 || authProvider != nil {
		client = withSameOriginRedirects(client)
	}
	return &StreamableHTTPTransport{
		endpoint:     endpoint,
		client:       client,
		toolClient:   toolHTTPClient(client),
		headers:      cloneStringMap(headers),
		authProvider: authProvider,
		ctx:          ctx,
		cancel:       cancel,
		inbox:        make(chan httpInboundMessage, 32),
		failed:       make(chan struct{}),
	}
}

// newStreamableHTTPClient builds an HTTP client suitable for both single JSON
// responses and long-lived SSE streams: no whole-request timeout (it would
// kill open streams), but a response-header timeout so dead servers fail fast.
func newStreamableHTTPClient() *http.Client {
	tr, ok := http.DefaultTransport.(*http.Transport)
	if ok {
		tr = tr.Clone()
	} else {
		tr = &http.Transport{Proxy: http.ProxyFromEnvironment}
	}
	tr.ResponseHeaderTimeout = 30 * time.Second
	return &http.Client{Transport: tr}
}

func (t *StreamableHTTPTransport) Send(ctx context.Context, req Request) error {
	body, err := json.Marshal(req)
	if err != nil {
		return fmt.Errorf("marshal message: %w", err)
	}
	err = t.postWithAuthRetry(ctx, req, body)
	if err == nil {
		return nil
	}
	// Session expiry (spec, Session Management #3-4): a 404 for a request
	// that carried the session ID means the server terminated the session;
	// the client MUST start a new session with a new InitializeRequest.
	// Re-initialize once and retry the original message once.
	var herr *streamableHTTPError
	if errors.As(err, &herr) && herr.status == http.StatusNotFound && herr.hadSession && req.Method != "initialize" {
		if rerr := t.reinitialize(ctx, herr.generation); rerr != nil {
			return fmt.Errorf("streamable HTTP session at %s expired (HTTP 404) and re-initialize failed: %w", t.endpoint, rerr)
		}
		// Recovery already completed this notification for the new session.
		if req.Method == "notifications/initialized" {
			return nil
		}
		if err := t.post(ctx, req, body, false); err != nil {
			return fmt.Errorf("streamable HTTP retry after session re-initialize: %w", err)
		}
		return nil
	}
	return err
}

func (t *StreamableHTTPTransport) postWithAuthRetry(ctx context.Context, req Request, body []byte) error {
	err := t.post(ctx, req, body, false)
	var authErr *streamableHTTPError
	if errors.As(err, &authErr) && authErr.status == http.StatusUnauthorized && t.authProvider != nil {
		return t.post(ctx, req, body, true)
	}
	return err
}

// post performs one JSON-RPC POST and dispatches whatever comes back (202,
// single JSON message, or SSE stream) into the inbox.
func (t *StreamableHTTPTransport) post(ctx context.Context, req Request, body []byte, forceRefresh bool) (err error) {
	for {
		t.mu.Lock()
		// Recovery owns the handshake notification. Ordinary messages must never
		// observe its temporarily empty or not-yet-initialized session.
		if recovery := t.recovery; recovery != nil && req.Method != "initialize" && req.Method != "notifications/initialized" {
			select {
			case <-recovery.done:
				if recovery.err != nil {
					t.mu.Unlock()
					return recovery.err
				}
			default:
				t.mu.Unlock()
				if err := t.waitRecovery(ctx, recovery); err != nil {
					return err
				}
				continue
			}
		}
		break
	}
	if t.closed {
		t.mu.Unlock()
		return fmt.Errorf("streamable HTTP transport for %s is closed", t.endpoint)
	}
	if req.Method == "initialize" && len(req.Params) > 0 {
		t.initParams = append(json.RawMessage(nil), req.Params...)
	}
	session := t.sessionID
	version := t.protocolVersion
	generation := t.generation
	t.mu.Unlock()
	defer func() {
		if err == nil {
			if req.Method == "notifications/initialized" {
				t.startListener(generation, false)
			} else if forceRefresh {
				t.startListener(generation, true)
			}
		}
	}()
	if req.Method == "initialize" {
		session, version = "", ""
	}
	if requestVersion := requestProtocolVersion(req.Params); requestVersion != "" {
		version = requestVersion
		if requestVersion == PreferredProtocolVersion {
			session = ""
		}
	}

	// A POST response stream belongs to its request, including after Send
	// returns. Keep caller cancellation attached until the body is released;
	// transport shutdown must still interrupt every outstanding request.
	reqCtx, cancelReq := context.WithCancel(t.ctx)
	detach := context.AfterFunc(ctx, cancelReq)
	finish := func() {
		detach()
		cancelReq()
	}

	hreq, err := http.NewRequestWithContext(reqCtx, http.MethodPost, t.endpoint, bytes.NewReader(body))
	if err != nil {
		finish()
		return fmt.Errorf("streamable HTTP POST %s: %w", t.endpoint, err)
	}
	t.applyHeaders(hreq, session, version)
	if err := applyToken(hreq, t.authProvider, ctx, forceRefresh); err != nil {
		finish()
		return err
	}
	if version == PreferredProtocolVersion && req.Method != "" {
		hreq.Header.Set(headerMethod, req.Method)
		if name := requestPrincipalName(req.Method, req.Params); name != "" {
			hreq.Header.Set(headerName, name)
		}
	}
	hreq.Header.Set("Content-Type", "application/json")
	hreq.Header.Set("Accept", acceptStreamableHTTP)

	client := t.client
	if req.Method == "tools/call" {
		client = t.toolClient
	}
	resp, err := client.Do(hreq)
	if err != nil {
		finish()
		return fmt.Errorf("streamable HTTP POST %s: %w", t.endpoint, err)
	}

	// Adopt the candidate session only with a valid matching InitializeResult.
	responseSession := strings.TrimSpace(resp.Header.Get(headerSessionID))

	if resp.StatusCode == http.StatusAccepted {
		// Notifications and responses are accepted with 202 and no body.
		drainAndClose(resp.Body)
		finish()
		return nil
	}
	if resp.StatusCode >= 300 {
		excerpt := readBodyExcerpt(resp.Body)
		finish()
		return &streamableHTTPError{
			endpoint:   t.endpoint,
			status:     resp.StatusCode,
			statusText: strings.TrimSpace(resp.Status),
			body:       excerpt,
			hadSession: session != "",
			generation: generation,
		}
	}

	// isCall: only JSON-RPC requests demand a JSON-RPC reply. Notifications
	// (no ID) and our responses to server requests (no method) may be
	// answered with any empty-ish 2xx.
	isCall := (req.hasID || req.ID != 0) && req.Method != ""
	contentType, _, _ := mime.ParseMediaType(resp.Header.Get("Content-Type"))
	switch contentType {
	case "application/json":
		data, err := readLimitedBody(resp.Body, maxMCPMessageBytes)
		resp.Body.Close()
		finish()
		if err != nil {
			return fmt.Errorf("streamable HTTP POST %s: read response: %w", t.endpoint, err)
		}
		if len(bytes.TrimSpace(data)) == 0 {
			if !isCall {
				return nil
			}
			return &streamableHTTPError{
				endpoint: t.endpoint,
				reason:   "empty response body for a JSON-RPC request",
			}
		}
		var msg Response
		if err := json.Unmarshal(data, &msg); err != nil {
			if !isCall {
				return nil
			}
			return &streamableHTTPError{
				endpoint: t.endpoint,
				reason:   fmt.Sprintf("response body is not a JSON-RPC message: %v", err),
			}
		}
		if isCall && !responseMatchesRequest(req, msg) {
			return &streamableHTTPError{
				endpoint: t.endpoint,
				reason:   "response does not match the originating request",
			}
		}
		t.observeInitialize(req, msg, responseSession, generation)
		t.deliverContext(t.ctx, msg, generation)
		return nil
	case "text/event-stream":
		// The stream carries one or more JSON-RPC messages and SHOULD
		// eventually include the response for the POSTed request. Reading in
		// the background lets the shared read loop answer reverse requests.
		t.mu.Lock()
		if t.closed {
			t.mu.Unlock()
			finish()
			resp.Body.Close()
			return fmt.Errorf("streamable HTTP transport for %s is closed", t.endpoint)
		}
		t.wg.Add(1)
		t.mu.Unlock()
		go t.consumeStream(reqCtx, resp.Body, finish, req, responseSession, generation)
		return nil
	default:
		drainAndClose(resp.Body)
		finish()
		if !isCall {
			return nil // server accepted a notification/response with a bare 2xx
		}
		return &streamableHTTPError{
			endpoint: t.endpoint,
			reason:   fmt.Sprintf("unexpected content type %q for a JSON-RPC request (want application/json or text/event-stream)", resp.Header.Get("Content-Type")),
		}
	}
}

func requestProtocolVersion(params json.RawMessage) string {
	var envelope struct {
		Meta map[string]json.RawMessage `json:"_meta"`
	}
	if len(params) == 0 || json.Unmarshal(params, &envelope) != nil {
		return ""
	}
	var version string
	_ = json.Unmarshal(envelope.Meta["io.modelcontextprotocol/protocolVersion"], &version)
	return strings.TrimSpace(version)
}

func requestPrincipalName(method string, params json.RawMessage) string {
	if method != "tools/call" && method != "prompts/get" && method != "resources/read" {
		return ""
	}
	var values struct {
		Name string `json:"name"`
		URI  string `json:"uri"`
	}
	if json.Unmarshal(params, &values) != nil {
		return ""
	}
	if method == "resources/read" {
		return values.URI
	}
	return values.Name
}

// applyHeaders sets user headers plus the protocol-mandated session and
// version headers. Protocol headers are applied last so they win over any
// clashing user-configured value.
func (t *StreamableHTTPTransport) applyHeaders(hreq *http.Request, session, version string) {
	for key, value := range t.headers {
		key = strings.TrimSpace(key)
		if key == "" {
			continue
		}
		hreq.Header.Set(key, value)
	}
	hreq.Header.Del(headerSessionID)
	hreq.Header.Del(headerProtocolVersion)
	hreq.Header.Del(headerLastEventID)
	if session != "" {
		hreq.Header.Set(headerSessionID, session)
	}
	if version != "" {
		// Spec, Protocol Version Header: the client MUST include
		// MCP-Protocol-Version on all requests after initialization, set to
		// the version negotiated during initialization.
		hreq.Header.Set(headerProtocolVersion, version)
	}
}

// observeInitialize sniffs the InitializeResult that answers our initialize
// request to record the negotiated protocol version (echoed on subsequent
// requests via MCP-Protocol-Version). GET starts after initialized is accepted.
func (t *StreamableHTTPTransport) observeInitialize(origin Request, msg Response, session string, generation uint64) {
	if origin.Method != "initialize" || !responseMatchesRequest(origin, msg) || msg.Error != nil || msg.JSONRPC != "2.0" {
		return
	}
	var result struct {
		ProtocolVersion string `json:"protocolVersion"`
	}
	_ = json.Unmarshal(msg.Result, &result)
	version := strings.TrimSpace(result.ProtocolVersion)
	if version == "" {
		// Defensive: a server that omits protocolVersion implicitly accepted
		// the version we offered in the request.
		var params struct {
			ProtocolVersion string `json:"protocolVersion"`
		}
		_ = json.Unmarshal(origin.Params, &params)
		version = strings.TrimSpace(params.ProtocolVersion)
	}
	t.mu.Lock()
	if t.closed || t.generation != generation {
		t.mu.Unlock()
		return
	}
	if t.listener != nil {
		t.listener.cancel()
		t.listener = nil
	}
	t.sessionID, t.protocolVersion = session, version
	t.generation++
	t.mu.Unlock()
}

func responseMatchesRequest(origin Request, msg Response) bool {
	return msg.Method == "" && msg.hasID && msg.ID == origin.ID && msg.StringID == origin.StringID && msg.stringID == origin.stringID
}

// consumeStream keeps a POST body alive only while its originating call is
// pending. A broken stream is a failure of that call, not of the shared session.
func (t *StreamableHTTPTransport) consumeStream(ctx context.Context, body io.ReadCloser, finish context.CancelFunc, origin Request, session string, generation uint64) {
	defer t.wg.Done()
	defer finish()
	defer body.Close()
	isCall := (origin.hasID || origin.ID != 0) && origin.Method != ""
	fail := func(err error) {
		if !isCall || ctx.Err() != nil {
			return
		}
		// Do not replay the POST: the server may already have performed effects.
		t.deliverContext(ctx, Response{
			JSONRPC: "2.0", ID: origin.ID, StringID: origin.StringID,
			stringID: origin.stringID, hasID: true,
			Error: &RPCError{Code: -32000, Message: fmt.Sprintf("streamable HTTP POST %s: response stream ended before reply: %v", t.endpoint, err)},
		}, generation)
	}
	reader := bufio.NewReader(body)
	for {
		event, err := readSSEEvent(reader)
		if err != nil {
			fail(err)
			return
		}
		if strings.TrimSpace(event.data) == "" {
			continue
		}
		var msg Response
		if err := json.Unmarshal([]byte(event.data), &msg); err != nil {
			fail(fmt.Errorf("invalid JSON-RPC event: %w", err))
			return
		}
		if msg.JSONRPC != "2.0" || (msg.Method == "" && (!msg.hasID || (msg.Result == nil && msg.Error == nil))) {
			fail(errors.New("invalid JSON-RPC event"))
			return
		}
		// A response on this POST may only settle its own call. Reverse
		// requests still use their independent, server-owned ID namespace.
		if isCall && msg.Method == "" && !responseMatchesRequest(origin, msg) {
			fail(errors.New("response does not match the originating request"))
			return
		}
		t.observeInitialize(origin, msg, session, generation)
		if !t.deliverContext(ctx, msg, generation) {
			return
		}
		// Server request IDs occupy the other direction's namespace. Even a
		// reverse request with the same ID must be delivered before continuing.
		if isCall && responseMatchesRequest(origin, msg) {
			return
		}
	}
}

// deliverContext preserves the session owner until Receive consumes the message.
func (t *StreamableHTTPTransport) deliverContext(ctx context.Context, msg Response, generation uint64) bool {
	select {
	case <-ctx.Done():
		return false
	case t.inbox <- httpInboundMessage{response: msg, generation: generation}:
		return true
	case <-t.ctx.Done():
		return false
	}
}

// reinitialize coalesces all 404s from an expired generation. Recovery is
// transport-owned and bounded, so cancelling one waiter cannot cancel others.
// A failed handshake is reported to all waiters; reconnect creates a new client.
func (t *StreamableHTTPTransport) reinitialize(ctx context.Context, generation uint64) error {
	t.mu.Lock()
	if t.closed {
		t.mu.Unlock()
		return fmt.Errorf("streamable HTTP transport for %s is closed", t.endpoint)
	}
	recovery := t.recovery
	if recovery != nil && recovery.generation == generation {
		t.mu.Unlock()
		return t.waitRecovery(ctx, recovery)
	}
	if generation != t.generation {
		t.mu.Unlock()
		return nil
	}
	recovery = &httpSessionRecovery{generation: generation, done: make(chan struct{})}
	t.recovery = recovery
	params := append(json.RawMessage(nil), t.initParams...)
	t.sessionID, t.protocolVersion = "", ""
	if t.listener != nil {
		t.listener.cancel()
		t.listener = nil
	}
	t.wg.Add(1)
	t.mu.Unlock()
	go func() {
		defer t.wg.Done()
		recoveryCtx, cancel := context.WithTimeout(t.ctx, defaultConnectionTimeout)
		defer cancel()
		recovery.err = t.initializeSession(recoveryCtx, params, generation)
		if recovery.err != nil {
			t.mu.Lock()
			if t.recovery == recovery {
				if !t.closed && t.terminalErr == nil {
					t.terminalErr = fmt.Errorf("streamable HTTP session recovery: %w", recovery.err)
					close(t.failed)
				}
				t.sessionID, t.protocolVersion = "", ""
				if t.listener != nil {
					t.listener.cancel()
					t.listener = nil
				}
			}
			t.mu.Unlock()
		}
		close(recovery.done)
	}()
	return t.waitRecovery(ctx, recovery)
}

func (t *StreamableHTTPTransport) waitRecovery(ctx context.Context, recovery *httpSessionRecovery) error {
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-t.ctx.Done():
		select {
		case <-t.failed:
			return t.terminalErr
		default:
			return t.ctx.Err()
		}
	case <-recovery.done:
		return recovery.err
	}
}

func (t *StreamableHTTPTransport) initializeSession(ctx context.Context, params json.RawMessage, generation uint64) error {
	if len(params) == 0 {
		return fmt.Errorf("no recorded initialize params")
	}

	initReq := Request{JSONRPC: "2.0", ID: nextRequestID(), Method: "initialize", Params: params}
	body, err := json.Marshal(initReq)
	if err != nil {
		return fmt.Errorf("marshal initialize: %w", err)
	}
	var resp *http.Response
	for attempt := 0; ; attempt++ {
		hreq, err := http.NewRequestWithContext(ctx, http.MethodPost, t.endpoint, bytes.NewReader(body))
		if err != nil {
			return err
		}
		t.applyHeaders(hreq, "", "")
		if err := applyToken(hreq, t.authProvider, ctx, attempt > 0); err != nil {
			return err
		}
		hreq.Header.Set("Content-Type", "application/json")
		hreq.Header.Set("Accept", acceptStreamableHTTP)
		resp, err = t.client.Do(hreq)
		if err != nil {
			return fmt.Errorf("streamable HTTP POST %s: %w", t.endpoint, err)
		}
		if resp.StatusCode == http.StatusUnauthorized && attempt == 0 && t.authProvider != nil {
			resp.Body.Close()
			continue
		}
		break
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 300 {
		return &streamableHTTPError{
			endpoint:   t.endpoint,
			status:     resp.StatusCode,
			statusText: strings.TrimSpace(resp.Status),
			body:       readBodyExcerpt(resp.Body),
		}
	}

	msg, err := readSingleResponse(resp, initReq.ID)
	if err != nil {
		return fmt.Errorf("initialize response from %s: %w", t.endpoint, err)
	}
	if msg.Error != nil {
		return msg.Error
	}
	var result InitializeResult
	if err := json.Unmarshal(msg.Result, &result); err != nil {
		return fmt.Errorf("initialize result: %w", err)
	}
	if err := validateProtocolVersion(result.ProtocolVersion); err != nil {
		return fmt.Errorf("initialize compatibility: %w", err)
	}
	if result.ProtocolVersion == PreferredProtocolVersion {
		return fmt.Errorf("initialize compatibility: protocol %s does not use initialize", result.ProtocolVersion)
	}
	t.observeInitialize(initReq, msg, strings.TrimSpace(resp.Header.Get(headerSessionID)), generation)

	note := Request{JSONRPC: "2.0", Method: "notifications/initialized"}
	noteBody, _ := json.Marshal(note)
	if err := t.postWithAuthRetry(ctx, note, noteBody); err != nil {
		return fmt.Errorf("notifications/initialized: %w", err)
	}
	return nil
}

// readSingleResponse extracts the JSON-RPC response with the given id from an
// HTTP response that is either a single JSON body or an SSE stream.
func readSingleResponse(resp *http.Response, id int64) (Response, error) {
	contentType, _, _ := mime.ParseMediaType(resp.Header.Get("Content-Type"))
	switch contentType {
	case "application/json":
		data, err := readLimitedBody(resp.Body, maxMCPMessageBytes)
		if err != nil {
			return Response{}, err
		}
		var msg Response
		if err := json.Unmarshal(data, &msg); err != nil {
			return Response{}, fmt.Errorf("not a JSON-RPC message: %w", err)
		}
		if msg.JSONRPC != "2.0" || !responseMatchesRequest(Request{ID: id}, msg) || (msg.Result == nil && msg.Error == nil) {
			return Response{}, errors.New("initialize response does not match its request")
		}
		return msg, nil
	case "text/event-stream":
		reader := bufio.NewReader(resp.Body)
		for {
			event, err := readSSEEvent(reader)
			if err != nil {
				return Response{}, fmt.Errorf("stream ended before the response arrived: %w", err)
			}
			if strings.TrimSpace(event.data) == "" {
				continue
			}
			var msg Response
			if err := json.Unmarshal([]byte(event.data), &msg); err != nil {
				continue
			}
			if msg.JSONRPC == "2.0" && responseMatchesRequest(Request{ID: id}, msg) && (msg.Result != nil || msg.Error != nil) {
				return msg, nil
			}
		}
	default:
		return Response{}, fmt.Errorf("unexpected content type %q", resp.Header.Get("Content-Type"))
	}
}

// startListener opens the optional GET SSE stream for server-initiated
// messages. See the file comment for why wuu needs it (tools/list_changed).
func (t *StreamableHTTPTransport) startListener(generation uint64, afterAuthRefresh bool) {
	t.mu.Lock()
	if t.closed || t.generation == 0 || t.generation != generation {
		t.mu.Unlock()
		return
	}
	if t.recovery != nil && t.recovery.generation == generation {
		t.mu.Unlock()
		return
	}
	cursor := ""
	if previous := t.listener; previous != nil {
		if !afterAuthRefresh || previous.unsupported {
			t.mu.Unlock()
			return
		}
		cursor = previous.lastEventID
		previous.cancel()
	}
	ctx, cancel := context.WithCancel(t.ctx)
	listener := &httpListener{ctx: ctx, cancel: cancel, generation: t.generation,
		session: t.sessionID, version: t.protocolVersion, lastEventID: cursor, initialDelay: t.listenerBackoff}
	t.listener = listener
	t.wg.Add(1)
	t.mu.Unlock()
	go t.listen(listener)
}

func (t *StreamableHTTPTransport) listen(listener *httpListener) {
	defer t.wg.Done()
	ctx := listener.ctx
	if listener.initialDelay > 0 && !sleepHTTPListener(ctx, listener.initialDelay) {
		return
	}
	backoff := time.Second
	forceRefresh, retriedAuth := false, false
	for {
		if ctx.Err() != nil {
			return
		}
		hreq, err := http.NewRequestWithContext(ctx, http.MethodGet, t.endpoint, nil)
		if err != nil {
			return
		}
		t.applyHeaders(hreq, listener.session, listener.version)
		if err := applyToken(hreq, t.authProvider, ctx, forceRefresh); err != nil {
			return
		}
		forceRefresh = false
		hreq.Header.Set("Accept", "text/event-stream")
		t.mu.Lock()
		cursor := listener.lastEventID
		t.mu.Unlock()
		if cursor != "" {
			hreq.Header.Set(headerLastEventID, cursor)
		}
		resp, err := t.client.Do(hreq)
		if err != nil {
			if !sleepHTTPListener(ctx, backoff) {
				return
			}
			backoff = minDuration(backoff*2, 30*time.Second)
			continue
		}
		if resp.StatusCode >= 400 && resp.StatusCode < 500 {
			resp.Body.Close()
			switch resp.StatusCode {
			case http.StatusUnauthorized:
				if t.authProvider != nil && !retriedAuth {
					forceRefresh, retriedAuth = true, true
					continue
				}
			case http.StatusNotFound:
				if listener.session != "" {
					// Repeatedly expired replacement sessions must not create
					// an unbounded initialize/GET loop against a broken server.
					t.mu.Lock()
					if t.listener == listener {
						t.listenerBackoff = minDuration(max(t.listenerBackoff*2, time.Second), 30*time.Second)
					}
					t.mu.Unlock()
					_ = t.reinitialize(ctx, listener.generation)
				}
			case http.StatusMethodNotAllowed:
				t.mu.Lock()
				listener.unsupported = true
				t.mu.Unlock()
			}
			return
		}
		contentType, _, _ := mime.ParseMediaType(resp.Header.Get("Content-Type"))
		if resp.StatusCode != http.StatusOK || contentType != "text/event-stream" {
			resp.Body.Close()
			if !sleepHTTPListener(ctx, backoff) {
				return
			}
			backoff = minDuration(backoff*2, 30*time.Second)
			continue
		}
		retriedAuth = false
		t.mu.Lock()
		if t.listener == listener {
			t.listenerBackoff = 0
		}
		t.mu.Unlock()
		backoff = time.Second
		reader := bufio.NewReader(resp.Body)
		for {
			event, err := readSSEEvent(reader)
			if err != nil {
				resp.Body.Close()
				break
			}
			t.mu.Lock()
			current := t.listener == listener && !t.closed
			if current && event.id != "" {
				listener.lastEventID = event.id
			}
			t.mu.Unlock()
			if !current || ctx.Err() != nil {
				resp.Body.Close()
				return
			}
			if strings.TrimSpace(event.data) == "" {
				continue
			}
			var msg Response
			if json.Unmarshal([]byte(event.data), &msg) != nil || msg.JSONRPC != "2.0" || msg.Method == "" {
				// This GET only resumes its own notification stream, never a POST.
				continue
			}
			if !t.deliverContext(ctx, msg, listener.generation) {
				resp.Body.Close()
				return
			}
		}
		if !sleepHTTPListener(ctx, backoff) {
			return
		}
	}
}

func sleepHTTPListener(ctx context.Context, d time.Duration) bool {
	timer := time.NewTimer(d)
	defer timer.Stop()
	select {
	case <-timer.C:
		return true
	case <-ctx.Done():
		return false
	}
}

func (t *StreamableHTTPTransport) Receive(ctx context.Context) (Response, error) {
	for {
		select {
		case <-ctx.Done():
			return Response{}, ctx.Err()
		case <-t.failed:
			return Response{}, t.terminalErr
		case <-t.ctx.Done():
			return Response{}, io.EOF
		case message := <-t.inbox:
			t.mu.Lock()
			current := !t.closed && t.terminalErr == nil
			if message.response.Method != "" {
				current = current && message.generation == t.generation &&
					(t.recovery == nil || t.recovery.generation != message.generation)
			}
			t.mu.Unlock()
			if current {
				return message.response, nil
			}
		}
	}
}

// Close terminates the session (best-effort HTTP DELETE with the session ID,
// per spec Session Management #5; servers MAY answer 405 and failures are
// tolerated) and tears down all streams.
func (t *StreamableHTTPTransport) Close() error {
	t.mu.Lock()
	if t.closed {
		t.mu.Unlock()
		return nil
	}
	t.closed = true
	t.cancel()
	session := t.sessionID
	version := t.protocolVersion
	t.mu.Unlock()

	if session != "" {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		if hreq, err := http.NewRequestWithContext(ctx, http.MethodDelete, t.endpoint, nil); err == nil {
			t.applyHeaders(hreq, session, version)
			if err := applyToken(hreq, t.authProvider, ctx, false); err == nil {
				if resp, err := t.client.Do(hreq); err == nil {
					drainAndClose(resp.Body)
				}
			}
		}
		cancel()
	}

	t.cancel()
	t.wg.Wait()
	t.client.CloseIdleConnections()
	t.toolClient.CloseIdleConnections()
	return nil
}

// sseEvent is one parsed Server-Sent Events event.
type sseEvent struct {
	id    string
	event string
	data  string
}

// readSSEEvent reads one SSE event (terminated by a blank line) per the WHATWG
// event-stream format: "data:" lines accumulate (joined by newlines), "id:"
// sets the event ID, lines starting with ":" are comments.
func readSSEEvent(r *bufio.Reader) (sseEvent, error) {
	return readSSEEventLimited(r, maxMCPEventBytes)
}

func readSSEEventLimited(r *bufio.Reader, limit int) (sseEvent, error) {
	var ev sseEvent
	var data []string
	seenField := false
	totalBytes := 0
	for {
		var line []byte
		for {
			part, err := r.ReadSlice('\n')
			if len(part) > 0 {
				if totalBytes+len(part) > limit {
					return sseEvent{}, fmt.Errorf("SSE event exceeds %d bytes", limit)
				}
				totalBytes += len(part)
				line = append(line, part...)
			}
			if err == bufio.ErrBufferFull {
				continue
			}
			if err != nil {
				return sseEvent{}, err
			}
			break
		}
		line = bytes.TrimRight(line, "\r\n")
		if len(line) == 0 {
			if !seenField {
				continue // skip leading blank lines between events
			}
			ev.data = strings.Join(data, "\n")
			return ev, nil
		}
		if bytes.HasPrefix(line, []byte(":")) {
			continue
		}
		seenField = true
		field, value, _ := strings.Cut(string(line), ":")
		value = strings.TrimPrefix(value, " ")
		switch field {
		case "data":
			data = append(data, value)
		case "id":
			ev.id = value
		case "event":
			ev.event = value
		}
	}
}

func drainAndClose(body io.ReadCloser) {
	_, _ = io.Copy(io.Discard, io.LimitReader(body, 4096))
	_ = body.Close()
}

func readBodyExcerpt(body io.ReadCloser) string {
	data, _ := io.ReadAll(io.LimitReader(body, maxMCPBodyExcerptBytes))
	_ = body.Close()
	return strings.TrimSpace(string(data))
}

func readLimitedBody(body io.Reader, limit int) ([]byte, error) {
	data, err := io.ReadAll(io.LimitReader(body, int64(limit)+1))
	if err != nil {
		return nil, err
	}
	if len(data) > limit {
		return nil, fmt.Errorf("HTTP MCP message exceeds %d bytes", limit)
	}
	return data, nil
}

func minDuration(a, b time.Duration) time.Duration {
	if a < b {
		return a
	}
	return b
}
