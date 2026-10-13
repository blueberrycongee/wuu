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
