package personalcalendar

import (
	"context"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/base64"
	"encoding/json"
	"errors"
	"net"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"

	"github.com/blueberrycongee/wuu/internal/credentialstore"
)

const callbackPath = "/oauth/google-calendar/callback"

type AuthorizationAttempt struct {
	ID               string    `json:"attempt_id"`
	AuthorizationURL string    `json:"authorization_url,omitempty"`
	State            string    `json:"state"`
	ErrorCode        string    `json:"error_code,omitempty"`
	ExpiresAt        time.Time `json:"expires_at"`
}
type pending struct {
	mu                                                                              sync.Mutex
	actor                                                                           Actor
	view                                                                            AuthorizationAttempt
	state, nonce, verifier, redirect, previousID, previousAccount, previousRevision string
	ctx                                                                             context.Context
	cancel                                                                          context.CancelFunc
	server                                                                          *http.Server
	done                                                                            chan struct{}
	cleanupError                                                                    error
}

func (b *Broker) Start(ctx context.Context, a Actor) (AuthorizationAttempt, error) {
	if e := a.validate(); e != nil {
		return AuthorizationAttempt{}, e
	}
	if b.config.ClientID == "" {
		return AuthorizationAttempt{}, fault("oauth_client_not_configured")
	}
	var previous stored
	raw, e := b.store.Get(ctx, namespace, key(a.UserSpaceID))
	if e == nil {
		previous, e = b.decode(raw, a)
	} else if errors.Is(e, credentialstore.ErrNotFound) {
		e = nil
	}
	if e != nil {
		return AuthorizationAttempt{}, fault("secure_store_unavailable")
	}
	if e = b.alive(ctx); e != nil {
		return AuthorizationAttempt{}, e
	}
	b.mu.Lock()
	defer b.mu.Unlock()
	if b.closed {
		return AuthorizationAttempt{}, fault("connector_closed")
	}
	for id, p := range b.attempts {
		p.mu.Lock()
		active := p.view.State == "pending" || p.view.State == "exchanging" || p.done != nil
		if !b.now().Before(p.view.ExpiresAt) && p.done == nil {
			p.cancel()
			delete(b.attempts, id)
			active = false
		}
		p.mu.Unlock()
		if active && p.actor.UserSpaceID == a.UserSpaceID {
			return AuthorizationAttempt{}, fault("authorization_pending")
		}
	}
	if len(b.attempts) >= 32 {
		return AuthorizationAttempt{}, fault("authorization_capacity")
	}
	listener, e := net.Listen("tcp4", "127.0.0.1:0")
	if e != nil {
		return AuthorizationAttempt{}, fault("loopback_unavailable")
	}
	attemptCtx, cancel := context.WithTimeout(b.lifetime, 5*time.Minute)
	p := &pending{actor: a, state: randomID(), nonce: randomID(), verifier: randomID(), redirect: "http://" + listener.Addr().String() + callbackPath, ctx: attemptCtx, cancel: cancel, previousID: previous.Connection.ID, previousAccount: previous.Connection.AccountID, previousRevision: previous.Revision}
	p.view = AuthorizationAttempt{ID: randomID(), State: "pending", ExpiresAt: b.now().Add(5 * time.Minute)}
	challenge := sha256.Sum256([]byte(p.verifier))
	q := url.Values{"client_id": {b.config.ClientID}, "redirect_uri": {p.redirect}, "response_type": {"code"}, "scope": {strings.Join(Scopes(), " ")}, "state": {p.state}, "nonce": {p.nonce}, "code_challenge": {base64.RawURLEncoding.EncodeToString(challenge[:])}, "code_challenge_method": {"S256"}, "access_type": {"offline"}, "prompt": {"consent select_account"}}
	p.view.AuthorizationURL = "https://accounts.google.com/o/oauth2/v2/auth?" + q.Encode()
	b.attempts[p.view.ID] = p
	p.server = &http.Server{Handler: http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { b.callback(p.view.ID, w, r) }), ReadHeaderTimeout: 5 * time.Second, ReadTimeout: 10 * time.Second, WriteTimeout: 45 * time.Second, MaxHeaderBytes: 16 * 1024}
	go func() { _ = p.server.Serve(listener) }()
	go func() {
		<-attemptCtx.Done()
		_ = p.server.Close()
		p.stop("expired", "authorization_expired")
	}()
	return p.view, nil
}
func (b *Broker) Attempt(a Actor, id string) (AuthorizationAttempt, error) {
	if e := a.validate(); e != nil {
		return AuthorizationAttempt{}, e
	}
	b.mu.Lock()
	p := b.attempts[id]
	b.mu.Unlock()
	if p == nil || p.actor != a {
		return AuthorizationAttempt{}, fault("attempt_scope_mismatch")
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	view := p.view
	view.AuthorizationURL = ""
	return view, nil
}
func (b *Broker) Cancel(a Actor, id string) error {
	if _, e := b.Attempt(a, id); e != nil {
		return e
	}
	b.mu.Lock()
	p := b.attempts[id]
	b.mu.Unlock()
	if p != nil {
		p.stop("cancelled", "authorization_cancelled")
		if e := p.drain(); e != nil {
			return e
		}
		p.mu.Lock()
		completed := p.view.State == "connected"
		p.mu.Unlock()
		if completed {
			return fault("authorization_already_completed")
		}
		return nil
	}
	return nil
}
func (b *Broker) callback(id string, w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("Referrer-Policy", "no-referrer")
	w.Header().Set("Content-Security-Policy", "default-src 'none'; frame-ancestors 'none'")
	b.mu.Lock()
	p := b.attempts[id]
	b.mu.Unlock()
	if p == nil {
		http.Error(w, "Authorization expired", http.StatusGone)
		return
	}
	expected, _ := url.Parse(p.redirect)
	q, e := url.ParseQuery(r.URL.RawQuery)
	if e != nil || r.Method != "GET" || r.Host != expected.Host || r.URL.EscapedPath() != callbackPath || r.URL.Fragment != "" || len(q["state"]) != 1 || subtle.ConstantTimeCompare([]byte(q.Get("state")), []byte(p.state)) != 1 || len(q["code"]) > 1 || len(q["error"]) > 1 || len(q.Get("code")) > 4096 {
		http.Error(w, "Invalid authorization callback", http.StatusBadRequest)
		return
	}
	p.mu.Lock()
	if p.view.State != "pending" || p.ctx.Err() != nil || !b.now().Before(p.view.ExpiresAt) {
		p.mu.Unlock()
		http.Error(w, "Authorization already used or expired", http.StatusConflict)
		return
	}
	p.view.State = "exchanging"
	p.done = make(chan struct{})
	done := p.done
	p.mu.Unlock()
	defer func() { p.mu.Lock(); close(done); p.done = nil; p.mu.Unlock() }()
	if q.Get("error") != "" || q.Get("code") == "" {
		p.finish("failed", "consent_denied")
		http.Error(w, "Authorization was not completed", http.StatusBadRequest)
		return
	}
	ctx, cancel := context.WithTimeout(p.ctx, 40*time.Second)
	defer cancel()
	token, e := b.exchange(ctx, map[string]string{"grant_type": "authorization_code", "code": q.Get("code"), "code_verifier": p.verifier, "redirect_uri": p.redirect})
	if e == nil {
		e = token.validate(true)
	}
	account := ""
	if e == nil {
		account, e = b.verifyIdentity(ctx, token.IDToken, p.nonce)
	}
	if e == nil && p.previousAccount != "" && p.previousAccount != account {
		e = fault("account_mismatch")
	}
	// Probe Calendar list access; no event history is read as part of connecting.
	if e == nil {
		_, e = b.calendars(ctx, token.AccessToken)
	}
	var candidate *stored
	if e == nil {
		connection := Connection{ID: randomID(), UserSpaceID: p.actor.UserSpaceID, ProductDomain: "personal", Provider: "google-calendar", AccountID: account, State: "connected", Scopes: Scopes(), SelectedCalendars: []string{}, SelectionRevision: randomID()}
		next := stored{Version: 1, ClientID: b.config.ClientID, Connection: connection, AccessToken: token.AccessToken, RefreshToken: token.RefreshToken, ExpiresAt: b.now().Add(time.Duration(token.ExpiresIn) * time.Second), Revision: randomID()}
		candidate = &next
		e = b.store.Update(ctx, namespace, key(p.actor.UserSpaceID), func(raw []byte) ([]byte, error) {
			if ctx.Err() != nil {
				return nil, fault("authorization_cancelled")
			}
			if p.previousID == "" {
				if len(raw) > 0 {
					return nil, fault("connection_changed")
				}
			} else {
				old, e := b.decode(raw, p.actor)
				if e != nil || old.Connection.ID != p.previousID || old.Revision != p.previousRevision || old.Connection.State == "disconnected" {
					return nil, fault("connection_changed")
				}
			}
			return json.Marshal(next)
		})
	}
	// A backend commit can finish after cancellation. Linearize success with
	// stop(), then remove only this attempt's exact generation on every failure.
	if e == nil {
		e = b.completeAuthorization(ctx, p, *candidate)
	}
	if e == nil {
		_, _ = w.Write([]byte("Google Calendar connected. Return to Twin to choose calendars."))
		return
	}
	if candidate != nil {
		cleanup, stop := context.WithTimeout(context.Background(), 5*time.Second)
		cleanupError := b.store.Update(cleanup, namespace, key(p.actor.UserSpaceID), func(raw []byte) ([]byte, error) {
			if len(raw) == 0 {
				return nil, nil
			}
			current, err := b.decode(raw, p.actor)
			if err != nil {
				return nil, err
			}
			// A new consent always has a new connection ID. Selection/refresh
			// revisions of this cancelled consent must not escape cleanup.
			if current.Connection.ID != candidate.Connection.ID {
				return raw, nil
			}
			return nil, nil
		})
		stop()
		if cleanupError != nil {
			b.deny(candidate.Connection.ID, fault("authorization_denied"))
			p.mu.Lock()
			p.cleanupError = fault("credential_cleanup_failed")
			p.view.ErrorCode = "credential_cleanup_failed"
			p.mu.Unlock()
		}
	}
	code := "authorization_failed"
	for _, candidate := range []string{"scope_mismatch", "account_mismatch", "authorization_expired", "identity_invalid", "provider_unavailable", "activation_record_unavailable"} {
		if e != nil && e.Error() == fault(candidate).Error() {
			code = candidate
		}
	}
	p.mu.Lock()
	if p.cleanupError != nil {
		code = "credential_cleanup_failed"
	}
	p.mu.Unlock()
	p.finish("failed", code)
	http.Error(w, "Could not connect. Return to Twin for details.", http.StatusBadRequest)
}

// Serialize the small local completion commit with attempt cancellation. The
// Keychain command is deliberately outside this critical section. A concurrent
// Cancel that loses to completion returns authorization_already_completed.
func (b *Broker) completeAuthorization(ctx context.Context, p *pending, candidate stored) error {
	b.mu.Lock()
	p.mu.Lock()
	defer b.mu.Unlock()
	defer p.mu.Unlock()
	if b.closed || ctx.Err() != nil || p.view.State != "exchanging" || !b.now().Before(p.view.ExpiresAt) {
		return fault("authorization_cancelled")
	}
	if e := b.writeActivation(ctx, candidate); e != nil {
		return e
	}
	p.view.State = "connected"
	p.view.ErrorCode = ""
	p.view.AuthorizationURL = ""
	p.closeListener()
	return nil
}
func (p *pending) stop(state, code string) {
	p.cancel()
	p.mu.Lock()
	if p.view.State == "pending" || p.view.State == "exchanging" {
		p.view.State = state
		p.view.ErrorCode = code
		p.view.AuthorizationURL = ""
	}
	p.mu.Unlock()
}
func (p *pending) drain() error {
	p.mu.Lock()
	done := p.done
	p.mu.Unlock()
	if done != nil {
		<-done
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.cleanupError
}
func (p *pending) finish(state, code string) bool {
	p.mu.Lock()
	if (p.view.State != "pending" && p.view.State != "exchanging") || p.ctx.Err() != nil {
		p.mu.Unlock()
		return false
	}
	p.view.State = state
	p.view.ErrorCode = code
	p.view.AuthorizationURL = ""
	p.mu.Unlock()
	p.closeListener()
	return true
}
func (p *pending) closeListener() {
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), time.Second)
		defer cancel()
		_ = p.server.Shutdown(ctx)
		p.cancel()
	}()
}

type tokenResponse struct {
	AccessToken  string `json:"access_token"`
	RefreshToken string `json:"refresh_token"`
	IDToken      string `json:"id_token"`
	ExpiresIn    int    `json:"expires_in"`
	Type         string `json:"token_type"`
	Scope        string `json:"scope"`
}

func (t tokenResponse) validate(initial bool) error {
	if !hasScopes(t.Scope) {
		return fault("scope_mismatch")
	}
	if t.AccessToken == "" || len(t.AccessToken) > 16384 || strings.ContainsAny(t.AccessToken, "\r\n") || t.Type != "Bearer" || t.ExpiresIn < 120 || t.ExpiresIn > 86400 || initial && (t.RefreshToken == "" || t.IDToken == "") {
		return fault("authorization_invalid")
	}
	return nil
}
func (b *Broker) exchange(ctx context.Context, params map[string]string) (tokenResponse, error) {
	params["client_id"] = b.config.ClientID
	if b.config.ClientSecret != "" {
		params["client_secret"] = b.config.ClientSecret
	}
	status, body, e := b.request(ctx, "POST", "https://oauth2.googleapis.com/token", "", params, false)
	var token tokenResponse
	if e != nil {
		return token, e
	}
	if status != 200 {
		var problem struct {
			Error string `json:"error"`
		}
		_ = json.Unmarshal(body, &problem)
		if problem.Error == "invalid_grant" {
			return token, fault("authorization_expired")
		}
		return token, fault("provider_unavailable")
	}
	if json.Unmarshal(body, &token) != nil {
		return token, fault("invalid_response")
	}
	return token, nil
}
