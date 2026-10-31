package appserver

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"net"
	"net/http"
	"sync"
	"sync/atomic"
	"time"

	"github.com/blueberrycongee/wuu/internal/access"
	"github.com/blueberrycongee/wuu/internal/executionauth"
	"github.com/blueberrycongee/wuu/internal/pluginhost"
	"github.com/blueberrycongee/wuu/internal/principal"
	"github.com/blueberrycongee/wuu/internal/runtime"
)

const DesktopSessionTransportModule = "wuu.desktop-session-transport"
const desktopSessionDispatch = "session/transport.dispatch"
const desktopSessionTargets = "session/transport.list-targets"
const desktopSessionRelease = "session/transport.release"

type desktopSessionTransportBridge struct {
	mu      sync.Mutex
	servers map[*Server]bool
}

func newDesktopSessionTransportBridge() *desktopSessionTransportBridge {
	return &desktopSessionTransportBridge{servers: make(map[*Server]bool)}
}
func (b *desktopSessionTransportBridge) bind(s *Server) {
	b.mu.Lock()
	b.servers[s] = true
	b.mu.Unlock()
	s.desktopSessionTransport = b
}
func (b *desktopSessionTransportBridge) unbind(s *Server) {
	b.mu.Lock()
	delete(b.servers, s)
	b.mu.Unlock()
}
func (b *desktopSessionTransportBridge) connection(method string) (*Server, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	var selected *Server
	for s := range b.servers {
		if !s.closed.Load() && s.trustedTransport.enabled && s.ConnectionPrincipal().Identity() == principal.LocalOwner().Identity() && s.ConnectionPrincipal().IsOwner() && s.supportsClientMethod(method) {
			if selected != nil {
				return nil, errors.New("registered desktop session transport is ambiguous")
			}
			selected = s
		}
	}
	if selected != nil {
		return selected, nil
	}
	return nil, errors.New("registered desktop session transport is unavailable")
}
func (b *desktopSessionTransportBridge) factory() runtime.NativeProviderFactory {
	return runtime.NativeProviderFactory{ID: DesktopSessionTransportModule, Build: func(input runtime.NativeProviderContext) (pluginhost.Client, error) {
		p := &desktopSessionTransport{bridge: b, registry: input.Registry, validators: make(map[string]*desktopSessionValidator)}
		_, err := input.Registry.RegisterRetirementCleanup(p.ID(), p.Close)
		return p, err
	}}
}

type desktopSessionValidator struct {
	peer      trustedTransportEnvelope
	server    *Server
	group     string
	state     *runtime.State
	principal principal.Verified
	expires   *time.Timer
}
type desktopSessionOrigin struct {
	server  *Server
	expires time.Time
}
type desktopSessionTransport struct {
	origins    map[string]desktopSessionOrigin
	bridge     *desktopSessionTransportBridge
	registry   *pluginhost.ServiceRegistry
	active     atomic.Bool
	checks     sync.WaitGroup
	mu         sync.Mutex
	closed     bool
	listener   net.Listener
	http       *http.Server
	validators map[string]*desktopSessionValidator
}

func (*desktopSessionTransport) RequiredServices() []pluginhost.ServiceRequirement { return nil }

var _ pluginhost.ServiceClient = (*desktopSessionTransport)(nil)

func (*desktopSessionTransport) ID() string { return DesktopSessionTransportModule }
func (p *desktopSessionTransport) Status() pluginhost.Status {
	state := pluginhost.StatePrepared
	if p.active.Load() {
		state = pluginhost.StateActive
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.closed {
		state = pluginhost.StateStopped
	}
	return pluginhost.Status{ID: p.ID(), State: state}
}
func (p *desktopSessionTransport) Activate(context.Context) error {
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.closed {
		return errors.New("desktop session transport stopped")
	}
	p.active.Store(true)
	return nil
}
func (*desktopSessionTransport) ProvidedServices() []pluginhost.ServiceDescriptor {
	return []pluginhost.ServiceDescriptor{{Name: pluginhost.SessionTransportService, Version: "1.0.0", Methods: []pluginhost.ServiceMethodDescriptor{{Name: "dispatch", InputSchema: "session.transport.dispatch.input.v1", OutputSchema: "session.transport.dispatch.output.v1"}, {Name: "list-targets", InputSchema: "session.transport.targets.input.v1", OutputSchema: "session.transport.targets.output.v1"}}}}
}
func (p *desktopSessionTransport) InvokeService(ctx context.Context, call pluginhost.ServiceInvokeParams) (json.RawMessage, error) {
	if !p.active.Load() {
		return nil, errors.New("desktop session transport is not active")
	}
	if _, ok := principal.FromContext(ctx); !ok && call.CallerScope != nil && call.CallerScope.Context != nil {
		ctx = call.CallerScope.Context
	}
	caller, ok := principal.FromContext(ctx)
	if !ok || !caller.Valid() {
		return nil, errors.New("authenticated source principal required")
	}
	if err := caller.Check(ctx); err != nil {
		return nil, err
	}
	method := desktopSessionTargets
	var route pluginhost.SessionRouteRequest
	key := caller.Identity()
	proof := access.DelegationFromContext(ctx)
	if call.Method == "dispatch" {
		if proof == nil {
			return nil, errors.New("bounded source execution grant required")
		}
		if err := json.Unmarshal(call.Params, &route); err != nil {
			return nil, err
		}
		if err := route.Validate(); err != nil {
			return nil, err
		}
		if route.Locator.ExecutionHost == "local" || (route.Operation != "send" && route.Operation != "inspect" && route.Operation != "cancel") {
			return nil, errors.New("desktop transport requires an exact registered existing-session operation")
		}
		method = desktopSessionDispatch
	} else if call.Method != "list-targets" {
		return nil, errors.New("unknown desktop session transport method")
	}
	s, err := p.bridge.connection(method)
	if err != nil {
		return nil, &pluginhost.HostServiceError{Code: "session_host_unavailable", Message: err.Error()}
	}
	// Each action keeps its own live grant check. An inspect callback must never
	// replace the send authority retained by an already queued receiver turn.
	cacheKey, _ := json.Marshal([]any{key, proof, route.Operation, route.Locator})
	group, _ := json.Marshal([]any{key, proof, route.Locator})
	peer, err := p.validator(ctx, caller, string(cacheKey), string(group), s, call.Method == "dispatch")
	if err != nil {
		return nil, err
	}
	payload := map[string]any{"version": 1, "peer": peer}
	if call.Method == "dispatch" {
		payload["request"] = route
		var scope executionauth.Scope
		if json.Unmarshal(proof.Scope, &scope) == nil && scope.Kind == "exact-run" {
			payload["controller"] = ownerLocalController{Consumer: scope.Consumer, UserSpaceID: scope.UserSpaceID, ProductDomain: scope.ProductDomain, SessionID: scope.SessionID, RunID: scope.RunID}
		}

	}
	encoded, err := json.Marshal(payload)
	if err != nil {
		return nil, err
	}
	delivery := desktopSessionDelivery{server: s, method: method, digest: sha256.Sum256(encoded), check: func() error { return caller.Check(ctx) }}
	result, err := s.callClient(context.WithValue(ctx, desktopSessionDeliveryKey{}, delivery), method, payload)
	if call.Method == "list-targets" {
		p.releaseGroup(ctx, string(group))
	}
	if err == nil && call.Method == "dispatch" {
		var receipt pluginhost.SessionRouteResult
		if json.Unmarshal(result, &receipt) != nil || receipt.Version != 1 || receipt.OperationID != route.OperationID || receipt.Locator.Version != 1 || receipt.Locator.ExecutionHost != route.Locator.ExecutionHost || receipt.Locator.SessionID != route.Locator.SessionID || (receipt.Status != "completed" && receipt.Status != "pending" && receipt.Status != "unknown") {
			err = errors.New("invalid registered session receipt")
		} else if registeredSessionTerminal(route, receipt) {
			p.releaseGroup(ctx, string(group))
		}
	}
	if err != nil && call.Method == "dispatch" {
		return json.Marshal(pluginhost.SessionRouteResult{Version: 1, OperationID: route.OperationID, Locator: route.Locator, Status: "unknown", Error: &pluginhost.SessionRouteError{Code: "session_delivery_unknown", Message: "Delivery may have been accepted; reconcile the same operation ID"}})
	}
	return result, err
}
func (p *desktopSessionTransport) validator(ctx context.Context, caller principal.Verified, key, group string, server *Server, pinOrigin bool) (trustedTransportEnvelope, error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.closed {
		return trustedTransportEnvelope{}, errors.New("desktop session transport stopped")
	}
	if pinOrigin {
		if p.origins == nil {
			p.origins = make(map[string]desktopSessionOrigin)
		}
		for existing, origin := range p.origins {
			if !origin.expires.IsZero() && time.Now().After(origin.expires) {
				delete(p.origins, existing)
			}
		}
		if origin, ok := p.origins[group]; ok && origin.server != server {
			return trustedTransportEnvelope{}, errors.New("source run belongs to another transport connection")
		}
		if _, ok := p.origins[group]; !ok && len(p.origins) >= 256 {
			return trustedTransportEnvelope{}, errors.New("source session transport run capacity exhausted")
		}
	}
	if v := p.validators[key]; v != nil {
		if v.server != server {
			return trustedTransportEnvelope{}, errors.New("source session transport connection changed; reconcile existing delivery")
		}
		return v.peer, nil
	}
	if len(p.validators) >= 256 {
		return trustedTransportEnvelope{}, errors.New("source session transport capacity exhausted")
	}
	state, err := runtime.RetainProductInvocationState(ctx)
	if err != nil {
		return trustedTransportEnvelope{}, err
	}
	if p.http == nil {
		listener, err := net.Listen("tcp", "127.0.0.1:0")
		if err != nil {
			_ = state.Close()
			return trustedTransportEnvelope{}, err
		}
		p.listener = listener
		p.http = &http.Server{Handler: http.HandlerFunc(p.validate), ReadHeaderTimeout: 5 * time.Second}
		go func() { _ = p.http.Serve(listener) }()
	}
	bytes := make([]byte, 32)
	if _, err := rand.Read(bytes); err != nil {
		_ = state.Close()
		return trustedTransportEnvelope{}, err
	}
	token := hex.EncodeToString(bytes)
	peer := trustedTransportEnvelope{Version: 1, PeerID: "session-route-" + token, Principal: caller.Identity(), Owner: false, Validation: trustedTransportValidator{Address: p.listener.Addr().String(), Token: token}}
	v := &desktopSessionValidator{peer: peer, state: state, principal: caller, server: server, group: group}
	p.validators[key] = v
	if pinOrigin {
		p.origins[group] = desktopSessionOrigin{server: server, expires: caller.ExpiresAt()}
	}
	if expiry := caller.ExpiresAt(); !expiry.IsZero() {
		v.expires = time.AfterFunc(time.Until(expiry), func() { p.expire(context.Background(), key, v) })
	}
	return peer, nil
}
func (p *desktopSessionTransport) validate(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost || r.URL.Path != "/v1/validate" {
		http.NotFound(w, r)
		return
	}
	p.mu.Lock()
	var state *runtime.State
	var caller principal.Verified
	var owned *desktopSessionValidator
	var ownedKey string
	if !p.closed {
		for key, v := range p.validators {
			if r.Header.Get("Authorization") == "Bearer "+v.peer.Validation.Token {
				state = v.state.Retain()
				caller = v.principal
				owned = v
				ownedKey = key
				if state != nil {
					p.checks.Add(1)
				}
				break
			}
		}
	}
	p.mu.Unlock()
	if state == nil {
		http.Error(w, "expired", http.StatusUnauthorized)
		return
	}
	defer state.Close()
	defer p.checks.Done()
	ctx, cancel := context.WithTimeout(runtime.WithInvocationState(r.Context(), state), 5*time.Second)
	defer cancel()
	if caller.Check(ctx) != nil {
		go p.expire(context.Background(), ownedKey, owned)
		http.Error(w, "revoked", http.StatusUnauthorized)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}
func (p *desktopSessionTransport) expire(ctx context.Context, key string, v *desktopSessionValidator) {
	p.mu.Lock()
	if p.validators[key] != v {
		p.mu.Unlock()
		return
	}
	delete(p.validators, key)
	p.mu.Unlock()
	p.release(ctx, v)
}
func (p *desktopSessionTransport) releaseGroup(ctx context.Context, group string) {
	p.mu.Lock()
	var owned []struct {
		key string
		v   *desktopSessionValidator
	}
	for key, v := range p.validators {
		if v.group == group {
			owned = append(owned, struct {
				key string
				v   *desktopSessionValidator
			}{key, v})
		}
	}
	p.mu.Unlock()
	for _, entry := range owned {
		p.expire(ctx, entry.key, entry.v)
	}
}
func (p *desktopSessionTransport) release(ctx context.Context, v *desktopSessionValidator) {
	if v.expires != nil {
		v.expires.Stop()
	}
	if v.server != nil && !v.server.closed.Load() {
		releaseCtx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		_, _ = v.server.callClient(releaseCtx, desktopSessionRelease, map[string]any{"peer_id": v.peer.PeerID})
		cancel()
	}
	if !pluginhost.DeferServiceCompletion(ctx, v.state.Close) {
		_ = v.state.Close()
	}
}
func (p *desktopSessionTransport) Close(ctx context.Context) error {
	p.mu.Lock()
	if p.closed {
		p.mu.Unlock()
		return nil
	}
	p.closed = true
	p.active.Store(false)
	server := p.http
	values := p.validators
	p.validators = nil
	p.origins = nil
	p.mu.Unlock()
	if server != nil {
		_ = server.Close()
	}
	p.checks.Wait()
	var released sync.WaitGroup
	for _, v := range values {
		released.Add(1)
		go func(v *desktopSessionValidator) { defer released.Done(); p.release(ctx, v) }(v)
	}
	released.Wait()
	return nil
}

// This process-local delivery capability is issued only by the selected native
// transport after source authorization. It binds the complete reverse envelope;
// receiver authorization remains independent and never inherits pipe ownership.
type desktopSessionDeliveryKey struct{}
type desktopSessionDelivery struct {
	server *Server
	method string
	digest [32]byte
	check  func() error
}

func (s *Server) authorizeDesktopSessionDelivery(ctx context.Context, request Request) (func() error, bool) {
	delivery, ok := ctx.Value(desktopSessionDeliveryKey{}).(desktopSessionDelivery)
	if !ok || delivery.server != s || delivery.method != request.Method || (request.Method != desktopSessionDispatch && request.Method != desktopSessionTargets) || delivery.digest != sha256.Sum256(request.Params) || delivery.check == nil {
		return nil, false
	}
	return delivery.check, true
}

func registeredSessionTerminal(route pluginhost.SessionRouteRequest, receipt pluginhost.SessionRouteResult) bool {
	if route.Operation != "inspect" || receipt.Status != "completed" || receipt.Error != nil || receipt.Version != 1 || receipt.OperationID != route.OperationID || receipt.Locator.Version != 1 || receipt.Locator.ExecutionHost != route.Locator.ExecutionHost || receipt.Locator.SessionID != route.Locator.SessionID {
		return false
	}
	var params pluginhost.SessionInspectParams
	var inspection pluginhost.SessionInspectResult
	return json.Unmarshal(route.Params, &params) == nil && params.RequestID != "" && json.Unmarshal(receipt.Result, &inspection) == nil && inspection.Session.SessionID == route.Locator.SessionID && inspection.Turn != nil && inspection.Turn.RequestID == params.RequestID && (terminalPluginTurnState(inspection.Turn.State) || inspection.Turn.State == "cancelled")
}
