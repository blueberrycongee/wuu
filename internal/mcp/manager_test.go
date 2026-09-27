package mcp

import (
	"context"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"
	"time"

	"github.com/blueberrycongee/wuu/internal/extensions"
)

func TestManagerCloseCancelsActiveConnectionAndPreventsLateRegistration(t *testing.T) {
	requestStarted := make(chan struct{})
	releaseRequest := make(chan struct{})
	var startedOnce sync.Once
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		startedOnce.Do(func() { close(requestStarted) })
		<-releaseRequest
	}))
	defer server.Close()
	defer close(releaseRequest)

	manager := NewManager()
	addDone := make(chan error, 1)
	go func() {
		addDone <- manager.Add(context.Background(), ServerConfig{Name: "slow", URL: server.URL})
	}()

	select {
	case <-requestStarted:
	case <-time.After(2 * time.Second):
		t.Fatal("MCP initialize request did not start")
	}
	if err := manager.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}
	select {
	case err := <-addDone:
		if !errors.Is(err, ErrManagerClosed) {
			t.Fatalf("Add error = %v, want ErrManagerClosed", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("Close returned without the active Add finishing")
	}

	if got := manager.NativeTools(); len(got) != 0 {
		t.Fatalf("closed manager retained tools: %+v", got)
	}
	status := manager.Status()["slow"]
	if status.State != MCPServerStateStopped || status.Connected {
		t.Fatalf("status after Close = %+v, want stopped", status)
	}
	if err := manager.Connect(context.Background(), "slow"); !errors.Is(err, ErrManagerClosed) {
		t.Fatalf("Connect after Close error = %v, want ErrManagerClosed", err)
	}
}

func TestManagerCloseWaitsForDetachedClientClose(t *testing.T) {
	transport := newBlockingCloseTransport()
	client := &Client{name: "docs", transport: transport, inFlight: newInFlight()}
	client.readLoop = newReadLoop(transport, client.inFlight, client.handleNotification, client.handleRequest, client.handleReadLoopExit)
	client.readLoop.Start()

	manager := NewManager()
	manager.mu.Lock()
	manager.configs["docs"] = ServerConfig{Name: "docs", Command: "unused"}
	manager.clients["docs"] = client
	manager.statuses["docs"] = ServerStatus{Name: "docs", State: MCPServerStateConnected, Connected: true}
	manager.mu.Unlock()

	var releaseOnce sync.Once
	release := func() { releaseOnce.Do(func() { close(transport.release) }) }
	t.Cleanup(release)
	disconnectDone := make(chan error, 1)
	go func() { disconnectDone <- manager.Disconnect("docs") }()
	select {
	case <-transport.closeStarted:
	case <-time.After(2 * time.Second):
		t.Fatal("Disconnect did not start closing the client")
	}

	managerCloseDone := make(chan error, 1)
	go func() { managerCloseDone <- manager.Close() }()
	select {
	case err := <-managerCloseDone:
		t.Fatalf("Manager.Close returned before detached client cleanup finished: %v", err)
	case <-time.After(50 * time.Millisecond):
	}

	release()
	select {
	case err := <-disconnectDone:
		if err != nil {
			t.Fatalf("Disconnect: %v", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("Disconnect did not finish after releasing transport Close")
	}
	select {
	case err := <-managerCloseDone:
		if err != nil {
			t.Fatalf("Manager.Close: %v", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("Manager.Close did not finish after detached client cleanup")
	}
}

type blockingCloseTransport struct {
	closeStarted chan struct{}
	release      chan struct{}
	closed       chan struct{}
	closeOnce    sync.Once
}

func newBlockingCloseTransport() *blockingCloseTransport {
	return &blockingCloseTransport{
		closeStarted: make(chan struct{}),
		release:      make(chan struct{}),
		closed:       make(chan struct{}),
	}
}

func (t *blockingCloseTransport) Send(context.Context, Request) error { return nil }

func (t *blockingCloseTransport) Receive(ctx context.Context) (Response, error) {
	select {
	case <-ctx.Done():
		return Response{}, ctx.Err()
	case <-t.closed:
		return Response{}, io.EOF
	}
}

func (t *blockingCloseTransport) Close() error {
	t.closeOnce.Do(func() {
		close(t.closeStarted)
		<-t.release
		close(t.closed)
	})
	return nil
}

func TestManagerConfigureRecordsConfiguredAndDisabledStatuses(t *testing.T) {
	manager := NewManager()
	disabled := false
	manager.Configure(map[string]ServerConfig{
		"docs":     {Name: "docs", Command: "mcp-docs"},
		"disabled": {Name: "disabled", Command: "mcp-disabled", Enabled: &disabled},
	})

	status := manager.Status()
	if status["docs"].State != MCPServerStateConfigured || status["docs"].Connected {
		t.Fatalf("docs status = %+v, want configured", status["docs"])
	}
	if status["disabled"].State != MCPServerStateDisabled || status["disabled"].Connected {
		t.Fatalf("disabled status = %+v, want disabled", status["disabled"])
	}
}

func TestManagerNativeToolsAndGenerationTrackCatalogChanges(t *testing.T) {
	manager := NewManager()
	client := &Client{name: "docs", tools: []Tool{{Name: "search", InputSchema: []byte(`{"type":"object"}`)}}}
	manager.mu.Lock()
	manager.clients["docs"] = client
	manager.statuses["docs"] = ServerStatus{Name: "docs", State: MCPServerStateReady, Connected: true}
	manager.generation++
	manager.mu.Unlock()

	firstGeneration := manager.Generation()
	native := manager.NativeTools()
	if len(native) != 1 || native[0].Definition.Name != "search" || native[0].Client != client {
		t.Fatalf("NativeTools = %+v", native)
	}
	if native[0].Timeout <= 0 || native[0].Provenance.Kind != extensions.KindMCP {
		t.Fatalf("native metadata = %+v", native[0])
	}

	client.mu.Lock()
	client.tools = []Tool{{Name: "fetch"}}
	client.mu.Unlock()
	manager.catalogChanged("docs")
	if manager.Generation() <= firstGeneration {
		t.Fatalf("generation did not advance: %d -> %d", firstGeneration, manager.Generation())
	}
	native = manager.NativeTools()
	if len(native) != 1 || native[0].Definition.Name != "fetch" {
		t.Fatalf("stale native tools: %+v", native)
	}
}

func TestManagerConfigureRecordsAuthStatus(t *testing.T) {
	manager := NewManager()
	manager.Configure(map[string]ServerConfig{
		"headers": {Name: "headers", URL: "https://example.test/sse", Headers: map[string]string{"Authorization": "Bearer token"}},
		"oauth":   {Name: "oauth", URL: "https://example.test/sse", OAuth: &OAuthConfig{ClientID: "client"}},
		"stdio":   {Name: "stdio", Command: "mcp-docs"},
	})

	status := manager.Status()
	if status["headers"].AuthStatus != MCPAuthStatusBearerToken {
		t.Fatalf("headers auth status = %s, want bearer_token", status["headers"].AuthStatus)
	}
	if status["oauth"].AuthStatus != MCPAuthStatusNotLoggedIn {
		t.Fatalf("oauth auth status = %s, want not_logged_in", status["oauth"].AuthStatus)
	}
	if status["stdio"].AuthStatus != MCPAuthStatusUnsupported {
		t.Fatalf("stdio auth status = %s, want unsupported", status["stdio"].AuthStatus)
	}
}

func TestManagerFailedConnectRecordsFailedState(t *testing.T) {
	manager := NewManager()
	err := manager.Add(context.Background(), ServerConfig{Name: "broken", Command: ""})
	if err == nil {
		t.Fatal("expected Add to fail")
	}

	status := manager.Status()["broken"]
	if status.State != MCPServerStateFailed || status.Connected {
		t.Fatalf("unexpected failed status: %+v", status)
	}
	if status.Error == "" {
		t.Fatalf("failed status should include error: %+v", status)
	}
}

func TestManagerMarksUnexpectedClientFailure(t *testing.T) {
	server := newLegacySSEServer(t)
	manager := NewManager()
	t.Cleanup(func() { _ = manager.Close() })

	err := manager.Add(context.Background(), ServerConfig{
		Name:      "legacy",
		URL:       server.srv.URL + "/sse",
		Transport: TransportSSE,
	})
	if err != nil {
		t.Fatalf("Add: %v", err)
	}
	initialGeneration := manager.Generation()
	if len(manager.NativeTools()) != 1 {
		t.Fatalf("NativeTools before failure = %+v", manager.NativeTools())
	}

	server.srv.CloseClientConnections()
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		status := manager.Status()["legacy"]
		if status.State == MCPServerStateFailed && !status.Connected && status.Error != "" {
			if len(manager.NativeTools()) != 0 {
				t.Fatalf("NativeTools retained tools from failed client: %+v", manager.NativeTools())
			}
			if manager.Generation() <= initialGeneration {
				t.Fatalf("generation did not advance after failure: %d -> %d", initialGeneration, manager.Generation())
			}
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatalf("manager did not record unexpected client failure: %+v", manager.Status()["legacy"])
}

func TestClassifyConnectErrorDetectsNeedsAuth(t *testing.T) {
	err := &RPCError{Code: 401, Message: "unauthorized"}
	if got := classifyConnectError(err); got != MCPServerStateNeedsAuth {
		t.Fatalf("classifyConnectError = %s, want needs_auth", got)
	}
}

func TestManagerDisconnectPreservesOAuthAuthenticationState(t *testing.T) {
	manager := NewManager()
	manager.Configure(map[string]ServerConfig{
		"docs": {Name: "docs", URL: "https://example.test/mcp", OAuth: &OAuthConfig{ClientID: "client"}},
	})
	manager.mu.Lock()
	manager.statuses["docs"] = ServerStatus{Name: "docs", State: MCPServerStateStopped, AuthStatus: MCPAuthStatusOAuth}
	manager.mu.Unlock()

	if err := manager.Disconnect("docs"); err != nil {
		t.Fatalf("Disconnect: %v", err)
	}
	status := manager.Status()["docs"]
	if status.State != MCPServerStateStopped || status.AuthStatus != MCPAuthStatusOAuth {
		t.Fatalf("status after disconnect = %+v", status)
	}
}
