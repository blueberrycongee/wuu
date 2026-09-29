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
//     4xx) and the listener shuts up permanently. Reconnects send
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
	headers      map[string]string
	authProvider TokenProvider

	ctx    context.Context
	cancel context.CancelFunc
	inbox  chan Response

	mu              sync.Mutex
	sessionID       string
	protocolVersion string          // as returned by the server's InitializeResult
	initParams      json.RawMessage // params of the last initialize request, for session re-init
	listenerStarted bool
	closed          bool

	wg sync.WaitGroup
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
	return &StreamableHTTPTransport{
		endpoint:     endpoint,
		client:       newStreamableHTTPClient(),
		headers:      cloneStringMap(headers),
		authProvider: authProvider,
		ctx:          ctx,
		cancel:       cancel,
		inbox:        make(chan Response, 32),
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
	err = t.post(ctx, req, body, false)
	if err == nil {
		return nil
	}
	var authErr *streamableHTTPError
	if errors.As(err, &authErr) && authErr.status == http.StatusUnauthorized && t.authProvider != nil {
		if retryErr := t.post(ctx, req, body, true); retryErr == nil {
			return nil
		} else {
			err = retryErr
		}
	}
	// Session expiry (spec, Session Management #3-4): a 404 for a request
	// that carried the session ID means the server terminated the session;
	// the client MUST start a new session with a new InitializeRequest.
	// Re-initialize once and retry the original message once.
	var herr *streamableHTTPError
	if errors.As(err, &herr) && herr.status == http.StatusNotFound && herr.hadSession && req.Method != "initialize" {
		if rerr := t.reinitialize(ctx); rerr != nil {
			return fmt.Errorf("streamable HTTP session at %s expired (HTTP 404) and re-initialize failed: %w", t.endpoint, rerr)
		}
		if err := t.post(ctx, req, body, false); err != nil {
			return fmt.Errorf("streamable HTTP retry after session re-initialize: %w", err)
		}
		return nil
	}
	return err
}

// post performs one JSON-RPC POST and dispatches whatever comes back (202,
// single JSON message, or SSE stream) into the inbox.
func (t *StreamableHTTPTransport) post(ctx context.Context, req Request, body []byte, forceRefresh bool) error {
	t.mu.Lock()
	if t.closed {
		t.mu.Unlock()
		return fmt.Errorf("streamable HTTP transport for %s is closed", t.endpoint)
	}
	if req.Method == "initialize" && len(req.Params) > 0 {
		t.initParams = append(json.RawMessage(nil), req.Params...)
	}
	session := t.sessionID
	version := t.protocolVersion
	t.mu.Unlock()
	if requestVersion := requestProtocolVersion(req.Params); requestVersion != "" {
		version = requestVersion
		if requestVersion == PreferredProtocolVersion {
			session = ""
		}
	}

	// Tie the HTTP request to the transport lifetime so an SSE response body
	// that outlives this Send is not killed when the caller's ctx ends, while
	// still honoring caller cancellation during the synchronous phase.
	reqCtx, cancelReq := context.WithCancel(t.ctx)
	detach := context.AfterFunc(ctx, cancelReq)

	hreq, err := http.NewRequestWithContext(reqCtx, http.MethodPost, t.endpoint, bytes.NewReader(body))
	if err != nil {
		detach()
		cancelReq()
		return fmt.Errorf("streamable HTTP POST %s: %w", t.endpoint, err)
	}
	t.applyHeaders(hreq, session, version)
	if err := applyToken(hreq, t.authProvider, ctx, forceRefresh); err != nil {
		cancelReq()
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

	resp, err := t.client.Do(hreq)
	if err != nil {
		detach()
		cancelReq()
		return fmt.Errorf("streamable HTTP POST %s: %w", t.endpoint, err)
	}

	// The server may assign a session at initialization time by putting an
	// Mcp-Session-Id header on the response carrying the InitializeResult.
	if req.Method == "initialize" {
		if sid := strings.TrimSpace(resp.Header.Get(headerSessionID)); sid != "" {
			t.mu.Lock()
			t.sessionID = sid
			t.mu.Unlock()
		}
	}

	finish := func() {
		detach()
		cancelReq()
	}

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
		t.observeInitialize(req, msg)
		t.deliver(msg)
		return nil
	case "text/event-stream":
		// The stream carries one or more JSON-RPC messages and SHOULD
		// eventually include the response for the POSTed request. Consume it
		// in the background; from here on its lifetime is the transport's.
		detach()
		t.mu.Lock()
		if t.closed {
			t.mu.Unlock()
			cancelReq()
			resp.Body.Close()
			return fmt.Errorf("streamable HTTP transport for %s is closed", t.endpoint)
		}
		t.wg.Add(1)
		t.mu.Unlock()
		go t.consumeStream(resp.Body, cancelReq, req)
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
// requests via MCP-Protocol-Version) and start the GET listening stream.
func (t *StreamableHTTPTransport) observeInitialize(origin Request, msg Response) {
	if origin.Method != "initialize" || !msg.hasID || msg.ID != origin.ID || msg.StringID != origin.StringID || msg.Error != nil {
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
	if version != "" {
		t.mu.Lock()
		t.protocolVersion = version
		t.mu.Unlock()
	}
	t.startListener()
}

// consumeStream reads JSON-RPC messages off a POST-response SSE stream until
// it ends. origin is the request that opened the stream (needed to sniff an
// initialize response delivered over SSE).
func (t *StreamableHTTPTransport) consumeStream(body io.ReadCloser, cancel context.CancelFunc, origin Request) {
	defer t.wg.Done()
	defer cancel()
	defer body.Close()
	reader := bufio.NewReader(body)
	for {
		event, err := readSSEEvent(reader)
		if err != nil {
			return
		}
		if strings.TrimSpace(event.data) == "" {
			continue
		}
		var msg Response
		if err := json.Unmarshal([]byte(event.data), &msg); err != nil {
			continue // tolerate non-JSON-RPC events (keep-alives etc.)
		}
		t.observeInitialize(origin, msg)
		if !t.deliver(msg) {
			return
		}
	}
}

// deliver pushes a message to Receive. Returns false when the transport shut
// down before the message could be handed over.
func (t *StreamableHTTPTransport) deliver(msg Response) bool {
	select {
	case t.inbox <- msg:
		return true
	case <-t.ctx.Done():
		return false
	}
}

// reinitialize starts a new session after the server reported the old one
// expired: POST a fresh InitializeRequest without a session ID (spec, Session
// Management #4), adopt the new session/version, and re-send
// notifications/initialized. It is synchronous so the caller can retry its
// original request immediately afterwards.
func (t *StreamableHTTPTransport) reinitialize(ctx context.Context) error {
	t.mu.Lock()
	params := t.initParams
	t.sessionID = ""
	t.protocolVersion = ""
	t.mu.Unlock()
	if len(params) == 0 {
		return fmt.Errorf("no recorded initialize params")
	}

	initReq := Request{JSONRPC: "2.0", ID: nextRequestID(), Method: "initialize", Params: params}
	body, err := json.Marshal(initReq)
	if err != nil {
		return fmt.Errorf("marshal initialize: %w", err)
	}
	hreq, err := http.NewRequestWithContext(ctx, http.MethodPost, t.endpoint, bytes.NewReader(body))
	if err != nil {
		return err
	}
	t.applyHeaders(hreq, "", "")
	if err := applyToken(hreq, t.authProvider, ctx, false); err != nil {
		return err
	}
	hreq.Header.Set("Content-Type", "application/json")
	hreq.Header.Set("Accept", acceptStreamableHTTP)
	resp, err := t.client.Do(hreq)
	if err != nil {
		return fmt.Errorf("streamable HTTP POST %s: %w", t.endpoint, err)
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
	if sid := strings.TrimSpace(resp.Header.Get(headerSessionID)); sid != "" {
		t.mu.Lock()
		t.sessionID = sid
		t.mu.Unlock()
	}

	msg, err := readSingleResponse(resp, initReq.ID)
	if err != nil {
		return fmt.Errorf("initialize response from %s: %w", t.endpoint, err)
	}
	if msg.Error != nil {
		return msg.Error
	}
	t.observeInitialize(initReq, msg)

	note := Request{JSONRPC: "2.0", Method: "notifications/initialized"}
	noteBody, _ := json.Marshal(note)
	if err := t.post(ctx, note, noteBody, false); err != nil {
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
			if msg.ID == id {
				return msg, nil
			}
		}
	default:
		return Response{}, fmt.Errorf("unexpected content type %q", resp.Header.Get("Content-Type"))
	}
}

// startListener opens the optional GET SSE stream for server-initiated
// messages. See the file comment for why wuu needs it (tools/list_changed).
func (t *StreamableHTTPTransport) startListener() {
	t.mu.Lock()
	if t.listenerStarted || t.closed {
		t.mu.Unlock()
		return
	}
	t.listenerStarted = true
	t.wg.Add(1)
	t.mu.Unlock()
	go t.listen()
}

func (t *StreamableHTTPTransport) listen() {
	defer t.wg.Done()
	backoff := time.Second
	var lastEventID string
	for {
		if t.ctx.Err() != nil {
			return
		}
		hreq, err := http.NewRequestWithContext(t.ctx, http.MethodGet, t.endpoint, nil)
		if err != nil {
			return
		}
		t.mu.Lock()
		session, version := t.sessionID, t.protocolVersion
		t.mu.Unlock()
		t.applyHeaders(hreq, session, version)
		if err := applyToken(hreq, t.authProvider, t.ctx, false); err != nil {
			return
		}
		hreq.Header.Set("Accept", "text/event-stream")
		if lastEventID != "" {
			// Resumability (spec, Resumability and Redelivery #2): resume the
			// broken stream from the last SSE event ID the server tagged.
			hreq.Header.Set(headerLastEventID, lastEventID)
		}
		resp, err := t.client.Do(hreq)
		if err != nil {
			if t.ctx.Err() != nil {
				return
			}
			if !t.sleep(backoff) {
				return
			}
			backoff = minDuration(backoff*2, 30*time.Second)
			continue
		}
		if resp.StatusCode >= 400 && resp.StatusCode < 500 {
			// 405 = the server does not offer a GET stream at this endpoint
			// (allowed by spec); other 4xx are equally final. Stay quiet —
			// the stream is optional and POST traffic is unaffected.
			drainAndClose(resp.Body)
			return
		}
		contentType, _, _ := mime.ParseMediaType(resp.Header.Get("Content-Type"))
		if resp.StatusCode != http.StatusOK || contentType != "text/event-stream" {
			drainAndClose(resp.Body)
			if !t.sleep(backoff) {
				return
			}
			backoff = minDuration(backoff*2, 30*time.Second)
			continue
		}
		backoff = time.Second
		reader := bufio.NewReader(resp.Body)
		for {
			event, err := readSSEEvent(reader)
			if err != nil {
				resp.Body.Close()
				break
			}
			if event.id != "" {
				lastEventID = event.id
			}
			if strings.TrimSpace(event.data) == "" {
				continue
			}
			var msg Response
			if err := json.Unmarshal([]byte(event.data), &msg); err != nil {
				continue
			}
			if !t.deliver(msg) {
				resp.Body.Close()
				return
			}
		}
		if t.ctx.Err() != nil {
			return
		}
		if !t.sleep(backoff) {
			return
		}
	}
}

func (t *StreamableHTTPTransport) sleep(d time.Duration) bool {
	select {
	case <-time.After(d):
		return true
	case <-t.ctx.Done():
		return false
	}
}

func (t *StreamableHTTPTransport) Receive(ctx context.Context) (Response, error) {
	select {
	case <-ctx.Done():
		return Response{}, ctx.Err()
	case <-t.ctx.Done():
		return Response{}, io.EOF
	case msg := <-t.inbox:
		return msg, nil
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
	session := t.sessionID
	version := t.protocolVersion
	t.mu.Unlock()

	if session != "" {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		if hreq, err := http.NewRequestWithContext(ctx, http.MethodDelete, t.endpoint, nil); err == nil {
			t.applyHeaders(hreq, session, version)
			_ = applyToken(hreq, t.authProvider, ctx, false)
			if resp, err := t.client.Do(hreq); err == nil {
				drainAndClose(resp.Body)
			}
		}
		cancel()
	}

	t.cancel()
	t.wg.Wait()
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
