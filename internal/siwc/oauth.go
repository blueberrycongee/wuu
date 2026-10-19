package siwc

import (
	"context"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"strings"
	"time"
)

type discoveryDocument struct {
	Issuer                string `json:"issuer"`
	AuthorizationEndpoint string `json:"authorization_endpoint"`
	TokenEndpoint         string `json:"token_endpoint"`
	JWKSURI               string `json:"jwks_uri"`
	RevocationEndpoint    string `json:"revocation_endpoint"`
}

type OAuthError struct {
	Code   string
	Status int
}

func (e *OAuthError) Error() string {
	return fmt.Sprintf("ChatGPT authorization failed (%s, HTTP %d)", e.Code, e.Status)
}

func (m *Manager) discover(ctx context.Context) (discoveryDocument, error) {
	m.discoveryMu.Lock()
	defer m.discoveryMu.Unlock()
	if m.discovery.Issuer != "" {
		return m.discovery, nil
	}
	var doc discoveryDocument
	if err := m.getJSON(ctx, m.config.Issuer+"/.well-known/openid-configuration", &doc); err != nil {
		return doc, err
	}
	if doc.Issuer != m.config.Issuer {
		return doc, errors.New("ChatGPT discovery issuer mismatch")
	}
	for _, endpoint := range []string{doc.AuthorizationEndpoint, doc.TokenEndpoint, doc.JWKSURI, doc.RevocationEndpoint} {
		u, err := url.Parse(endpoint)
		base, _ := url.Parse(m.config.Issuer)
		if err != nil || u.Scheme != base.Scheme || u.Host != base.Host || u.User != nil || u.Fragment != "" || u.RawQuery != "" || u.Path == "" {
			return doc, errors.New("ChatGPT discovery returned an unsafe endpoint")
		}
	}
	m.discovery = doc
	return doc, nil
}
func (m *Manager) getJSON(ctx context.Context, endpoint string, dest any) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint, nil)
	if err != nil {
		return errors.New("invalid ChatGPT endpoint")
	}
	resp, err := m.client.Do(req)
	if err != nil {
		return errors.New("ChatGPT authorization service is unavailable")
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return &OAuthError{Code: "service_unavailable", Status: resp.StatusCode}
	}
	body, err := io.ReadAll(io.LimitReader(resp.Body, 1024*1024+1))
	if err != nil || len(body) > 1024*1024 {
		return errors.New("invalid ChatGPT authorization response")
	}
	if json.Unmarshal(body, dest) != nil {
		return errors.New("invalid ChatGPT authorization response")
	}
	return nil
}

// Start starts the exact 127.0.0.1 callback listener before exposing a browser
// URL. The caller's request context governs setup only; Cancel/Close and the
// attempt deadline govern the browser flow after Start returns.
func (m *Manager) Start(ctx context.Context, opts StartOptions) (Attempt, error) {
	m.mu.Lock()
	closed := m.closed
	m.mu.Unlock()
	if closed {
		return Attempt{}, errors.New("ChatGPT sign-in service is closed")
	}
	hostID, err := m.hostID()
	if err != nil {
		return Attempt{}, err
	}
	doc, err := m.discover(ctx)
	if err != nil {
		return Attempt{}, err
	}
	r := registration{ID: dynamicClient}
	var saved credentials
	if opts.AccountID != "" {
		r, err = m.registration(opts.AccountID)
		if err != nil {
			return Attempt{}, err
		}
		saved, err = m.readCredentials(ctx, r)
		if err != nil && !errors.Is(err, ErrSignInRequired) {
			return Attempt{}, err
		}
	}
	state, err := randomValue()
	if err != nil {
		return Attempt{}, err
	}
	nonce, err := randomValue()
	if err != nil {
		return Attempt{}, err
	}
	verifier, err := randomValue()
	if err != nil {
		return Attempt{}, err
	}
	id, err := randomValue()
	if err != nil {
		return Attempt{}, err
	}
	listener, err := net.Listen("tcp4", "127.0.0.1:0")
	if err != nil {
		return Attempt{}, errors.New("could not start the ChatGPT loopback callback listener")
	}
	redirect := "http://" + listener.Addr().String() + callbackPath
	attemptCtx, cancel := context.WithTimeout(context.Background(), attemptLifetime)
	p := &pendingAttempt{ctx: attemptCtx, cancel: cancel, state: state, nonce: nonce, verifier: verifier, redirect: redirect, clientID: r.ID, subject: r.Subject, revision: r.Revision, view: Attempt{ID: id, Status: "pending", AccountID: opts.AccountID, ExpiresAt: m.now().Add(attemptLifetime)}}
	p.server = &http.Server{Handler: http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) { m.callback(p, w, req) }), ReadHeaderTimeout: 5 * time.Second, ReadTimeout: 10 * time.Second, WriteTimeout: 45 * time.Second, IdleTimeout: 5 * time.Second, MaxHeaderBytes: 16 * 1024}
	challenge := sha256.Sum256([]byte(verifier))
	q := url.Values{"client_id": {r.ID}, "ext_agent_host_id": {hostID}, "response_type": {"code"}, "redirect_uri": {redirect}, "scope": {requestedScopes}, "resource": {resource}, "state": {state}, "nonce": {nonce}, "code_challenge_method": {"S256"}, "code_challenge": {base64.RawURLEncoding.EncodeToString(challenge[:])}}
	if r.ID == dynamicClient {
		q.Set("agent_name_hint", "Wuu")
	} else {
		if saved.IDToken != "" {
			q.Set("id_token_hint", saved.IDToken)
		}
		if r.Email != "" {
			q.Set("login_hint", r.Email)
		}
	}
	if opts.ForceConsent {
		q.Set("prompt", "consent")
	}
	p.view.AuthorizationURL = doc.AuthorizationEndpoint + "?" + q.Encode()
	m.mu.Lock()
	if m.closed {
		m.mu.Unlock()
		cancel()
		_ = listener.Close()
		return Attempt{}, errors.New("ChatGPT sign-in service is closed")
	}
	pending := 0
	for key, old := range m.attempts {
		old.mu.Lock()
		if old.view.Status == "pending" {
			pending++
		} else if !m.now().Before(old.view.ExpiresAt) {
			delete(m.attempts, key)
		}
		old.mu.Unlock()
	}
	if pending > 0 {
		m.mu.Unlock()
		cancel()
		_ = listener.Close()
		return Attempt{}, errors.New("a ChatGPT sign-in attempt is already pending")
	}
	for len(m.attempts) >= 32 {
		var oldestID string
		var oldest time.Time
		for key, previous := range m.attempts {
			previous.mu.Lock()
			expiry := previous.view.ExpiresAt
			previous.mu.Unlock()
			if oldestID == "" || expiry.Before(oldest) {
				oldestID = key
				oldest = expiry
			}
		}
		delete(m.attempts, oldestID)
	}
	m.attempts[id] = p
	m.mu.Unlock()
	go func() {
		if err := p.server.Serve(listener); err != nil && !errors.Is(err, http.ErrServerClosed) {
			p.finish("failed", "callback_unavailable", "The ChatGPT callback listener stopped")
		}
	}()
	go func() {
		<-attemptCtx.Done()
		if errors.Is(attemptCtx.Err(), context.DeadlineExceeded) {
			p.finish("expired", "expired", "ChatGPT sign-in expired; start again")
		}
	}()
	p.mu.Lock()
	view := p.view
	p.mu.Unlock()
	return view, nil
}

func (m *Manager) callback(p *pendingAttempt, w http.ResponseWriter, req *http.Request) {
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("Referrer-Policy", "no-referrer")
	w.Header().Set("Content-Security-Policy", "default-src 'none'")
	w.Header().Set("Content-Type", "text/plain; charset=utf-8")
	expected, _ := url.Parse(p.redirect)
	if req.Method != http.MethodGet || req.URL.Path != callbackPath || req.Host != expected.Host {
		http.Error(w, "Invalid callback", http.StatusBadRequest)
		return
	}
	q, err := url.ParseQuery(req.URL.RawQuery)
	if err != nil {
		http.Error(w, "Invalid callback", http.StatusBadRequest)
		return
	}
	for _, key := range []string{"state", "code", "client_id", "error"} {
		if len(q[key]) > 1 {
			http.Error(w, "Invalid callback", http.StatusBadRequest)
			return
		}
	}
	p.mu.Lock()
	if p.view.Status != "pending" || p.consumed {
		p.mu.Unlock()
		http.Error(w, "This sign-in attempt is already complete", http.StatusConflict)
		return
	}
	if q.Get("state") == "" || subtle.ConstantTimeCompare([]byte(q.Get("state")), []byte(p.state)) != 1 {
		p.mu.Unlock()
		http.Error(w, "Invalid callback state", http.StatusBadRequest)
		return
	}
	p.consumed = true
	if q.Get("error") != "" {
		p.setFinished("failed", "access_denied", "ChatGPT sign-in was not approved")
		p.mu.Unlock()
		http.Error(w, "Sign-in was not approved. Return to Wuu.", http.StatusBadRequest)
		return
	}
	clientID := p.clientID
	if clientID == dynamicClient {
		clientID = q.Get("client_id")
	} else if q.Has("client_id") && q.Get("client_id") != clientID {
		p.setFinished("failed", "account_mismatch", "The sign-in returned a different ChatGPT registration")
		p.mu.Unlock()
		http.Error(w, "Account mismatch. Return to Wuu.", http.StatusBadRequest)
		return
	}
	if !validClientID(clientID) || q.Get("code") == "" {
		p.setFinished("failed", "incomplete_registration", "ChatGPT registration was incomplete; start again")
		p.mu.Unlock()
		http.Error(w, "Incomplete registration. Return to Wuu.", http.StatusBadRequest)
		return
	}
	// Copy single-use values before releasing the mutex. Cancellation erases the
	// pending copies and prevents the final persistence step after network I/O.
	verifier, nonce, wasNew := p.verifier, p.nonce, p.clientID == dynamicClient
	p.view.AccountID = clientID
	p.mu.Unlock()
	err = m.exchangeCallback(p, clientID, q.Get("code"), verifier, nonce, wasNew)
	if err != nil {
		code, message := "sign_in_failed", "ChatGPT sign-in could not be completed; try again"
		var oauth *OAuthError
		if errors.As(err, &oauth) {
			code = oauth.Code
			if oauth.Code == "invalid_grant" {
				message = "The sign-in code expired; reconnect using the saved ChatGPT account"
			}
		}
		if errors.Is(err, errIdentityMismatch) {
			code = "account_mismatch"
			message = "The sign-in returned a different ChatGPT identity"
		}
		p.finish("failed", code, message)
		http.Error(w, "Sign-in could not be completed. Return to Wuu.", http.StatusBadRequest)
		return
	}
	_, _ = io.WriteString(w, "ChatGPT sign-in completed. You can close this window and return to Wuu.")
}
func validClientID(id string) bool {
	if id == "" || id == dynamicClient || len(id) > 256 {
		return false
	}
	for _, r := range id {
		if !(r >= 'a' && r <= 'z' || r >= 'A' && r <= 'Z' || r >= '0' && r <= '9' || r == '_' || r == '-') {
			return false
		}
	}
	return true
}

var errIdentityMismatch = errors.New("ChatGPT identity mismatch")

func (m *Manager) exchangeCallback(p *pendingAttempt, clientID, code, verifier, nonce string, wasNew bool) error {
	lock, err := m.accountLock(clientID)
	if err != nil {
		return err
	}
	defer lock.Release()
	var r registration
	if wasNew {
		revision, err := randomValue()
		if err != nil {
			return err
		}
		r = registration{ID: clientID, Issuer: m.config.Issuer, Revision: revision}
		// Persist the server-issued client before consuming the authorization code.
		// A failed exchange can therefore resume without registering a second client.
		err = m.mutateState(func(s *diskState) error {
			if _, exists := s.Accounts[clientID]; exists {
				return errIdentityMismatch
			}
			s.Accounts[clientID] = r
			return nil
		})
		if err != nil {
			return err
		}
	} else {
		r, err = m.registration(clientID)
		if err != nil {
			return err
		}
		if r.Revision != p.revision || r.Subject != p.subject {
			return errIdentityMismatch
		}
	}
	doc, err := m.discover(p.ctx)
	if err != nil {
		return err
	}
	response, err := m.requestTokens(p.ctx, doc.TokenEndpoint, url.Values{"grant_type": {"authorization_code"}, "client_id": {clientID}, "code": {code}, "code_verifier": {verifier}, "redirect_uri": {p.redirect}, "resource": {resource}})
	if err != nil {
		return err
	}
	claims, err := m.verifyIdentity(p.ctx, response.IDToken, clientID, nonce, true)
	if err != nil {
		return err
	}
	if r.Subject != "" && r.Subject != claims.Subject {
		return errIdentityMismatch
	}
	c, err := m.makeCredentials(response, r, nonce, credentials{})
	if err != nil {
		return err
	}
	c.Subject = claims.Subject
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.view.Status != "pending" || p.ctx.Err() != nil {
		return context.Canceled
	}
	r.Subject = claims.Subject
	r.Email = claims.Email
	revision, err := randomValue()
	if err != nil {
		return err
	}
	r.Revision = revision
	c.Revision = revision
	// The metadata revision is authoritative; stale token writes cannot restore
	// a signed-out session. CredentialStore.Set atomically replaces the token set.
	if err = m.mutateState(func(s *diskState) error { s.Accounts[clientID] = r; return nil }); err != nil {
		return err
	}
	if err = m.saveCredentials(p.ctx, c); err != nil {
		return err
	}
	p.setFinished("succeeded", "", "")
	return nil
}

type tokenResponse struct {
	AccessToken       string          `json:"access_token"`
	RefreshToken      string          `json:"refresh_token"`
	IDToken           string          `json:"id_token"`
	TokenType         string          `json:"token_type"`
	ExpiresIn         int64           `json:"expires_in"`
	Scope             *string         `json:"scope"`
	EarliestRefreshAt json.RawMessage `json:"earliest_refresh_at"`
}

func (m *Manager) requestTokens(ctx context.Context, endpoint string, form url.Values) (tokenResponse, error) {
	var result tokenResponse
	resp, err := m.postForm(ctx, endpoint, form)
	if err != nil {
		return result, err
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(io.LimitReader(resp.Body, 1024*1024+1))
	if err != nil || len(body) > 1024*1024 {
		return result, errors.New("invalid ChatGPT token response")
	}
	if resp.StatusCode != http.StatusOK {
		return result, parseOAuthError(resp.StatusCode, body)
	}
	if json.Unmarshal(body, &result) != nil || result.AccessToken == "" || !strings.EqualFold(result.TokenType, "Bearer") || result.ExpiresIn <= 0 || result.ExpiresIn > 31*86400 {
		return result, errors.New("invalid ChatGPT token response")
	}
	return result, nil
}
func (m *Manager) postForm(ctx context.Context, endpoint string, form url.Values) (*http.Response, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, strings.NewReader(form.Encode()))
	if err != nil {
		return nil, errors.New("invalid ChatGPT token endpoint")
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.Header.Set("Accept", "application/json")
	resp, err := m.client.Do(req)
	if err != nil {
		return nil, errors.New("ChatGPT authorization service is unavailable")
	}
	return resp, nil
}
func parseOAuthError(status int, body []byte) error {
	var response struct {
		Error json.RawMessage `json:"error"`
	}
	var code string
	if json.Unmarshal(body, &response) == nil {
		if json.Unmarshal(response.Error, &code) != nil {
			var detail struct {
				Code string `json:"code"`
			}
			if json.Unmarshal(response.Error, &detail) == nil {
				code = detail.Code
			}
		}
	}
	switch code {
	case "invalid_grant", "invalid_refresh_token", "token_expired", "refresh_token_expired", "refresh_token_invalidated", "refresh_token_reused", "invalid_client", "access_denied", "temporarily_unavailable", "server_error", "invalid_scope", "invalid_request", "unsupported_grant_type":
	default:
		code = "oauth_error"
	}
	return &OAuthError{Code: code, Status: status}
}
func (m *Manager) makeCredentials(response tokenResponse, r registration, nonce string, previous credentials) (credentials, error) {
	c := credentials{ClientID: r.ID, Issuer: r.Issuer, Subject: r.Subject, Revision: r.Revision, AccessToken: response.AccessToken, RefreshToken: response.RefreshToken, IDToken: response.IDToken, Nonce: nonce, ExpiresAt: m.now().Add(time.Duration(response.ExpiresIn) * time.Second)}
	if response.Scope != nil {
		c.Scopes = strings.Fields(*response.Scope)
	} else if previous.AccessToken != "" {
		c.Scopes = previous.Scopes
	} else {
		return c, errors.New("ChatGPT token response did not include granted scopes")
	}
	if hasScope(c.Scopes, "offline_access") && c.RefreshToken == "" {
		return c, errors.New("ChatGPT token response did not include a refresh token")
	}
	if c.RefreshToken != "" {
		c.RefreshExpiresAt = m.now().Add(30 * 24 * time.Hour)
	}
	if c.IDToken == "" {
		c.IDToken = previous.IDToken
	}
	if len(response.EarliestRefreshAt) > 0 && string(response.EarliestRefreshAt) != "null" {
		var epoch int64
		if json.Unmarshal(response.EarliestRefreshAt, &epoch) == nil {
			c.EarliestRefreshAt = time.Unix(epoch, 0)
		} else {
			var timestamp string
			if json.Unmarshal(response.EarliestRefreshAt, &timestamp) != nil {
				return c, errors.New("invalid ChatGPT refresh time")
			}
			parsed, err := time.Parse(time.RFC3339, timestamp)
			if err != nil {
				return c, errors.New("invalid ChatGPT refresh time")
			}
			c.EarliestRefreshAt = parsed
		}
	}
	return c, nil
}
