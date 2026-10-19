package siwc

// Protocol failure matrix (written before implementation): first registration must
// persist its issued ID before exchange; callbacks bind state, nonce, PKCE and
// exact redirect URI; signatures/issuer/audience/expiry and returning identity
// fail closed; duplicate/cancelled callbacks cannot persist tokens; refreshes
// rotate atomically across managers, terminal errors clear tokens but transient
// errors preserve them; sign-out keeps registration and host without token hints.

import (
	"context"
	"crypto"
	"crypto/rand"
	"crypto/rsa"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math/big"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/blueberrycongee/wuu/internal/credentialstore"
)

type fixture struct {
	t                                                  *testing.T
	server                                             *httptest.Server
	key                                                *rsa.PrivateKey
	manager                                            *Manager
	config                                             Config
	nonce, challenge, redirect, client, subject, scope string
	mutate                                             func(map[string]any)
	badSignature                                       bool
	tokenError                                         string
	tokenStatus                                        int
	refreshes                                          atomic.Int32
	exchanges                                          atomic.Int32
	revokes                                            atomic.Int32
	revokeStatus                                       int
	beforeToken                                        func(url.Values)
	beforeRevoke                                       func()
	jwksUnavailable                                    bool
}

func newFixture(t *testing.T) *fixture {
	t.Helper()
	key, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatal(err)
	}
	f := &fixture{t: t, key: key, client: "oaiapp_first", subject: "subject-one", scope: "openid profile email offline_access resource.invoke chatgpt.tokens.use.direct"}
	f.server = httptest.NewServer(http.HandlerFunc(f.serve))
	t.Cleanup(f.server.Close)
	root := t.TempDir()
	f.config = Config{StateDir: filepath.Join(root, "siwc"), Credentials: credentialstore.NewFileStore(filepath.Join(root, "credentials.json")), Issuer: f.server.URL, HTTPClient: f.server.Client()}
	f.manager, err = New(f.config)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = f.manager.Close() })
	return f
}

func (f *fixture) serve(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	switch r.URL.Path {
	case "/.well-known/openid-configuration":
		_ = json.NewEncoder(w).Encode(map[string]string{"issuer": f.server.URL, "authorization_endpoint": f.server.URL + "/authorize", "token_endpoint": f.server.URL + "/token", "jwks_uri": f.server.URL + "/jwks", "revocation_endpoint": f.server.URL + "/revoke"})
	case "/jwks":
		if f.jwksUnavailable {
			w.WriteHeader(503)
			return
		}
		_ = json.NewEncoder(w).Encode(map[string]any{"keys": []any{map[string]string{"kty": "RSA", "kid": "fixture", "alg": "RS256", "use": "sig", "n": base64.RawURLEncoding.EncodeToString(f.key.N.Bytes()), "e": base64.RawURLEncoding.EncodeToString(big.NewInt(int64(f.key.E)).Bytes())}}})
	case "/token":
		_ = r.ParseForm()
		if f.beforeToken != nil {
			f.beforeToken(r.Form)
		}
		if r.Form.Get("client_id") != f.client || r.Form.Get("resource") != "https://api.openai.com/v1" {
			f.t.Errorf("token identity/resource mismatch: %v", r.Form)
		}
		if r.Form.Get("grant_type") == "authorization_code" {
			f.exchanges.Add(1)
			sum := sha256.Sum256([]byte(r.Form.Get("code_verifier")))
			if r.Form.Get("redirect_uri") != f.redirect || base64.RawURLEncoding.EncodeToString(sum[:]) != f.challenge {
				f.t.Error("redirect or PKCE mismatch")
			}
			if _, err := f.manager.Account(r.Context(), f.client); err != nil {
				f.t.Errorf("issued client not persisted before exchange: %v", err)
			}
		} else {
			n := f.refreshes.Add(1)
			if r.Form.Get("refresh_token") != fmt.Sprintf("refresh-%d", n-1) || r.Form.Has("scope") {
				f.t.Errorf("refresh rotation or scope mismatch: %v", r.Form)
			}
		}
		if f.tokenError != "" {
			status := f.tokenStatus
			if status == 0 {
				status = 400
			}
			w.WriteHeader(status)
			_ = json.NewEncoder(w).Encode(map[string]string{"error": f.tokenError, "error_description": "private token must not appear in public errors"})
			return
		}
		claims := map[string]any{"iss": f.server.URL, "sub": f.subject, "aud": f.client, "exp": time.Now().Add(time.Hour).Unix(), "iat": time.Now().Unix(), "nonce": f.nonce, "email": "same@example.test"}
		if f.mutate != nil {
			f.mutate(claims)
		}
		b, _ := json.Marshal(claims)
		header := base64.RawURLEncoding.EncodeToString([]byte(`{"alg":"RS256","kid":"fixture"}`))
		input := header + "." + base64.RawURLEncoding.EncodeToString(b)
		sum := sha256.Sum256([]byte(input))
		signature, _ := rsa.SignPKCS1v15(rand.Reader, f.key, crypto.SHA256, sum[:])
		if f.badSignature {
			signature[0] ^= 1
		}
		_ = json.NewEncoder(w).Encode(map[string]any{"access_token": fmt.Sprintf("access-%d", f.refreshes.Load()), "refresh_token": fmt.Sprintf("refresh-%d", f.refreshes.Load()), "id_token": input + "." + base64.RawURLEncoding.EncodeToString(signature), "token_type": "Bearer", "expires_in": 3600, "scope": f.scope})
	case "/revoke":
		if f.beforeRevoke != nil {
			f.beforeRevoke()
		}
		f.revokes.Add(1)
		_ = r.ParseForm()
		if r.Form.Get("token") == "" || r.Form.Get("token_type_hint") != "refresh_token" || r.Form.Get("client_id") != f.client {
			f.t.Errorf("bad revocation form: %v", r.Form)
		}
		if f.revokeStatus != 0 {
			w.WriteHeader(f.revokeStatus)
		}
	default:
		http.NotFound(w, r)
	}
}

func (f *fixture) start(opts StartOptions) (Attempt, url.Values) {
	f.t.Helper()
	a, err := f.manager.Start(context.Background(), opts)
	if err != nil {
		f.t.Fatal(err)
	}
	u, err := url.Parse(a.AuthorizationURL)
	if err != nil {
		f.t.Fatal(err)
	}
	q := u.Query()
	f.nonce = q.Get("nonce")
	f.challenge = q.Get("code_challenge")
	f.redirect = q.Get("redirect_uri")
	if !strings.HasPrefix(f.redirect, "http://127.0.0.1:") || !strings.HasSuffix(f.redirect, "/auth/callback") {
		f.t.Fatal("non-loopback or wrong callback URI")
	}
	return a, q
}
func (f *fixture) callback(a Attempt, q url.Values) Attempt {
	f.t.Helper()
	u, _ := url.Parse(f.redirect)
	u.RawQuery = q.Encode()
	resp, err := http.Get(u.String())
	if err != nil {
		f.t.Fatal(err)
	}
	_, _ = io.Copy(io.Discard, resp.Body)
	_ = resp.Body.Close()
	a, err = f.manager.Poll(a.ID)
	if err != nil {
		f.t.Fatal(err)
	}
	return a
}
func (f *fixture) login() Account {
	f.t.Helper()
	a, q := f.start(StartOptions{})
	a = f.callback(a, url.Values{"state": {q.Get("state")}, "code": {"test-code"}, "client_id": {f.client}})
	if a.Status != "succeeded" {
		f.t.Fatalf("login failed: %+v", a)
	}
	account, err := f.manager.Account(context.Background(), f.client)
	if err != nil {
		f.t.Fatal(err)
	}
	return account
}

func TestProtocolRegistrationReauthorizationRefreshAndSignOut(t *testing.T) {
	f := newFixture(t)
	account := f.login()
	if !account.Connected || !account.PlanEnabled || account.ID != f.client || account.Subject != f.subject {
		t.Fatalf("bad summary: %+v", account)
	}
	raw, _ := json.Marshal(account)
	if strings.Contains(string(raw), "token") {
		t.Fatalf("credentials in summary: %s", raw)
	}
	a, q := f.start(StartOptions{AccountID: f.client})
	host := q.Get("ext_agent_host_id")
	if q.Get("client_id") != f.client || q.Get("id_token_hint") == "" || q.Has("agent_name_hint") || q.Get("login_hint") != "same@example.test" {
		t.Fatal("returning registration was not reused")
	}
	a = f.callback(a, url.Values{"state": {q.Get("state")}, "code": {"again"}})
	if a.Status != "succeeded" {
		t.Fatalf("reauthorization: %+v", a)
	}
	other, err := New(f.config)
	if err != nil {
		t.Fatal(err)
	}
	defer other.Close()
	f.manager.now = func() time.Time { return time.Now().Add(59*time.Minute + 45*time.Second) }
	other.now = f.manager.now
	var wg sync.WaitGroup
	for _, m := range []*Manager{f.manager, other} {
		wg.Add(1)
		go func(m *Manager) {
			defer wg.Done()
			token, err := m.AccessToken(context.Background(), f.client)
			if err != nil || token != "access-1" {
				t.Errorf("fresh token=%q, err=%v", token, err)
			}
		}(m)
	}
	wg.Wait()
	if f.refreshes.Load() != 1 {
		t.Fatalf("concurrent refreshes=%d", f.refreshes.Load())
	}
	result, err := f.manager.SignOut(context.Background(), f.client)
	if err != nil || !result.RevocationConfirmed {
		t.Fatalf("sign out: %+v %v", result, err)
	}
	if _, err := other.AccessToken(context.Background(), f.client); !errors.Is(err, ErrSignInRequired) {
		t.Fatalf("signed out token available: %v", err)
	}
	a, q = f.start(StartOptions{AccountID: f.client})
	if q.Get("ext_agent_host_id") != host || q.Get("client_id") != f.client || q.Has("id_token_hint") {
		t.Fatal("sign out discarded registration or retained token hint")
	}
	_ = f.manager.Cancel(a.ID)
	for _, name := range []string{"state.json"} {
		info, err := os.Stat(filepath.Join(f.config.StateDir, name))
		if err != nil || info.Mode().Perm() != 0600 {
			t.Fatalf("protected metadata: %v %v", info, err)
		}
	}
	t.Log("verified loopback PKCE -> signed identity -> protected registration -> serialized rotating refresh -> revoked sign-out -> reused client/host")
}

func TestProtocolRejectsInvalidIdentityAndCallbacks(t *testing.T) {
	tests := []struct {
		name   string
		mutate func(*fixture, url.Values)
	}{
		{"wrong-state", func(f *fixture, q url.Values) { q.Set("state", "wrong") }},
		{"missing-issued-client", func(f *fixture, q url.Values) { q.Del("client_id") }},
		{"dynamic-is-not-issued", func(f *fixture, q url.Values) { q.Set("client_id", "dynamic_agent_client") }},
		{"denied", func(f *fixture, q url.Values) { q.Set("error", "access_denied") }},
		{"nonce", func(f *fixture, q url.Values) { f.mutate = func(c map[string]any) { c["nonce"] = "wrong" } }},
		{"issuer", func(f *fixture, q url.Values) {
			f.mutate = func(c map[string]any) { c["iss"] = "https://evil.example" }
		}},
		{"audience", func(f *fixture, q url.Values) { f.mutate = func(c map[string]any) { c["aud"] = "other-client" } }},
		{"expired", func(f *fixture, q url.Values) {
			f.mutate = func(c map[string]any) { c["exp"] = time.Now().Add(-time.Hour).Unix() }
		}},
		{"missing-subject", func(f *fixture, q url.Values) { f.mutate = func(c map[string]any) { delete(c, "sub") } }},
		{"signature", func(f *fixture, q url.Values) { f.badSignature = true }},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			f := newFixture(t)
			a, q := f.start(StartOptions{})
			callback := url.Values{"state": {q.Get("state")}, "code": {"code"}, "client_id": {f.client}}
			test.mutate(f, callback)
			a = f.callback(a, callback)
			if a.Status == "succeeded" {
				t.Fatal("invalid callback signed in")
			}
			accounts, err := f.manager.ListAccounts(context.Background())
			if err != nil {
				t.Fatal(err)
			}
			for _, a := range accounts {
				if a.Connected {
					t.Fatal("invalid callback persisted usable credentials")
				}
			}
		})
	}
}

func TestProtocolRetainsRegistrationOnExchangeFailureAndRejectsIdentitySwitch(t *testing.T) {
	f := newFixture(t)
	f.tokenError = "invalid_grant"
	a, q := f.start(StartOptions{})
	a = f.callback(a, url.Values{"state": {q.Get("state")}, "code": {"expired"}, "client_id": {f.client}})
	if a.Status != "failed" || a.AccountID != f.client {
		t.Fatalf("failed exchange lost issued registration: %+v", a)
	}
	f.tokenError = ""
	a, q = f.start(StartOptions{AccountID: f.client})
	if q.Get("client_id") != f.client {
		t.Fatal("retry re-registered")
	}
	a = f.callback(a, url.Values{"state": {q.Get("state")}, "code": {"new"}})
	if a.Status != "succeeded" {
		t.Fatal(a)
	}
	f.subject = "other-subject"
	a, q = f.start(StartOptions{AccountID: f.client})
	a = f.callback(a, url.Values{"state": {q.Get("state")}, "code": {"switch"}})
	if a.Status != "failed" {
		t.Fatal("accepted changed subject")
	}
	saved, _ := f.manager.Account(context.Background(), f.client)
	if saved.Subject != "subject-one" || !saved.Connected {
		t.Fatal("identity mismatch overwrote existing account")
	}
}

func TestProtocolMissingPermissionAndRefreshRecovery(t *testing.T) {
	t.Run("identity-only", func(t *testing.T) {
		f := newFixture(t)
		f.scope = "openid profile email"
		account := f.login()
		if !account.Connected || account.PlanEnabled {
			t.Fatal(account)
		}
		if _, err := f.manager.AccessToken(context.Background(), f.client); !errors.Is(err, ErrPlanDisabled) {
			t.Fatal(err)
		}
	})
	for _, code := range []string{"invalid_grant", "invalid_refresh_token", "token_expired", "refresh_token_expired", "refresh_token_invalidated", "refresh_token_reused", "invalid_client", "temporarily_unavailable"} {
		t.Run(code, func(t *testing.T) {
			f := newFixture(t)
			f.login()
			f.manager.now = func() time.Time { return time.Now().Add(time.Hour) }
			f.tokenError = code
			_, err := f.manager.AccessToken(context.Background(), f.client)
			if err == nil {
				t.Fatal("refresh unexpectedly succeeded")
			}
			account, _ := f.manager.Account(context.Background(), f.client)
			terminal := code != "invalid_client" && code != "temporarily_unavailable"
			if account.Connected == terminal {
				t.Fatalf("wrong token retention for %s: %+v", code, account)
			}
			if strings.Contains(err.Error(), "private token") {
				t.Fatal("untrusted server body leaked")
			}
		})
	}
}

func TestProtocolCancellationAndFailedRevocation(t *testing.T) {
	f := newFixture(t)
	a, _ := f.start(StartOptions{})
	if err := f.manager.Cancel(a.ID); err != nil {
		t.Fatal(err)
	}
	p, _ := f.manager.Poll(a.ID)
	if p.Status != "cancelled" {
		t.Fatal(p)
	}
	f.login()
	f.revokeStatus = 503
	result, err := f.manager.SignOut(context.Background(), f.client)
	if err != nil || result.RevocationConfirmed {
		t.Fatalf("unconfirmed revocation: %+v %v", result, err)
	}
	if _, err := f.manager.AccessToken(context.Background(), f.client); !errors.Is(err, ErrSignInRequired) {
		t.Fatal(err)
	}
}

func TestProtocolRefreshRotationSurvivesVerificationOutage(t *testing.T) {
	f := newFixture(t)
	f.login()
	f.manager.now = func() time.Time { return time.Now().Add(59*time.Minute + 45*time.Second) }
	f.manager.keysFetched = time.Time{}
	f.jwksUnavailable = true
	if _, err := f.manager.AccessToken(context.Background(), f.client); err == nil {
		t.Fatal("unverified replacement token was usable")
	}
	f.jwksUnavailable = false
	token, err := f.manager.AccessToken(context.Background(), f.client)
	if err != nil || token != "access-1" || f.refreshes.Load() != 1 {
		t.Fatalf("rotation checkpoint was lost: token=%q, refreshes=%d, err=%v", token, f.refreshes.Load(), err)
	}
}

func TestProtocolChangedRefreshIdentityStaysQuarantined(t *testing.T) {
	f := newFixture(t)
	f.login()
	f.manager.now = func() time.Time { return time.Now().Add(59*time.Minute + 45*time.Second) }
	f.subject = "malicious-other-account"
	for i := 0; i < 2; i++ {
		if _, err := f.manager.AccessToken(context.Background(), f.client); err == nil {
			t.Fatal("changed refresh identity became usable")
		}
	}
	if f.refreshes.Load() != 1 {
		t.Fatal("rotated token was lost after rejected identity")
	}
	account, err := f.manager.Account(context.Background(), f.client)
	if err != nil || account.Subject != "subject-one" {
		t.Fatalf("account identity replaced: %+v %v", account, err)
	}
}

func TestProtocolSignOutCancelsBoundRequests(t *testing.T) {
	f := newFixture(t)
	f.login()
	other, err := New(f.config)
	if err != nil {
		t.Fatal(err)
	}
	defer other.Close()
	ctx, release, err := other.BindRequest(context.Background(), f.client)
	if err != nil {
		t.Fatal(err)
	}
	defer release()
	if _, err := f.manager.SignOut(context.Background(), f.client); err != nil {
		t.Fatal(err)
	}
	select {
	case <-ctx.Done():
	default:
		t.Fatal("sign-out left authenticated request running")
	}
	if _, release, err := other.BindRequest(context.Background(), f.client); err == nil {
		release()
		t.Fatal("signed-out account accepted a new request")
	}
}

func TestProtocolCallbackIsSingleUseAndCancellationFencesPersistence(t *testing.T) {
	f := newFixture(t)
	a, q := f.start(StartOptions{})
	entered, release := make(chan struct{}), make(chan struct{})
	f.beforeToken = func(url.Values) { close(entered); <-release }
	callback, _ := url.Parse(f.redirect)
	callback.RawQuery = url.Values{"state": {q.Get("state")}, "code": {"once"}, "client_id": {f.client}}.Encode()
	done := make(chan error, 1)
	go func() {
		resp, err := http.Get(callback.String())
		if resp != nil {
			_, _ = io.Copy(io.Discard, resp.Body)
			_ = resp.Body.Close()
		}
		done <- err
	}()
	<-entered
	duplicate, err := http.Get(callback.String())
	if err != nil {
		close(release)
		t.Fatal(err)
	}
	_ = duplicate.Body.Close()
	if duplicate.StatusCode != http.StatusConflict {
		close(release)
		t.Fatal("duplicate callback was accepted")
	}
	if err := f.manager.Cancel(a.ID); err != nil {
		close(release)
		t.Fatal(err)
	}
	close(release)
	if err := <-done; err != nil {
		t.Fatal(err)
	}
	account, err := f.manager.Account(context.Background(), f.client)
	if err != nil || account.Connected {
		t.Fatalf("cancelled exchange persisted tokens: %+v %v", account, err)
	}
	if f.exchanges.Load() != 1 {
		t.Fatalf("authorization code exchanged %d times", f.exchanges.Load())
	}
}

func TestProtocolMultipleRegistrationsNeverMergeByEmail(t *testing.T) {
	f := newFixture(t)
	first := f.login()
	f.client = "oaiapp_second"
	second := f.login()
	if first.ID == second.ID || first.Email != second.Email {
		t.Fatal("fixture did not create separate same-email registrations")
	}
	accounts, err := f.manager.ListAccounts(context.Background())
	if err != nil || len(accounts) != 2 {
		t.Fatalf("registrations merged: %+v %v", accounts, err)
	}
	a, q := f.start(StartOptions{AccountID: first.ID})
	a = f.callback(a, url.Values{"state": {q.Get("state")}, "code": {"wrong-client"}, "client_id": {second.ID}})
	if a.Status != "failed" || f.exchanges.Load() != 2 {
		t.Fatalf("returning client mismatch exchanged code: %+v", a)
	}
	for _, id := range []string{first.ID, second.ID} {
		account, err := f.manager.Account(context.Background(), id)
		if err != nil || !account.Connected {
			t.Fatal("client mismatch damaged saved registration")
		}
	}
}

func TestProtocolRefreshExpiryAndTerminalFailureCancelRequests(t *testing.T) {
	for _, expired := range []bool{false, true} {
		t.Run(fmt.Sprint(expired), func(t *testing.T) {
			f := newFixture(t)
			f.login()
			ctx, release, err := f.manager.BindRequest(context.Background(), f.client)
			if err != nil {
				t.Fatal(err)
			}
			defer release()
			if expired {
				f.manager.now = func() time.Time { return time.Now().Add(31 * 24 * time.Hour) }
			} else {
				f.manager.now = func() time.Time { return time.Now().Add(time.Hour) }
				f.tokenError = "refresh_token_invalidated"
			}
			if _, err := f.manager.AccessToken(ctx, f.client); !errors.Is(err, ErrSignInRequired) {
				t.Fatalf("expected reauthorization: %v", err)
			}
			select {
			case <-ctx.Done():
			default:
				t.Fatal("invalidated credentials left requests running")
			}
			if expired && f.refreshes.Load() != 0 {
				t.Fatal("expired refresh token was transmitted")
			}
		})
	}
}

func TestProtocolCrossProcessSignOutCancelsRequest(t *testing.T) {
	f := newFixture(t)
	f.login()
	ctx, release, err := f.manager.BindRequest(context.Background(), f.client)
	if err != nil {
		t.Fatal(err)
	}
	defer release()
	command := exec.Command(os.Args[0], "-test.run=^TestProtocolSignOutProcessHelper$")
	command.Env = append(os.Environ(), "WUU_SIWC_HELPER=1", "WUU_SIWC_STATE="+f.config.StateDir, "WUU_SIWC_ISSUER="+f.server.URL, "WUU_SIWC_ACCOUNT="+f.client)
	if output, err := command.CombinedOutput(); err != nil {
		t.Fatalf("subprocess sign-out: %v %s", err, output)
	}
	select {
	case <-ctx.Done():
	case <-time.After(3 * time.Second):
		t.Fatal("another process signed out but request stayed active")
	}
}
func TestProtocolSignOutProcessHelper(t *testing.T) {
	if os.Getenv("WUU_SIWC_HELPER") != "1" {
		return
	}
	dir := os.Getenv("WUU_SIWC_STATE")
	manager, err := New(Config{StateDir: dir, Credentials: credentialstore.NewFileStore(filepath.Join(filepath.Dir(dir), "credentials.json")), Issuer: os.Getenv("WUU_SIWC_ISSUER"), HTTPClient: &http.Client{Timeout: 5 * time.Second}})
	if err != nil {
		t.Fatal(err)
	}
	defer manager.Close()
	if result, err := manager.SignOut(context.Background(), os.Getenv("WUU_SIWC_ACCOUNT")); err != nil || !result.RevocationConfirmed {
		t.Fatalf("sign-out: %+v %v", result, err)
	}
}

func TestProtocolOnlyOnePendingAttemptAndBoundedHistory(t *testing.T) {
	f := newFixture(t)
	a, _ := f.start(StartOptions{})
	if _, err := f.manager.Start(context.Background(), StartOptions{}); err == nil {
		t.Fatal("parallel registration allowed")
	}
	_ = f.manager.Cancel(a.ID)
	for i := 0; i < 40; i++ {
		a, _ = f.start(StartOptions{})
		_ = f.manager.Cancel(a.ID)
	}
	if len(f.manager.attempts) > 32 {
		t.Fatalf("unbounded sign-in history: %d", len(f.manager.attempts))
	}
	if p, err := f.manager.Poll(a.ID); err != nil || p.Status != "cancelled" {
		t.Fatal("latest status unavailable")
	}
}

func TestProtocolSignOutFencesStorageBeforeRevocationCompletes(t *testing.T) {
	f := newFixture(t)
	f.login()
	before, err := f.manager.registration(f.client)
	if err != nil {
		t.Fatal(err)
	}
	entered, release := make(chan struct{}), make(chan struct{})
	f.beforeRevoke = func() { close(entered); <-release }
	done := make(chan error, 1)
	go func() { _, err := f.manager.SignOut(context.Background(), f.client); done <- err }()
	<-entered
	after, err := f.manager.registration(f.client)
	close(release)
	if err != nil || after.Revision == before.Revision {
		t.Errorf("session was not fenced before slow remote revocation: %v", err)
	}
	if err := <-done; err != nil {
		t.Fatal(err)
	}
}
