package mcp

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"github.com/blueberrycongee/wuu/internal/config"
	"github.com/blueberrycongee/wuu/internal/credentialstore"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

func identityTestProbe() *config.MCPIdentityProbe {
	return &config.MCPIdentityProbe{Tool: "get-self", Arguments: json.RawMessage(`{}`), Result: "single_text_json", Account: config.MCPIdentityAccountMapping{ID: "/user/id", Label: "/user/name", Email: "/user/email", Kind: "/user/type"}, Workspace: &config.MCPIdentityWorkspaceMapping{ID: "/workspace/id", Label: "/workspace/name"}}
}

func TestNormalizeConnectorIdentityRejectsAmbiguousUnboundedAndFailedResults(t *testing.T) {
	valid := `{"user":{"id":"user-1","name":"","type":"bot"},"workspace":{"id":"space-1","name":""},"secret":"never expose"}`
	for _, tc := range []struct{ name, payload, code string }{
		{"valid optional fields absent", valid, ""},
		{"missing account id", `{"user":{},"workspace":{"id":"w"}}`, "identity_invalid_response"},
		{"numeric account id", `{"user":{"id":7},"workspace":{"id":"w"}}`, "identity_invalid_response"},
		{"blank account id", `{"user":{"id":" "},"workspace":{"id":"w"}}`, "identity_invalid_response"},
		{"missing workspace id", `{"user":{"id":"u"}}`, "identity_invalid_response"},
		{"control id", `{"user":{"id":"user\nforged"},"workspace":{"id":"w"}}`, "identity_invalid_response"},
		{"null optional field", `{"user":{"id":"u","email":null},"workspace":{"id":"w"}}`, "identity_invalid_response"},
		{"wrong optional type", `{"user":{"id":"u","email":false},"workspace":{"id":"w"}}`, "identity_invalid_response"},
		{"long id", `{"user":{"id":"` + strings.Repeat("x", 513) + `"},"workspace":{"id":"w"}}`, "identity_invalid_response"},
		{"not an object", `[]`, "identity_invalid_response"},
		{"oversized", `{"padding":"` + strings.Repeat("x", 65536) + `"}`, "identity_response_too_large"},
		{"invalid JSON", `token-secret`, "identity_invalid_response"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			identity := normalizeIdentity(identityTestProbe(), &CallToolResult{Content: []ToolContent{{Type: "text", Text: tc.payload}}})
			if identity.ErrorCode != tc.code {
				t.Fatalf("identity=%+v, want code=%q", identity, tc.code)
			}
			if tc.code == "" && (identity.State != "verified" || identity.Account.ID != "user-1" || identity.Account.Kind != "bot" || identity.Workspace.ID != "space-1" || identity.VerifiedAt == "") {
				t.Fatalf("identity=%+v", identity)
			}
			data, _ := json.Marshal(identity)
			if strings.Contains(string(data), "secret") {
				t.Fatalf("raw payload escaped: %s", data)
			}
		})
	}
	for _, result := range []*CallToolResult{{IsError: true, Content: []ToolContent{{Type: "text", Text: "secret-token"}}}, {Content: []ToolContent{{Type: "text", Text: valid}, {Type: "text", Text: valid}}}, {Content: []ToolContent{{Type: "image", Text: valid}}}} {
		identity := normalizeIdentity(identityTestProbe(), result)
		if identity.State != "unavailable" || identity.Account != nil || identity.Workspace != nil {
			t.Fatalf("unsafe result accepted: %+v", identity)
		}
	}
	for _, pointer := range []string{"/users/+1/id", "/users/-0/id", "/users/01/id"} {
		probe := identityTestProbe()
		probe.Account.ID = pointer
		result := normalizeIdentity(probe, &CallToolResult{Content: []ToolContent{{Type: "text", Text: `{"users":[{"id":"a"},{"id":"b"}],"workspace":{"id":"w"}}`}}})
		if result.State != "unavailable" {
			t.Fatalf("noncanonical array pointer %q resolved: %+v", pointer, result)
		}
	}
	probe := identityTestProbe()
	probe.Result = "structured_content"
	if identity := normalizeIdentity(probe, &CallToolResult{StructuredContent: json.RawMessage(valid)}); identity.State != "verified" {
		t.Fatalf("structured rejected: %+v", identity)
	}
}

type identityFixture struct {
	manager   *Manager
	oauth     *OAuthManager
	server    *httptest.Server
	calls     atomic.Int32
	exchanges atomic.Int32
	mu        sync.Mutex
	result    *CallToolResult
	started   chan struct{}
	release   chan struct{}
}

func newIdentityFixture(t *testing.T, legacy bool) *identityFixture {
	t.Helper()
	f := &identityFixture{result: &CallToolResult{Content: []ToolContent{{Type: "text", Text: `{"user":{"id":"u","name":"Owner","type":"person"},"workspace":{"id":"w","name":"Workspace"}}`}}}}
	var endpoint string
	f.server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/.well-known/oauth-protected-resource":
			json.NewEncoder(w).Encode(map[string]any{"resource": endpoint, "authorization_servers": []string{endpoint}})
			return
		case "/.well-known/oauth-authorization-server":
			json.NewEncoder(w).Encode(map[string]any{"issuer": endpoint, "authorization_endpoint": endpoint + "/authorize", "token_endpoint": endpoint + "/token"})
			return
		case "/token":
			f.exchanges.Add(1)
			json.NewEncoder(w).Encode(map[string]any{"access_token": "secret-access-token", "refresh_token": "secret-refresh-token", "expires_in": 3600})
			return
		}
		if r.Method == http.MethodDelete {
			w.WriteHeader(http.StatusOK)
			return
		}
		if r.Header.Get("Authorization") != "Bearer secret-access-token" {
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
		if r.Method != http.MethodPost {
			w.WriteHeader(http.StatusMethodNotAllowed)
			return
		}
		var req Request
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			t.Error(err)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		switch req.Method {
		case "initialize":
			fmt.Fprint(w, initializeResultJSON(req.ID, "2025-03-26"))
		case "tools/list":
			fmt.Fprint(w, toolsListResultJSON(req.ID, "get-self"))
		case "tools/call":
			f.calls.Add(1)
			var params CallToolParams
			json.Unmarshal(req.Params, &params)
			if params.Name != "get-self" || string(params.Arguments) != "{}" {
				t.Errorf("probe params=%+v", params)
			}
			f.mu.Lock()
			result, started, release := f.result, f.started, f.release
			f.mu.Unlock()
			if started != nil {
				close(started)
				<-release
			}
			json.NewEncoder(w).Encode(map[string]any{"jsonrpc": "2.0", "id": req.ID, "result": result})
		default:
			w.WriteHeader(http.StatusAccepted)
		}
	}))
	endpoint = f.server.URL
	f.oauth = NewOAuthManager(credentialstore.NewFileStore(filepath.Join(t.TempDir(), "oauth.json")), f.server.Client())
	f.manager = NewManager()
	f.manager.SetOAuthManager(f.oauth)
	readonly := true
	f.manager.Configure(map[string]ServerConfig{"docs": {URL: endpoint, Transport: TransportStreamableHTTP, OAuth: &OAuthConfig{ClientID: "fixture", RedirectURI: "http://127.0.0.1/callback"}, IdentityProbe: identityTestProbe(), IdentitySource: "trusted-package", ToolOverrides: map[string]ToolOverride{"get-self": {ReadOnly: &readonly}}}})
	if legacy {
		data, _ := json.Marshal(storedOAuthToken{AccessToken: "secret-access-token", ResourceURL: endpoint})
		if err := f.oauth.store.Set(context.Background(), oauthNamespace("docs"), "oauth", data); err != nil {
			t.Fatal(err)
		}
	}
	t.Cleanup(func() { f.manager.Close(); f.server.Close() })
	return f
}
func (f *identityFixture) login(t *testing.T) {
	t.Helper()
	start, err := f.manager.StartOAuth(context.Background(), "docs")
	if err != nil {
		t.Fatal(err)
	}
	if _, err = f.manager.FinishOAuth(context.Background(), "docs", start.State, "code"); err != nil {
		t.Fatal(err)
	}
}

func TestConnectorIdentityOwnerProbeGrantPersistenceAndReadOnlyStatus(t *testing.T) {
	for _, legacy := range []bool{false, true} {
		t.Run(fmt.Sprint("legacy=", legacy), func(t *testing.T) {
			f := newIdentityFixture(t, legacy)
			ctx := context.Background()
			if !legacy {
				if status, err := f.manager.RefreshIdentity(ctx, "docs"); err != nil || status.Identity != nil {
					t.Fatalf("unauthorized identity=%+v, %v", status, err)
				}
				f.login(t)
			}
			before, err := f.oauth.loadRecord(ctx, "docs")
			if err != nil {
				t.Fatal(err)
			}
			if !legacy && before.GrantID == "" {
				t.Fatal("completed OAuth omitted stable grant id")
			}
			for i := 0; i < 2; i++ {
				status, err := f.manager.OAuthStatus(ctx, "docs")
				if err != nil || status.Identity != nil {
					t.Fatalf("status=%+v, %v", status, err)
				}
			}
			after, _ := f.oauth.loadRecord(ctx, "docs")
			if legacy && after.GrantID != "" {
				t.Fatal("status migrated legacy record")
			}
			if err := f.manager.Connect(ctx, "docs"); err != nil {
				t.Fatal(err)
			}
			if err := f.manager.Refresh(ctx, "docs"); err != nil {
				t.Fatal(err)
			}
			if f.calls.Load() != 0 {
				t.Fatal("generic connection probed identity")
			}
			status, err := f.manager.RefreshIdentity(ctx, "docs")
			if err != nil || status.Identity == nil || status.Identity.State != "verified" {
				t.Fatalf("owner probe=%+v, %v", status, err)
			}
			saved, _ := f.oauth.loadRecord(ctx, "docs")
			if saved.GrantID == "" || saved.Identity == nil {
				t.Fatal("identity not persisted with grant")
			}
			if !legacy {
				if _, err := f.oauth.RefreshAccessToken(ctx, "docs", f.server.URL); err != nil {
					t.Fatal(err)
				}
				refreshed, _ := f.oauth.loadRecord(ctx, "docs")
				if refreshed.GrantID != saved.GrantID || refreshed.Identity == nil {
					t.Fatal("refresh changed identity grant")
				}
			}
			status, err = f.manager.OAuthStatus(ctx, "docs")
			if err != nil || status.Identity == nil {
				t.Fatalf("cached status=%+v, %v", status, err)
			}
			data, _ := json.Marshal(status)
			if strings.Contains(string(data), "secret-") || strings.Contains(string(data), "grant_id") {
				t.Fatalf("credentials leaked: %s", data)
			}
			f.login(t)
			next, _ := f.oauth.loadRecord(ctx, "docs")
			if next.GrantID == saved.GrantID || next.Identity != nil {
				t.Fatal("new authorization reused identity")
			}
		})
	}
}

func TestConnectorIdentityLateProbeCannotPublishAfterLifecycleChanges(t *testing.T) {
	for _, action := range []string{"disconnect", "logout", "delete", "new login", "cancel", "replace", "revoke"} {
		t.Run(action, func(t *testing.T) {
			f := newIdentityFixture(t, false)
			f.login(t)
			ctx := context.Background()
			if err := f.manager.Connect(ctx, "docs"); err != nil {
				t.Fatal(err)
			}
			f.started = make(chan struct{})
			f.release = make(chan struct{})
			var once sync.Once
			release := func() { once.Do(func() { close(f.release) }) }
			defer release()
			done := make(chan OAuthStatus, 1)
			go func() { status, _ := f.manager.RefreshIdentity(ctx, "docs"); done <- status }()
			select {
			case <-f.started:
			case <-time.After(3 * time.Second):
				t.Fatal("probe did not start")
			}
			switch action {
			case "disconnect":
				f.manager.Disconnect("docs")
			case "logout":
				f.manager.RemoveOAuth(ctx, "docs")
			case "delete":
				f.oauth.store.Delete(ctx, oauthNamespace("docs"), "oauth")
			case "new login":
				f.login(t)
			case "cancel":
				start, err := f.manager.StartOAuth(ctx, "docs")
				if err != nil {
					t.Fatal(err)
				}
				f.manager.CancelOAuth(ctx, "docs", start.State)
			case "replace":
				if err := f.manager.Refresh(ctx, "docs"); err != nil {
					t.Fatal(err)
				}
			case "revoke":
				f.manager.Revoke("docs")
			}
			release()
			select {
			case status := <-done:
				if status.Identity != nil {
					t.Fatalf("late identity published: %+v", status)
				}
			case <-time.After(3 * time.Second):
				t.Fatal("probe did not stop")
			}
			record, err := f.oauth.loadRecord(ctx, "docs")
			if action == "delete" {
				if !errors.Is(err, credentialstore.ErrNotFound) {
					t.Fatalf("deleted credentials resurrected: %+v %v", record, err)
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			if record.Identity != nil {
				t.Fatal("late identity persisted")
			}
		})
	}
}

func TestConnectorIdentityProbeSafetyAndFailureDoesNotInvalidateTransport(t *testing.T) {
	for _, safety := range []string{"no source", "no override", "write override", "absent tool", "server error", "oversized"} {
		t.Run(safety, func(t *testing.T) {
			f := newIdentityFixture(t, false)
			f.login(t)
			ctx := context.Background()
			cfg, _ := f.manager.Config("docs")
			switch safety {
			case "no source":
				cfg.IdentitySource = ""
			case "no override":
				cfg.ToolOverrides = nil
			case "write override":
				v := false
				cfg.ToolOverrides["get-self"] = ToolOverride{ReadOnly: &v}
			case "absent tool":
				cfg.IdentityProbe.Tool = "missing"
				v := true
				cfg.ToolOverrides["missing"] = ToolOverride{ReadOnly: &v}
			case "server error":
				f.result = &CallToolResult{IsError: true, Content: []ToolContent{{Type: "text", Text: "secret-access-token"}}}
			case "oversized":
				f.result = &CallToolResult{Content: []ToolContent{{Type: "text", Text: strings.Repeat("x", 65537)}}}
			}
			f.manager.Configure(map[string]ServerConfig{"docs": cfg})
			if err := f.manager.Connect(ctx, "docs"); err != nil {
				t.Fatal(err)
			}
			status, err := f.manager.RefreshIdentity(ctx, "docs")
			if err != nil {
				t.Fatal(err)
			}
			wantCalls := int32(0)
			if safety == "server error" || safety == "oversized" {
				wantCalls = 1
			}
			if f.calls.Load() != wantCalls {
				t.Fatalf("unsafe probe executed: %d", f.calls.Load())
			}
			if safety == "absent tool" || safety == "server error" || safety == "oversized" {
				cached, cacheErr := f.manager.OAuthStatus(ctx, "docs")
				if cacheErr != nil || cached.Identity == nil || cached.Identity.State != "unavailable" {
					t.Fatalf("failure diagnostic not persisted: %+v, %v", cached, cacheErr)
				}
			}
			if status.Identity != nil && status.Identity.State == "verified" {
				t.Fatal("unsafe identity verified")
			}
			if !status.Authenticated || !f.manager.Status()["docs"].Connected {
				t.Fatal("probe invalidated working auth/transport")
			}
			data, _ := json.Marshal(status)
			if strings.Contains(string(data), "secret-") {
				t.Fatalf("error leaked token: %s", data)
			}
		})
	}
}

func TestConnectorIdentityCacheBoundToAdmittedPackageAndGrantWithoutNetwork(t *testing.T) {
	f := newIdentityFixture(t, false)
	f.login(t)
	ctx := context.Background()
	if err := f.manager.Connect(ctx, "docs"); err != nil {
		t.Fatal(err)
	}
	if status, err := f.manager.RefreshIdentity(ctx, "docs"); err != nil || status.Identity == nil {
		t.Fatalf("probe=%+v %v", status, err)
	}
	cfg, _ := f.manager.Config("docs")
	for _, change := range []string{"source", "mapping", "resource", "disabled", "legacy", "restart", "disconnected"} {
		t.Run(change, func(t *testing.T) {
			candidate := NewManager()
			defer candidate.Close()
			candidate.SetOAuthManager(f.oauth)
			next := cloneServerConfig(cfg)
			switch change {
			case "source":
				next.IdentitySource = "replacement"
			case "mapping":
				next.IdentityProbe.Account.Label = "/user/other"
			case "resource":
				next.URL += "/other"
			case "disabled":
				disabled := false
				next.Enabled = &disabled
			case "legacy":
				original, err := f.oauth.store.Get(ctx, oauthNamespace("docs"), "oauth")
				if err != nil {
					t.Fatal(err)
				}
				defer f.oauth.store.Set(ctx, oauthNamespace("docs"), "oauth", original)
				var token storedOAuthToken
				json.Unmarshal(original, &token)
				token.GrantID = ""
				data, _ := json.Marshal(token)
				f.oauth.store.Set(ctx, oauthNamespace("docs"), "oauth", data)
			}
			candidate.Configure(map[string]ServerConfig{"docs": next})
			if change == "disconnected" {
				if err := candidate.Disconnect("docs"); err != nil {
					t.Fatal(err)
				}
			}
			status, err := candidate.OAuthStatus(ctx, "docs")
			if err != nil {
				t.Fatal(err)
			}
			wantIdentity := change == "restart" || change == "disconnected"
			if (status.Identity != nil) != wantIdentity {
				t.Fatalf("status=%+v, want identity=%v", status, wantIdentity)
			}
		})
	}
	if f.calls.Load() != 1 {
		t.Fatal("cache reads triggered probe")
	}
	start, err := f.manager.StartOAuth(ctx, "docs")
	if err != nil {
		t.Fatal(err)
	}
	if err = f.manager.CancelOAuth(ctx, "docs", start.State); err != nil {
		t.Fatal(err)
	}
	status, err := f.manager.OAuthStatus(ctx, "docs")
	if err != nil || !status.Authenticated || status.Identity != nil {
		t.Fatalf("cancelled reconnect lost old auth or kept identity: %+v %v", status, err)
	}
}

type identityCredentialStore struct {
	credentialstore.Store
	fail    bool
	entered chan struct{}
}

func (s *identityCredentialStore) Update(ctx context.Context, namespace, key string, update func([]byte) ([]byte, error)) error {
	if s.fail {
		return fmt.Errorf("credential provider secret-access-token")
	}
	close(s.entered)
	<-ctx.Done()
	return ctx.Err()
}

func TestConnectorIdentityCredentialFailurePreservesLegacyGrantAndRetirement(t *testing.T) {
	for _, failure := range []bool{true, false} {
		t.Run(fmt.Sprint("failure=", failure), func(t *testing.T) {
			f := newIdentityFixture(t, true)
			ctx := context.Background()
			if err := f.manager.Connect(ctx, "docs"); err != nil {
				t.Fatal(err)
			}
			original, err := f.oauth.store.Get(ctx, oauthNamespace("docs"), "oauth")
			if err != nil {
				t.Fatal(err)
			}
			store := &identityCredentialStore{Store: f.oauth.store, fail: failure, entered: make(chan struct{})}
			f.oauth.store = store
			done := make(chan OAuthStatus, 1)
			go func() { status, _ := f.manager.RefreshIdentity(ctx, "docs"); done <- status }()
			if !failure {
				select {
				case <-store.entered:
				case <-time.After(time.Second):
					t.Fatal("identity did not pin credential")
				}
				if err := f.manager.Disconnect("docs"); err != nil {
					t.Fatal(err)
				}
			}
			var status OAuthStatus
			select {
			case status = <-done:
			case <-time.After(time.Second):
				t.Fatal("identity did not return")
			}
			if !failure && status.Identity != nil {
				t.Fatalf("retired identity published error: %+v", status)
			}
			if failure && (status.Identity == nil || status.Identity.State != "unavailable") {
				t.Fatalf("storage failure omitted safe diagnostic: %+v", status)
			}
			data, _ := json.Marshal(status)
			if strings.Contains(string(data), "secret-") {
				t.Fatalf("provider leaked credential: %s", data)
			}
			after, err := store.Get(ctx, oauthNamespace("docs"), "oauth")
			if err != nil || string(after) != string(original) {
				t.Fatalf("failed migration changed credentials: %s %v", after, err)
			}
			if f.calls.Load() != 0 {
				t.Fatal("probe ran without a pinned credential")
			}
		})
	}
}

type identityPersistenceFailureStore struct {
	credentialstore.Store
	updates atomic.Int32
}

func (s *identityPersistenceFailureStore) Update(ctx context.Context, namespace, key string, update func([]byte) ([]byte, error)) error {
	if s.updates.Add(1) == 2 {
		return fmt.Errorf("storage failure secret-access-token")
	}
	return s.Store.Update(ctx, namespace, key, update)
}
func TestConnectorIdentityPersistenceFailureReturnsSafeDiagnostic(t *testing.T) {
	f := newIdentityFixture(t, false)
	f.login(t)
	ctx := context.Background()
	if err := f.manager.Connect(ctx, "docs"); err != nil {
		t.Fatal(err)
	}
	before, err := f.oauth.loadRecord(ctx, "docs")
	if err != nil {
		t.Fatal(err)
	}
	f.oauth.store = &identityPersistenceFailureStore{Store: f.oauth.store}
	status, err := f.manager.RefreshIdentity(ctx, "docs")
	if err != nil || !status.Authenticated || status.Identity == nil || status.Identity.ErrorCode != "identity_credentials_unavailable" {
		t.Fatalf("persistence failure=%+v, %v", status, err)
	}
	after, err := f.oauth.loadRecord(ctx, "docs")
	if err != nil || after.AccessToken != before.AccessToken || after.GrantID != before.GrantID || after.Identity != nil {
		t.Fatalf("failed save changed accepted grant: %+v, %v", after, err)
	}
	encoded, _ := json.Marshal(status)
	if strings.Contains(string(encoded), "secret-") {
		t.Fatalf("raw storage diagnostic leaked: %s", encoded)
	}
}

func TestConnectorIdentityNewerOwnerRetryWinsOverLateResponse(t *testing.T) {
	f := newIdentityFixture(t, false)
	f.login(t)
	ctx := context.Background()
	if err := f.manager.Connect(ctx, "docs"); err != nil {
		t.Fatal(err)
	}
	started, release := make(chan struct{}), make(chan struct{})
	var once sync.Once
	unblock := func() { once.Do(func() { close(release) }) }
	defer unblock()
	f.mu.Lock()
	f.started = started
	f.release = release
	f.mu.Unlock()
	done := make(chan struct{})
	go func() { f.manager.RefreshIdentity(ctx, "docs"); close(done) }()
	select {
	case <-started:
	case <-time.After(time.Second):
		t.Fatal("first probe did not start")
	}
	f.mu.Lock()
	f.started = nil
	f.release = nil
	f.result = &CallToolResult{Content: []ToolContent{{Type: "text", Text: `{"user":{"id":"newer"},"workspace":{"id":"w"}}`}}}
	f.mu.Unlock()
	newer, err := f.manager.RefreshIdentity(ctx, "docs")
	if err != nil || newer.Identity == nil || newer.Identity.Account == nil || newer.Identity.Account.ID != "newer" {
		t.Fatalf("newer retry=%+v, %v", newer, err)
	}
	unblock()
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("superseded probe did not stop")
	}
	final, err := f.manager.OAuthStatus(ctx, "docs")
	if err != nil || final.Identity == nil || final.Identity.Account.ID != "newer" {
		t.Fatalf("late response overwrote newer owner result: %+v %v", final, err)
	}
}
