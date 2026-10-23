package sdk

import (
	"context"
	"crypto"
	"crypto/rand"
	"crypto/rsa"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"errors"
	"io"
	"math/big"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/blueberrycongee/wuu/internal/appserver"
	"github.com/blueberrycongee/wuu/internal/clientauthority"
	"github.com/blueberrycongee/wuu/internal/credentialstore"
	"github.com/blueberrycongee/wuu/internal/personalcalendar"
	"github.com/blueberrycongee/wuu/internal/userspace"
)

// This store belongs only to the test. Neither the desktop secure store nor an
// actual account is accessed by the SDK composition exercised below.
type calendarE2EStore struct {
	mu            sync.Mutex
	values        map[string][]byte
	failDelete    bool
	commitStarted chan struct{}
}

func (s *calendarE2EStore) Get(_ context.Context, ns, key string) ([]byte, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	v, ok := s.values[ns+key]
	if !ok {
		return nil, credentialstore.ErrNotFound
	}
	return append([]byte(nil), v...), nil
}
func (s *calendarE2EStore) Set(ctx context.Context, ns, key string, v []byte) error {
	return s.Update(ctx, ns, key, func([]byte) ([]byte, error) { return v, nil })
}
func (s *calendarE2EStore) Delete(ctx context.Context, ns, key string) error {
	return s.Update(ctx, ns, key, func([]byte) ([]byte, error) { return nil, nil })
}
func (s *calendarE2EStore) Update(ctx context.Context, ns, key string, f func([]byte) ([]byte, error)) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	v, e := f(append([]byte(nil), s.values[ns+key]...))
	if e != nil {
		return e
	}
	if v == nil {
		if s.failDelete {
			return errors.New("synthetic deletion failure")
		}
		delete(s.values, ns+key)
	} else {
		if s.commitStarted != nil {
			started := s.commitStarted
			s.commitStarted = nil
			close(started)
			<-ctx.Done()
		}
		s.values[ns+key] = append([]byte(nil), v...)
	}
	return nil
}

type calendarE2ETransport func(*http.Request) (*http.Response, error)

func (f calendarE2ETransport) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }
func calendarResponse(code int, v any) *http.Response {
	b, _ := json.Marshal(v)
	return &http.Response{StatusCode: code, Header: make(http.Header), Body: io.NopCloser(strings.NewReader(string(b)))}
}

// The public SDK constructor assembles the real UserHost, generated Personal
// and Calendar processes, service registry, SQLite, secure-store adapter and
// OAuth broker. Only storage and Google HTTP are substituted. The sole socket
// is the broker's actual ephemeral loopback callback; no model turn is sent.
func TestPersonalCalendarSDKCompositionE2E(t *testing.T) {
	if os.Getenv("WUU_CALENDAR_E2E") != "1" {
		t.Skip("set WUU_CALENDAR_E2E=1 for local loopback and generated plugin E2E")
	}
	state := t.TempDir()
	t.Setenv("WUU_HOME", state)
	configPath := filepath.Join(t.TempDir(), "config.json")
	if err := os.WriteFile(configPath, []byte(`{"default_provider":"fixture","providers":{"fixture":{"type":"openai-compatible","base_url":"https://model.invalid/v1","model":"fixture"}}}`), 0600); err != nil {
		t.Fatal(err)
	}
	store := &calendarE2EStore{values: map[string][]byte{}}
	const clientID = "sdk-fixture.apps.googleusercontent.com"
	if _, err := personalcalendar.ImportDesktopClient(context.Background(), store, strings.NewReader(`{"client_id":"sdk-fixture.apps.googleusercontent.com","client_secret":"synthetic-client-secret"}`)); err != nil {
		t.Fatal(err)
	}
	private, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatal(err)
	}
	jwks := map[string]any{"keys": []any{map[string]any{"kid": "sdk-fixture", "kty": "RSA", "use": "sig", "alg": "RS256", "n": base64.RawURLEncoding.EncodeToString(private.N.Bytes()), "e": base64.RawURLEncoding.EncodeToString(big.NewInt(int64(private.E)).Bytes())}}}
	var mu sync.Mutex
	nonce, mode := "", "ok"
	openMode := "normal"
	calls := map[string]int{}
	transport := calendarE2ETransport(func(r *http.Request) (*http.Response, error) {
		mu.Lock()
		defer mu.Unlock()
		calls[r.URL.Host+r.URL.Path]++
		if r.URL.Host == "oauth2.googleapis.com" && r.URL.Path == "/token" {
			if err := r.ParseForm(); err != nil {
				return nil, err
			}
			if r.Form.Get("client_secret") != "synthetic-client-secret" {
				return nil, errors.New("host client not used")
			}
			payload, _ := json.Marshal(map[string]any{"iss": "https://accounts.google.com", "aud": clientID, "sub": "sdk-fixture-sub", "nonce": nonce, "iat": time.Now().Unix(), "exp": time.Now().Add(time.Hour).Unix()})
			signed := base64.RawURLEncoding.EncodeToString([]byte(`{"alg":"RS256","kid":"sdk-fixture"}`)) + "." + base64.RawURLEncoding.EncodeToString(payload)
			hash := sha256.Sum256([]byte(signed))
			signature, e := rsa.SignPKCS1v15(rand.Reader, private, crypto.SHA256, hash[:])
			if e != nil {
				return nil, e
			}
			return calendarResponse(200, map[string]any{"access_token": "synthetic-access-token", "refresh_token": "synthetic-refresh-token", "token_type": "Bearer", "expires_in": 3600, "scope": strings.Join(personalcalendar.Scopes(), " "), "id_token": signed + "." + base64.RawURLEncoding.EncodeToString(signature)}), nil
		}
		if r.URL.Host != "www.googleapis.com" {
			return nil, errors.New("mock rejected external target")
		}
		if r.URL.Path == "/oauth2/v3/certs" {
			return calendarResponse(200, jwks), nil
		}
		if r.Method != "GET" || r.Header.Get("Authorization") != "Bearer synthetic-access-token" {
			return nil, errors.New("read-only credential boundary failed")
		}
		if r.URL.Path == "/calendar/v3/users/me/calendarList" {
			return calendarResponse(200, map[string]any{"items": []any{map[string]any{"id": "fixture@example.invalid", "summary": "SDK fixture", "accessRole": "owner", "primary": true, "timeZone": "UTC"}}}), nil
		}
		if !strings.HasSuffix(r.URL.Path, "/events") {
			return nil, errors.New("mock rejected unknown route")
		}
		if mode == "offline" {
			return nil, errors.New("synthetic offline")
		}
		return calendarResponse(200, map[string]any{"timeZone": "UTC", "items": []any{map[string]any{"id": "fixture-event", "status": "confirmed", "summary": "SDK synthetic appointment", "start": map[string]string{"dateTime": "2026-10-24T10:00:00Z"}, "end": map[string]string{"dateTime": "2026-10-24T11:00:00Z"}}}}), nil
	})
	urls := make(chan string, 4)
	options := Options{UserSpaceID: "local", ProductDomain: "personal", WorkDir: t.TempDir(), HomeDir: t.TempDir(), ConfigPath: configPath, PersonalCalendar: personalcalendar.DesktopOptions{ClientID: clientID, Store: store, HTTPClient: &http.Client{Transport: transport}}, OpenPersonalCalendarAuthorizationURL: func(_ context.Context, raw string) error {
		if err := personalcalendar.ValidateAuthorizationURL(raw); err != nil {
			return err
		}
		u, _ := url.Parse(raw)
		mu.Lock()
		nonce = u.Query().Get("nonce")
		selectedMode := openMode
		mu.Unlock()
		if selectedMode == "complete-then-fail" {
			response, e := (&http.Client{Timeout: 5 * time.Second}).Get(u.Query().Get("redirect_uri") + "?state=" + url.QueryEscape(u.Query().Get("state")) + "&code=synthetic-code")
			if e != nil {
				return e
			}
			_ = response.Body.Close()
			if response.StatusCode != 200 {
				return errors.New("fixture callback failed")
			}
			return errors.New("synthetic opener failed after completion")
		}
		urls <- raw
		return nil
	}}
	ctx, cancel := context.WithTimeout(context.Background(), 45*time.Second)
	defer cancel()
	binding := userspace.Binding{UserSpaceID: "local", ProductDomain: "personal"}
	owner, err := clientauthority.AdmitDesktop(ctx, binding, strings.NewReader(`{"version":1,"user_space_id":"local","product_domain":"personal"}`))
	if err != nil {
		t.Fatal(err)
	}
	start := func(opts Options) (*Runtime, *Client, func(*Client, string, any) (json.RawMessage, error)) {
		rt, e := New(opts)
		if e != nil {
			t.Fatal(e)
		}
		c, e := rt.Connect(owner, ClientOptions{Name: "sdk-calendar-fixture"})
		if e != nil {
			rt.Close()
			t.Fatal(e)
		}
		t.Cleanup(func() { _ = c.Close(ctx); _ = rt.Close() })
		id, fingerprint := "", ""
		for _, p := range rt.core.ActivePlugins {
			if p.ID == "personal" {
				id = p.SubjectID
				fingerprint = p.Fingerprint
			}
		}
		if id == "" {
			t.Fatal("actual Personal plugin not activated")
		}
		invoke := func(client *Client, method string, input any) (json.RawMessage, error) {
			raw, _ := json.Marshal(input)
			var out appserver.PluginClientRequestResult
			e := client.rpc.call(ctx, appserver.MethodPluginClientRequest, appserver.PluginClientRequestParams{ID: id, Fingerprint: fingerprint, Method: method, Input: raw}, &out)
			return out.Result, e
		}
		return rt, c, invoke
	}
	must := func(call func(*Client, string, any) (json.RawMessage, error), c *Client, method string, input any) json.RawMessage {
		out, e := call(c, method, input)
		if e != nil {
			t.Fatalf("%s: %v", method, e)
		}
		if strings.Contains(string(out), "synthetic-access-token") || strings.Contains(string(out), "synthetic-client-secret") || strings.Contains(string(out), "accounts.google.com") {
			t.Fatal("host-only material escaped")
		}
		return out
	}
	unconfigured := options
	unconfigured.PersonalCalendar.ClientID = ""
	empty, emptyClient, emptyCall := start(unconfigured)
	if out := must(emptyCall, emptyClient, "connections.status", map[string]any{}); !strings.Contains(string(out), `"unavailable"`) {
		t.Fatalf("unconfigured status=%s", out)
	}
	if len(calls) != 0 {
		t.Fatal("unconfigured host contacted Google")
	}
	_ = emptyClient.Close(ctx)
	if e := empty.Close(); e != nil {
		t.Fatal(e)
	}
	rt, c, call := start(options)
	untrusted, e := rt.Connect(ctx, ClientOptions{Name: "untrusted-fixture"})
	if e != nil {
		t.Fatal(e)
	}
	if _, e = call(untrusted, "connections.start", map[string]any{"owner": true}); e == nil {
		t.Fatal("payload forged admission")
	}
	_ = untrusted.Close(ctx)
	attempt := must(call, c, "connections.start", map[string]any{})
	var envelope struct {
		Value personalcalendar.AuthorizationAttempt `json:"value"`
	}
	if e = json.Unmarshal(attempt, &envelope); e != nil || envelope.Value.ID == "" {
		t.Fatalf("start=%s %v", attempt, e)
	}
	var raw string
	select {
	case raw = <-urls:
	case <-ctx.Done():
		t.Fatal(ctx.Err())
	}
	u, _ := url.Parse(raw)
	callback := u.Query().Get("redirect_uri") + "?state=" + url.QueryEscape(u.Query().Get("state")) + "&code=synthetic-code"
	response, e := (&http.Client{Timeout: 5 * time.Second}).Get(callback)
	if e != nil {
		t.Fatal(e)
	}
	_ = response.Body.Close()
	if response.StatusCode != 200 {
		t.Fatalf("callback status=%d", response.StatusCode)
	}
	if out := must(call, c, "connections.attempt", map[string]any{"attempt_id": envelope.Value.ID}); !strings.Contains(string(out), `"connected"`) {
		t.Fatalf("attempt=%s", out)
	}
	var status struct {
		Value personalcalendar.Connection `json:"value"`
	}
	out := must(call, c, "connections.status", map[string]any{})
	if e = json.Unmarshal(out, &status); e != nil || status.Value.State != "connected" {
		t.Fatalf("status=%s %v", out, e)
	}
	must(call, c, "connections.select", map[string]any{"connection_id": status.Value.ID, "calendar_ids": []string{"fixture@example.invalid"}})
	query := map[string]any{"calendar_id": "fixture@example.invalid", "time_min": "2026-10-24T00:00:00Z", "time_max": "2026-10-25T00:00:00Z", "timezone": "UTC"}
	out = must(call, c, "connections.agenda", query)
	if !strings.Contains(string(out), "SDK synthetic appointment") || !strings.Contains(string(out), `"freshness":"fresh"`) {
		t.Fatalf("agenda=%s", out)
	}
	mu.Lock()
	mode = "offline"
	mu.Unlock()
	out = must(call, c, "connections.agenda", query)
	if !strings.Contains(string(out), `"state":"error"`) || !strings.Contains(string(out), `"freshness":"stale"`) || !strings.Contains(string(out), "SDK synthetic appointment") {
		t.Fatalf("stale=%s", out)
	}
	mu.Lock()
	mode = "ok"
	mu.Unlock()
	_ = c.Close(ctx)
	if e = rt.Close(); e != nil {
		t.Fatal(e)
	}
	rt, c, call = start(options)
	out = must(call, c, "connections.status", map[string]any{})
	if !strings.Contains(string(out), `"connected"`) {
		t.Fatalf("production state-root recovery=%s", out)
	}
	must(call, c, "connections.disconnect", map[string]any{"connection_id": status.Value.ID, "revoke": false})
	out = must(call, c, "connections.agenda", query)
	if strings.Contains(string(out), "SDK synthetic appointment") {
		t.Fatalf("disconnect retained cache=%s", out)
	}
	// The browser can report failure after authorization has already won.
	// The UI must receive already_completed, never a false cancellation success.
	mu.Lock()
	openMode = "complete-then-fail"
	mu.Unlock()
	out = must(call, c, "connections.start", map[string]any{})
	if !strings.Contains(string(out), `"authorization_already_completed"`) {
		t.Fatalf("opener completion outcome lost=%s", out)
	}
	out = must(call, c, "connections.status", map[string]any{})
	if e = json.Unmarshal(out, &status); e != nil || status.Value.State != "connected" {
		t.Fatalf("completion lost=%s", out)
	}
	must(call, c, "connections.disconnect", map[string]any{"connection_id": status.Value.ID, "revoke": false})
	mu.Lock()
	openMode = "normal"
	mu.Unlock()
	// Exercise cancellation while the secure backend is completing its write.
	// The synthetic store acknowledges the write after cancellation, then fails
	// conditional cleanup. This reproduces the original cross-restart race.
	must(call, c, "connections.start", map[string]any{})
	raw = <-urls
	u, _ = url.Parse(raw)
	started := make(chan struct{})
	store.mu.Lock()
	store.failDelete = true
	store.commitStarted = started
	store.mu.Unlock()
	callbackDone := make(chan error, 1)
	go func() {
		response, e := (&http.Client{Timeout: 5 * time.Second}).Get(u.Query().Get("redirect_uri") + "?state=" + url.QueryEscape(u.Query().Get("state")) + "&code=synthetic-code")
		if e == nil {
			_ = response.Body.Close()
		}
		callbackDone <- e
	}()
	select {
	case <-started:
	case <-ctx.Done():
		t.Fatal(ctx.Err())
	}
	_ = c.Close(ctx)
	e = rt.Close()
	var failure *personalcalendar.Failure
	if !errors.As(e, &failure) || failure.Code != "credential_cleanup_failed" {
		t.Fatalf("SDK shutdown lost cleanup error: %v", e)
	}
	<-callbackDone
	store.mu.Lock()
	store.failDelete = false
	store.mu.Unlock()
	mu.Lock()
	before := len(calls)
	total := 0
	for _, n := range calls {
		total += n
	}
	mu.Unlock()
	rt, c, call = start(options)
	out = must(call, c, "connections.status", map[string]any{})
	if strings.Contains(string(out), `"connected"`) {
		t.Fatalf("cancelled grant recovered after restart=%s", out)
	}
	mu.Lock()
	after := 0
	for _, n := range calls {
		after += n
	}
	if len(calls) != before || after != total {
		t.Fatal("cancelled grant contacted Google after restart")
	}
	mu.Unlock()
	_ = c.Close(ctx)
	if e = rt.Close(); e != nil {
		t.Fatal(e)
	}
	mu.Lock()
	defer mu.Unlock()
	t.Logf("SDK -> UserHost JSONL -> generated Personal -> registry -> generated Calendar -> real broker -> mock Google passed; calls=%v; no real Google, Keychain, model or device", calls)
}
