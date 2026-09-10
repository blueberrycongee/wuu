package account

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"io"
	"net/http"
	"net/http/cookiejar"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/blueberrycongee/wuu/internal/remote/pgtest"
	"github.com/blueberrycongee/wuu/internal/remote/secure"
)

func TestGitHubConfiguration(t *testing.T) {
	for _, c := range []GitHubConfig{{ClientID: "id"}, {ClientSecret: "secret"}, {ClientID: "id", ClientSecret: "secret", PublicURL: "http://example.com"}} {
		if _, err := NewGitHubAuth(c); err == nil {
			t.Fatal("invalid configuration accepted")
		}
	}
	if g, err := NewGitHubAuth(GitHubConfig{}); err != nil || g != nil {
		t.Fatal("optional provider should be disabled")
	}
}
func oauthPost(t *testing.T, base, path string, in any, status int) map[string]any {
	t.Helper()
	data, _ := json.Marshal(in)
	res, err := http.Post(base+"/v1/account/github/"+path, "application/json", bytes.NewReader(data))
	if err != nil {
		t.Fatal(err)
	}
	defer res.Body.Close()
	raw, _ := io.ReadAll(res.Body)
	if res.StatusCode != status {
		t.Fatalf("%s: status %d, %s", path, res.StatusCode, raw)
	}
	out := map[string]any{}
	if json.Unmarshal(raw, &out) != nil {
		t.Fatalf("invalid JSON: %s", raw)
	}
	return out
}
func TestGitHubBoundAuthorization(t *testing.T) {
	g, _ := NewGitHubAuth(GitHubConfig{ClientID: "id", ClientSecret: "secret", PublicURL: "http://localhost"})
	h := NewHTTP(nil, true)
	h.GitHub = g
	srv := httptest.NewServer(h)
	defer srv.Close()
	g.config.PublicURL = srv.URL
	verifier := randomToken()
	start := oauthPost(t, srv.URL, "start", map[string]any{"challenge": challenge(verifier)}, 200)
	id := start["request_id"].(string)
	oauthPost(t, srv.URL, "poll", map[string]string{"request_id": id, "verifier": randomToken()}, 401)
	if got := oauthPost(t, srv.URL, "poll", map[string]string{"request_id": id, "verifier": verifier}, 200); got["status"] != "pending" {
		t.Fatal(got)
	}
	res, err := http.Get(srv.URL + "/v1/account/github/callback?state=" + id + "&code=stolen")
	if err != nil {
		t.Fatal(err)
	}
	res.Body.Close()
	if res.StatusCode != 400 {
		t.Fatal("callback without browser cookie accepted")
	}
	jar, _ := cookiejar.New(nil)
	browser := &http.Client{Jar: jar, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
	res, err = browser.Get(start["authorize_url"].(string))
	if err != nil {
		t.Fatal(err)
	}
	res.Body.Close()
	u, _ := url.Parse(res.Header.Get("Location"))
	q := u.Query()
	if q.Get("scope") != "" || q.Get("code_challenge_method") != "S256" || q.Get("code_challenge") == "" {
		t.Fatalf("unexpected authorization parameters: %v", q)
	}
	res, err = browser.Get(srv.URL + "/v1/account/github/callback?state=" + id + "&error=access_denied")
	if err != nil {
		t.Fatal(err)
	}
	res.Body.Close()
	oauthPost(t, srv.URL, "poll", map[string]string{"request_id": id, "verifier": verifier}, 400)
	oauthPost(t, srv.URL, "cancel", map[string]string{"request_id": id, "verifier": verifier}, 200)
	oauthPost(t, srv.URL, "poll", map[string]string{"request_id": id, "verifier": verifier}, 401)
}
func TestGitHubLoginRoundTrip(t *testing.T) {
	s, err := Open(pgtest.URL(t))
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	var expectedPKCE string
	provider := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/token" {
			r.ParseForm()
			if r.Form.Get("client_secret") != "private-test-secret" || challenge(r.Form.Get("code_verifier")) != expectedPKCE {
				t.Error("OAuth secret or PKCE missing")
			}
			writeJSON(w, 200, map[string]string{"access_token": "provider-token"})
			return
		}
		if r.Header.Get("Authorization") != "Bearer provider-token" {
			t.Error("identity fetched without provider token")
		}
		writeJSON(w, 200, map[string]any{"id": 12345, "login": "mutable-handle"})
	}))
	defer provider.Close()
	g, _ := NewGitHubAuth(GitHubConfig{ClientID: "id", ClientSecret: "private-test-secret", PublicURL: "http://localhost"})
	g.tokenURL = provider.URL + "/token"
	g.userURL = provider.URL + "/user"
	h := NewHTTP(s, true)
	h.GitHub = g
	srv := httptest.NewServer(h)
	defer srv.Close()
	g.config.PublicURL = srv.URL
	verifier := randomToken()
	start := oauthPost(t, srv.URL, "start", map[string]any{"challenge": challenge(verifier), "native": true}, 200)
	id := start["request_id"].(string)
	jar, _ := cookiejar.New(nil)
	browser := &http.Client{Jar: jar, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
	res, err := browser.Get(start["authorize_url"].(string))
	if err != nil {
		t.Fatal(err)
	}
	res.Body.Close()
	u, _ := url.Parse(res.Header.Get("Location"))
	expectedPKCE = u.Query().Get("code_challenge")
	res, err = browser.Get(srv.URL + "/v1/account/github/callback?state=" + id + "&code=test-code")
	if err != nil {
		t.Fatal(err)
	}
	raw, _ := io.ReadAll(res.Body)
	res.Body.Close()
	if !bytes.Contains(raw, []byte("wuu://account/github")) || bytes.Contains(raw, []byte("provider-token")) || bytes.Contains(raw, []byte(verifier)) {
		t.Fatal("callback must only return to app, without credentials")
	}
	poll := oauthPost(t, srv.URL, "poll", map[string]string{"request_id": id, "verifier": verifier}, 200)
	username := poll["username"].(string)
	device, _ := secure.NewIdentity()
	pub := secure.EncodeKey(device.Public())
	body := map[string]string{"request_id": id, "verifier": verifier, "pub": pub, "role": "phone", "name": "test phone", "proof": "bad"}
	oauthPost(t, srv.URL, "complete", body, 400)
	body["proof"] = base64.RawURLEncoding.EncodeToString(device.SignRelayAuth([]byte("wuu/account/enroll/v1:"+username), "phone"))
	result := oauthPost(t, srv.URL, "complete", body, 200)
	token := result["token"].(string)
	if d, err := s.Authenticate(token); err != nil || d.Account != username {
		t.Fatalf("invalid Wuu session: %v", err)
	}
	repeated := oauthPost(t, srv.URL, "complete", body, 200)
	if repeated["token"] != token {
		t.Fatal("lost response retry rotated session")
	}
	same, err := s.githubAccount("12345", false)
	if err != nil || same != username {
		t.Fatal("provider subject must resolve to same account")
	}
	if _, err = s.githubAccount("67890", false); err == nil {
		t.Fatal("registration disabled but new account created")
	}
	// A GitHub handle cannot claim an existing password account; identity uses subject.
	if s.AuthMethod(username) != "github" {
		t.Fatal("provider identity missing")
	}
	if _, err = s.Reset(username, "", "a long new password", false); err == nil {
		t.Fatal("OAuth account acquired a password")
	}
	g.mu.Lock()
	g.pending[id].expires = time.Now().Add(-time.Second)
	g.mu.Unlock()
	oauthPost(t, srv.URL, "poll", map[string]string{"request_id": id, "verifier": verifier}, 401)
}
func TestGitHubExchangeDoesNotTrustErrorResponses(t *testing.T) {
	provider := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(401)
		io.WriteString(w, `{"access_token":"not-a-token"}`)
	}))
	defer provider.Close()
	g, _ := NewGitHubAuth(GitHubConfig{ClientID: "id", ClientSecret: "never-log-this", PublicURL: "http://localhost"})
	g.tokenURL = provider.URL
	_, err := g.exchange(context.Background(), "code", randomToken())
	if err == nil || strings.Contains(err.Error(), "never-log-this") {
		t.Fatal("provider failure was accepted or exposed secret")
	}
}

func TestGitHubMigrationPreservesPasswordAccounts(t *testing.T) {
	dsn := pgtest.URL(t)
	s, err := Open(dsn)
	if err != nil {
		t.Fatal(err)
	}
	in := loginInput(t, "legacy-user", "host")
	session, err := s.Login(in, true)
	if err != nil {
		t.Fatal(err)
	}
	// Reconstruct the released v1 schema with a real account and active device.
	if _, err = s.db.Exec(`DROP TABLE account_identities; UPDATE schema_version SET version=1`); err != nil {
		t.Fatal(err)
	}
	s.Close()
	s, err = Open(dsn)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	if _, err = s.Authenticate(session.Token); err != nil {
		t.Fatalf("migration invalidated old session: %v", err)
	}
	if _, err = s.Login(in, false); err != nil {
		t.Fatalf("migration invalidated password: %v", err)
	}
	username, err := s.githubAccount("9876", true, "first-handle")
	if err != nil {
		t.Fatal(err)
	}
	renamed, err := s.githubAccount("9876", true, "new-handle")
	if err != nil || renamed != username {
		t.Fatal("GitHub rename split identity")
	}
	if s.DisplayName(username) != "new-handle" {
		t.Fatal("display name did not refresh")
	}
}
