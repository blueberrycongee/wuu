package mcp

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"sync"
	"testing"
	"time"
)

func TestConnectHonorsInitializationDeadlineAndClosesTransport(t *testing.T) {
	transport := newScriptedTransport()
	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()

	client, err := Connect(ctx, "slow", transport)
	if client != nil {
		t.Fatal("Connect returned a client after the initialization deadline")
	}
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("Connect error = %v, want context deadline exceeded", err)
	}
	select {
	case <-transport.closed:
	default:
		t.Fatal("initialization timeout did not close the transport")
	}
}

func TestConnectRollsBackWhenInitializedNotificationFails(t *testing.T) {
	transport := newHandshakeTransport(errors.New("notification write failed"))

	client, err := Connect(context.Background(), "broken", transport)
	if client != nil {
		t.Fatal("Connect returned a client after the initialized notification failed")
	}
	if err == nil || !strings.Contains(err.Error(), "initialized notification") {
		t.Fatalf("Connect error = %v, want initialized notification failure", err)
	}
	select {
	case <-transport.closed:
	default:
		t.Fatal("initialized notification failure did not close the transport")
	}
}

func TestCallSendsCancelledNotificationOnContextCancel(t *testing.T) {
	transport := newScriptedTransport()
	f := newInFlight()

	// tools/call is never auto-answered by the scripted transport, so the
	// call blocks until the context is cancelled.
	ctx, cancel := context.WithCancel(context.Background())
	go func() {
		time.Sleep(10 * time.Millisecond)
		cancel()
	}()

	_, err := call(ctx, transport, f, "tools/call", map[string]any{"name": "slow"})
	if err == nil {
		t.Fatalf("expected cancellation error")
	}

	transport.mu.Lock()
	defer transport.mu.Unlock()
	var cancelled *Request
	for i := range transport.sent {
		if transport.sent[i].Method == "notifications/cancelled" {
			cancelled = &transport.sent[i]
			break
		}
	}
	if cancelled == nil {
		t.Fatalf("no notifications/cancelled sent; sent=%+v", transport.sent)
	}
	if cancelled.ID != 0 {
		t.Fatalf("cancelled notification must have no id, got %d", cancelled.ID)
	}
	var params cancelledParams
	if err := json.Unmarshal(cancelled.Params, &params); err != nil {
		t.Fatalf("decode cancelled params: %v", err)
	}
	if params.RequestID == 0 {
		t.Fatalf("cancelled notification missing requestId: %+v", params)
	}
}

func TestCallDoesNotCancelInitialize(t *testing.T) {
	transport := newScriptedTransport()
	f := newInFlight()

	ctx, cancel := context.WithCancel(context.Background())
	go func() {
		time.Sleep(10 * time.Millisecond)
		cancel()
	}()

	_, _ = call(ctx, transport, f, "initialize", map[string]any{})

	transport.mu.Lock()
	defer transport.mu.Unlock()
	for _, req := range transport.sent {
		if req.Method == "notifications/cancelled" {
			t.Fatalf("initialize must not be cancelled per MCP spec")
		}
	}
}

func TestClientRefreshesToolsOnListChangedNotification(t *testing.T) {
	transport := newScriptedTransport()
	client := &Client{
		name:      "server",
		transport: transport,
		inFlight:  newInFlight(),
	}
	client.readLoop = newReadLoop(transport, client.inFlight, client.handleNotification, client.handleRequest, client.handleReadLoopExit)
	client.readLoop.Start()
	t.Cleanup(func() { _ = client.Close() })

	tools, err := client.DiscoverTools(context.Background())
	if err != nil {
		t.Fatalf("initial DiscoverTools: %v", err)
	}
	if len(tools) != 1 || tools[0].Name != "initial" {
		t.Fatalf("initial tools = %+v", tools)
	}

	transport.notify(Response{JSONRPC: "2.0", Method: "notifications/tools/list_changed"})
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		tools = client.Tools()
		if len(tools) == 2 && tools[1].Name == "refreshed" {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatalf("tools did not refresh after notification: %+v", client.Tools())
}

func TestReadLoopRejectsUnsupportedServerRequest(t *testing.T) {
	transport := newScriptedTransport()
	client := &Client{
		name:      "server",
		transport: transport,
		inFlight:  newInFlight(),
	}
	client.readLoop = newReadLoop(transport, client.inFlight, client.handleNotification, client.handleRequest, client.handleReadLoopExit)
	client.readLoop.Start()
	t.Cleanup(func() { _ = client.Close() })

	transport.notify(Response{JSONRPC: "2.0", ID: 99, Method: "elicitation/create"})
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		if sent, ok := transport.sentResponse(99); ok {
			if sent.Error == nil || !strings.Contains(sent.Error.Message, "elicitation") {
				t.Fatalf("unexpected elicitation response: %+v", sent)
			}
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatal("read loop did not reject server request")
}

func TestReadLoopTerminalErrorFailsPendingCallAndStops(t *testing.T) {
	transport := newScriptedTransport()
	client := &Client{
		name:      "server",
		transport: transport,
		inFlight:  newInFlight(),
	}
	client.readLoop = newReadLoop(transport, client.inFlight, client.handleNotification, client.handleRequest, client.handleReadLoopExit)
	client.readLoop.Start()

	callErr := make(chan error, 1)
	go func() {
		_, err := client.CallTool(context.Background(), "slow", nil)
		callErr <- err
	}()

	deadline := time.Now().Add(2 * time.Second)
	for !transport.sentMethod("tools/call") && time.Now().Before(deadline) {
		time.Sleep(time.Millisecond)
	}
	if !transport.sentMethod("tools/call") {
		t.Fatal("tools/call was not sent")
	}

	if err := transport.Close(); err != nil {
		t.Fatalf("close transport: %v", err)
	}
	select {
	case err := <-callErr:
		if err == nil || !strings.Contains(err.Error(), "mcp transport receive") {
			t.Fatalf("CallTool error = %v, want terminal receive error", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("pending CallTool did not fail after transport termination")
	}
	select {
	case <-client.readLoop.stopped:
	case <-time.After(2 * time.Second):
		t.Fatal("read loop did not stop after transport termination")
	}
	if got := transport.receiveCount(); got != 1 {
		t.Fatalf("Receive called %d times, want 1; read loop may be retrying a terminal error", got)
	}
	if err := client.Close(); err != nil {
		t.Fatalf("close client after terminal error: %v", err)
	}
}

func TestStdioEnvOverlay(t *testing.T) {
	got := mergeProcessEnv([]string{"A=1", "B=2"}, map[string]string{
		"B": "override",
		"C": "3",
	})
	values := map[string]string{}
	for _, item := range got {
		key, value, ok := strings.Cut(item, "=")
		if ok {
			values[key] = value
		}
	}
	if values["A"] != "1" || values["B"] != "override" || values["C"] != "3" {
		t.Fatalf("unexpected env overlay: %+v", values)
	}
}

type scriptedTransport struct {
	mu           sync.Mutex
	listCalls    int
	receiveCalls int
	inbox        chan Response
	closed       chan struct{}
	sent         []Request
}

type handshakeTransport struct {
	*scriptedTransport
	initializedErr error
}

func newHandshakeTransport(initializedErr error) *handshakeTransport {
	return &handshakeTransport{scriptedTransport: newScriptedTransport(), initializedErr: initializedErr}
}

func (t *handshakeTransport) Send(ctx context.Context, req Request) error {
	if req.Method == "server/discover" {
		t.mu.Lock()
		t.sent = append(t.sent, req)
		t.mu.Unlock()
		t.inbox <- Response{JSONRPC: "2.0", ID: req.ID, Error: &RPCError{Code: -32601, Message: "method not found"}}
		return nil
	}
	if req.Method == "initialize" {
		t.mu.Lock()
		t.sent = append(t.sent, req)
		t.mu.Unlock()
		result, _ := json.Marshal(InitializeResult{ProtocolVersion: PreferredLegacyProtocolVersion})
		t.inbox <- Response{JSONRPC: "2.0", ID: req.ID, Result: result}
		return nil
	}
	if req.Method == "notifications/initialized" {
		return t.initializedErr
	}
	return t.scriptedTransport.Send(ctx, req)
}

func newScriptedTransport() *scriptedTransport {
	return &scriptedTransport{
		inbox:  make(chan Response, 8),
		closed: make(chan struct{}),
	}
}

func (t *scriptedTransport) Send(_ context.Context, req Request) error {
	t.mu.Lock()
	t.sent = append(t.sent, req)
	t.mu.Unlock()
	if req.Method != "tools/list" {
		return nil
	}
	t.mu.Lock()
	t.listCalls++
	call := t.listCalls
	t.mu.Unlock()

	tools := []Tool{{Name: "initial"}}
	if call > 1 {
		tools = []Tool{{Name: "initial"}, {Name: "refreshed"}}
	}
	result, _ := json.Marshal(ListToolsResult{Tools: tools})
	t.inbox <- Response{JSONRPC: "2.0", ID: req.ID, Result: result}
	return nil
}

func (t *scriptedTransport) Receive(ctx context.Context) (Response, error) {
	t.mu.Lock()
	t.receiveCalls++
	t.mu.Unlock()
	select {
	case <-ctx.Done():
		return Response{}, ctx.Err()
	case <-t.closed:
		return Response{}, context.Canceled
	case resp := <-t.inbox:
		return resp, nil
	}
}

func (t *scriptedTransport) Close() error {
	select {
	case <-t.closed:
	default:
		close(t.closed)
	}
	return nil
}

func (t *scriptedTransport) notify(resp Response) {
	t.inbox <- resp
}

func (t *scriptedTransport) sentResponse(id int64) (Request, bool) {
	t.mu.Lock()
	defer t.mu.Unlock()
	for _, req := range t.sent {
		if req.ID == id {
			return req, true
		}
	}
	return Request{}, false
}

func (t *scriptedTransport) sentMethod(method string) bool {
	t.mu.Lock()
	defer t.mu.Unlock()
	for _, req := range t.sent {
		if req.Method == method {
			return true
		}
	}
	return false
}

func (t *scriptedTransport) receiveCount() int {
	t.mu.Lock()
	defer t.mu.Unlock()
	return t.receiveCalls
}
