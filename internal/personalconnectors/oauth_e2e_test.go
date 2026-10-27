package personalconnectors

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"
)

func TestGoogleOAuthCallbackRefreshAndDisconnectE2E(t *testing.T) {
	ctx := context.Background()
	s := &memoryStore{values: map[string][]byte{}}
	tokenCalls := 0
	client := &http.Client{Transport: transport(func(r *http.Request) (*http.Response, error) {
		body := `{}`
		switch r.URL.Host + r.URL.Path {
		case "oauth2.googleapis.com/token":
			tokenCalls++
			if r.Method != "POST" {
				t.Fatal("token method")
			}
			form, _ := io.ReadAll(r.Body)
			if !strings.Contains(string(form), "client_id=client") {
				t.Fatal("client missing")
			}
			body = `{"access_token":"access-secret","refresh_token":"refresh-secret","expires_in":3600,"scope":"https://www.googleapis.com/auth/gmail.readonly openid email"}`
		case "www.googleapis.com/oauth2/v2/userinfo":
			body = `{"id":"google-subject","email":"synthetic@example.invalid","verified_email":true}`
		case "gmail.googleapis.com/gmail/v1/users/me/threads":
			body = `{"threads":[]}`
		default:
			t.Fatalf("unexpected endpoint %s", r.URL.Host+r.URL.Path)
		}
		return &http.Response{StatusCode: 200, Header: http.Header{}, Body: io.NopCloser(strings.NewReader(body))}, nil
	})}
	if _, e := ImportClient(ctx, s, strings.NewReader(`{"provider":"gmail","client_id":"client"}`)); e != nil {
		t.Fatal(e)
	}
	b := newTestBroker(Options{StateDir: t.TempDir(), Store: s, HTTPClient: client})
	defer b.Close()
	a := Actor{"personal", "space", "owner-ui:live"}
	v, e := b.Start(ctx, a, ConnectRequest{Provider: "gmail", ConnectionID: "gmail", Domain: "personal", Scopes: []string{"https://www.googleapis.com/auth/gmail.readonly"}, Resources: []string{"me"}})
	if e != nil {
		t.Fatal(e)
	}
	if e = ValidateAuthorizationURL(v.AuthorizationURL); e != nil {
		t.Fatal(e)
	}
	u, _ := url.Parse(v.AuthorizationURL)
	if u.Query().Get("code_challenge_method") != "S256" {
		t.Fatal("PKCE missing")
	}
	p := b.attempts[v.ID]
	invalid := httptest.NewRecorder()
	b.oauthCallback(p, invalid, httptest.NewRequest("GET", p.redirect+"?state=wrong&code=x", nil))
	if invalid.Code != 400 || tokenCalls != 0 {
		t.Fatal("state bypass")
	}
	callback := httptest.NewRecorder()
	b.oauthCallback(p, callback, httptest.NewRequest("GET", p.redirect+"?state="+url.QueryEscape(p.state)+"&code=code", nil))
	if callback.Code != 200 {
		t.Fatalf("callback %d %s", callback.Code, callback.Body.String())
	}
	status, e := b.Attempt(a, v.ID)
	if e != nil || status.State != "connected" || status.AuthorizationURL != "" {
		t.Fatalf("status %#v %v", status, e)
	}
	// Expire only the access token; ordinary read must refresh once without user input.
	if e = b.update(ctx, a, func(s *stored) error {
		g := s.Grants["gmail"]
		g.ExpiresAt = time.Now().Add(-time.Minute)
		s.Grants["gmail"] = g
		return nil
	}); e != nil {
		t.Fatal(e)
	}
	result, e := b.Call(ctx, Actor{"personal", "space", "session"}, Request{ConnectionID: "gmail", Operation: "gmail.search", Arguments: json.RawMessage(`{}`)})
	if e != nil || result.State != "completed" || tokenCalls != 2 {
		t.Fatalf("refresh %#v %v %d", result, e, tokenCalls)
	}
	if e = b.Disconnect(ctx, a, "gmail"); e != nil {
		t.Fatal(e)
	}
	if _, e = b.Call(ctx, Actor{"personal", "space", "session"}, Request{ConnectionID: "gmail", Operation: "gmail.search", Arguments: json.RawMessage(`{}`)}); e == nil {
		t.Fatal("disconnected grant used")
	}
}
func TestOAuthDisconnectBeforeFirstCallbackCannotResurrect(t *testing.T) {
	ctx := context.Background()
	s := &memoryStore{values: map[string][]byte{}}
	_, _ = ImportClient(ctx, s, strings.NewReader(`{"provider":"gmail","client_id":"client"}`))
	b := newTestBroker(Options{StateDir: t.TempDir(), Store: s})
	defer b.Close()
	a := Actor{"personal", "space", "owner-ui:live"}
	v, e := b.Start(ctx, a, ConnectRequest{Provider: "gmail", ConnectionID: "new", Domain: "personal", Scopes: []string{"https://www.googleapis.com/auth/gmail.readonly"}, Resources: []string{"me"}})
	if e != nil {
		t.Fatal(e)
	}
	p := b.attempts[v.ID]
	if e = b.Disconnect(ctx, a, "new"); e != nil {
		t.Fatal(e)
	}
	w := httptest.NewRecorder()
	b.oauthCallback(p, w, httptest.NewRequest("GET", p.redirect+"?state="+url.QueryEscape(p.state)+"&code=late", nil))
	if w.Code != 409 {
		t.Fatalf("late callback %d", w.Code)
	}
	connections, e := b.List(ctx, a)
	if e != nil || len(connections) != 0 {
		t.Fatal("grant resurrected")
	}
}
func TestWorkReadRequiresExactOwnerConsentUnlessSelectedResourcesPolicy(t *testing.T) {
	ctx := context.Background()
	s := &memoryStore{values: map[string][]byte{}}
	calls := 0
	b := newTestBroker(Options{StateDir: t.TempDir(), Store: s, Authorization: func(context.Context, Actor) (string, error) { return "turn", nil }, HTTPClient: &http.Client{Transport: transport(func(r *http.Request) (*http.Response, error) {
		calls++
		return &http.Response{StatusCode: 200, Header: http.Header{}, Body: io.NopCloser(strings.NewReader(`{"ok":true,"messages":[]}`))}, nil
	})}})
	defer b.Close()
	a := Actor{"personal", "space", "session"}
	g := Grant{Connection: Connection{ID: "slack", Provider: "slack", AccountID: "T:U", Domain: "work", Scopes: []string{"channels:history"}, Resources: []string{"C123"}}, AccessToken: "token", ExpiresAt: time.Now().Add(time.Hour)}
	if e := b.RegisterVerified(ctx, a, g); e != nil {
		t.Fatal(e)
	}
	r := Request{"slack", "slack.history", json.RawMessage(`{"channel_id":"C123"}`), "read"}
	pending, e := b.Call(ctx, a, r)
	if e != nil || pending.State != "awaiting_confirmation" || calls != 0 {
		t.Fatalf("work leaked %#v %v", pending, e)
	}
	done, e := b.Decide(ctx, a, pending.ActionID, pending.Digest, true)
	if e != nil || done.State != "completed" || calls != 1 {
		t.Fatalf("consent %#v %v", done, e)
	}
	g.Connection.WorkReadPolicy = "selected_resources"
	if e = b.RegisterVerified(ctx, a, g); e != nil {
		t.Fatal(e)
	}
	r.RequestID = "read2"
	done, e = b.Call(ctx, a, r)
	if e != nil || done.State != "completed" || calls != 2 {
		t.Fatalf("owner selected read %#v %v", done, e)
	}
}

func TestOAuthCrossBrokerDisconnectTombstoneRejectsLateCallback(t *testing.T) {
	ctx := context.Background()
	s := &memoryStore{values: map[string][]byte{}}
	_, _ = ImportClient(ctx, s, strings.NewReader(`{"provider":"gmail","client_id":"client"}`))
	client := &http.Client{Transport: transport(func(r *http.Request) (*http.Response, error) {
		body := `{"access_token":"secret","expires_in":3600,"scope":"https://www.googleapis.com/auth/gmail.readonly"}`
		if r.URL.Path == "/oauth2/v2/userinfo" {
			body = `{"id":"subject","email":"synthetic@example.invalid","verified_email":true}`
		}
		return &http.Response{StatusCode: 200, Header: http.Header{}, Body: io.NopCloser(strings.NewReader(body))}, nil
	})}
	b1 := newTestBroker(Options{StateDir: t.TempDir(), Store: s, HTTPClient: client})
	defer b1.Close()
	b2 := newTestBroker(Options{StateDir: t.TempDir(), Store: s, HTTPClient: client})
	defer b2.Close()
	a := Actor{"personal", "space", "owner-ui:live"}
	v, e := b1.Start(ctx, a, ConnectRequest{Provider: "gmail", ConnectionID: "new", Domain: "personal", Scopes: []string{"https://www.googleapis.com/auth/gmail.readonly"}, Resources: []string{"me"}})
	if e != nil {
		t.Fatal(e)
	}
	p := b1.attempts[v.ID]
	if e = b2.Disconnect(ctx, a, "new"); e != nil {
		t.Fatal(e)
	}
	w := httptest.NewRecorder()
	b1.oauthCallback(p, w, httptest.NewRequest("GET", p.redirect+"?state="+url.QueryEscape(p.state)+"&code=late", nil))
	if w.Code != 500 {
		t.Fatalf("late callback %d", w.Code)
	}
	connections, e := b1.List(ctx, a)
	if e != nil || len(connections) != 0 {
		t.Fatal("cross-process grant resurrected")
	}
}

// This store models a secure backend that can commit after its context becomes
// cancelled. A successful Update alone must never be completed OAuth consent.
type oauthCancelAtCommit struct {
	*memoryStore
	hook func()
}

func (s *oauthCancelAtCommit) Update(c context.Context, n, k string, f func([]byte) ([]byte, error)) error {
	return s.memoryStore.Update(c, n, k, func(v []byte) ([]byte, error) {
		next, e := f(v)
		if e == nil && s.hook != nil {
			hook := s.hook
			s.hook = nil
			hook()
		}
		return next, e
	})
}
func TestCancelledOAuthCredentialCommitCannotActivateAfterRestart(t *testing.T) {
	ctx := context.Background()
	s := &oauthCancelAtCommit{memoryStore: &memoryStore{values: map[string][]byte{}}}
	_, _ = ImportClient(ctx, s, strings.NewReader(`{"provider":"gmail","client_id":"client"}`))
	client := &http.Client{Transport: transport(func(r *http.Request) (*http.Response, error) {
		body := `{"access_token":"secret","expires_in":3600,"scope":"https://www.googleapis.com/auth/gmail.readonly"}`
		if r.URL.Path == "/oauth2/v2/userinfo" {
			body = `{"id":"subject","email":"synthetic@example.invalid","verified_email":true}`
		}
		return &http.Response{StatusCode: 200, Header: http.Header{}, Body: io.NopCloser(strings.NewReader(body))}, nil
	})}
	dir := t.TempDir()
	b1 := newTestBroker(Options{StateDir: dir, Store: s, HTTPClient: client})
	defer b1.Close()
	a := Actor{"personal", "space", "owner-ui:live"}
	v, e := b1.Start(ctx, a, ConnectRequest{Provider: "gmail", ConnectionID: "new", Domain: "personal", Scopes: []string{"https://www.googleapis.com/auth/gmail.readonly"}, Resources: []string{"me"}})
	if e != nil {
		t.Fatal(e)
	}
	p := b1.attempts[v.ID]
	s.hook = p.cancel
	w := httptest.NewRecorder()
	b1.oauthCallback(p, w, httptest.NewRequest("GET", p.redirect+"?state="+url.QueryEscape(p.state)+"&code=late", nil))
	if w.Code == 200 {
		t.Fatal("cancelled commit reported success")
	}
	b2 := newTestBroker(Options{StateDir: dir, Store: s, HTTPClient: client})
	defer b2.Close()
	connections, e := b2.List(ctx, a)
	if e != nil {
		t.Fatal(e)
	}
	if len(connections) > 0 && connections[0].State == "connected" {
		t.Fatal("cancelled grant activated after restart")
	}
	if _, e = b2.Call(ctx, Actor{"personal", "space", "session"}, Request{ConnectionID: "new", Operation: "gmail.search", Arguments: json.RawMessage(`{}`)}); e == nil {
		t.Fatal("cancelled grant authorized read")
	}
}
