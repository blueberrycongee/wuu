package personalconnectors

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/base64"
	"encoding/json"
	"errors"
	"github.com/blueberrycongee/wuu/internal/credentialstore"
	"io"
	"net"
	"net/http"
	"net/url"
	"path/filepath"
	"slices"
	"strings"
	"time"
)

const clientNamespace = "personal.connectors.clients.v1"
const oauthCallbackPath = "/oauth/personal-connectors/callback"

func nonce() string {
	v := make([]byte, 32)
	if _, err := rand.Read(v); err != nil {
		panic("secure random unavailable")
	}
	return base64.RawURLEncoding.EncodeToString(v)
}
func isGoogle(provider string) bool {
	return slices.Contains([]string{"gmail", "drive", "google-calendar"}, provider)
}
func definition(provider string) (oauthDefinition, error) {
	if isGoogle(provider) {
		return oauthDefinition{AuthorizationURL: "https://accounts.google.com/o/oauth2/v2/auth", TokenURL: "https://oauth2.googleapis.com/token", PKCE: true, AuthParams: map[string]string{"access_type": "offline", "prompt": "consent select_account", "include_granted_scopes": "true"}}, nil
	}
	return otherOAuthProvider(provider)
}

// ImportClient stores only user-supplied registered app configuration. It does
// not create a developer app, grant scopes, or initiate OAuth.
func ImportClient(ctx context.Context, store credentialstore.Store, input io.Reader) (string, error) {
	raw, e := io.ReadAll(io.LimitReader(input, 16385))
	if e != nil || len(raw) > 16384 {
		return "", fault("invalid_client_configuration")
	}
	var c OAuthClient
	if decode(raw, &c) != nil || c.ClientID == "" || len(c.ClientID) > 1024 || len(c.ClientSecret) > 8192 || strings.ContainsAny(c.ClientID, "\r\n") {
		return "", fault("invalid_client_configuration")
	}
	if _, e = definition(c.Provider); e != nil {
		return "", e
	}
	if c.RedirectURI != "" {
		if e = validRedirect(c.RedirectURI); e != nil {
			return "", e
		}
	} else if !isGoogle(c.Provider) {
		return "", fault("registered_loopback_redirect_required")
	}
	if store == nil {
		return "", fault("secure_store_unavailable")
	}
	if e = store.Set(ctx, clientNamespace, c.Provider, raw); e != nil {
		return "", fault("secure_store_unavailable")
	}
	return c.Provider, nil
}
func redirectAddress(raw string) (string, error) {
	u, e := url.Parse(raw)
	if e != nil || u.Scheme != "http" || u.Hostname() != "127.0.0.1" || u.Port() == "" || u.Path != oauthCallbackPath || u.User != nil || u.RawQuery != "" || u.Fragment != "" {
		return "", fault("invalid_loopback_redirect")
	}
	return u.Host, nil
}
func (b *Broker) clientConfig(ctx context.Context, provider string) (OAuthClient, error) {
	var c OAuthClient
	if b.store == nil {
		return c, fault("secure_store_unavailable")
	}
	raw, e := b.store.Get(ctx, clientNamespace, provider)
	if errors.Is(e, credentialstore.ErrNotFound) {
		return c, fault("oauth_client_not_configured")
	}
	if e != nil || decode(raw, &c) != nil || c.Provider != provider || c.ClientID == "" || (c.RedirectURI != "" && validRedirect(c.RedirectURI) != nil) {
		return c, fault("invalid_client_configuration")
	}
	return c, nil
}
func (b *Broker) Start(ctx context.Context, a Actor, r ConnectRequest) (AuthorizationAttempt, error) {
	if !filepath.IsAbs(b.stateDir) {
		return AuthorizationAttempt{}, fault("activation_record_unavailable")
	}
	if e := validateActor(a); e != nil {
		return AuthorizationAttempt{}, e
	}
	if r.WorkReadPolicy != "" && r.WorkReadPolicy != "confirm_each_request" && r.WorkReadPolicy != "selected_resources" {
		return AuthorizationAttempt{}, fault("invalid_work_read_policy")
	}
	if r.ConnectionID == "" || len(r.ConnectionID) > 128 || len(r.Resources) == 0 || len(r.Scopes) == 0 || (r.Domain != "personal" && r.Domain != "work") {
		return AuthorizationAttempt{}, fault("invalid_connection")
	}
	if (r.Provider == "slack" || r.Provider == "feishu") && r.Domain != "work" {
		return AuthorizationAttempt{}, fault("work_domain_required")
	}

	def, e := definition(r.Provider)
	if e != nil {
		return AuthorizationAttempt{}, e
	}
	c, e := b.clientConfig(ctx, r.Provider)
	if e != nil {
		return AuthorizationAttempt{}, e
	}
	previous := ""
	epoch := ""
	e = b.update(ctx, a, func(s *stored) error {
		epoch = s.Epochs[r.ConnectionID]
		if old, ok := s.Grants[r.ConnectionID]; ok {
			if old.Connection.Provider != r.Provider || r.AccountID != "" && old.Connection.AccountID != r.AccountID {
				return fault("connection_changed")
			}
			r.AccountID = old.Connection.AccountID
			previous = old.Connection.Revision
		}
		return nil
	})
	if e != nil {
		return AuthorizationAttempt{}, e
	}
	b.oauthMu.Lock()
	defer b.oauthMu.Unlock()
	for id, p := range b.attempts {
		p.mu.Lock()
		active := p.view.State == "pending" || p.view.State == "exchanging"
		if time.Now().After(p.view.ExpiresAt) && !active {
			delete(b.attempts, id)
		}
		p.mu.Unlock()
		if active && p.actor.UserSpaceID == a.UserSpaceID && p.request.ConnectionID == r.ConnectionID {
			return AuthorizationAttempt{}, fault("authorization_pending")
		}
	}
	if len(b.attempts) >= 32 {
		return AuthorizationAttempt{}, fault("authorization_capacity")
	}
	var listener net.Listener
	redirect := c.RedirectURI
	if strings.HasPrefix(redirect, "https://") {
		if b.httpsCallbacks == nil {
			return AuthorizationAttempt{}, fault("https_callback_transport_required")
		}
	} else {
		if r.Provider == "slack" {
			return AuthorizationAttempt{}, fault("https_callback_transport_required")
		}
		address := "127.0.0.1:0"
		if c.RedirectURI != "" {
			address, e = redirectAddress(c.RedirectURI)
			if e != nil {
				return AuthorizationAttempt{}, e
			}
		}
		listener, e = net.Listen("tcp4", address)
		if e != nil {
			return AuthorizationAttempt{}, fault("loopback_unavailable")
		}
		redirect = "http://" + listener.Addr().String() + oauthCallbackPath
	}
	attemptCtx, cancel := context.WithTimeout(b.lifetime, 5*time.Minute)
	p := &oauthPending{actor: a, request: r, client: c, state: nonce(), verifier: nonce(), redirect: redirect, previousRevision: previous, previousEpoch: epoch, ctx: attemptCtx, cancel: cancel}
	p.view = AuthorizationAttempt{ID: nonce(), State: "pending", ExpiresAt: time.Now().Add(5 * time.Minute)}
	scopes := append([]string{}, r.Scopes...)
	if isGoogle(r.Provider) {
		scopes = append(scopes, "openid", "email")
	}
	q := url.Values{"client_id": {c.ClientID}, "redirect_uri": {redirect}, "response_type": {"code"}, "state": {p.state}, "scope": {strings.Join(scopes, " ")}}
	if r.Provider == "notion" || r.Provider == "github" {
		q.Del("scope")
	}
	if r.Provider == "slack" {
		q.Del("scope")
		q.Set("user_scope", strings.Join(scopes, ","))
	}
	for k, v := range def.AuthParams {
		q.Set(k, v)
	}
	if def.PKCE {
		sum := sha256.Sum256([]byte(p.verifier))
		q.Set("code_challenge", base64.RawURLEncoding.EncodeToString(sum[:]))
		q.Set("code_challenge_method", "S256")
	}
	p.view.AuthorizationURL = def.AuthorizationURL + "?" + q.Encode()
	b.attempts[p.view.ID] = p
	if listener != nil {
		p.server = &http.Server{Handler: http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) { b.oauthCallback(p, w, req) }), ReadHeaderTimeout: 5 * time.Second, ReadTimeout: 10 * time.Second, WriteTimeout: 60 * time.Second, MaxHeaderBytes: 16 * 1024}
		go func() { _ = p.server.Serve(listener) }()
	} else {
		p.stopTransport, e = b.httpsCallbacks.Register(attemptCtx, redirect, func(raw string) error {
			incoming, err := url.Parse(raw)
			expected, _ := url.Parse(redirect)
			if err != nil || incoming.Scheme != expected.Scheme || incoming.Host != expected.Host || incoming.EscapedPath() != expected.EscapedPath() || incoming.User != nil || incoming.Fragment != "" {
				return fault("callback_origin_mismatch")
			}
			request, err := http.NewRequest("GET", raw, nil)
			if err != nil {
				return fault("callback_invalid")
			}
			capture := &callbackResponse{header: http.Header{}, status: 200}
			b.oauthCallback(p, capture, request)
			if capture.status != 200 {
				return fault("callback_rejected")
			}
			return nil
		})
		if e != nil {
			cancel()
			delete(b.attempts, p.view.ID)
			return AuthorizationAttempt{}, fault("https_callback_transport_unavailable")
		}
	}
	go func() {
		<-attemptCtx.Done()
		if p.server != nil {
			_ = p.server.Close()
		}
		if p.stopTransport != nil {
			p.stopTransport()
		}
		p.mu.Lock()
		if p.view.State == "pending" || p.view.State == "exchanging" {
			p.view.State = "expired"
			p.view.ErrorCode = "authorization_expired"
		}
		p.mu.Unlock()
	}()
	p.mu.Lock()
	view := p.view
	p.mu.Unlock()
	return view, nil
}
func (b *Broker) Attempt(a Actor, id string) (AuthorizationAttempt, error) {
	if e := validateActor(a); e != nil {
		return AuthorizationAttempt{}, e
	}
	b.oauthMu.Lock()
	p := b.attempts[id]
	b.oauthMu.Unlock()
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
	b.oauthMu.Lock()
	p := b.attempts[id]
	b.oauthMu.Unlock()
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.view.State == "connected" {
		return fault("authorization_already_completed")
	}
	p.view.State = "cancelled"
	p.view.ErrorCode = "authorization_cancelled"
	p.cancel()
	return nil
}
func (b *Broker) oauthCallback(p *oauthPending, w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("Referrer-Policy", "no-referrer")
	w.Header().Set("Content-Security-Policy", "default-src 'none'; frame-ancestors 'none'")
	expected, _ := url.Parse(p.redirect)
	q, e := url.ParseQuery(r.URL.RawQuery)
	if e != nil || r.Method != "GET" || r.Host != expected.Host || r.URL.EscapedPath() != expected.EscapedPath() || len(q["state"]) != 1 || subtle.ConstantTimeCompare([]byte(q.Get("state")), []byte(p.state)) != 1 || len(q["code"]) != 1 || len(q.Get("code")) > 4096 || q.Get("code") == "" || q.Get("error") != "" {
		http.Error(w, "Invalid authorization callback", 400)
		return
	}
	p.mu.Lock()
	if p.view.State != "pending" || p.ctx.Err() != nil {
		p.mu.Unlock()
		http.Error(w, "Authorization used or expired", 409)
		return
	}
	p.view.State = "exchanging"
	p.mu.Unlock()
	values := url.Values{"grant_type": {"authorization_code"}, "code": {q.Get("code")}, "redirect_uri": {p.redirect}, "code_verifier": {p.verifier}}
	token, e := b.exchangeOAuth(p.ctx, p.client, values)
	if e == nil {
		token, e = b.verifyOAuth(p.ctx, p.client.Provider, token)
	}
	if e == nil && p.request.AccountID != "" && token.AccountID != p.request.AccountID {
		e = fault("account_identity_mismatch")
	}
	if e == nil && p.request.Provider != "notion" && p.request.Provider != "github" {
		for _, scope := range p.request.Scopes {
			if !slices.Contains(token.Scopes, scope) {
				e = fault("scope_mismatch")
				break
			}
		}
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.view.State != "exchanging" || p.ctx.Err() != nil {
		http.Error(w, "Authorization cancelled", 409)
		return
	}
	if e != nil {
		p.view.State = "failed"
		p.view.ErrorCode = code(e)
		http.Error(w, "Authorization failed", 400)
		return
	}
	grant := Grant{ActivationID: nonce(), NonExpiring: token.ExpiresIn == 0 && slices.Contains([]string{"notion", "github", "slack"}, p.request.Provider), Connection: Connection{ID: p.request.ConnectionID, Provider: p.request.Provider, AccountID: token.AccountID, AccountLabel: token.AccountLabel, WorkspaceID: token.WorkspaceID, WorkReadPolicy: p.request.WorkReadPolicy, ScopeVerification: "provider_enforced_per_request", Domain: p.request.Domain, Scopes: p.request.Scopes, Resources: p.request.Resources, Revision: nonce(), State: "connected"}, AccessToken: token.AccessToken, RefreshToken: token.RefreshToken, OAuthProvider: p.client.Provider, ExpiresAt: time.Now().Add(time.Duration(token.ExpiresIn) * time.Second)}
	if isGoogle(p.request.Provider) || p.request.Provider == "slack" || p.request.Provider == "feishu" {
		grant.Connection.ScopeVerification = "provider_verified"
	}
	b.mu.Lock()
	e = b.update(p.ctx, p.actor, func(s *stored) error {
		if s.Epochs[grant.Connection.ID] != p.previousEpoch {
			return fault("connection_changed")
		}
		current, exists := s.Grants[grant.Connection.ID]
		if exists && (current.Connection.Revision != p.previousRevision || current.Connection.AccountID != grant.Connection.AccountID || current.Connection.WorkspaceID != grant.Connection.WorkspaceID) || !exists && p.previousRevision != "" {
			return fault("connection_changed")
		}
		if p.ctx.Err() != nil {
			return fault("authorization_cancelled")
		}
		s.Grants[grant.Connection.ID] = grant
		return nil
	})
	if e == nil {
		e = b.activate(p.ctx, p.actor, grant)
	}
	b.mu.Unlock()
	if e != nil {
		p.view.State = "failed"
		p.view.ErrorCode = code(e)
		http.Error(w, "Authorization could not be saved", 500)
		return
	}
	p.view.State = "connected"
	p.view.AuthorizationURL = ""
	w.Write([]byte("Connected. You may close this window."))
}
func (b *Broker) exchangeOAuth(ctx context.Context, c OAuthClient, values url.Values) (OAuthToken, error) {
	var req *http.Request
	var e error
	if isGoogle(c.Provider) {
		values.Set("client_id", c.ClientID)
		if c.ClientSecret != "" {
			values.Set("client_secret", c.ClientSecret)
		}
		req, e = http.NewRequest("POST", "https://oauth2.googleapis.com/token", strings.NewReader(values.Encode()))
		if e == nil {
			req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
		}
	} else {
		req, e = otherOAuthTokenRequest(c, values)
	}
	if e != nil {
		return OAuthToken{}, fault("token_request_invalid")
	}
	req = req.WithContext(ctx)
	req.Header.Set("Accept", "application/json")
	resp, e := b.client.Do(req)
	if e != nil {
		return OAuthToken{}, fault("token_exchange_uncertain")
	}
	defer resp.Body.Close()
	raw, e := io.ReadAll(io.LimitReader(resp.Body, 65537))
	if e != nil || len(raw) > 65536 || resp.StatusCode != 200 {
		return OAuthToken{}, fault("token_exchange_failed")
	}
	if !isGoogle(c.Provider) {
		return decodeOtherOAuthToken(c.Provider, raw)
	}
	var token struct {
		AccessToken  string `json:"access_token"`
		RefreshToken string `json:"refresh_token"`
		ExpiresIn    int64  `json:"expires_in"`
		Scope        string `json:"scope"`
	}
	if json.Unmarshal(raw, &token) != nil || token.AccessToken == "" || token.ExpiresIn < 60 {
		return OAuthToken{}, fault("invalid_token_response")
	}
	return OAuthToken{AccessToken: token.AccessToken, RefreshToken: token.RefreshToken, ExpiresIn: token.ExpiresIn, Scopes: strings.Fields(token.Scope), Raw: raw}, nil
}
func (b *Broker) verifyOAuth(ctx context.Context, provider string, t OAuthToken) (OAuthToken, error) {
	if !isGoogle(provider) {
		return b.verifyOtherOAuthIdentity(ctx, provider, t)
	}
	req, _ := http.NewRequestWithContext(ctx, "GET", "https://www.googleapis.com/oauth2/v2/userinfo", nil)
	req.Header.Set("Authorization", "Bearer "+t.AccessToken)
	resp, e := b.client.Do(req)
	if e != nil {
		return t, fault("identity_verification_failed")
	}
	defer resp.Body.Close()
	raw, e := io.ReadAll(io.LimitReader(resp.Body, 65537))
	var identity struct {
		ID            string `json:"id"`
		Email         string `json:"email"`
		VerifiedEmail bool   `json:"verified_email"`
	}
	if e != nil || len(raw) > 65536 || resp.StatusCode != 200 || json.Unmarshal(raw, &identity) != nil || !identity.VerifiedEmail || identity.Email == "" || identity.ID == "" {
		return t, fault("identity_verification_failed")
	}
	t.AccountID = identity.ID
	t.AccountLabel = identity.Email
	return t, nil
}

func (b *Broker) Configured(ctx context.Context, provider string) bool {
	_, e := b.clientConfig(ctx, provider)
	return e == nil
}

// ValidateAuthorizationURL protects the native opener; endpoints are code-owned,
// never arbitrary client configuration or model input.
func ValidateAuthorizationURL(raw string) error {
	u, e := url.Parse(raw)
	if e != nil || u.Scheme != "https" || u.User != nil || u.Fragment != "" || u.Query().Get("state") == "" || u.Query().Get("client_id") == "" {
		return fault("authorization_url_rejected")
	}
	for _, provider := range []string{"gmail", "notion", "github", "slack", "feishu"} {
		def, e := definition(provider)
		if e != nil {
			continue
		}
		allowed, _ := url.Parse(def.AuthorizationURL)
		if u.Host == allowed.Host && u.Path == allowed.Path {
			return nil
		}
	}
	return fault("authorization_url_rejected")
}

func (b *Broker) identityRequest(ctx context.Context, provider, token string, p Plan) (json.RawMessage, error) {
	u, e := url.Parse(p.URL)
	hosts := map[string]string{"notion": "api.notion.com", "github": "api.github.com", "slack": "slack.com", "feishu": "open.feishu.cn"}
	if e != nil || u.Scheme != "https" || u.Host != hosts[provider] || u.User != nil || u.Fragment != "" || p.Method != "GET" {
		return nil, fault("identity_endpoint_rejected")
	}
	req, e := http.NewRequestWithContext(ctx, "GET", p.URL, nil)
	if e != nil {
		return nil, fault("identity_request_invalid")
	}
	for k, v := range p.Headers {
		req.Header.Set(k, v)
	}
	req.Header.Set("Authorization", "Bearer "+token)
	resp, e := b.client.Do(req)
	if e != nil {
		return nil, fault("identity_verification_failed")
	}
	defer resp.Body.Close()
	raw, e := io.ReadAll(io.LimitReader(resp.Body, 65537))
	if e != nil || len(raw) > 65536 || resp.StatusCode != 200 {
		return nil, fault("identity_verification_failed")
	}
	return sanitize(raw, token)
}

func validRedirect(raw string) error {
	u, e := url.Parse(raw)
	if e != nil {
		return fault("invalid_redirect")
	}
	if u.Scheme == "http" {
		_, e = redirectAddress(raw)
		return e
	}
	if u.Scheme != "https" || u.Hostname() == "" || u.User != nil || u.RawQuery != "" || u.Fragment != "" || u.Path == "" {
		return fault("invalid_redirect")
	}
	return nil
}

type callbackResponse struct {
	header http.Header
	status int
}

func (w *callbackResponse) Header() http.Header         { return w.header }
func (w *callbackResponse) WriteHeader(status int)      { w.status = status }
func (w *callbackResponse) Write(p []byte) (int, error) { return len(p), nil }
