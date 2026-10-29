// Package personalcalendar owns the desktop Personal Calendar credential boundary.
// It is deliberately not registered with Coding, MCP, or the app-server here.
package personalcalendar

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"net/http"
	"slices"
	"strings"
	"sync"
	"time"

	"github.com/blueberrycongee/wuu/internal/credentialstore"
)

const namespace = "personal.google-calendar.v1"
const ownedScope = "https://www.googleapis.com/auth/calendar.events.owned.readonly"
const listScope = "https://www.googleapis.com/auth/calendar.calendarlist.readonly"

func Scopes() []string { return []string{"openid", ownedScope, listScope} }

// Actor MUST be resolved by the host from the authenticated session, never JSON input.
type Actor struct{ ProductDomain, UserSpaceID, SessionID string }

func (a Actor) validate() error {
	if a.ProductDomain != "personal" || a.UserSpaceID == "" || len(a.UserSpaceID) > 512 || a.SessionID == "" {
		return fault("personal_scope_required")
	}
	return nil
}

type Config struct {
	ClientID     string
	ClientSecret string
}
type Connection struct {
	ID                string   `json:"connection_id,omitempty"`
	UserSpaceID       string   `json:"user_space_id,omitempty"`
	ProductDomain     string   `json:"product_domain"`
	Provider          string   `json:"provider"`
	AccountID         string   `json:"account_id,omitempty"`
	State             string   `json:"state"`
	Scopes            []string `json:"scopes,omitempty"`
	SelectedCalendars []string `json:"selected_calendars"`
	SelectionRevision string   `json:"selection_revision,omitempty"`
	ErrorCode         string   `json:"error_code,omitempty"`
}
type stored struct {
	Version      int        `json:"version"`
	ClientID     string     `json:"client_id"`
	Connection   Connection `json:"connection"`
	AccessToken  string     `json:"access_token,omitempty"`
	RefreshToken string     `json:"refresh_token,omitempty"`
	ExpiresAt    time.Time  `json:"expires_at"`
	Revision     string     `json:"revision"`
	Lease        string     `json:"lease,omitempty"`
	LeaseUntil   time.Time  `json:"lease_until,omitempty"`
}

// Tokens and PKCE verifiers never appear in exported status or error structures.
type Broker struct {
	lifetime       context.Context
	cancel         context.CancelFunc
	config         Config
	store          credentialstore.Store
	client         *http.Client
	now            func() time.Time
	wait           func(context.Context, time.Duration) error
	verifyIdentity func(context.Context, string, string) (string, error)
	mu             sync.Mutex
	attempts       map[string]*pending
	closed         bool
	denied         map[string]string
	activationDir  string
}

func newBroker(c Config, s credentialstore.Store, client *http.Client, activationDir string) *Broker {
	copy := *client
	copy.Timeout = 15 * time.Second
	copy.CheckRedirect = func(*http.Request, []*http.Request) error { return fault("redirect_rejected") }
	lifetime, cancel := context.WithCancel(context.Background())
	b := &Broker{lifetime: lifetime, cancel: cancel, config: c, store: s, client: &copy, now: time.Now, attempts: map[string]*pending{}, denied: map[string]string{}, activationDir: activationDir}
	b.wait = func(ctx context.Context, d time.Duration) error {
		timer := time.NewTimer(d)
		defer timer.Stop()
		select {
		case <-ctx.Done():
			return fault("request_cancelled")
		case <-timer.C:
			return nil
		}
	}
	b.verifyIdentity = b.verifyIDToken
	return b
}

// Failure is the sanitized host error envelope. Bridges must preserve Code,
// never infer authorization from provider response bodies or error text.
type Failure struct {
	Code              string `json:"code"`
	PersistenceFailed bool   `json:"persistence_failed,omitempty"`
}

func (e *Failure) Error() string { return "calendar: " + e.Code }
func fault(code string) error    { return &Failure{Code: code} }
func authorizationError(e error) bool {
	var failure *Failure
	return errors.As(e, &failure) && slices.Contains([]string{"authorization_denied", "authorization_expired", "scope_mismatch"}, failure.Code)
}
func (b *Broker) alive(ctx context.Context) error {
	if b.lifetime.Err() != nil {
		return fault("connector_closed")
	}
	if ctx.Err() != nil {
		return fault("request_cancelled")
	}
	return nil
}
func (b *Broker) allowed(ctx context.Context, id string) error {
	if e := b.alive(ctx); e != nil {
		return e
	}
	b.mu.Lock()
	code := b.denied[id]
	b.mu.Unlock()
	if code != "" {
		return fault(code)
	}
	return nil
}
func (b *Broker) deny(id string, e error) {
	var failure *Failure
	if errors.As(e, &failure) && authorizationError(e) {
		b.mu.Lock()
		b.denied[id] = failure.Code
		b.mu.Unlock()
	}
}
func randomID() string {
	var b [32]byte
	if _, e := rand.Read(b[:]); e != nil {
		panic("secure random unavailable")
	}
	return base64.RawURLEncoding.EncodeToString(b[:])
}
func key(space string) string { h := sha256.Sum256([]byte(space)); return hex.EncodeToString(h[:]) }
func (b *Broker) decode(raw []byte, a Actor) (stored, error) {
	var r stored
	if json.Unmarshal(raw, &r) != nil || r.Version != 1 || r.ClientID != b.config.ClientID || r.Connection.UserSpaceID != a.UserSpaceID || r.Connection.ProductDomain != "personal" || r.Connection.Provider != "google-calendar" {
		return r, fault("connection_scope_mismatch")
	}
	return r, nil
}
func (b *Broker) load(ctx context.Context, a Actor) (stored, error) {
	if e := a.validate(); e != nil {
		return stored{}, e
	}
	if e := b.alive(ctx); e != nil {
		return stored{}, e
	}
	raw, e := b.store.Get(ctx, namespace, key(a.UserSpaceID))
	if err := b.alive(ctx); err != nil {
		return stored{}, err
	}
	if errors.Is(e, credentialstore.ErrNotFound) {
		return stored{}, fault("account_not_connected")
	}
	if e != nil {
		return stored{}, fault("secure_store_unavailable")
	}
	return b.decode(raw, a)
}
func (b *Broker) Status(ctx context.Context, a Actor) (Connection, error) {
	if e := a.validate(); e != nil {
		return Connection{}, e
	}
	if e := b.alive(ctx); e != nil {
		return Connection{}, e
	}
	raw, e := b.store.Get(ctx, namespace, key(a.UserSpaceID))
	if err := b.alive(ctx); err != nil {
		return Connection{}, err
	}
	if errors.Is(e, credentialstore.ErrNotFound) {
		return Connection{ProductDomain: "personal", Provider: "google-calendar", State: "unconnected", SelectedCalendars: []string{}}, nil
	}
	if e != nil {
		return Connection{}, fault("secure_store_unavailable")
	}
	r, e := b.decode(raw, a)
	if e != nil {
		return Connection{}, e
	}
	if r.Connection.State == "connected" && !b.hasActivation(r) {
		r.Connection.State = "needs_reauth"
		r.Connection.ErrorCode = "authorization_incomplete"
		r.Connection.SelectedCalendars = []string{}
	}
	b.mu.Lock()
	code := b.denied[r.Connection.ID]
	b.mu.Unlock()
	if code != "" {
		r.Connection.State = "needs_reauth"
		r.Connection.ErrorCode = code
	}
	if e = b.alive(ctx); e != nil {
		return Connection{}, e
	}
	return r.Connection, nil
}

// Authorize verifies cached-read authority against Google as well as local
// state. A new host process cannot trust an old secure-store record after a
// previous process failed to persist revocation. It reads no event history.
func (b *Broker) Authorize(ctx context.Context, a Actor) (Connection, error) {
	c, e := b.Status(ctx, a)
	if e != nil || c.State != "connected" {
		return c, e
	}
	if _, e = b.Calendars(ctx, a, c.ID); e != nil {
		return Connection{}, e
	}
	current, e := b.Status(ctx, a)
	if e == nil && (current.ID != c.ID || current.SelectionRevision != c.SelectionRevision) {
		e = fault("connection_changed")
	}
	return current, e
}
func (b *Broker) change(ctx context.Context, a Actor, id string, fn func(*stored) error) error {
	if e := a.validate(); e != nil {
		return e
	}
	var inner error
	e := b.store.Update(ctx, namespace, key(a.UserSpaceID), func(raw []byte) ([]byte, error) {
		r, e := b.decode(raw, a)
		if e != nil {
			inner = e
			return nil, e
		}
		if r.Connection.ID != id {
			inner = fault("connection_changed")
			return nil, inner
		}
		if e = fn(&r); e != nil {
			inner = e
			return nil, e
		}
		return json.Marshal(r)
	})
	if inner != nil {
		return inner
	}
	if e != nil {
		return fault("secure_store_unavailable")
	}
	return nil
}

// Close fences reads immediately, then drains OAuth commits and their
// conditional cleanup. A cleanup failure is returned, never reported as deletion.
func (b *Broker) Close() error {
	b.cancel()
	b.mu.Lock()
	b.closed = true
	attempts := make([]*pending, 0, len(b.attempts))
	for _, p := range b.attempts {
		attempts = append(attempts, p)
	}
	b.mu.Unlock()
	for _, p := range attempts {
		p.stop("cancelled", "authorization_cancelled")
	}
	var failure error
	for _, p := range attempts {
		if e := p.drain(); e != nil {
			failure = e
		}
	}
	return failure
}
func (b *Broker) grant(ctx context.Context, a Actor, id string) (stored, error) {
	if b.lifetime.Err() != nil {
		return stored{}, fault("connector_closed")
	}
	r, e := b.load(ctx, a)
	if e != nil {
		return r, e
	}
	if r.Connection.ID != id {
		return r, fault("connection_changed")
	}
	if r.Connection.State != "connected" || !b.hasActivation(r) {
		return r, fault("reconnect_required")
	}
	if e = b.allowed(ctx, r.Connection.ID); e != nil {
		return stored{}, e
	}
	if b.now().Add(time.Minute).Before(r.ExpiresAt) {
		return r, nil
	}
	lease := randomID()
	e = b.change(ctx, a, id, func(current *stored) error {
		if current.Revision != r.Revision || current.Connection.State != "connected" {
			return fault("connection_changed")
		}
		if current.Lease != "" && b.now().Before(current.LeaseUntil) {
			return fault("refresh_busy")
		}
		current.Lease = lease
		current.LeaseUntil = b.now().Add(30 * time.Second)
		return nil
	})
	if e != nil {
		return r, e
	}
	token, e := b.exchange(ctx, map[string]string{"grant_type": "refresh_token", "refresh_token": r.RefreshToken})
	if e == nil {
		if token.Scope == "" {
			token.Scope = strings.Join(r.Connection.Scopes, " ")
		}
		e = token.validate(false)
	}
	b.deny(id, e)
	cleanup, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	saved := r
	commit := b.change(cleanup, a, id, func(current *stored) error {
		if current.Lease != lease || current.Revision != r.Revision || current.Connection.State != "connected" {
			return fault("connection_changed")
		}
		current.Lease = ""
		current.LeaseUntil = time.Time{}
		if e != nil {
			if e.Error() == fault("authorization_expired").Error() || e.Error() == fault("scope_mismatch").Error() {
				current.Connection.State = "needs_reauth"
				current.Connection.ErrorCode = "authorization_expired"
				current.AccessToken = ""
				current.RefreshToken = ""
			}
			return nil
		}
		current.AccessToken = token.AccessToken
		if token.RefreshToken != "" {
			current.RefreshToken = token.RefreshToken
		}
		current.ExpiresAt = b.now().Add(time.Duration(token.ExpiresIn) * time.Second)
		current.Revision = randomID()
		saved = *current
		return nil
	})
	if commit != nil {
		if authorizationError(e) {
			return stored{}, &Failure{Code: e.(*Failure).Code, PersistenceFailed: true}
		}
		return stored{}, commit
	}
	if e != nil {
		return stored{}, e
	}
	if e = b.allowed(ctx, id); e != nil {
		return stored{}, e
	}
	return saved, nil
}
func (b *Broker) check(ctx context.Context, a Actor, r stored) error {
	if e := b.allowed(ctx, r.Connection.ID); e != nil {
		return e
	}
	current, e := b.load(ctx, a)
	if e != nil {
		return e
	}
	if e = b.allowed(ctx, r.Connection.ID); e != nil {
		return e
	}
	if current.Connection.ID != r.Connection.ID || current.Revision != r.Revision || current.Connection.State != "connected" || !b.hasActivation(current) {
		return fault("connection_changed")
	}
	return b.allowed(ctx, r.Connection.ID)
}

type DisconnectResult struct {
	LocalDeleted bool   `json:"local_deleted"`
	Revocation   string `json:"revocation"`
}

// Disconnect disables use before deletion. Revocation is opt-in because Google
// may revoke all grants for the same project. Failure never restores local tokens.
func (b *Broker) Disconnect(ctx context.Context, a Actor, id string, revoke bool) (DisconnectResult, error) {
	result := DisconnectResult{Revocation: "not_requested"}
	r, e := b.load(ctx, a)
	if e != nil {
		return result, e
	}
	if r.Connection.ID != id {
		return result, fault("connection_changed")
	}
	b.mu.Lock()
	var attempts []*pending
	for _, p := range b.attempts {
		if p.actor.UserSpaceID == a.UserSpaceID {
			attempts = append(attempts, p)
		}
	}
	b.mu.Unlock()
	for _, p := range attempts {
		p.stop("cancelled", "authorization_cancelled")
	}
	for _, p := range attempts {
		if e = p.drain(); e != nil {
			return result, e
		}
	}
	e = b.change(ctx, a, id, func(c *stored) error {
		c.Connection.State = "disconnected"
		c.Connection.SelectedCalendars = []string{}
		c.AccessToken = ""
		c.RefreshToken = ""
		c.Revision = randomID()
		c.Lease = ""
		return nil
	})
	if e != nil {
		return result, e
	}
	// Delete through atomic Update: a concurrent explicit reconnect must not be deleted.
	e = b.store.Update(ctx, namespace, key(a.UserSpaceID), func(raw []byte) ([]byte, error) {
		c, e := b.decode(raw, a)
		if e != nil {
			return nil, e
		}
		if c.Connection.ID != id || c.Connection.State != "disconnected" {
			return nil, fault("connection_changed")
		}
		return nil, nil
	})
	if e != nil {
		return result, fault("local_deletion_failed")
	}
	result.LocalDeleted = true
	if revoke {
		result.Revocation = "unconfirmed"
		token := r.RefreshToken
		if token == "" {
			token = r.AccessToken
		}
		if token != "" {
			status, _, err := b.request(ctx, http.MethodPost, "https://oauth2.googleapis.com/revoke", "", map[string]string{"token": token}, false)
			if err == nil && status == 200 {
				result.Revocation = "confirmed"
			}
		}
	}
	return result, nil
}
func hasScopes(raw string) bool {
	actual := strings.Fields(raw)
	expected := Scopes()
	if len(actual) != len(expected) {
		return false
	}
	for _, s := range expected {
		if !slices.Contains(actual, s) {
			return false
		}
	}
	return true
}
