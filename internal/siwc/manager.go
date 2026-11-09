// Package siwc implements the public Sign in with ChatGPT native-app protocol.
// Tokens stay in the injected protected credential store and never appear in
// account summaries or login status responses.
package siwc

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/blueberrycongee/wuu/internal/credentialstore"
)

const (
	productionIssuer = "https://auth.openai.com"
	resource         = "https://api.openai.com/v1"
	dynamicClient    = "dynamic_agent_client"
	requestedScopes  = "openid profile email offline_access resource.invoke chatgpt.tokens.use.direct"
	directScope      = "chatgpt.tokens.use.direct"
	callbackPath     = "/auth/callback"
	attemptLifetime  = 15 * time.Minute
)

var (
	ErrNotFound       = errors.New("ChatGPT account or sign-in attempt was not found")
	ErrSignInRequired = errors.New("sign in to this ChatGPT account again")
	ErrPlanDisabled   = errors.New("ChatGPT plan usage is not enabled for this account")
)

// Config requires an explicit credential backend. Desktop callers must supply
// the system keyring; a headless runtime may explicitly choose protected files.
// Issuer and HTTPClient allow isolated protocol fixtures, not user-configurable
// inference endpoints. Production callers leave both unset.
type Config struct {
	StateDir    string
	Credentials credentialstore.Store
	HTTPClient  *http.Client
	Issuer      string
}

type StartOptions struct {
	AccountID string `json:"account_id,omitempty"`
	// ForceConsent is only for an explicit request to enable plan usage.
	ForceConsent bool `json:"force_consent,omitempty"`
}

// Attempt carries privileged main-process data. AuthorizationURL may include an
// ID-token hint: open it in the system browser after Start returns, then remove
// it before sending the remaining status to a renderer or logging it.
type Attempt struct {
	ID               string    `json:"id"`
	AuthorizationURL string    `json:"authorization_url,omitempty"`
	Status           string    `json:"status"`
	AccountID        string    `json:"account_id,omitempty"`
	ErrorCode        string    `json:"error_code,omitempty"`
	ErrorMessage     string    `json:"error_message,omitempty"`
	ExpiresAt        time.Time `json:"expires_at"`
}

type Account struct {
	ID          string    `json:"id"`
	Email       string    `json:"email,omitempty"`
	Subject     string    `json:"subject,omitempty"`
	Issuer      string    `json:"issuer"`
	Connected   bool      `json:"connected"`
	PlanEnabled bool      `json:"plan_enabled"`
	ExpiresAt   time.Time `json:"expires_at,omitempty"`
}

type SignOutResult struct {
	RevocationConfirmed bool `json:"revocation_confirmed"`
}

type Manager struct {
	config      Config
	client      *http.Client
	namespace   string
	now         func() time.Time
	mu          sync.Mutex
	attempts    map[string]*pendingAttempt
	closed      bool
	discoveryMu sync.Mutex
	discovery   discoveryDocument
	keysMu      sync.Mutex
	keys        jwksDocument
	keysFetched time.Time
}

type pendingAttempt struct {
	mu                                                            sync.Mutex
	view                                                          Attempt
	ctx                                                           context.Context
	cancel                                                        context.CancelFunc
	server                                                        *http.Server
	state, nonce, verifier, redirect, clientID, subject, revision string
	consumed                                                      bool
}

// New does no network I/O and does not create credentials.
func New(config Config) (*Manager, error) {
	if config.Credentials == nil {
		return nil, errors.New("ChatGPT requires a protected credential store")
	}
	if strings.TrimSpace(config.StateDir) == "" {
		return nil, errors.New("ChatGPT state directory is required")
	}
	abs, err := filepath.Abs(config.StateDir)
	if err != nil {
		return nil, err
	}
	config.StateDir = abs
	if config.Issuer == "" {
		config.Issuer = productionIssuer
	}
	u, err := url.Parse(config.Issuer)
	if err != nil || u.User != nil || u.RawQuery != "" || u.Fragment != "" || u.Path != "" || u.Host == "" {
		return nil, errors.New("invalid ChatGPT issuer")
	}
	// HTTP is accepted solely for an explicitly injected loopback test issuer.
	if config.Issuer != productionIssuer && !(config.HTTPClient != nil && u.Scheme == "http" && u.Hostname() == "127.0.0.1") {
		return nil, errors.New("ChatGPT issuer must be the official issuer or an injected loopback fixture")
	}
	client := http.Client{Timeout: 20 * time.Second}
	if config.HTTPClient != nil {
		client = *config.HTTPClient
		if client.Timeout == 0 {
			client.Timeout = 20 * time.Second
		}
	}
	// Redirects must never forward OAuth codes, refresh tokens or token hints.
	client.CheckRedirect = func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }
	sum := sha256.Sum256([]byte(abs + "\x00" + config.Issuer))
	return &Manager{config: config, client: &client, namespace: fmt.Sprintf("siwc:%x", sum[:]), now: time.Now, attempts: map[string]*pendingAttempt{}}, nil
}

func randomValue() (string, error) {
	b := make([]byte, 32)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	return base64.RawURLEncoding.EncodeToString(b), nil
}

func (m *Manager) Poll(id string) (Attempt, error) {
	m.mu.Lock()
	p := m.attempts[id]
	m.mu.Unlock()
	if p == nil {
		return Attempt{}, ErrNotFound
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.view, nil
}

func (m *Manager) Cancel(id string) error {
	m.mu.Lock()
	p := m.attempts[id]
	m.mu.Unlock()
	if p == nil {
		return ErrNotFound
	}
	p.finish("cancelled", "cancelled", "ChatGPT sign-in was cancelled")
	return nil
}

// Close closes all callback listeners, cancels pending exchanges and prevents
// further sign-in attempts. Saved account registrations are unaffected.
func (m *Manager) Close() error {
	m.mu.Lock()
	m.closed = true
	pending := make([]*pendingAttempt, 0, len(m.attempts))
	for _, p := range m.attempts {
		pending = append(pending, p)
	}
	m.mu.Unlock()
	cancelManagerRequests(m)
	for _, p := range pending {
		p.finish("cancelled", "cancelled", "ChatGPT sign-in was cancelled")
	}
	return nil
}

func (p *pendingAttempt) finish(status, code, message string) {
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.view.Status != "pending" {
		return
	}
	p.setFinished(status, code, message)
}
func (p *pendingAttempt) setFinished(status, code, message string) {
	p.view.Status = status
	p.view.AuthorizationURL = ""
	p.view.ErrorCode = code
	p.view.ErrorMessage = message
	p.state = ""
	p.nonce = ""
	p.verifier = ""
	p.cancel()
	// Shutdown waits for the callback response without cutting off that request.
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), time.Second)
		defer cancel()
		_ = p.server.Shutdown(ctx)
	}()
}
