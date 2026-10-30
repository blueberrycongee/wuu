package mcp

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/blueberrycongee/wuu/internal/credentialstore"
)

func TestManagerOAuthLifecycleControlsConnectionAuthState(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	var foreignURL string
	newResource := func(transport, token string) (*httptest.Server, *atomic.Int64) {
		var baseURL string
		var requests atomic.Int64
		messages := make(chan string, 16)
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			switch r.URL.Path {
			case "/route/redirect":
				http.Redirect(w, r, "/nested/stream", http.StatusTemporaryRedirect)
				return
			case "/redirect-other":
				http.Redirect(w, r, foreignURL, http.StatusTemporaryRedirect)
				return
			case "/announce-other":
				w.Header().Set("Content-Type", "text/event-stream")
				fmt.Fprintf(w, "event: endpoint\ndata: %s/message\n\n", foreignURL)
				w.(http.Flusher).Flush()
				<-r.Context().Done()
				return
			case "/.well-known/oauth-protected-resource":
				_ = json.NewEncoder(w).Encode(map[string]any{"resource": baseURL, "authorization_servers": []string{baseURL}})
				return
			case "/.well-known/oauth-authorization-server":
				_ = json.NewEncoder(w).Encode(map[string]any{
					"issuer": baseURL, "authorization_endpoint": baseURL + "/authorize", "token_endpoint": baseURL + "/token", "registration_endpoint": baseURL + "/register",
				})
				return
			case "/register":
				_ = json.NewEncoder(w).Encode(map[string]any{"client_id": "dynamic-client"})
				return
			case "/token":
				_ = json.NewEncoder(w).Encode(map[string]any{"access_token": token, "token_type": "Bearer", "expires_in": 3600})
				return
			}
			requests.Add(1)
			if r.Header.Get("Authorization") != "Bearer "+token {
				w.WriteHeader(http.StatusUnauthorized)
				return
			}
			if r.Method == http.MethodGet {
				if transport != TransportSSE {
					w.WriteHeader(http.StatusMethodNotAllowed)
					return
				}
				w.Header().Set("Content-Type", "text/event-stream")
				fmt.Fprint(w, "event: endpoint\ndata: message\n\n")
				w.(http.Flusher).Flush()
				for {
					select {
					case message := <-messages:
						fmt.Fprintf(w, "data: %s\n\n", message)
						w.(http.Flusher).Flush()
					case <-r.Context().Done():
						return
					}
				}
			}
			if r.Method == http.MethodDelete {
				w.WriteHeader(http.StatusOK)
				return
			}
			if transport == TransportSSE && r.URL.Path != "/message" && r.URL.Path != "/nested/message" {
				http.NotFound(w, r)
				return
			}
			var req Request
			if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
				http.Error(w, err.Error(), http.StatusBadRequest)
				return
			}
			var result string
			switch req.Method {
			case "initialize":
				w.Header().Set(headerSessionID, "synthetic-session")
				result = initializeResultJSON(req.ID, "2025-03-26")
			case "tools/list":
				result = toolsListResultJSON(req.ID, "echo")
			case "tools/call":
				result = fmt.Sprintf(`{"jsonrpc":"2.0","id":%d,"result":{"content":[{"type":"text","text":"ok"}]}}`, req.ID)
			default:
				if req.ID == 0 {
					w.WriteHeader(http.StatusAccepted)
					return
				}
				result = fmt.Sprintf(`{"jsonrpc":"2.0","id":%d,"error":{"code":-32601,"message":"unsupported"}}`, req.ID)
			}
			if transport == TransportSSE {
				messages <- result
				w.WriteHeader(http.StatusAccepted)
				return
			}
			w.Header().Set("Content-Type", "application/json")
			fmt.Fprint(w, result)
		}))
		baseURL = server.URL
		t.Cleanup(server.Close)
		return server, &requests
	}
	server, requests := newResource(TransportStreamableHTTP, "manager-secret")
	replacement, replacementRequests := newResource(TransportSSE, "replacement-secret")
	foreignURL = replacement.URL
	oauth := NewOAuthManager(credentialstore.NewFileStore(filepath.Join(t.TempDir(), "oauth.json")), server.Client())
	manager := NewManager()
	t.Cleanup(func() { _ = manager.Close() })
	manager.SetOAuthManager(oauth)
	cfg := ServerConfig{Name: "docs", URL: server.URL, Transport: TransportStreamableHTTP,
		Headers: map[string]string{"authorization": "Bearer configured-header"},
		OAuth:   &OAuthConfig{ClientID: "desktop-client", RedirectURI: "http://127.0.0.1/callback", Scopes: []string{"tools"}},
	}
	manager.Configure(map[string]ServerConfig{"docs": cfg})

	if err := manager.Connect(ctx, "docs"); !errors.Is(err, ErrOAuthRequired) {
		t.Fatalf("Connect without credentials = %v, want ErrOAuthRequired", err)
	}
	status := manager.Status()["docs"]
	if status.State != MCPServerStateAuthRequired || status.AuthStatus != MCPAuthStatusNotLoggedIn {
		t.Fatalf("status before auth = %+v", status)
	}
	start, err := manager.StartOAuth(ctx, "docs")
	if err != nil {
		t.Fatalf("StartOAuth: %v", err)
	}
	status, err = manager.FinishOAuth(ctx, "docs", start.State, "code-1")
	if err != nil || status.AuthStatus != MCPAuthStatusOAuth || status.State != MCPServerStateStopped {
		t.Fatalf("status after auth = %+v, %v", status, err)
	}
	encoded, err := json.Marshal(status)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(encoded), "manager-secret") {
		t.Fatalf("status leaked token: %s", encoded)
	}
	if err := manager.Connect(ctx, "docs"); err != nil {
		t.Fatalf("HTTP Connect after login: %v", err)
	}
	if err := manager.Refresh(ctx, "docs"); err != nil {
		t.Fatalf("same-resource reconnect: %v", err)
	}
	originalTools := manager.NativeTools()
	if len(originalTools) != 1 {
		t.Fatalf("original tool count = %d", len(originalTools))
	}
	if saved, _ := manager.Config("docs"); saved.Headers["authorization"] != "Bearer configured-header" || saved.Headers["Authorization"] != "" {
		t.Fatal("OAuth changed the configured headers")
	}

	// Another project's manager can reuse the name, but not its credentials.
	other := NewManager()
	t.Cleanup(func() { _ = other.Close() })
	other.SetOAuthManager(oauth)
	replacementCfg := cfg
	replacementCfg.URL = replacement.URL
	replacementCfg.Transport = TransportSSE
	replacementCfg.OAuth = nil // Auto-discovered OAuth must enforce the same boundary.
	other.Configure(map[string]ServerConfig{"docs": replacementCfg})
	auth, err := other.OAuthStatus(ctx, "docs")
	if err != nil || auth.Authenticated {
		t.Fatalf("replacement auth status = %+v, %v", auth, err)
	}
	if err := other.Connect(ctx, "docs"); !errors.Is(err, ErrOAuthRequired) {
		t.Fatalf("replacement SSE Connect = %v, want ErrOAuthRequired", err)
	}
	if got := replacementRequests.Load(); got != 0 {
		t.Fatalf("replacement received %d requests before its own login", got)
	}
	if token, err := oauth.AccessToken(ctx, "docs", cfg.URL); err != nil || token != "manager-secret" {
		t.Fatalf("original credentials were not preserved: %v", err)
	}
	start, err = other.StartOAuth(ctx, "docs")
	if err != nil {
		t.Fatal(err)
	}
	other.Configure(map[string]ServerConfig{"docs": cfg})
	if _, err := other.FinishOAuth(ctx, "docs", start.State, "stale-code"); !errors.Is(err, ErrOAuthRequired) {
		t.Fatalf("FinishOAuth after URL change = %v, want ErrOAuthRequired", err)
	}
	other.Configure(map[string]ServerConfig{"docs": replacementCfg})
	start, err = other.StartOAuth(ctx, "docs")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := other.FinishOAuth(ctx, "docs", start.State, "replacement-code"); err != nil {
		t.Fatal(err)
	}
	if err := other.Connect(ctx, "docs"); err != nil {
		t.Fatalf("SSE Connect after its own login: %v", err)
	}
	if tools := other.NativeTools(); len(tools) != 1 {
		t.Fatalf("replacement tool count = %d", len(tools))
	} else if _, err := tools[0].Client.CallTool(ctx, "echo", nil); err != nil {
		t.Fatalf("SSE tool call: %v", err)
	}
	before := requests.Load()
	if _, err := originalTools[0].Client.CallTool(ctx, "echo", nil); !errors.Is(err, ErrOAuthRequired) {
		t.Fatalf("old HTTP connection used replacement credentials: %v", err)
	}
	if got := requests.Load(); got != before {
		t.Fatalf("old HTTP resource received %d requests with replacement credentials", got-before)
	}
	if err := manager.Connect(ctx, "docs"); !errors.Is(err, ErrOAuthRequired) {
		t.Fatalf("original HTTP reconnect = %v, want ErrOAuthRequired", err)
	}
	if got := requests.Load(); got != before {
		t.Fatalf("rejected reconnect cleanup sent %d requests", got-before)
	}
	status = manager.Status()["docs"]
	if status.State != MCPServerStateAuthRequired || status.Connected {
		t.Fatalf("rejected reconnect status = %+v", status)
	}
	if err := other.RemoveOAuth(ctx, "docs"); err != nil {
		t.Fatalf("RemoveOAuth: %v", err)
	}
	status = other.Status()["docs"]
	if status.AuthStatus != MCPAuthStatusNotLoggedIn || status.State != MCPServerStateAuthRequired {
		t.Fatalf("status after remove = %+v", status)
	}
	// A resource can route within its origin, but neither an SSE announcement
	// nor a redirect may transfer its credentials to another loopback port.
	for _, resource := range []struct {
		server    *httptest.Server
		transport string
		token     string
	}{{server, TransportStreamableHTTP, "manager-secret"}, {replacement, TransportSSE, "replacement-secret"}} {
		provider := func(context.Context, bool) (string, error) { return resource.token, nil }
		client, err := connectRemoteWithTokenProvider(ctx, ServerConfig{Name: "redirect", URL: resource.server.URL + "/route/redirect", Transport: resource.transport}, provider)
		if err != nil {
			t.Fatalf("%s same-origin redirect: %v", resource.transport, err)
		}
		if _, err := client.DiscoverTools(ctx); err != nil {
			t.Fatalf("%s same-origin redirected discovery: %v", resource.transport, err)
		}
		_ = client.Close()
	}
	before = replacementRequests.Load()
	for _, transport := range []string{TransportStreamableHTTP, TransportSSE} {
		provider := func(context.Context, bool) (string, error) { return "manager-secret", nil }
		client, err := connectRemoteWithTokenProvider(ctx, ServerConfig{Name: "redirect", URL: server.URL + "/redirect-other", Transport: transport}, provider)
		if client != nil {
			_ = client.Close()
		}
		if err == nil {
			t.Fatalf("%s accepted a cross-origin authenticated redirect", transport)
		}
		client, err = ConnectRemote(ctx, ServerConfig{Name: "redirect", URL: server.URL + "/redirect-other", Transport: transport, Headers: map[string]string{"X-Service-Secret": "synthetic-header"}})
		if client != nil {
			_ = client.Close()
		}
		if err == nil {
			t.Fatalf("%s redirected configured headers across origins", transport)
		}
	}
	if _, err := oauth.Start(ctx, OAuthStartOptions{
		ServerID: "redirect", ResourceURL: server.URL + "/redirect-other", RedirectURI: "http://127.0.0.1/callback",
		Headers: map[string]string{"X-Service-Secret": "synthetic-header"},
	}); err == nil {
		t.Fatal("OAuth discovery accepted a credential-bearing cross-origin redirect")
	}
	client, err := ConnectSSE(ctx, ServerConfig{Name: "announcement", URL: server.URL + "/announce-other"})
	if client != nil {
		_ = client.Close()
	}
	if err == nil {
		t.Fatal("SSE accepted a cross-origin message endpoint")
	}
	if got := replacementRequests.Load(); got != before {
		t.Fatalf("untrusted origin received %d redirected/announced requests", got-before)
	}
	t.Log("HTTP/SSE login, discovery, reconnect, audience and stale-login rejection, retained connections, sign-out, same-origin routing, and cross-port destination rejection passed")
}

func TestOAuthPKCEDynamicRegistrationFinishRefreshAndRemove(t *testing.T) {
	var baseURL string
	var tokenCalls atomic.Int64
	var refreshReplacement atomic.Value
	var refreshSucceeds atomic.Bool
	var holdRefresh atomic.Bool
	refreshStarted := make(chan struct{})
	refreshRelease := make(chan struct{}, 1)
	finishExchanged := make(chan struct{})
	var redirectRefresh atomic.Bool
	var redirectedRequests atomic.Int64
	redirectTarget := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		redirectedRequests.Add(1)
		w.WriteHeader(http.StatusBadRequest)
	}))
	defer redirectTarget.Close()
	store := credentialstore.NewFileStore(filepath.Join(t.TempDir(), "oauth.json"))
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/.well-known/oauth-protected-resource", "/.well-known/oauth-protected-resource/replacement":
			resource := baseURL
			if strings.HasSuffix(r.URL.Path, "/replacement") {
				resource += "/replacement"
			}
			_ = json.NewEncoder(w).Encode(map[string]any{"resource": resource, "authorization_servers": []string{baseURL}})
		case "/.well-known/oauth-authorization-server":
			_ = json.NewEncoder(w).Encode(map[string]any{
				"issuer": baseURL, "authorization_endpoint": baseURL + "/authorize", "token_endpoint": baseURL + "/token", "registration_endpoint": baseURL + "/register",
			})
		case "/register":
			_ = json.NewEncoder(w).Encode(map[string]any{"client_id": "dynamic-client"})
		case "/token":
			tokenCalls.Add(1)
			_ = r.ParseForm()
			if r.Form.Get("grant_type") == "authorization_code" && r.Form.Get("code_verifier") == "" {
				t.Error("authorization code exchange omitted PKCE verifier")
				http.Error(w, "missing verifier", http.StatusBadRequest)
				return
			}
			if r.Form.Get("grant_type") == "refresh_token" {
				if holdRefresh.Load() {
					close(refreshStarted)
					select {
					case <-refreshRelease:
					case <-r.Context().Done():
						return
					}
				}
				if r.Form.Get("resource") != baseURL {
					t.Error("refresh omitted its original resource")
				}
				if redirectRefresh.Load() {
					http.Redirect(w, r, redirectTarget.URL, http.StatusTemporaryRedirect)
					return
				}
				if resource, _ := refreshReplacement.Load().(string); resource != "" {
					data, _ := json.Marshal(storedOAuthToken{AccessToken: "rotated-by-another-process", ResourceURL: resource})
					if err := store.Set(r.Context(), oauthNamespace("docs"), "oauth", data); err != nil {
						t.Error(err)
					}
					if !refreshSucceeds.Load() {
						http.Error(w, "refresh already rotated", http.StatusBadRequest)
						return
					}
				}
			}
			_ = json.NewEncoder(w).Encode(map[string]any{
				"access_token": "access-" + r.Form.Get("grant_type"), "refresh_token": "refresh-rotated", "token_type": "Bearer", "expires_in": 3600,
			})
			if r.Form.Get("code") == "replacement-code" {
				w.(http.Flusher).Flush()
				close(finishExchanged)
			}
		default:
			http.NotFound(w, r)
		}
	}))
	defer server.Close()
	defer func() {
		select {
		case refreshRelease <- struct{}{}:
		default:
		}
	}()
	baseURL = server.URL

	oauth := NewOAuthManager(store, server.Client())
	start, err := oauth.Start(context.Background(), OAuthStartOptions{ServerID: "docs", ResourceURL: baseURL, RedirectURI: "http://127.0.0.1/callback", Scopes: []string{"tools"}})
	if err != nil {
		t.Fatalf("Start: %v", err)
	}
	authURL, err := url.Parse(start.AuthorizationURL)
	if err != nil {
		t.Fatal(err)
	}
	query := authURL.Query()
	if query.Get("client_id") != "dynamic-client" || query.Get("state") == "" || query.Get("code_challenge") == "" || query.Get("code_challenge_method") != "S256" {
		t.Fatalf("authorization URL missing OAuth/PKCE fields: %s", start.AuthorizationURL)
	}

	status, err := oauth.Finish(context.Background(), OAuthFinishOptions{ServerID: "docs", ResourceURL: baseURL, State: start.State, Code: "code-1"})
	if err != nil || !status.Authenticated {
		t.Fatalf("Finish status = %+v, %v", status, err)
	}
	token, err := oauth.AccessToken(context.Background(), "docs", baseURL)
	if err != nil || !strings.HasPrefix(token, "access-") {
		t.Fatalf("AccessToken = %q, %v", token, err)
	}
	for _, resource := range []string{baseURL + "/other", baseURL + "?tenant=other", ""} {
		if token, err := oauth.AccessToken(context.Background(), "docs", resource); token != "" || !errors.Is(err, ErrOAuthRequired) {
			t.Fatalf("AccessToken for another resource = %q, %v", token, err)
		}
		if token, err := oauth.RefreshAccessToken(context.Background(), "docs", resource); token != "" || !errors.Is(err, ErrOAuthRequired) {
			t.Fatalf("RefreshAccessToken for another resource = %q, %v", token, err)
		}
	}
	if tokenCalls.Load() != 1 {
		t.Fatalf("resource mismatch refreshed a credential: %d token calls", tokenCalls.Load())
	}
	if token, err := oauth.AccessToken(context.Background(), "docs", strings.Replace(baseURL, "http:", "HTTP:", 1)+"/"); token == "" || err != nil {
		t.Fatalf("equivalent resource spelling: %v", err)
	}
	saved, err := oauth.loadToken(context.Background(), "docs")
	if err != nil {
		t.Fatal(err)
	}
	saved.ExpiresAt = time.Now().Add(-time.Second)
	if err := oauth.saveToken(context.Background(), "docs", saved); err != nil {
		t.Fatal(err)
	}
	token, err = oauth.AccessToken(context.Background(), "docs", baseURL)
	if err != nil || token != "access-refresh_token" || tokenCalls.Load() < 2 {
		t.Fatalf("refreshed token = %q calls=%d err=%v", token, tokenCalls.Load(), err)
	}
	saved, err = oauth.loadToken(context.Background(), "docs")
	if err != nil {
		t.Fatal(err)
	}
	refreshReplacement.Store(baseURL + "/another-resource")
	if token, err := oauth.RefreshAccessToken(context.Background(), "docs", baseURL); token != "" || !errors.Is(err, ErrOAuthRequired) {
		t.Fatalf("refresh fallback used another resource: token=%q err=%v", token, err)
	}
	if err := oauth.saveToken(context.Background(), "docs", saved); err != nil {
		t.Fatal(err)
	}
	refreshSucceeds.Store(true)
	if token, err := oauth.RefreshAccessToken(context.Background(), "docs", baseURL); token != "" || !errors.Is(err, ErrOAuthRequired) {
		t.Fatalf("successful refresh overwrote a replacement resource: token=%q err=%v", token, err)
	}
	if token, err := oauth.AccessToken(context.Background(), "docs", baseURL+"/another-resource"); token != "rotated-by-another-process" || err != nil {
		t.Fatalf("replacement resource credential was lost: token=%q err=%v", token, err)
	}
	if err := oauth.saveToken(context.Background(), "docs", saved); err != nil {
		t.Fatal(err)
	}
	refreshSucceeds.Store(false)
	refreshReplacement.Store(baseURL)
	if token, err := oauth.RefreshAccessToken(context.Background(), "docs", baseURL); token != "rotated-by-another-process" || err != nil {
		t.Fatalf("same-resource refresh recovery = %q, %v", token, err)
	}
	if err := oauth.saveToken(context.Background(), "docs", saved); err != nil {
		t.Fatal(err)
	}
	refreshReplacement.Store("")
	concurrentCtx, cancelConcurrent := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancelConcurrent()
	replacementStart, err := oauth.Start(concurrentCtx, OAuthStartOptions{ServerID: "docs", ResourceURL: baseURL + "/replacement", RedirectURI: "http://127.0.0.1/callback"})
	if err != nil {
		t.Fatal(err)
	}
	holdRefresh.Store(true)
	refreshDone := make(chan error, 1)
	go func() {
		token, err := oauth.RefreshAccessToken(concurrentCtx, "docs", baseURL)
		if err == nil && token != "access-refresh_token" {
			err = fmt.Errorf("concurrent refresh returned %q", token)
		}
		refreshDone <- err
	}()
	select {
	case <-refreshStarted:
	case <-concurrentCtx.Done():
		t.Fatal("concurrent refresh did not reach the token endpoint")
	}
	finishDone := make(chan error, 1)
	go func() {
		_, err := oauth.Finish(concurrentCtx, OAuthFinishOptions{ServerID: "docs", ResourceURL: baseURL + "/replacement", State: replacementStart.State, Code: "replacement-code"})
		finishDone <- err
	}()
	select {
	case <-finishExchanged:
	case <-concurrentCtx.Done():
		t.Fatal("replacement login did not exchange its code during the refresh")
	}
	if err := <-finishDone; err != nil {
		t.Fatalf("replacement login during refresh: %v", err)
	}
	refreshRelease <- struct{}{}
	if err := <-refreshDone; !errors.Is(err, ErrOAuthResourceMismatch) {
		t.Fatalf("superseded refresh after replacement login: %v", err)
	}
	holdRefresh.Store(false)
	if token, err := oauth.AccessToken(concurrentCtx, "docs", baseURL+"/replacement"); token != "access-authorization_code" || err != nil {
		t.Fatalf("concurrent login lost its replacement binding: token=%q err=%v", token, err)
	}
	if err := oauth.saveToken(context.Background(), "docs", saved); err != nil {
		t.Fatal(err)
	}
	redirectRefresh.Store(true)
	if token, err := oauth.RefreshAccessToken(context.Background(), "docs", baseURL); token != "" || err == nil {
		t.Fatalf("cross-origin token endpoint redirect = %q, %v", token, err)
	}
	if got := redirectedRequests.Load(); got != 0 {
		t.Fatalf("redirect target received %d refresh requests", got)
	}
	// A legacy credential without an audience requires login, without deletion.
	saved.ResourceURL = ""
	if err := oauth.saveToken(context.Background(), "docs", saved); err != nil {
		t.Fatal(err)
	}
	if token, err := oauth.AccessToken(context.Background(), "docs", baseURL); token != "" || !errors.Is(err, ErrOAuthRequired) {
		t.Fatalf("unbound legacy credential = %q, %v", token, err)
	}
	if status, err := oauth.Status(context.Background(), "docs", baseURL); status.Authenticated || err != nil {
		t.Fatalf("unbound legacy status = %+v, %v", status, err)
	}
	if _, err := store.Get(context.Background(), oauthNamespace("docs"), "oauth"); err != nil {
		t.Fatalf("legacy credential was deleted: %v", err)
	}
	saved.ResourceURL = "HTTPS://EXAMPLE.test:443/mcp?tenant=a"
	saved.ExpiresAt = time.Time{}
	if err := oauth.saveToken(context.Background(), "docs", saved); err != nil {
		t.Fatal(err)
	}
	if token, err := oauth.AccessToken(context.Background(), "docs", "https://example.test/mcp?tenant=a"); token == "" || err != nil {
		t.Fatalf("equivalent origin spelling: %v", err)
	}
	for _, resource := range []string{"http://example.test/mcp?tenant=a", "https://example.test:444/mcp?tenant=a", "https://example.test/mcp/?tenant=a", "https://example.test/mcp?tenant=b"} {
		if token, err := oauth.AccessToken(context.Background(), "docs", resource); token != "" || !errors.Is(err, ErrOAuthRequired) {
			t.Fatalf("distinct resource %q = %q, %v", resource, token, err)
		}
	}
	if err := oauth.Remove(context.Background(), "docs"); err != nil {
		t.Fatal(err)
	}
	status, err = oauth.Status(context.Background(), "docs", baseURL)
	if err != nil || status.Authenticated {
		t.Fatalf("status after remove = %+v, %v", status, err)
	}
	t.Log("PKCE, refresh/recovery, replacement preservation after successful refresh, concurrent Finish, redirect rejection, canonical resource identity, and legacy audience migration passed")
}

func (m *OAuthManager) saveToken(ctx context.Context, serverID string, token storedOAuthToken) error {
	data, err := json.Marshal(token)
	if err != nil {
		return err
	}
	return m.store.Set(ctx, oauthNamespace(serverID), "oauth", data)
}

// The package manager lifecycle owns a login attempt, not the account that was
// already accepted. Late callbacks must not cross disable/re-enable generations.
func TestManagerOAuthLifecycleFencesRevokedAndReplacedOwners(t *testing.T) {
	for _, retire := range []string{"revoke", "close"} {
		t.Run(retire, func(t *testing.T) {
			ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			defer cancel()
			var baseURL string
			exchanged := make(chan struct{})
			release := make(chan struct{})
			defer close(release)
			var tokenCalls atomic.Int32
			endpoint := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				switch r.URL.Path {
				case "/.well-known/oauth-protected-resource":
					_ = json.NewEncoder(w).Encode(map[string]any{"resource": baseURL, "authorization_servers": []string{baseURL}})
				case "/.well-known/oauth-authorization-server":
					_ = json.NewEncoder(w).Encode(map[string]any{"issuer": baseURL, "authorization_endpoint": baseURL + "/authorize", "token_endpoint": baseURL + "/token"})
				case "/token":
					tokenCalls.Add(1)
					_ = r.ParseForm()
					if r.Form.Get("code") == "late" {
						close(exchanged)
						select {
						case <-release:
						case <-r.Context().Done():
							return
						}
					}
					_ = json.NewEncoder(w).Encode(map[string]any{"access_token": "accepted-" + r.Form.Get("code"), "expires_in": 3600})
				default:
					http.NotFound(w, r)
				}
			}))
			defer endpoint.Close()
			baseURL = endpoint.URL
			oauth := NewOAuthManager(credentialstore.NewFileStore(filepath.Join(t.TempDir(), "oauth.json")), endpoint.Client())
			cfg := ServerConfig{Name: "plugin.fixture.docs", URL: baseURL, OAuth: &OAuthConfig{ClientID: "fixture", RedirectURI: "http://127.0.0.1/callback"}}
			manager := NewManager()
			defer manager.Close()
			manager.Configure(map[string]ServerConfig{cfg.Name: cfg})
			manager.SetOAuthManager(oauth)
			accepted, err := manager.StartOAuth(ctx, cfg.Name)
			if err != nil {
				t.Fatal(err)
			}
			if _, err = manager.FinishOAuth(ctx, cfg.Name, accepted.State, "original"); err != nil {
				t.Fatal(err)
			}
			pending, err := manager.StartOAuth(ctx, cfg.Name)
			if err != nil {
				t.Fatal(err)
			}
			finished := make(chan error, 1)
			go func() { _, err := manager.FinishOAuth(ctx, cfg.Name, pending.State, "late"); finished <- err }()
			select {
			case <-exchanged:
			case <-ctx.Done():
				t.Fatal("exchange did not start")
			}
			if retire == "revoke" {
				err = manager.Revoke(cfg.Name)
			} else {
				err = manager.Close()
			}
			if err != nil {
				t.Fatal(err)
			}
			select {
			case err = <-finished:
				if err == nil {
					t.Fatal("retired owner accepted in-flight callback")
				}
			case <-ctx.Done():
				t.Fatal("retirement did not cancel exchange")
			}
			if token, err := oauth.AccessToken(ctx, cfg.Name, baseURL); err != nil || token != "accepted-original" {
				t.Fatalf("retirement removed accepted account: token=%q err=%v", token, err)
			}
			if _, err = manager.StartOAuth(ctx, cfg.Name); err == nil {
				t.Fatal("retired owner started another login")
			}
			replacement := NewManager()
			defer replacement.Close()
			replacement.Configure(map[string]ServerConfig{cfg.Name: cfg})
			replacement.SetOAuthManager(oauth)
			if _, err = replacement.FinishOAuth(ctx, cfg.Name, pending.State, "replay"); err == nil {
				t.Fatal("replacement owner accepted old callback")
			}
			fresh, err := replacement.StartOAuth(ctx, cfg.Name)
			if err != nil {
				t.Fatal(err)
			}
			// Old cleanup is scoped to its exact state and must not erase a newer login.
			if err = manager.CancelOAuth(ctx, cfg.Name, pending.State); err != nil {
				t.Fatal(err)
			}
			if _, err = replacement.FinishOAuth(ctx, cfg.Name, fresh.State, "replacement"); err != nil {
				t.Fatalf("old cleanup invalidated replacement: %v", err)
			}
			if token, err := oauth.AccessToken(ctx, cfg.Name, baseURL); err != nil || token != "accepted-replacement" {
				t.Fatalf("replacement account not saved: %q %v", token, err)
			}
			if tokenCalls.Load() != 3 {
				t.Fatalf("unexpected token exchanges: %d", tokenCalls.Load())
			}
		})
	}
}

func TestManagerOAuthPendingAttemptCannotMoveToSuccessorManager(t *testing.T) {
	var baseURL string
	var exchanges atomic.Int32
	endpoint := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/.well-known/oauth-protected-resource":
			_ = json.NewEncoder(w).Encode(map[string]any{"resource": baseURL, "authorization_servers": []string{baseURL}})
		case "/.well-known/oauth-authorization-server":
			_ = json.NewEncoder(w).Encode(map[string]any{"issuer": baseURL, "authorization_endpoint": baseURL + "/authorize", "token_endpoint": baseURL + "/token"})
		case "/token":
			exchanges.Add(1)
			_ = json.NewEncoder(w).Encode(map[string]any{"access_token": "must-not-be-accepted"})
		default:
			http.NotFound(w, r)
		}
	}))
	defer endpoint.Close()
	baseURL = endpoint.URL
	oauth := NewOAuthManager(credentialstore.NewFileStore(filepath.Join(t.TempDir(), "oauth.json")), endpoint.Client())
	cfg := ServerConfig{Name: "plugin.fixture.docs", URL: baseURL, OAuth: &OAuthConfig{ClientID: "fixture", RedirectURI: "http://127.0.0.1/callback"}}
	original := NewManager()
	defer original.Close()
	original.SetOAuthManager(oauth)
	original.Configure(map[string]ServerConfig{cfg.Name: cfg})
	pending, err := original.StartOAuth(context.Background(), cfg.Name)
	if err != nil {
		t.Fatal(err)
	}
	if err = original.Revoke(cfg.Name); err != nil {
		t.Fatal(err)
	}
	replacement := NewManager()
	defer replacement.Close()
	replacement.SetOAuthManager(oauth)
	replacement.Configure(map[string]ServerConfig{cfg.Name: cfg})
	if _, err = replacement.FinishOAuth(context.Background(), cfg.Name, pending.State, "callback"); err == nil {
		t.Fatal("old login crossed manager generation")
	}
	if exchanges.Load() != 0 {
		t.Fatal("old callback reached token endpoint")
	}
}

type blockingOAuthCredentialStore struct {
	credentialstore.Store
	block    atomic.Bool
	entered  chan struct{}
	canceled chan struct{}
	release  chan struct{}
}

func (s *blockingOAuthCredentialStore) Update(ctx context.Context, namespace, key string, update func([]byte) ([]byte, error)) error {
	if s.block.Load() && namespace == oauthNamespace("blocked") {
		close(s.entered)
		select {
		case <-ctx.Done():
			close(s.canceled)
			return ctx.Err()
		case <-s.release:
		}
	}
	return s.Store.Update(ctx, namespace, key, update)
}

func TestManagerOAuthRevocationCancelsBlockedCredentialProviderWithoutBlockingOtherServers(t *testing.T) {
	for _, phase := range []string{"start", "finish"} {
		t.Run(phase, func(t *testing.T) {
			var baseURL string
			endpoint := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				switch r.URL.Path {
				case "/.well-known/oauth-protected-resource":
					_ = json.NewEncoder(w).Encode(map[string]any{"resource": baseURL, "authorization_servers": []string{baseURL}})
				case "/.well-known/oauth-authorization-server":
					_ = json.NewEncoder(w).Encode(map[string]any{"issuer": baseURL, "authorization_endpoint": baseURL + "/authorize", "token_endpoint": baseURL + "/token"})
				case "/token":
					_ = json.NewEncoder(w).Encode(map[string]any{"access_token": "late-token"})
				default:
					http.NotFound(w, r)
				}
			}))
			defer endpoint.Close()
			baseURL = endpoint.URL
			store := &blockingOAuthCredentialStore{Store: credentialstore.NewFileStore(filepath.Join(t.TempDir(), "oauth.json")), entered: make(chan struct{}), canceled: make(chan struct{}), release: make(chan struct{})}
			defer close(store.release)
			manager := NewManager()
			defer manager.Close()
			oauth := NewOAuthManager(store, endpoint.Client())
			manager.SetOAuthManager(oauth)
			cfg := ServerConfig{URL: baseURL, OAuth: &OAuthConfig{ClientID: "fixture", RedirectURI: "http://127.0.0.1/callback"}}
			manager.Configure(map[string]ServerConfig{"blocked": cfg, "other": cfg})
			if err := oauth.saveToken(context.Background(), "blocked", storedOAuthToken{AccessToken: "accepted-account", ResourceURL: baseURL}); err != nil {
				t.Fatal(err)
			}
			var pending OAuthStartResult
			var err error
			if phase == "finish" {
				pending, err = manager.StartOAuth(context.Background(), "blocked")
				if err != nil {
					t.Fatal(err)
				}
			}
			store.block.Store(true)
			blocked := make(chan error, 1)
			go func() {
				if phase == "start" {
					_, err := manager.StartOAuth(context.Background(), "blocked")
					blocked <- err
				} else {
					_, err := manager.FinishOAuth(context.Background(), "blocked", pending.State, "late")
					blocked <- err
				}
			}()
			select {
			case <-store.entered:
			case <-time.After(2 * time.Second):
				t.Fatal("credential provider not reached")
			}
			other := make(chan error, 1)
			go func() { _, err := manager.StartOAuth(context.Background(), "other"); other <- err }()
			select {
			case err := <-other:
				if err != nil {
					t.Fatalf("unrelated OAuth failed while provider was blocked: %v", err)
				}
			case <-time.After(time.Second):
				store.block.Store(false)
				store.release <- struct{}{}
				<-blocked
				<-other
				t.Fatal("one blocked credential provider stalled another server")
			}
			revoked := make(chan error, 1)
			go func() { revoked <- manager.Revoke("blocked") }()
			select {
			case <-store.canceled:
			case <-time.After(time.Second):
				// Release the synthetic provider so a failing test can still clean up.
				store.block.Store(false)
				store.release <- struct{}{}
				<-blocked
				<-revoked
				t.Fatal("revocation could not cancel the blocked credential provider")
			}
			if err := <-blocked; err == nil {
				t.Fatal("blocked login succeeded after revoke")
			}
			if err := <-revoked; err != nil {
				t.Fatal(err)
			}
			if token, err := oauth.AccessToken(context.Background(), "blocked", baseURL); err != nil || token != "accepted-account" {
				t.Fatalf("accepted account changed: %q %v", token, err)
			}
		})
	}
}

func TestOAuthRefreshCoordinatorPreservesWaiterDeadlineAndSingleExchange(t *testing.T) {
	started, release := make(chan struct{}), make(chan struct{})
	var exchanges atomic.Int32
	endpoint := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if exchanges.Add(1) == 1 {
			close(started)
			select {
			case <-release:
			case <-r.Context().Done():
				return
			}
		}
		json.NewEncoder(w).Encode(map[string]any{"access_token": "rotated", "expires_in": 3600})
	}))
	defer endpoint.Close()
	defer close(release)
	store := credentialstore.NewFileStore(filepath.Join(t.TempDir(), "oauth.json"))
	token := storedOAuthToken{AccessToken: "expired", RefreshToken: "refresh", ExpiresAt: time.Now().Add(-time.Hour), TokenEndpoint: endpoint.URL, ResourceURL: endpoint.URL + "/resource", CredentialVersion: "original"}
	data, _ := json.Marshal(token)
	if err := store.Set(context.Background(), oauthNamespace("docs"), "oauth", data); err != nil {
		t.Fatal(err)
	}
	old, current := NewOAuthManager(store, endpoint.Client()), NewOAuthManager(store, endpoint.Client())
	current.ShareRefreshCoordinator(old)
	done := make(chan error, 1)
	go func() { _, err := old.AccessToken(context.Background(), "docs", token.ResourceURL); done <- err }()
	<-started
	ctx, cancel := context.WithTimeout(context.Background(), 40*time.Millisecond)
	defer cancel()
	if _, err := current.AccessToken(ctx, "docs", token.ResourceURL); !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("waiting caller ignored its deadline: %v", err)
	}
	if got := exchanges.Load(); got != 1 {
		t.Fatalf("waiting caller started %d exchanges", got)
	}
	release <- struct{}{}
	if err := <-done; err != nil {
		t.Fatalf("short waiter canceled retained refresh: %v", err)
	}
	if token, err := current.AccessToken(context.Background(), "docs", token.ResourceURL); err != nil || token != "rotated" {
		t.Fatalf("successor did not read rotated credential: %q %v", token, err)
	}
	if got := exchanges.Load(); got != 1 {
		t.Fatalf("rotated credential caused %d exchanges", got)
	}
}

func TestOAuthRefreshCoordinatorSeparatesCredentialAuthoritiesAndResources(t *testing.T) {
	for _, separation := range []string{"authority", "resource"} {
		t.Run(separation, func(t *testing.T) {
			started, release := make(chan struct{}), make(chan struct{})
			var exchanges atomic.Int32
			endpoint := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if exchanges.Add(1) == 1 {
					close(started)
					select {
					case <-release:
					case <-r.Context().Done():
						return
					}
				}
				json.NewEncoder(w).Encode(map[string]any{"access_token": "rotated", "expires_in": 3600})
			}))
			defer endpoint.Close()
			defer close(release)
			store := credentialstore.NewFileStore(filepath.Join(t.TempDir(), "oauth.json"))
			resource := endpoint.URL + "/resource"
			seed := func(target credentialstore.Store, resource string) {
				t.Helper()
				data, _ := json.Marshal(storedOAuthToken{AccessToken: "expired", RefreshToken: "refresh", ExpiresAt: time.Now().Add(-time.Hour), TokenEndpoint: endpoint.URL, ResourceURL: resource, CredentialVersion: resource})
				if err := target.Set(context.Background(), oauthNamespace("docs"), "oauth", data); err != nil {
					t.Fatal(err)
				}
			}
			seed(store, resource)
			old := NewOAuthManager(store, endpoint.Client())
			done := make(chan error, 1)
			go func() { _, err := old.AccessToken(context.Background(), "docs", resource); done <- err }()
			<-started
			nextStore := credentialstore.Store(store)
			nextResource := resource
			if separation == "authority" {
				nextStore = credentialstore.NewFileStore(filepath.Join(t.TempDir(), "other.json"))
			} else {
				nextResource += "?account=other"
			}
			seed(nextStore, nextResource)
			current := NewOAuthManager(nextStore, endpoint.Client())
			if separation == "resource" {
				current.ShareRefreshCoordinator(old)
			}
			ctx, cancel := context.WithTimeout(context.Background(), time.Second)
			defer cancel()
			if _, err := current.AccessToken(ctx, "docs", nextResource); err != nil {
				t.Fatalf("independent %s blocked on old refresh: %v", separation, err)
			}
			release <- struct{}{}
			err := <-done
			if separation == "authority" && err != nil {
				t.Fatal(err)
			}
			if separation == "resource" && !errors.Is(err, ErrOAuthResourceMismatch) {
				t.Fatalf("old resource overwrote replacement: %v", err)
			}
		})
	}
}
