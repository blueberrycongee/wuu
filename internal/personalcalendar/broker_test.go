package personalcalendar

// These protocol contracts are written before implementation. All credentials,
// account IDs and Google responses are synthetic. No system keychain is opened.
import (
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/blueberrycongee/wuu/internal/credentialstore"
)

type memoryStore struct {
	mu         sync.Mutex
	values     map[string][]byte
	failDelete bool
}

func (s *memoryStore) Get(_ context.Context, n, k string) ([]byte, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	v, ok := s.values[n+k]
	if !ok {
		return nil, credentialstore.ErrNotFound
	}
	return append([]byte(nil), v...), nil
}
func (s *memoryStore) Set(c context.Context, n, k string, v []byte) error {
	return s.Update(c, n, k, func([]byte) ([]byte, error) { return v, nil })
}
func (s *memoryStore) Delete(c context.Context, n, k string) error {
	return s.Update(c, n, k, func([]byte) ([]byte, error) { return nil, nil })
}
func (s *memoryStore) Update(_ context.Context, n, k string, f func([]byte) ([]byte, error)) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	v, e := f(append([]byte(nil), s.values[n+k]...))
	if e != nil {
		return e
	}
	if v == nil {
		if s.failDelete {
			return errors.New("fixture delete failed")
		}
		delete(s.values, n+k)
	} else {
		s.values[n+k] = append([]byte(nil), v...)
	}
	return nil
}

type roundTrip func(*http.Request) (*http.Response, error)

func (f roundTrip) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }
func response(status int, body string) *http.Response {
	return &http.Response{StatusCode: status, Body: io.NopCloser(strings.NewReader(body)), Header: make(http.Header)}
}

var actor = Actor{ProductDomain: "personal", UserSpaceID: "fixture-space", SessionID: "fixture-session"}

func fixture(t *testing.T) (*Broker, *memoryStore) {
	t.Helper()
	s := &memoryStore{values: map[string][]byte{}}
	b := newBroker(Config{ClientID: "fixture.apps.googleusercontent.com"}, s, &http.Client{Transport: roundTrip(func(r *http.Request) (*http.Response, error) {
		if r.URL.Host == "oauth2.googleapis.com" && r.URL.Path == "/token" {
			return response(200, `{"access_token":"fixture-access","refresh_token":"fixture-refresh","expires_in":3600,"token_type":"Bearer","scope":"`+strings.Join(Scopes(), " ")+`","id_token":"fixture-id-token"}`), nil
		}
		if r.URL.Path == "/revoke" {
			return response(200, ``), nil
		}
		if r.URL.Path == "/calendar/v3/users/me/calendarList" {
			return response(200, `{"items":[{"id":"fixture@example.invalid","summary":"Fixture calendar","accessRole":"owner","primary":true,"timeZone":"America/New_York"}]}`), nil
		}
		return response(200, `{"timeZone":"America/New_York","items":[]}`), nil
	})}, t.TempDir()+"/activation")
	b.verifyIdentity = func(context.Context, string, string) (string, error) { return "fixture-sub", nil }
	b.wait = func(context.Context, time.Duration) error { return nil }
	t.Cleanup(func() { _ = b.Close() })
	return b, s
}
func connect(t *testing.T, b *Broker) Connection {
	t.Helper()
	a, e := b.Start(context.Background(), actor)
	if e != nil {
		t.Fatal(e)
	}
	u, _ := url.Parse(a.AuthorizationURL)
	req := httptest.NewRequest("GET", u.Query().Get("redirect_uri")+"?state="+url.QueryEscape(u.Query().Get("state"))+"&code=fixture-code", nil)
	w := httptest.NewRecorder()
	b.callback(a.ID, w, req)
	if w.Code != 200 {
		t.Fatalf("callback: %d %s", w.Code, w.Body)
	}
	c, e := b.Status(context.Background(), actor)
	if e != nil || c.State != "connected" {
		t.Fatalf("status: %+v %v", c, e)
	}
	return c
}
func TestBoundaryAndCallback(t *testing.T) {
	b, s := fixture(t)
	for _, a := range []Actor{{ProductDomain: "coding", UserSpaceID: actor.UserSpaceID, SessionID: "x"}, {ProductDomain: "personal", UserSpaceID: "", SessionID: "x"}} {
		if _, e := b.Start(context.Background(), a); e == nil {
			t.Fatal("accepted invalid actor")
		}
	}
	a, e := b.Start(context.Background(), actor)
	if e != nil {
		t.Fatal(e)
	}
	u, _ := url.Parse(a.AuthorizationURL)
	q := u.Query()
	if q.Get("code_challenge_method") != "S256" || q.Get("access_type") != "offline" {
		t.Fatal("missing PKCE/offline")
	}
	redirect := q.Get("redirect_uri")
	for _, target := range []string{redirect + "?state=forged&code=bad", strings.Replace(redirect, "127.0.0.1", "localhost", 1) + "?state=" + q.Get("state") + "&code=bad", redirect + "?state=" + q.Get("state") + "&state=x&code=bad"} {
		w := httptest.NewRecorder()
		b.callback(a.ID, w, httptest.NewRequest("GET", target, nil))
		if w.Code == 200 {
			t.Fatal("accepted forged callback")
		}
	}
	if len(s.values) != 0 {
		t.Fatal("forged callback stored credentials")
	}
	if _, e = b.Attempt(Actor{ProductDomain: "personal", UserSpaceID: "other", SessionID: "x"}, a.ID); e == nil {
		t.Fatal("foreign attempt accessible")
	}
	req := httptest.NewRequest("GET", redirect+"?state="+q.Get("state")+"&code=fixture-code", nil)
	w := httptest.NewRecorder()
	b.callback(a.ID, w, req)
	if w.Code != 200 {
		t.Fatal(w.Code, w.Body)
	}
	w = httptest.NewRecorder()
	b.callback(a.ID, w, req)
	if w.Code != 409 {
		t.Fatal("replay not rejected", w.Code)
	}
	c, _ := b.Status(context.Background(), actor)
	raw, _ := json.Marshal(c)
	if strings.Contains(string(raw), "fixture-access") || strings.Contains(string(raw), "fixture-refresh") {
		t.Fatal("secret leaked")
	}
	other, _ := b.Status(context.Background(), Actor{ProductDomain: "personal", UserSpaceID: "other", SessionID: "x"})
	if other.State != "unconnected" {
		t.Fatal("cross-user binding")
	}
}
func TestAccountPinAndPartialConsent(t *testing.T) {
	b, _ := fixture(t)
	first := connect(t, b)
	b.verifyIdentity = func(context.Context, string, string) (string, error) { return "other-sub", nil }
	a, _ := b.Start(context.Background(), actor)
	u, _ := url.Parse(a.AuthorizationURL)
	w := httptest.NewRecorder()
	b.callback(a.ID, w, httptest.NewRequest("GET", u.Query().Get("redirect_uri")+"?state="+u.Query().Get("state")+"&code=x", nil))
	if w.Code == 200 {
		t.Fatal("changed Google account silently rebound")
	}
	c, _ := b.Status(context.Background(), actor)
	if c.AccountID != first.AccountID {
		t.Fatal("old binding lost")
	}
	b2, s := fixture(t)
	b2.client.Transport = roundTrip(func(*http.Request) (*http.Response, error) {
		return response(200, `{"access_token":"fixture-access","refresh_token":"fixture-refresh","expires_in":3600,"token_type":"Bearer","scope":"openid","id_token":"fixture-id-token"}`), nil
	})
	a, _ = b2.Start(context.Background(), actor)
	u, _ = url.Parse(a.AuthorizationURL)
	w = httptest.NewRecorder()
	b2.callback(a.ID, w, httptest.NewRequest("GET", u.Query().Get("redirect_uri")+"?state="+u.Query().Get("state")+"&code=x", nil))
	if w.Code == 200 || len(s.values) > 0 {
		t.Fatal("partial consent became connected")
	}
}
func TestRefreshDisconnectAndLateRefresh(t *testing.T) {
	b, s := fixture(t)
	c := connect(t, b)
	b.now = func() time.Time { return time.Now().Add(2 * time.Hour) }
	var count int
	old := b.client.Transport
	b.client.Transport = roundTrip(func(r *http.Request) (*http.Response, error) {
		if r.URL.Path == "/token" {
			count++
			return response(400, `{"error":"invalid_grant","error_description":"fixture-secret"}`), nil
		}
		return old.RoundTrip(r)
	})
	if _, e := b.Calendars(context.Background(), actor, c.ID); e == nil || strings.Contains(e.Error(), "fixture-secret") {
		t.Fatal("refresh failure not sanitized", e)
	}
	state, _ := b.Status(context.Background(), actor)
	if state.State != "needs_reauth" || count != 1 {
		t.Fatal("invalid_grant not fenced", state, count)
	}
	b2, s2 := fixture(t)
	c = connect(t, b2)
	b2.now = func() time.Time { return time.Now().Add(2 * time.Hour) }
	started, release := make(chan struct{}), make(chan struct{})
	old = b2.client.Transport
	b2.client.Transport = roundTrip(func(r *http.Request) (*http.Response, error) {
		if r.URL.Path == "/token" {
			close(started)
			<-release
		}
		return old.RoundTrip(r)
	})
	done := make(chan error, 1)
	go func() { _, e := b2.Calendars(context.Background(), actor, c.ID); done <- e }()
	<-started
	result, e := b2.Disconnect(context.Background(), actor, c.ID, false)
	if e != nil || !result.LocalDeleted {
		t.Fatal(result, e)
	}
	close(release)
	if e = <-done; e == nil {
		t.Fatal("late refresh survived disconnect")
	}
	if len(s2.values) != 0 {
		t.Fatal("late refresh resurrected credential")
	}
	if len(s.values) == 0 {
		t.Fatal("reauth metadata unexpectedly removed")
	}
}
func TestRevokeFailureAndDeleteFailure(t *testing.T) {
	b, s := fixture(t)
	c := connect(t, b)
	b.client.Transport = roundTrip(func(*http.Request) (*http.Response, error) { return nil, errors.New("fixture-refresh must not leak") })
	r, e := b.Disconnect(context.Background(), actor, c.ID, true)
	if e != nil || !r.LocalDeleted || r.Revocation != "unconfirmed" || len(s.values) > 0 {
		t.Fatal(r, e)
	}
	b2, s2 := fixture(t)
	c = connect(t, b2)
	s2.failDelete = true
	r, e = b2.Disconnect(context.Background(), actor, c.ID, false)
	if e == nil || r.LocalDeleted {
		t.Fatal("reported deletion success despite failure")
	}
	state, _ := b2.Status(context.Background(), actor)
	if state.State != "disconnected" {
		t.Fatal("failed deletion left active connection")
	}
}
func TestPageSelectionRetryAndBounds(t *testing.T) {
	b, _ := fixture(t)
	c := connect(t, b)
	q := EventQuery{CalendarID: "fixture@example.invalid", TimeMin: "2026-11-01T00:00:00-04:00", TimeMax: "2026-11-02T00:00:00-05:00", TimeZone: "America/New_York"}
	if _, e := b.EventsPage(context.Background(), actor, c.ID, q); e == nil {
		t.Fatal("read before explicit selection")
	}
	if e := b.Select(context.Background(), actor, c.ID, []string{q.CalendarID}); e != nil {
		t.Fatal(e)
	}
	old := b.client.Transport
	var tries int
	b.client.Transport = roundTrip(func(r *http.Request) (*http.Response, error) {
		if strings.HasSuffix(r.URL.Path, "/events") {
			tries++
			if r.Method != "GET" || r.URL.Query().Get("singleEvents") != "true" || r.URL.Query().Get("showDeleted") != "true" {
				t.Fatal("wrong event request")
			}
			if tries == 1 {
				r := response(429, `{}`)
				r.Header.Set("Retry-After", "0")
				return r, nil
			}
		}
		return old.RoundTrip(r)
	})
	if _, e := b.EventsPage(context.Background(), actor, c.ID, q); e != nil || tries != 2 {
		t.Fatal(e, tries)
	}
	for _, bad := range []EventQuery{{CalendarID: q.CalendarID, TimeMin: q.TimeMin, TimeMax: "2027-01-01T00:00:00Z", TimeZone: q.TimeZone}, {CalendarID: "other", TimeMin: q.TimeMin, TimeMax: q.TimeMax, TimeZone: q.TimeZone}, {CalendarID: q.CalendarID, TimeMin: q.TimeMin, TimeMax: q.TimeMax, TimeZone: "Invalid/Zone"}} {
		if _, e := b.EventsPage(context.Background(), actor, c.ID, bad); e == nil {
			t.Fatal("invalid query accepted")
		}
	}
	foreign := actor
	foreign.UserSpaceID = "other"
	if _, e := b.EventsPage(context.Background(), foreign, c.ID, q); e == nil {
		t.Fatal("cross-user read")
	}
}

func TestRefreshLeaseAndRotation(t *testing.T) {
	b, s := fixture(t)
	c := connect(t, b)
	b.now = func() time.Time { return time.Now().Add(2 * time.Hour) }
	started, release := make(chan struct{}), make(chan struct{})
	old := b.client.Transport
	b.client.Transport = roundTrip(func(r *http.Request) (*http.Response, error) {
		if r.URL.Path == "/token" {
			close(started)
			<-release
			return response(200, `{"access_token":"fixture-rotated-access","refresh_token":"fixture-rotated-refresh","expires_in":3600,"token_type":"Bearer"}`), nil
		}
		return old.RoundTrip(r)
	})
	done := make(chan error, 1)
	go func() { _, e := b.Calendars(context.Background(), actor, c.ID); done <- e }()
	<-started
	second := newBroker(b.config, s, b.client, b.activationDir)
	second.now = b.now
	t.Cleanup(func() { _ = second.Close() })
	if _, e := second.Calendars(context.Background(), actor, c.ID); e == nil || !strings.Contains(e.Error(), "refresh_busy") {
		t.Fatal("parallel refresh not fenced", e)
	}
	close(release)
	if e := <-done; e != nil {
		t.Fatal(e)
	}
	saved, e := b.load(context.Background(), actor)
	if e != nil || saved.RefreshToken != "fixture-rotated-refresh" {
		t.Fatal("rotation not saved", e)
	}
}
func TestTimeoutRetryAndCalendarListCycle(t *testing.T) {
	b, _ := fixture(t)
	c := connect(t, b)
	tries := 0
	b.client.Transport = roundTrip(func(*http.Request) (*http.Response, error) { tries++; return nil, context.DeadlineExceeded })
	if _, e := b.Calendars(context.Background(), actor, c.ID); e == nil || tries != 3 {
		t.Fatal("timeout not bounded", tries, e)
	}
	tries = 0
	b.client.Transport = roundTrip(func(*http.Request) (*http.Response, error) {
		tries++
		return response(200, `{"items":[],"nextPageToken":"repeat"}`), nil
	})
	if _, e := b.Calendars(context.Background(), actor, c.ID); e == nil || tries != 2 {
		t.Fatal("pagination cycle", tries, e)
	}
	b.client.Transport = roundTrip(func(*http.Request) (*http.Response, error) { return response(401, `{"error":"fixture-secret"}`), nil })
	if _, e := b.Calendars(context.Background(), actor, c.ID); e == nil {
		t.Fatal("revoked token accepted")
	}
	status, _ := b.Status(context.Background(), actor)
	if status.State != "needs_reauth" {
		t.Fatal(status)
	}
}
func TestGrantDenialCancelAndConfirmedRevoke(t *testing.T) {
	b, s := fixture(t)
	a, _ := b.Start(context.Background(), actor)
	u, _ := url.Parse(a.AuthorizationURL)
	w := httptest.NewRecorder()
	b.callback(a.ID, w, httptest.NewRequest("GET", u.Query().Get("redirect_uri")+"?state="+u.Query().Get("state")+"&error=access_denied", nil))
	if w.Code == 200 || len(s.values) > 0 {
		t.Fatal("denial connected")
	}
	a, _ = b.Start(context.Background(), actor)
	if e := b.Cancel(actor, a.ID); e != nil {
		t.Fatal(e)
	}
	u, _ = url.Parse(a.AuthorizationURL)
	w = httptest.NewRecorder()
	b.callback(a.ID, w, httptest.NewRequest("GET", u.Query().Get("redirect_uri")+"?state="+u.Query().Get("state")+"&code=x", nil))
	if w.Code == 200 {
		t.Fatal("cancelled attempt connected")
	}
	b2, _ := fixture(t)
	c := connect(t, b2)
	result, e := b2.Disconnect(context.Background(), actor, c.ID, true)
	if e != nil || result.Revocation != "confirmed" || !result.LocalDeleted {
		t.Fatal(result, e)
	}
}

func TestCloseStopsCachedGrantAndLoopbackWorks(t *testing.T) {
	b, _ := fixture(t)
	a, e := b.Start(context.Background(), actor)
	if e != nil {
		t.Fatal(e)
	}
	u, _ := url.Parse(a.AuthorizationURL)
	client := &http.Client{Timeout: time.Second}
	r, e := client.Get(u.Query().Get("redirect_uri") + "?state=" + u.Query().Get("state") + "&code=fixture-code")
	if e != nil {
		t.Fatal(e)
	}
	_ = r.Body.Close()
	if r.StatusCode != 200 {
		t.Fatal(r.StatusCode)
	}
	c, _ := b.Status(context.Background(), actor)
	b.Close()
	if _, e := b.Calendars(context.Background(), actor, c.ID); e == nil {
		t.Fatal("closed broker used cached grant")
	}
}

func TestPKCEExchangeAndRedirectIsolation(t *testing.T) {
	b, _ := fixture(t)
	a, _ := b.Start(context.Background(), actor)
	u, _ := url.Parse(a.AuthorizationURL)
	old := b.client.Transport
	b.client.Transport = roundTrip(func(r *http.Request) (*http.Response, error) {
		if r.URL.Path == "/token" {
			_ = r.ParseForm()
			sum := sha256.Sum256([]byte(r.Form.Get("code_verifier")))
			if base64.RawURLEncoding.EncodeToString(sum[:]) != u.Query().Get("code_challenge") || r.Form.Get("redirect_uri") != u.Query().Get("redirect_uri") || r.Form.Get("code") != "fixture-code" {
				t.Fatal("exchange is not bound to original PKCE/redirect")
			}
		}
		return old.RoundTrip(r)
	})
	w := httptest.NewRecorder()
	b.callback(a.ID, w, httptest.NewRequest("GET", u.Query().Get("redirect_uri")+"?state="+u.Query().Get("state")+"&code=fixture-code", nil))
	if w.Code != 200 {
		t.Fatal(w.Code, w.Body)
	}
	c, _ := b.Status(context.Background(), actor)
	var calls int
	b.client.Transport = roundTrip(func(r *http.Request) (*http.Response, error) {
		calls++
		resp := response(302, "")
		resp.Header.Set("Location", "https://evil.invalid/steal")
		return resp, nil
	})
	if _, e := b.Calendars(context.Background(), actor, c.ID); e == nil || calls != 1 {
		t.Fatal("redirect was followed", e, calls)
	}
}
