package personalconnectors

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"testing"
)

func TestOtherOAuthTokenProtocolAndErrors(t *testing.T) {
	for _, provider := range []string{"notion", "github", "slack", "feishu"} {
		if _, err := decodeOtherOAuthToken(provider, []byte(`{"error":"secret provider details","access_token":"must-not-save"}`)); err == nil {
			t.Fatalf("%s accepted error token", provider)
		}
	}
	config := OAuthClient{Provider: "notion", ClientID: "synthetic-client", ClientSecret: "synthetic-secret", RedirectURI: "https://owner.invalid/callback"}
	req, err := otherOAuthTokenRequest(config, url.Values{"grant_type": {"authorization_code"}, "code": {"synthetic-code"}, "redirect_uri": {config.RedirectURI}})
	if err != nil {
		t.Fatal(err)
	}
	if req.URL.String() != "https://api.notion.com/v1/oauth/token" || req.Header.Get("Content-Type") != "application/json" {
		t.Fatal("incorrect Notion token exchange")
	}
	user, password, ok := req.BasicAuth()
	if !ok || user != config.ClientID || password != config.ClientSecret {
		t.Fatal("missing confidential client authentication")
	}
	body, _ := io.ReadAll(req.Body)
	if strings.Contains(string(body), config.ClientSecret) {
		t.Fatal("Notion secret duplicated into body")
	}
	tok, err := decodeOtherOAuthToken("slack", []byte(`{"ok":true,"team":{"id":"T1"},"access_token":"bot-not-selected","authed_user":{"id":"U1","access_token":"user-selected","scope":"channels:history,groups:history"}}`))
	if err != nil || tok.AccessToken != "user-selected" || tok.WorkspaceID != "T1" {
		t.Fatalf("wrong Slack token selection %#v %v", tok, err)
	}
}
func TestOtherOAuthIdentityBindsWorkspaceAndUser(t *testing.T) {
	for _, tc := range []struct{ provider, body, account, workspace string }{
		{"notion", `{"object":"user","type":"bot","id":"bot-1"}`, "bot-1", "workspace-1"},
		{"github", `{"id":42,"login":"synthetic"}`, "42", ""},
		{"slack", `{"ok":true,"team_id":"T1","user_id":"U1"}`, "T1:U1", "T1"},
		{"feishu", `{"code":0,"data":{"tenant_key":"tenant-1","open_id":"ou_user"}}`, "tenant-1:ou_user", "tenant-1"},
	} {
		t.Run(tc.provider, func(t *testing.T) {
			b := newTestBroker(Options{HTTPClient: &http.Client{Transport: transport(func(r *http.Request) (*http.Response, error) {
				if r.Header.Get("Authorization") != "Bearer synthetic" {
					t.Fatal("no bearer")
				}
				return &http.Response{StatusCode: 200, Body: io.NopCloser(strings.NewReader(tc.body)), Header: http.Header{}}, nil
			})}})
			defer b.Close()
			result, err := b.verifyOtherOAuthIdentity(context.Background(), tc.provider, OAuthToken{AccessToken: "synthetic", AccountID: tc.account, WorkspaceID: tc.workspace})
			if err != nil || result.AccountID != tc.account || result.WorkspaceID != tc.workspace {
				t.Fatalf("identity mismatch %#v %v", result, err)
			}
			if _, err = b.verifyOtherOAuthIdentity(context.Background(), tc.provider, OAuthToken{AccessToken: "synthetic", AccountID: "wrong-account", WorkspaceID: tc.workspace}); err == nil {
				t.Fatal("cross-account accepted")
			}
		})
	}
}
func TestOtherOAuthNeverAcceptsBotOrMissingTenantAsUser(t *testing.T) {
	if _, err := decodeOtherOAuthToken("slack", []byte(`{"ok":true,"access_token":"bot","token_type":"bot","team":{"id":"T1"},"authed_user":{"id":"U1"}}`)); err == nil {
		t.Fatal("bot token substituted for user token")
	}
	refreshed, err := decodeOtherOAuthToken("slack", []byte(`{"ok":true,"access_token":"user-refreshed","token_type":"user","refresh_token":"rotated","expires_in":43200,"scope":"channels:history"}`))
	if err != nil || refreshed.AccessToken != "user-refreshed" || refreshed.RefreshToken != "rotated" {
		t.Fatalf("rotation response rejected %#v %v", refreshed, err)
	}
	b := newTestBroker(Options{HTTPClient: &http.Client{Transport: transport(func(r *http.Request) (*http.Response, error) {
		return &http.Response{StatusCode: 200, Body: io.NopCloser(strings.NewReader(`{"code":0,"data":{"open_id":"ou_user"}}`)), Header: http.Header{}}, nil
	})}})
	defer b.Close()
	if _, err = b.verifyOtherOAuthIdentity(context.Background(), "feishu", OAuthToken{AccessToken: "synthetic"}); err == nil {
		t.Fatal("tenantless identity accepted")
	}
}

type providerHTTPSFixture struct {
	callback func(string) error
	redirect string
}

func (f *providerHTTPSFixture) Register(ctx context.Context, redirect string, callback func(string) error) (func(), error) {
	f.callback = callback
	f.redirect = redirect
	return func() {}, nil
}
func TestSlackHTTPSCallbackExchangeIsSingleUseAndWorkspaceBound(t *testing.T) {
	for _, wrongWorkspace := range []bool{false, true} {
		t.Run(fmt.Sprint("wrong_workspace=", wrongWorkspace), func(t *testing.T) {
			ctx := context.Background()
			a := Actor{"personal", "owner-space", "owner-ui"}
			s := &memoryStore{values: map[string][]byte{}}
			relay := &providerHTTPSFixture{}
			exchanges := 0
			b := newTestBroker(Options{Store: s, HTTPSCallbacks: relay, StateDir: t.TempDir(), HTTPClient: &http.Client{Transport: transport(func(r *http.Request) (*http.Response, error) {
				body := ""
				switch r.URL.Path {
				case "/api/oauth.v2.access":
					exchanges++
					if r.Method != "POST" {
						t.Fatal("not token POST")
					}
					body = `{"ok":true,"team":{"id":"T1"},"authed_user":{"id":"U1","access_token":"private-user-token","scope":"channels:history","expires_in":3600}}`
				case "/api/auth.test":
					body = `{"ok":true,"team_id":"T1","user_id":"U1"}`
					if wrongWorkspace {
						body = `{"ok":true,"team_id":"T2","user_id":"U1"}`
					}
				default:
					t.Fatal("unexpected endpoint")
				}
				return &http.Response{StatusCode: 200, Body: io.NopCloser(strings.NewReader(body)), Header: http.Header{}}, nil
			})}})
			defer b.Close()
			config := `{"provider":"slack","client_id":"fake-client","client_secret":"fake-client-secret","redirect_uri":"https://trusted-owner.invalid/oauth/personal-connectors/callback"}`
			if _, err := ImportClient(ctx, s, strings.NewReader(config)); err != nil {
				t.Fatal(err)
			}
			attempt, err := b.Start(ctx, a, ConnectRequest{Provider: "slack", ConnectionID: "work", AccountID: "T1:U1", Domain: "work", Scopes: []string{"channels:history"}, Resources: []string{"C1"}})
			if err != nil {
				t.Fatal(err)
			}
			auth, _ := url.Parse(attempt.AuthorizationURL)
			if auth.Query().Get("scope") != "" || auth.Query().Get("user_scope") != "channels:history" {
				t.Fatal("requested bot scopes")
			}
			if err = relay.callback(relay.redirect + "?state=wrong&code=fake"); err == nil || exchanges != 0 {
				t.Fatal("state mismatch exchanged token")
			}
			callback := relay.redirect + "?" + url.Values{"state": {auth.Query().Get("state")}, "code": {"fake-code"}}.Encode()
			err = relay.callback(callback)
			if wrongWorkspace {
				if err == nil {
					t.Fatal("cross-workspace auth accepted")
				}
			} else if err != nil {
				t.Fatal(err)
			}
			if exchanges != 1 {
				t.Fatal("unexpected exchange count")
			}
			if err = relay.callback(callback); err == nil || exchanges != 1 {
				t.Fatal("authorization code replay")
			}
			connections, err := b.List(ctx, a)
			if err != nil {
				t.Fatal(err)
			}
			if wrongWorkspace {
				if len(connections) != 0 {
					t.Fatal("failed identity persisted")
				}
			} else if len(connections) != 1 || connections[0].WorkspaceID != "T1" || connections[0].AccountID != "T1:U1" {
				t.Fatal("wrong identity persisted")
			}
		})
	}
}
