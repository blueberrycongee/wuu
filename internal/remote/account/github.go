package account

import (
	"context"
	"crypto/subtle"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"sync"
	"time"
)

// GitHubConfig holds operator-owned OAuth credentials on the server only.
type GitHubConfig struct{ ClientID, ClientSecret, PublicURL string }

func (c GitHubConfig) Validate() error {
	if c.ClientID == "" && c.ClientSecret == "" {
		return nil
	}
	if c.ClientID == "" || c.ClientSecret == "" {
		return errors.New("configure both WUU_GITHUB_CLIENT_ID and WUU_GITHUB_CLIENT_SECRET")
	}
	if _, err := Origin(c.PublicURL); err != nil {
		return errors.New("GitHub login requires an HTTPS WUU_ACCOUNT_PUBLIC_URL (localhost allowed for development)")
	}
	return nil
}

type githubAttempt struct {
	challenge, providerVerifier, browser, username, failure string
	native, exchanging                                      bool
	expires                                                 time.Time
	session                                                 *Session
}
type GitHubAuth struct {
	config                          GitHubConfig
	mu                              sync.Mutex
	pending                         map[string]*githubAttempt
	client                          *http.Client
	authorizeURL, tokenURL, userURL string
}

func NewGitHubAuth(c GitHubConfig) (*GitHubAuth, error) {
	if err := c.Validate(); err != nil {
		return nil, err
	}
	if c.ClientID == "" {
		return nil, nil
	}
	c.PublicURL, _ = Origin(c.PublicURL)
	return &GitHubAuth{config: c, pending: map[string]*githubAttempt{}, client: &http.Client{Timeout: 15 * time.Second, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}, authorizeURL: "https://github.com/login/oauth/authorize", tokenURL: "https://github.com/login/oauth/access_token", userURL: "https://api.github.com/user"}, nil
}
func (g *GitHubAuth) callbackURL() string { return g.config.PublicURL + "/v1/account/github/callback" }
func challenge(verifier string) string    { return enc.EncodeToString(digest(verifier)) }
func secretEqual(a, b string) bool        { return subtle.ConstantTimeCompare([]byte(a), []byte(b)) == 1 }
func oauthDecode(w http.ResponseWriter, r *http.Request, v any) bool {
	d := json.NewDecoder(http.MaxBytesReader(w, r.Body, 8192))
	d.DisallowUnknownFields()
	if d.Decode(v) != nil {
		writeJSON(w, 400, map[string]string{"error": "invalid OAuth request"})
		return false
	}
	var extra any
	if d.Decode(&extra) != io.EOF {
		writeJSON(w, 400, map[string]string{"error": "invalid OAuth request"})
		return false
	}
	return true
}
func (g *GitHubAuth) attempt(id string) *githubAttempt {
	// Called under mu; keep pending state bounded even when browsers abandon login.
	for key, a := range g.pending {
		if time.Now().After(a.expires) {
			delete(g.pending, key)
		}
	}
	return g.pending[id]
}
func (h *HTTP) githubHTTP(w http.ResponseWriter, r *http.Request, path string) {
	g := h.GitHub
	if g == nil {
		writeJSON(w, 404, map[string]string{"error": "GitHub login is not configured"})
		return
	}
	w.Header().Set("Referrer-Policy", "no-referrer")
	if path == "/github/start" && r.Method == "POST" {
		if !h.admit(h.clientAddress(r)) {
			writeJSON(w, 429, map[string]string{"error": "too many login attempts"})
			return
		}
		var in struct {
			Challenge string `json:"challenge"`
			Native    bool   `json:"native"`
		}
		if !oauthDecode(w, r, &in) {
			return
		}
		decoded, err := enc.DecodeString(in.Challenge)
		if err != nil || len(decoded) != 32 || len(in.Challenge) != 43 {
			writeJSON(w, 400, map[string]string{"error": "invalid challenge"})
			return
		}
		g.mu.Lock()
		defer g.mu.Unlock()
		g.attempt("")
		if len(g.pending) >= 1024 {
			writeJSON(w, 429, map[string]string{"error": "login busy; retry shortly"})
			return
		}
		id := randomToken()
		g.pending[id] = &githubAttempt{challenge: in.Challenge, providerVerifier: randomToken(), native: in.Native, expires: time.Now().Add(10 * time.Minute)}
		writeJSON(w, 200, map[string]any{"request_id": id, "authorize_url": g.config.PublicURL + "/v1/account/github/authorize?state=" + id, "expires_in": 600})
		return
	}
	if path == "/github/authorize" && r.Method == "GET" {
		id := r.URL.Query().Get("state")
		g.mu.Lock()
		a := g.attempt(id)
		if a == nil || a.exchanging || a.username != "" || a.failure != "" {
			g.mu.Unlock()
			http.Error(w, "Login expired. Return to Wuu and try again.", 400)
			return
		}
		browser := randomToken()
		a.browser = challenge(browser)
		pkce := challenge(a.providerVerifier)
		g.mu.Unlock()
		http.SetCookie(w, &http.Cookie{Name: "wuu_oauth_" + id[:16], Value: browser, Path: "/v1/account/github/callback", HttpOnly: true, Secure: strings.HasPrefix(g.config.PublicURL, "https:"), SameSite: http.SameSiteLaxMode, MaxAge: 600})
		params := url.Values{"client_id": {g.config.ClientID}, "redirect_uri": {g.callbackURL()}, "state": {id}, "code_challenge": {pkce}, "code_challenge_method": {"S256"}}
		// Public identity is sufficient: no repository, organization or email scope.
		http.Redirect(w, r, g.authorizeURL+"?"+params.Encode(), http.StatusFound)
		return
	}
	if path == "/github/callback" && r.Method == "GET" {
		g.callback(h, w, r)
		return
	}
	if (path == "/github/poll" || path == "/github/complete" || path == "/github/cancel") && r.Method == "POST" {
		var in struct {
			RequestID string `json:"request_id"`
			Verifier  string `json:"verifier"`
			Pub       string `json:"pub,omitempty"`
			Proof     string `json:"proof,omitempty"`
			Role      string `json:"role,omitempty"`
			Name      string `json:"name,omitempty"`
		}
		if !oauthDecode(w, r, &in) {
			return
		}
		g.mu.Lock()
		defer g.mu.Unlock()
		a := g.attempt(in.RequestID)
		if a == nil || len(in.Verifier) != 43 || !secretEqual(a.challenge, challenge(in.Verifier)) {
			writeJSON(w, 401, map[string]string{"error": "login expired or cancelled; start again"})
			return
		}
		if path == "/github/cancel" {
			delete(g.pending, in.RequestID)
			writeJSON(w, 200, map[string]bool{"ok": true})
			return
		}
		if a.failure != "" {
			writeJSON(w, 400, map[string]string{"error": a.failure})
			return
		}
		if a.username == "" {
			writeJSON(w, 200, map[string]string{"status": "pending"})
			return
		}
		if path == "/github/poll" {
			writeJSON(w, 200, map[string]string{"status": "authorized", "username": a.username})
			return
		}
		if a.session != nil {
			// Retry after a lost completion response is safe only for the enrolled key.
			proof := Login{Username: a.username, Password: "oauth-validation-only", Pub: in.Pub, Proof: in.Proof, Role: in.Role, Name: in.Name}
			if a.session.Pub != in.Pub || validateLogin(&proof) != nil {
				writeJSON(w, 401, map[string]string{"error": "invalid device proof"})
				return
			}
			if _, err := h.Store.Authenticate(a.session.Token); err != nil {
				writeJSON(w, 401, map[string]string{"error": "device session revoked; start again"})
				return
			}
			writeJSON(w, 200, a.session)
			return
		}
		session, err := h.Store.githubLogin(Login{Username: a.username, Pub: in.Pub, Proof: in.Proof, Role: in.Role, Name: in.Name})
		if err != nil {
			writeJSON(w, 400, map[string]string{"error": "could not enroll this device"})
			return
		}
		a.session = &session
		writeJSON(w, 200, session)
		return
	}
	writeJSON(w, 404, map[string]string{"error": "unknown GitHub login endpoint"})
}
func (g *GitHubAuth) callback(h *HTTP, w http.ResponseWriter, r *http.Request) {
	id := r.URL.Query().Get("state")
	g.mu.Lock()
	a := g.attempt(id)
	if a == nil || a.exchanging || a.username != "" || a.failure != "" {
		g.mu.Unlock()
		http.Error(w, "Login expired. Return to Wuu and try again.", 400)
		return
	}
	cookie, err := r.Cookie("wuu_oauth_" + id[:16])
	if err != nil || a.browser == "" || !secretEqual(a.browser, challenge(cookie.Value)) {
		g.mu.Unlock()
		http.Error(w, "Invalid login browser. Start again in Wuu.", 400)
		return
	}
	a.exchanging = true
	verifier := a.providerVerifier
	native := a.native
	g.mu.Unlock()
	http.SetCookie(w, &http.Cookie{Name: cookie.Name, Path: "/v1/account/github/callback", MaxAge: -1, HttpOnly: true, Secure: strings.HasPrefix(g.config.PublicURL, "https:"), SameSite: http.SameSiteLaxMode})
	username := ""
	failure := ""
	if r.URL.Query().Get("error") != "" {
		failure = "GitHub authorization was cancelled"
	} else {
		identity, err := g.exchange(r.Context(), r.URL.Query().Get("code"), verifier)
		if err != nil {
			failure = "GitHub authorization failed; please try again"
		} else {
			username, err = h.Store.githubAccount(identity.Subject, h.AllowRegistration, identity.Name)
			if err != nil {
				failure = "GitHub account could not sign in; check registration or retry"
			}
		}
	}
	g.mu.Lock()
	if current := g.attempt(id); current == a {
		a.username = username
		a.failure = failure
		a.exchanging = false
	}
	g.mu.Unlock()
	nonce := randomToken()
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Header().Set("Content-Security-Policy", "default-src 'none'; style-src 'unsafe-inline'; script-src 'nonce-"+nonce+"'; base-uri 'none'; frame-ancestors 'none'; form-action 'none'")
	message := "授权完成，请返回 Wuu。 / Signed in. Return to Wuu."
	if failure != "" {
		message = "授权未完成，请返回 Wuu 重试。 / Sign-in did not complete. Return to Wuu to retry."
	}
	action := `<button>返回 Wuu / Close this tab</button>`
	script := "document.querySelector('button').onclick=()=>window.close();"
	if native {
		action = `<a href="wuu://account/github">返回 Wuu / Return to Wuu</a>`
		script = "window.location.href='wuu://account/github';"
	}
	fmt.Fprintf(w, `<!doctype html><html><meta name="viewport" content="width=device-width,initial-scale=1"><title>Wuu</title><body style="font:18px system-ui;max-width:480px;margin:15vh auto;padding:24px"><h1>Wuu</h1><p>%s</p>%s<script nonce="%s">%s</script></body></html>`, message, action, nonce, script)
}

type githubIdentity struct{ Subject, Name string }

func (g *GitHubAuth) exchange(ctx context.Context, code, verifier string) (githubIdentity, error) {
	if code == "" || len(code) > 1024 {
		return githubIdentity{}, errors.New("invalid code")
	}
	body := url.Values{"client_id": {g.config.ClientID}, "client_secret": {g.config.ClientSecret}, "code": {code}, "redirect_uri": {g.callbackURL()}, "code_verifier": {verifier}}
	req, _ := http.NewRequestWithContext(ctx, "POST", g.tokenURL, strings.NewReader(body.Encode()))
	req.Header.Set("Accept", "application/json")
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	res, err := g.client.Do(req)
	if err != nil {
		return githubIdentity{}, errors.New("GitHub unavailable")
	}
	defer res.Body.Close()
	var token struct {
		AccessToken string `json:"access_token"`
		Error       string `json:"error"`
	}
	if res.StatusCode != 200 || json.NewDecoder(io.LimitReader(res.Body, 1<<20)).Decode(&token) != nil || token.Error != "" || token.AccessToken == "" {
		return githubIdentity{}, errors.New("GitHub exchange failed")
	}
	req, _ = http.NewRequestWithContext(ctx, "GET", g.userURL, nil)
	req.Header.Set("Authorization", "Bearer "+token.AccessToken)
	req.Header.Set("Accept", "application/vnd.github+json")
	req.Header.Set("X-GitHub-Api-Version", "2022-11-28")
	res, err = g.client.Do(req)
	if err != nil {
		return githubIdentity{}, errors.New("GitHub unavailable")
	}
	defer res.Body.Close()
	var user struct {
		ID    int64  `json:"id"`
		Login string `json:"login"`
	}
	if res.StatusCode != 200 || json.NewDecoder(io.LimitReader(res.Body, 1<<20)).Decode(&user) != nil || user.ID <= 0 {
		return githubIdentity{}, errors.New("invalid GitHub identity")
	}
	return githubIdentity{Subject: strconv.FormatInt(user.ID, 10), Name: user.Login}, nil
}
