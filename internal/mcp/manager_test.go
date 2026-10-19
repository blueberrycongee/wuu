package mcp

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
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
	for _, action := range []string{"disconnect", "refresh"} {
		t.Run(action, func(t *testing.T) {
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
			retired := make(chan string, 1)
			var retirementOnce sync.Once
			manager.SetConnectionRetiredCallback(func(name, connectionID string) {
				// Reading status proves cleanup is outside the manager lock.
				_ = manager.Status()
				retirementOnce.Do(func() { retired <- name })
			})

			var releaseOnce sync.Once
			release := func() { releaseOnce.Do(func() { close(transport.release) }) }
			t.Cleanup(release)
			disconnectDone := make(chan error, 1)
			go func() {
				if action == "refresh" {
					disconnectDone <- manager.Refresh(context.Background(), "docs")
				} else {
					disconnectDone <- manager.Disconnect("docs")
				}
			}()
			select {
			case <-transport.closeStarted:
			case <-time.After(2 * time.Second):
				t.Fatal("Disconnect did not start closing the client")
			}

			select {
			case name := <-retired:
				if name != "docs" {
					t.Fatalf("retired provider = %q", name)
				}
			default:
				t.Fatal("provider cleanup started before ownership retirement")
			}

			if action == "refresh" {
				if err := manager.Disconnect("docs"); err != nil {
					t.Fatal(err)
				}
				release()
				select {
				case err := <-disconnectDone:
					if !errors.Is(err, context.Canceled) {
						t.Fatalf("superseded Refresh = %v", err)
					}
				case <-time.After(2 * time.Second):
					t.Fatal("superseded Refresh did not return after detached client cleanup")
				}
				if status := manager.Status()["docs"]; status.State != MCPServerStateStopped || status.Connected {
					t.Fatalf("Refresh resumed after Disconnect: %+v", status)
				}
				if err := manager.Close(); err != nil {
					t.Fatal(err)
				}
				return
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

		})
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
	manager.servers["docs"] = &serverLifecycle{generation: 1}
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
	manager.catalogChanged("docs", 1, client)
	if manager.Generation() <= firstGeneration {
		t.Fatalf("generation did not advance: %d -> %d", firstGeneration, manager.Generation())
	}
	native = manager.NativeTools()
	if len(native) != 1 || native[0].Definition.Name != "fetch" {
		t.Fatalf("stale native tools: %+v", native)
	}

	manager.mu.Lock()
	manager.servers["docs"].generation++
	manager.clients["docs"] = &Client{name: "docs", tools: []Tool{{Name: "replacement"}}}
	manager.statuses["docs"] = ServerStatus{Name: "docs", State: MCPServerStateConnecting}
	manager.mu.Unlock()
	generation := manager.Generation()
	manager.catalogChanged("docs", 1, client)
	retirements := 0
	manager.SetConnectionRetiredCallback(func(string, string) { retirements++ })
	manager.clientConnectionFailed("docs", 1, client, errors.New("late connection failure"))
	if retirements != 0 {
		t.Fatal("retired client callback revoked replacement ownership")
	}
	if err := manager.recordConnectionStatus(1, ServerStatus{Name: "docs", State: MCPServerStateAuthRequired}, true); !errors.Is(err, context.Canceled) {
		t.Fatalf("retired OAuth failure = %v, want cancellation without closing the replacement", err)
	}
	if manager.Generation() != generation {
		t.Fatal("retired client callback changed the current catalog generation")
	}
	manager.mu.RLock()
	status := manager.statuses["docs"]
	manager.mu.RUnlock()
	if status.State != MCPServerStateConnecting {
		t.Fatalf("retired client callback replaced a newer attempt's status: %+v", status)
	}
	if native := manager.NativeTools(); len(native) != 1 || native[0].Definition.Name != "replacement" {
		t.Fatalf("retired client callback changed the current tools: %+v", native)
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
	retired := make(chan string, 1)
	var retirementOnce sync.Once
	manager.SetConnectionRetiredCallback(func(name, connectionID string) {
		_ = manager.Status()
		retirementOnce.Do(func() { retired <- name })
	})
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
	select {
	case name := <-retired:
		if name != "legacy" {
			t.Fatalf("retired provider = %q", name)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("unexpected provider failure did not retire owned resources")
	}
	status := manager.Status()["legacy"]
	if status.State != MCPServerStateFailed || status.Connected || status.Error == "" {
		t.Fatalf("manager did not record unexpected client failure: %+v", status)
	}
	if len(manager.NativeTools()) != 0 {
		t.Fatalf("NativeTools retained tools from failed client: %+v", manager.NativeTools())
	}
	if manager.Generation() <= initialGeneration {
		t.Fatalf("generation did not advance after failure: %d -> %d", initialGeneration, manager.Generation())
	}
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

func TestManagerRevokeFencesPendingConnectionsAndPreservesOtherServers(t *testing.T) {
	for _, action := range []string{"revoke", "disconnect", "connect", "refresh"} {
		for _, blockedMethod := range []string{"initialize", "tools/list"} {
			t.Run(action+"/"+blockedMethod, func(t *testing.T) {
				started := make(chan struct{})
				release := make(chan struct{})
				var once sync.Once
				server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
					if r.Method != http.MethodPost {
						w.WriteHeader(http.StatusMethodNotAllowed)
						return
					}
					var req Request
					if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
						http.Error(w, err.Error(), http.StatusBadRequest)
						return
					}
					if r.URL.Path == "/retired" && req.Method == blockedMethod {
						blocked := false
						once.Do(func() { blocked = true; close(started) })
						if blocked {
							<-release
						}
					}
					w.Header().Set("Content-Type", "application/json")
					switch req.Method {
					case "server/discover":
						fmt.Fprintf(w, `{"jsonrpc":"2.0","id":%d,"error":{"code":-32601,"message":"unsupported"}}`, req.ID)
					case "initialize":
						fmt.Fprint(w, initializeResultJSON(req.ID, "2025-06-18"))
					case "tools/list":
						fmt.Fprint(w, toolsListResultJSON(req.ID, "search"))
					default:
						w.WriteHeader(http.StatusAccepted)
					}
				}))
				defer server.Close()
				defer close(release)
				manager := NewManager()
				defer manager.Close()
				retired := ServerConfig{Name: "retired", URL: server.URL + "/retired", Transport: TransportStreamableHTTP}
				retained := ServerConfig{Name: "retained", URL: server.URL + "/retained", Transport: TransportStreamableHTTP}
				if err := manager.Add(context.Background(), retained); err != nil {
					t.Fatal(err)
				}
				finished := make(chan error, 1)
				go func() { finished <- manager.Add(context.Background(), retired) }()
				select {
				case <-started:
				case err := <-finished:
					t.Fatalf("connection ended before blocked request: %v", err)
				case <-time.After(2 * time.Second):
					t.Fatal("pending connection did not reach blocked request")
				}
				var stopErr error
				wantErr := error(context.Canceled)
				switch action {
				case "revoke":
					stopErr = manager.Revoke(retired.Name)
					wantErr = ErrServerRevoked
				case "disconnect":
					stopErr = manager.Disconnect(retired.Name)
				case "connect":
					stopErr = manager.Connect(context.Background(), retired.Name)
				case "refresh":
					stopErr = manager.Refresh(context.Background(), retired.Name)
				}
				if stopErr != nil {
					t.Fatal(stopErr)
				}
				select {
				case err := <-finished:
					if !errors.Is(err, wantErr) {
						t.Fatalf("pending Add = %v, want %v", err, wantErr)
					}
				case <-time.After(2 * time.Second):
					t.Fatalf("%s did not cancel the pending connection", action)
				}
				if action != "revoke" {
					wantState := MCPServerStateConnected
					if action == "disconnect" {
						wantState = MCPServerStateStopped
					}
					if status := manager.Status()[retired.Name]; status.State != wantState {
						t.Fatalf("%s status = %+v, want %s", action, status, wantState)
					}
					if err := manager.Connect(context.Background(), retired.Name); err != nil {
						t.Fatalf("explicit reconnect after %s: %v", action, err)
					}
					if tools := manager.NativeTools(); len(tools) != 2 {
						t.Fatalf("%s lost reconnected or unrelated tools: %+v", action, tools)
					}
					if err := manager.Disconnect(retired.Name); err != nil {
						t.Fatal(err)
					}
					if tools := manager.NativeTools(); len(tools) != 1 || tools[0].Provenance.Source != retained.Name {
						t.Fatalf("disconnect affected unrelated tools: %+v", tools)
					}
					return
				}
				manager.Configure(map[string]ServerConfig{retired.Name: retired})
				for name, attempt := range map[string]func() error{
					"Add":     func() error { return manager.Add(context.Background(), retired) },
					"Connect": func() error { return manager.Connect(context.Background(), retired.Name) },
					"Refresh": func() error { return manager.Refresh(context.Background(), retired.Name) },
				} {
					if err := attempt(); !errors.Is(err, ErrServerRevoked) {
						t.Fatalf("%s after revoke = %v, want ErrServerRevoked", name, err)
					}
				}
				if cfg, ok := manager.Config(retired.Name); !ok || cfg.IsEnabled() {
					t.Fatalf("revoked configuration resurrected: %+v, %v", cfg, ok)
				}
				if status := manager.Status()[retired.Name]; status.State != MCPServerStateDisabled || status.Connected {
					t.Fatalf("revoked status resurrected: %+v", status)
				}
				if err := manager.Refresh(context.Background(), ""); err != nil {
					t.Fatalf("refresh unrelated servers: %v", err)
				}
				if tools := manager.NativeTools(); len(tools) != 1 || tools[0].Provenance.Source != retained.Name {
					t.Fatalf("revocation affected unrelated tools: %+v", tools)
				}
				if err := manager.Revoke(retained.Name); err != nil {
					t.Fatal(err)
				}
				if tools := manager.NativeTools(); len(tools) != 0 {
					t.Fatalf("active revoked client still publishes tools: %+v", tools)
				}
				generation := manager.Generation()
				if err := manager.Revoke(retained.Name); err != nil || manager.Generation() != generation {
					t.Fatalf("repeat revocation changed generation or failed: %v", err)
				}
			})
		}
	}

}
