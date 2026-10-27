package personalconnectors

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"github.com/blueberrycongee/wuu/internal/credentialstore"
	"io"
	"net/http"
	"net/url"
	"slices"
	"strings"
	"sync"
	"time"
)

const namespace = "personal.connectors.v1"

type Grant struct {
	ActivationID  string     `json:"activation_id,omitempty"`
	NonExpiring   bool       `json:"non_expiring,omitempty"`
	Connection    Connection `json:"connection"`
	AccessToken   string     `json:"access_token"`
	RefreshToken  string     `json:"refresh_token,omitempty"`
	OAuthProvider string     `json:"oauth_provider,omitempty"`
	RefreshLease  string     `json:"refresh_lease,omitempty"`
	ExpiresAt     time.Time  `json:"expires_at"`
}
type Options struct {
	StateDir       string
	HTTPSCallbacks HTTPSCallbackTransport
	Authorization  func(context.Context, Actor) (string, error)
	Store          credentialstore.Store
	HTTPClient     *http.Client
}
type Broker struct {
	stateDir       string
	httpsCallbacks HTTPSCallbackTransport
	mu             sync.Mutex
	activeMu       sync.Mutex
	oauthMu        sync.Mutex
	attempts       map[string]*oauthPending
	active         map[string]context.CancelFunc
	authorization  func(context.Context, Actor) (string, error)
	store          credentialstore.Store
	client         *http.Client
	lifetime       context.Context
	cancel         context.CancelFunc
}
type action struct {
	Result    Result  `json:"result"`
	Request   Request `json:"request"`
	Revision  string  `json:"revision"`
	Session   string  `json:"session"`
	Authority string  `json:"authority"`
}
type stored struct {
	Polls   map[string]pollState `json:"polls"`
	Epochs  map[string]string    `json:"epochs"`
	Grants  map[string]Grant     `json:"grants"`
	Actions map[string]action    `json:"actions"`
}

func New(o Options) *Broker {
	c := http.Client{Timeout: 20 * time.Second}
	if o.HTTPClient != nil {
		c = *o.HTTPClient
		c.Timeout = 20 * time.Second
	}
	c.CheckRedirect = func(*http.Request, []*http.Request) error { return fault("redirect_rejected") }
	ctx, cancel := context.WithCancel(context.Background())
	return &Broker{stateDir: o.StateDir, httpsCallbacks: o.HTTPSCallbacks, attempts: map[string]*oauthPending{}, active: map[string]context.CancelFunc{}, authorization: o.Authorization, store: o.Store, client: &c, lifetime: ctx, cancel: cancel}
}
func (b *Broker) SetAuthorization(f func(context.Context, Actor) (string, error)) {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.authorization = f
}
func (b *Broker) Close() error { b.cancel(); return nil }
func hash(s string) string     { v := sha256.Sum256([]byte(s)); return hex.EncodeToString(v[:]) }
func validateActor(a Actor) error {
	if a.ProductDomain != "personal" || a.UserSpaceID == "" || a.SessionID == "" || len(a.UserSpaceID) > 512 {
		return fault("personal_scope_required")
	}
	return nil
}
func (b *Broker) update(ctx context.Context, a Actor, f func(*stored) error) error {
	if e := validateActor(a); e != nil {
		return e
	}
	if ctx.Err() != nil || b.lifetime.Err() != nil {
		return fault("connector_closed")
	}
	if b.store == nil {
		return fault("secure_store_unavailable")
	}
	err := b.store.Update(ctx, namespace, hash(a.UserSpaceID), func(raw []byte) ([]byte, error) {
		s := stored{Polls: map[string]pollState{}, Epochs: map[string]string{}, Grants: map[string]Grant{}, Actions: map[string]action{}}
		if len(raw) > 0 {
			if json.Unmarshal(raw, &s) != nil || s.Grants == nil || s.Actions == nil {
				return nil, fault("state_corrupt")
			}
		}
		if s.Polls == nil {
			s.Polls = map[string]pollState{}
		}
		if s.Epochs == nil {
			s.Epochs = map[string]string{}
		}
		if e := f(&s); e != nil {
			return nil, e
		}
		return json.Marshal(s)
	})
	if err != nil {
		var failure *Failure
		if errors.As(err, &failure) {
			return err
		}
		return fault("secure_store_unavailable")
	}
	return nil
}

// RegisterVerified is only for a trusted host OAuth completion. Never expose it as plugin RPC.
func (b *Broker) RegisterVerified(ctx context.Context, a Actor, g Grant) error {
	b.mu.Lock()
	defer b.mu.Unlock()
	if g.Connection.ID == "" || len(g.Connection.ID) > 128 || g.Connection.AccountID == "" || g.AccessToken == "" || strings.ContainsAny(g.AccessToken, "\r\n") || len(g.AccessToken) > 8192 || (!g.NonExpiring && !g.ExpiresAt.After(time.Now())) || len(g.Connection.Resources) == 0 {
		return fault("invalid_grant")
	}
	if g.NonExpiring && !slices.Contains([]string{"notion", "github", "slack"}, g.Connection.Provider) {
		return fault("invalid_nonexpiring_grant")
	}
	if !slices.Contains([]string{"gmail", "google-calendar", "notion", "drive", "github", "slack", "feishu"}, g.Connection.Provider) {
		return fault("unsupported_provider")
	}
	if g.Connection.Domain != "personal" && g.Connection.Domain != "work" {
		return fault("invalid_domain")
	}
	if (g.Connection.Provider == "slack" || g.Connection.Provider == "feishu") && g.Connection.Domain != "work" {
		return fault("work_domain_required")
	}
	if g.Connection.WorkReadPolicy != "" && g.Connection.WorkReadPolicy != "confirm_each_request" && g.Connection.WorkReadPolicy != "selected_resources" {
		return fault("invalid_work_read_policy")
	}
	g.Connection.State = "connected"
	g.Connection.Revision = hash(time.Now().String() + g.Connection.ID)
	return b.update(ctx, a, func(s *stored) error { s.Grants[g.Connection.ID] = g; s.Epochs[g.Connection.ID] = nonce(); return nil })
}
func (b *Broker) List(ctx context.Context, a Actor) ([]Connection, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	out := []Connection{}
	e := b.update(ctx, a, func(s *stored) error {
		for _, g := range s.Grants {
			c := g.Connection
			if !b.activated(a, g) {
				c.State = "needs_reauth"
			}
			if (!g.NonExpiring && !g.ExpiresAt.After(time.Now().Add(30*time.Second))) && g.RefreshToken == "" {
				c.State = "needs_reauth"
			}
			out = append(out, c)
		}
		return nil
	})
	slices.SortFunc(out, func(a, b Connection) int { return strings.Compare(a.ID, b.ID) })
	return out, e
}
func build(c Connection, op string, args json.RawMessage) (Plan, error) {
	switch c.Provider {
	case "gmail":
		return buildGmail(c, op, args)
	case "google-calendar":
		return buildCalendar(c, op, args)
	case "notion":
		return buildNotion(c, op, args)
	case "drive":
		return buildDrive(c, op, args)
	case "github":
		return buildGitHub(c, op, args)
	case "slack":
		return buildSlack(c, op, args)
	case "feishu":
		return buildFeishu(c, op, args)
	}
	return Plan{}, fault("unsupported_provider")
}
func check(g Grant, p Plan) error {
	if g.Connection.State != "connected" || (!g.NonExpiring && !g.ExpiresAt.After(time.Now().Add(30*time.Second))) {
		return fault("authorization_expired")
	}
	for _, scope := range p.RequiredScopes {
		if !slices.Contains(g.Connection.Scopes, scope) {
			return fault("scope_required")
		}
	}
	if p.Resource != "" && !slices.Contains(g.Connection.Resources, p.Resource) {
		return fault("resource_not_selected")
	}
	if p.Resource == "" && p.Filter == nil {
		return fault("resource_scope_required")
	}
	return nil
}
func (b *Broker) Call(ctx context.Context, a Actor, r Request) (Result, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	if err := b.ensureFresh(ctx, a, r.ConnectionID); err != nil {
		return Result{}, err
	}
	var g Grant
	var result Result
	var p Plan
	authority := ""
	e := b.update(ctx, a, func(s *stored) error {
		var ok bool
		g, ok = s.Grants[r.ConnectionID]
		if !ok {
			return fault("account_not_connected")
		}
		var e error
		p, e = build(g.Connection, r.Operation, r.Arguments)
		if e != nil {
			return e
		}
		if e = check(g, p); e != nil {
			return e
		}
		if !p.Write && (g.Connection.Domain != "work" || g.Connection.WorkReadPolicy == "selected_resources") {
			return nil
		}
		if b.authorization == nil {
			return fault("authorization_context_unavailable")
		}
		authority, e = b.authorization(ctx, a)
		if e != nil || authority == "" {
			return fault("authorization_context_changed")
		}
		if r.RequestID == "" || len(r.RequestID) > 128 {
			return fault("request_id_required")
		}
		id := hash(a.UserSpaceID + "\x00" + a.SessionID + "\x00" + r.RequestID)
		raw, _ := json.Marshal(r)
		digest := hash(string(raw) + g.Connection.Revision + authority + a.SessionID)
		if old, ok := s.Actions[id]; ok {
			if old.Result.Digest != digest {
				return fault("request_id_conflict")
			}
			result = old.Result
			if result.State == "executing" {
				result.State = "uncertain"
			}
			return nil
		}
		for _, old := range s.Actions {
			if old.Session == a.SessionID && old.Request.ConnectionID == r.ConnectionID && old.Request.Operation == r.Operation && (old.Result.State == "uncertain" || old.Result.State == "executing") {
				var left, right any
				_ = json.Unmarshal(old.Request.Arguments, &left)
				_ = json.Unmarshal(r.Arguments, &right)
				l, _ := json.Marshal(left)
				rr, _ := json.Marshal(right)
				if string(l) == string(rr) {
					result = old.Result
					result.State = "uncertain"
					return nil
				}
			}
		}
		if len(s.Actions) >= 5000 {
			return fault("action_limit")
		}
		result = Result{State: "awaiting_confirmation", ActionID: id, Digest: digest, Summary: p.Summary, Arguments: r.Arguments, ArgumentsJSON: string(r.Arguments), AccountID: g.Connection.AccountID, Domain: g.Connection.Domain, Resource: p.Resource, ConnectionID: r.ConnectionID, Operation: r.Operation}
		s.Actions[id] = action{result, r, g.Connection.Revision, a.SessionID, authority}
		return nil
	})
	if e != nil {
		return Result{}, e
	}
	if p.Write || g.Connection.Domain == "work" && g.Connection.WorkReadPolicy != "selected_resources" {
		return result, nil
	}
	data, _, e := b.request(ctx, a, g, p)
	if e != nil {
		return Result{}, e
	}
	return Result{State: "completed", Data: data, ConnectionID: r.ConnectionID, Operation: r.Operation, Domain: g.Connection.Domain}, nil
}
func (b *Broker) Actions(ctx context.Context, a Actor) ([]Result, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	out := []Result{}
	e := b.update(ctx, a, func(s *stored) error {
		for _, v := range s.Actions {
			if !strings.HasPrefix(a.SessionID, "owner-ui:") && v.Session != a.SessionID {
				continue
			}
			r := v.Result
			if r.State == "executing" {
				r.State = "uncertain"
			}
			out = append(out, r)
		}
		return nil
	})
	slices.SortFunc(out, func(a, b Result) int { return strings.Compare(a.ActionID, b.ActionID) })
	return out, e
}

// Decide requires a separately owner-admitted host caller. The model path must never expose this method.
func (b *Broker) Decide(ctx context.Context, a Actor, id, digest string, approve bool) (Result, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	var v action
	var g Grant
	var p Plan
	execute := false
	var connectionID string
	var initial Result
	if err := b.update(ctx, a, func(s *stored) error {
		item, ok := s.Actions[id]
		if !ok || item.Result.Digest != digest {
			return fault("action_mismatch")
		}
		connectionID = item.Request.ConnectionID
		initial = item.Result
		return nil
	}); err != nil {
		return Result{}, err
	}
	if initial.State != "awaiting_confirmation" {
		if initial.State == "executing" {
			initial.State = "uncertain"
		}
		return initial, nil
	}
	if approve {
		if err := b.ensureFresh(ctx, a, connectionID); err != nil {
			return Result{}, err
		}
	}
	e := b.update(ctx, a, func(s *stored) error {
		var ok bool
		v, ok = s.Actions[id]
		if !ok || v.Result.Digest != digest {
			return fault("action_mismatch")
		}
		if v.Result.State != "awaiting_confirmation" {
			if v.Result.State == "executing" {
				v.Result.State = "uncertain"
			}
			return nil
		}
		if !approve {
			v.Result.State = "rejected"
			s.Actions[id] = v
			return nil
		}
		g, ok = s.Grants[v.Request.ConnectionID]
		if !ok || g.Connection.Revision != v.Revision {
			return fault("connection_changed")
		}
		if b.authorization == nil {
			return fault("authorization_context_unavailable")
		}
		current, e := b.authorization(ctx, Actor{a.ProductDomain, a.UserSpaceID, v.Session})
		if e != nil || current == "" || current != v.Authority {
			return fault("authorization_context_changed")
		}
		p, e = build(g.Connection, v.Request.Operation, v.Request.Arguments)
		if e != nil {
			return e
		}
		if e = check(g, p); e != nil {
			return e
		}
		if !p.Write && (g.Connection.Domain != "work" || g.Connection.WorkReadPolicy == "selected_resources") {
			return fault("action_mismatch")
		}
		v.Result.State = "executing"
		s.Actions[id] = v
		execute = true
		return nil
	})
	if e != nil {
		return Result{}, e
	}
	if !execute {
		return v.Result, nil
	}
	var data json.RawMessage
	uncertain := false
	var err error
	if len(p.Preflight) > 5 {
		err = fault("preflight_limit")
	}
	if err == nil {
		for _, read := range p.Preflight {
			if read.Write {
				err = fault("invalid_preflight")
				break
			}
			if err = check(g, read); err != nil {
				break
			}
			_, _, err = b.request(ctx, a, g, read)
			if err != nil {
				break
			}
		}
	}
	if err == nil {
		current, e := b.authorization(ctx, Actor{a.ProductDomain, a.UserSpaceID, v.Session})
		if e != nil || current != v.Authority {
			err = fault("authorization_context_changed")
		}
	}
	if err == nil {
		data, uncertain, err = b.request(ctx, a, g, p)
	}
	if err == nil {
		v.Result.Verification = "provider_accepted"
		if p.Verify != nil {
			var plans []Plan
			plans, err = p.Verify(data)
			if len(plans) > 100 {
				err = fault("verification_limit")
			}
			if err == nil {
				for _, read := range plans {
					if read.Write {
						err = fault("invalid_verification")
						break
					}
					if err = check(g, read); err != nil {
						break
					}
					_, _, err = b.request(ctx, a, g, read)
					if err != nil {
						break
					}
				}
			}
			if err != nil {
				uncertain = true
			} else {
				v.Result.Verification = "read_back_verified"
			}
		}
	}
	v.Result.State = "completed"
	v.Result.Data = data
	if err != nil {
		v.Result.Code = code(err)
		v.Result.State = "failed"
		if uncertain {
			v.Result.State = "uncertain"
		}
	}
	if e = b.update(context.WithoutCancel(ctx), a, func(s *stored) error { s.Actions[id] = v; return nil }); e != nil {
		return Result{State: "uncertain", ActionID: id, Digest: digest, Code: "receipt_persistence_failed"}, nil
	}
	return v.Result, nil
}
func (b *Broker) Disconnect(ctx context.Context, a Actor, id string) error {
	if err := validateActor(a); err != nil {
		return err
	}
	b.activeMu.Lock()
	if cancel := b.active[a.UserSpaceID+"\x00"+id]; cancel != nil {
		cancel()
	}
	b.activeMu.Unlock()
	b.oauthMu.Lock()
	pending := []*oauthPending{}
	for _, p := range b.attempts {
		if p.actor.UserSpaceID == a.UserSpaceID && p.request.ConnectionID == id {
			pending = append(pending, p)
		}
	}
	b.oauthMu.Unlock()
	for _, p := range pending {
		p.mu.Lock()
		if p.view.State == "pending" || p.view.State == "exchanging" {
			p.view.State = "cancelled"
			p.view.ErrorCode = "connection_disconnected"
			p.cancel()
		}
		p.mu.Unlock()
	}
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.update(ctx, a, func(s *stored) error { delete(s.Grants, id); s.Epochs[id] = nonce(); return nil })
}
func (b *Broker) request(ctx context.Context, a Actor, g Grant, p Plan) (json.RawMessage, bool, error) {
	u, e := url.Parse(p.URL)
	hosts := map[string][]string{"gmail": {"gmail.googleapis.com"}, "google-calendar": {"www.googleapis.com"}, "drive": {"www.googleapis.com", "docs.googleapis.com"}, "notion": {"api.notion.com"}, "github": {"api.github.com"}, "slack": {"slack.com"}, "feishu": {"open.feishu.cn", "open.larksuite.com"}}
	if e != nil || u.Scheme != "https" || u.User != nil || u.Fragment != "" || !slices.Contains(hosts[g.Connection.Provider], u.Host) {
		return nil, false, fault("endpoint_rejected")
	}
	if !slices.Contains([]string{"GET", "POST", "PATCH", "DELETE", "PUT"}, p.Method) {
		return nil, false, fault("method_rejected")
	}
	ctx, cancel := context.WithTimeout(ctx, 20*time.Second)
	defer cancel()
	activeKey := a.UserSpaceID + "\x00" + g.Connection.ID
	b.activeMu.Lock()
	b.active[activeKey] = cancel
	b.activeMu.Unlock()
	defer func() { b.activeMu.Lock(); delete(b.active, activeKey); b.activeMu.Unlock() }()
	if err := b.current(ctx, a, g); err != nil {
		return nil, false, err
	}
	stop := context.AfterFunc(b.lifetime, cancel)
	defer stop()
	if ctx.Err() != nil || b.lifetime.Err() != nil {
		return nil, false, fault("request_cancelled")
	}
	req, e := http.NewRequestWithContext(ctx, p.Method, p.URL, strings.NewReader(string(p.Body)))
	if e != nil {
		return nil, false, fault("invalid_request")
	}
	for k, v := range p.Headers {
		if strings.EqualFold(k, "Authorization") || strings.EqualFold(k, "Host") {
			return nil, false, fault("header_rejected")
		}
		req.Header.Set(k, v)
	}
	req.Header.Set("Authorization", "Bearer "+g.AccessToken)
	req.Header.Set("Accept", "application/json")
	if len(p.Body) > 0 {
		req.Header.Set("Content-Type", "application/json")
	}
	if e = b.current(ctx, a, g); e != nil {
		return nil, false, e
	}
	resp, e := b.client.Do(req)
	if e != nil {
		return nil, p.Write, fault("provider_unavailable")
	}
	defer resp.Body.Close()
	raw, e := io.ReadAll(io.LimitReader(resp.Body, 2*1024*1024+1))
	if e != nil || len(raw) > 2*1024*1024 {
		return nil, p.Write, fault("response_limit")
	}
	if e = b.current(ctx, a, g); e != nil {
		return nil, p.Write, e
	}
	if resp.StatusCode == 401 || resp.StatusCode == 403 {
		_ = b.update(context.WithoutCancel(ctx), a, func(s *stored) error {
			current, ok := s.Grants[g.Connection.ID]
			if ok && current.Connection.Revision == g.Connection.Revision {
				current.Connection.State = "needs_reauth"
				s.Grants[g.Connection.ID] = current
			}
			return nil
		})
		return nil, false, fault("authorization_rejected")
	}
	if !p.Write && p.AcceptGone && resp.StatusCode == http.StatusGone {
		return json.RawMessage(`{"deleted":true}`), false, nil
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		uncertain := p.Write && (resp.StatusCode >= 500 || resp.StatusCode == 408 || resp.StatusCode == 429)
		return nil, uncertain, fault("provider_rejected")
	}
	if p.Filter != nil {
		data, e := p.Filter(raw)
		if e != nil {
			return nil, p.Write, e
		}
		clean, e := sanitize(data, g.AccessToken)
		return clean, p.Write && e != nil, e
	}
	if len(raw) == 0 {
		return json.RawMessage(`{}`), false, nil
	}
	if p.ResponseKind == "pdf" && !strings.HasPrefix(string(raw), "%PDF-") {
		return nil, false, fault("invalid_pdf_response")
	}
	if p.ResponseKind == "binary" || p.ResponseKind == "pdf" {
		if bytes.Contains(raw, []byte(g.AccessToken)) {
			return nil, false, fault("credential_echo_rejected")
		}
		data, _ := json.Marshal(map[string]string{"encoding": "base64", "content": base64.StdEncoding.EncodeToString(raw), "content_type": resp.Header.Get("Content-Type")})
		return data, false, nil
	}
	if p.ResponseKind == "text" {
		data, _ := json.Marshal(map[string]string{"text": strings.ReplaceAll(string(raw), g.AccessToken, "[redacted]")})
		return data, false, nil
	}
	if !json.Valid(raw) {
		return nil, p.Write, fault("invalid_response")
	}
	clean, e := sanitize(raw, g.AccessToken)
	return clean, p.Write && e != nil, e
}

func (b *Broker) current(ctx context.Context, a Actor, g Grant) error {
	return b.update(ctx, a, func(s *stored) error {
		current, ok := s.Grants[g.Connection.ID]
		if !ok || current.Connection.Revision != g.Connection.Revision || current.Connection.State != "connected" || !b.activated(a, current) {
			return fault("connection_changed")
		}
		return nil
	})
}
func sanitize(raw []byte, token string) (json.RawMessage, error) {
	var value any
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.UseNumber()
	if decoder.Decode(&value) != nil {
		return nil, fault("invalid_response")
	}
	var clean func(any) any
	clean = func(v any) any {
		switch x := v.(type) {
		case map[string]any:
			for k, item := range x {
				key := strings.ToLower(k)
				if slices.Contains([]string{"access_token", "refresh_token", "id_token", "client_secret", "authorization", "password"}, key) {
					delete(x, k)
				} else {
					x[k] = clean(item)
				}
			}
			return x
		case []any:
			for i, item := range x {
				x[i] = clean(item)
			}
			return x
		case string:
			if token != "" {
				return strings.ReplaceAll(x, token, "[redacted]")
			}
			return x
		default:
			return v
		}
	}
	data, e := json.Marshal(clean(value))
	if e != nil {
		return nil, fault("invalid_response")
	}
	return data, nil
}
