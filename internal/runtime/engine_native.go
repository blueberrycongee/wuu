package runtime

import (
	"context"
	"crypto/rand"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"sync"
	"time"

	"github.com/blueberrycongee/wuu/internal/agentengine"
	"github.com/blueberrycongee/wuu/internal/claudeengine"
	"github.com/blueberrycongee/wuu/internal/codexengine"
	"github.com/blueberrycongee/wuu/internal/config"
	"github.com/blueberrycongee/wuu/internal/credentialstore"
	"github.com/blueberrycongee/wuu/internal/enginecatalog"
	"github.com/blueberrycongee/wuu/internal/externalengine"
	"github.com/blueberrycongee/wuu/internal/loopdriver"
	"github.com/blueberrycongee/wuu/internal/modelwire"
	"github.com/blueberrycongee/wuu/internal/pluginhost"
	"github.com/blueberrycongee/wuu/internal/pluginprotocol"
	"github.com/blueberrycongee/wuu/internal/providers"
)

const nativeEnginesModule = "wuu.engines.native"
const engineExecutionConsumer = "wuu.engine-execution"

type nativeEngineEntry struct {
	retain  func() func()
	factory agentengine.Factory
	admin   *nativeEngineAdmin
	release func()
	refs    int
	enabled bool
	key     string
}
type nativeEngineSession struct {
	session   agentengine.Session
	entry     *nativeEngineEntry
	callbacks *nativeEngineCallbacks
	owner     string
	engine    string
}
type nativeEngineProvider struct {
	mu          sync.Mutex
	state       pluginhost.State
	registry    *pluginhost.ServiceRegistry
	root        string
	config      *config.Client
	credentials credentialstore.Store
	entries     map[string]*nativeEngineEntry
	sessions    map[string]*nativeEngineSession
	leases      map[string]*nativeEngineSession
}

func nativeEngineProviderFactory() NativeProviderFactory {
	return NativeProviderFactory{ID: nativeEnginesModule, DefaultEnabled: true, Build: func(input NativeProviderContext) (pluginhost.Client, error) {
		p := &nativeEngineProvider{state: pluginhost.StatePrepared, registry: input.Registry, root: input.RootDir, entries: map[string]*nativeEngineEntry{}, sessions: map[string]*nativeEngineSession{}, leases: map[string]*nativeEngineSession{}}
		call := func(service string) func(context.Context, string, json.RawMessage) (json.RawMessage, error) {
			return func(ctx context.Context, method string, raw json.RawMessage) (json.RawMessage, error) {
				return p.call(ctx, service, method, raw)
			}
		}
		p.config = config.NewClient(call(config.ServiceName))
		p.credentials = credentialstore.NewClient(call(credentialstore.ServiceName))
		return p, nil
	}}
}
func (p *nativeEngineProvider) ID() string { return nativeEnginesModule }
func (p *nativeEngineProvider) Status() pluginhost.Status {
	p.mu.Lock()
	defer p.mu.Unlock()
	return pluginhost.Status{ID: p.ID(), State: p.state}
}
func (p *nativeEngineProvider) Prepare(ctx context.Context) error { return ctx.Err() }
func (p *nativeEngineProvider) Activate(ctx context.Context) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	p.mu.Lock()
	p.state = pluginhost.StateActive
	p.mu.Unlock()
	return nil
}
func engineServiceDescriptor(id string) pluginhost.ServiceDescriptor {
	methods := []pluginhost.ServiceMethodDescriptor{}
	for _, name := range []string{"descriptor", "retain", "release", "bind", "run", "interrupt", "steer", "close", "refresh", "describe", "models", "auth", "quota", "history_fork"} {
		methods = append(methods, pluginhost.ServiceMethodDescriptor{Name: name, InputSchema: "engine." + strings.ReplaceAll(name, "_", "-") + ".input.v1", OutputSchema: "engine." + strings.ReplaceAll(name, "_", "-") + ".output.v1", Cleanup: name == "close" || name == "release"})
	}
	return pluginhost.ServiceDescriptor{Name: "engine." + id, Version: "1.0.0", Methods: methods}
}
func (p *nativeEngineProvider) ProvidedServices() []pluginhost.ServiceDescriptor {
	result := []pluginhost.ServiceDescriptor{engineServiceDescriptor("wuu")}
	for _, entry := range enginecatalog.Entries() {
		result = append(result, engineServiceDescriptor(entry.ID))
	}
	return result
}
func (p *nativeEngineProvider) RequiredServices() []pluginhost.ServiceRequirement {
	return []pluginhost.ServiceRequirement{{Name: config.ServiceName, MajorVersion: 1, Required: true}, {Name: credentialstore.ServiceName, MajorVersion: 1, Required: true}, {Name: pluginprotocol.EngineGatewayService, MajorVersion: 1, Required: true}}
}
func (p *nativeEngineProvider) call(ctx context.Context, service, method string, raw json.RawMessage) (json.RawMessage, error) {
	result, err := p.registry.RouteServiceCall(ctx, p.ID(), pluginhost.ServiceCallParams{Service: service, Method: method, Params: raw})
	if err != nil {
		return nil, err
	}
	return result, nil
}
func (p *nativeEngineProvider) retainEntry(ctx context.Context, id string, refresh bool) (*nativeEngineEntry, error) {
	view, err := p.config.Read(ctx)
	if err != nil {
		return nil, err
	}
	cfg := view.Config.Engines
	binary := ""
	enabled := true
	if id != "wuu" {
		catalog, ok := enginecatalog.Lookup(id)
		if !ok {
			return nil, fmt.Errorf("unknown native engine %q", id)
		}
		setting := cfg.Binary(id)
		override := ""
		if setting != nil {
			override = setting.BinaryPath
		}
		var resolveErr error
		if id == "codex" && override == "" {
			binary, resolveErr = codexengine.ResolveBinary()
		} else if id == "claude" && override == "" {
			binary, resolveErr = claudeengine.ResolveBinary()
		} else {
			binary, resolveErr = catalog.Resolve(override)
		}
		enabled = resolveErr == nil
		if setting != nil {
			if choice, explicit := setting.EngineEnabled(); explicit {
				enabled = choice
			}
			if override != "" {
				binary = override
			}
		}
	}
	key := fmt.Sprintf("%t:%s", enabled, binary)
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.state != pluginhost.StateActive {
		return nil, errors.New("native engine provider is unavailable")
	}
	entry := p.entries[id]
	if entry == nil || entry.key != key || refresh {
		var factory agentengine.Factory
		retain := func() func() { return func() {} }
		switch id {
		case "wuu":
			factory = &nativeWuuEngineFactory{}
		case "codex":
			host := codexengine.NewHost(binary, p.root)
			retain = host.Retain
			factory = codexengine.NewEngine(host)
		case "claude":
			factory = claudeengine.NewEngine(binary, p.root)
		default:
			catalog, _ := enginecatalog.Lookup(id)
			factory = externalengine.New(catalog, binary, p.root)
		}
		admin := newNativeEngineAdmin(factory, agentengine.EngineID(id), binary, p.root, p.credentials)
		entry = &nativeEngineEntry{factory: factory, admin: admin, retain: retain, enabled: enabled, key: key}
		entry.admin.disabled = !enabled
		p.entries[id] = entry
	}
	if entry.refs == 0 {
		entry.release = entry.retain()
	}
	entry.refs++
	return entry, nil
}
func (p *nativeEngineProvider) releaseEntry(entry *nativeEngineEntry) {
	p.mu.Lock()
	entry.refs--
	var release func()
	if entry.refs == 0 {
		release = entry.release
		entry.release = nil
	}
	p.mu.Unlock()
	if release != nil {
		release()
	}
}
func (p *nativeEngineProvider) Close(ctx context.Context) error {
	p.mu.Lock()
	p.state = pluginhost.StateStopped
	sessions := p.sessions
	p.sessions = map[string]*nativeEngineSession{}
	leases := p.leases
	p.leases = map[string]*nativeEngineSession{}
	p.mu.Unlock()
	var result error
	for _, lease := range leases {
		p.releaseEntry(lease.entry)
	}
	for _, s := range sessions {
		result = errors.Join(result, s.session.Close(ctx))
		p.releaseEntry(s.entry)
	}
	return result
}
func (p *nativeEngineProvider) InvokeService(ctx context.Context, in pluginhost.ServiceInvokeParams) (json.RawMessage, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	id := strings.TrimPrefix(in.Service, "engine.")
	if in.Method == "release" {
		var input pluginprotocol.EngineSessionParams
		if err := decodeServiceParams(in.Params, &input); err != nil {
			return nil, err
		}
		p.mu.Lock()
		lease := p.leases[input.SessionID]
		if lease != nil && (lease.owner != in.Caller || lease.engine != id) {
			lease = nil
		}
		if lease != nil {
			delete(p.leases, input.SessionID)
		}
		p.mu.Unlock()
		if lease == nil {
			return nil, errors.New("engine scope lease is unavailable")
		}
		p.releaseEntry(lease.entry)
		return json.RawMessage(`{}`), nil
	}
	switch in.Method {
	case "retain", "descriptor", "refresh", "describe", "models", "auth", "quota", "history_fork", "bind":
		entry, err := p.retainEntry(ctx, id, in.Method == "refresh")
		if err != nil {
			return nil, err
		}
		retained := false
		defer func() {
			if !retained {
				p.releaseEntry(entry)
			}
		}()
		if !entry.enabled && in.Method != "descriptor" && in.Method != "describe" && in.Method != "refresh" {
			return nil, errors.New("selected engine is disabled or unavailable")
		}
		if in.Method == "retain" {
			leaseID := rand.Text()
			p.mu.Lock()
			p.leases[leaseID] = &nativeEngineSession{entry: entry, owner: in.Caller, engine: id}
			p.mu.Unlock()
			retained = true
			return json.Marshal(pluginprotocol.EngineSessionParams{SessionID: leaseID})
		}
		if in.Method == "refresh" {
			return json.RawMessage(`{}`), nil
		}
		if in.Method == "descriptor" {
			descriptor, err := entry.factory.Descriptor(ctx)
			if err != nil {
				return nil, err
			}
			descriptor.Capabilities = append(descriptor.Capabilities, "warm-scope")
			if id == "claude" || id == "grok" {
				descriptor.Capabilities = append(descriptor.Capabilities, "account-quota")
			}
			return json.Marshal(struct {
				ID           agentengine.EngineID `json:"id"`
				Version      string               `json:"version"`
				Capabilities []string             `json:"capabilities"`
				Enabled      bool                 `json:"enabled"`
			}{descriptor.ID, descriptor.Version, descriptor.Capabilities, entry.enabled})
		}
		if in.Method != "bind" {
			raw, err := entry.admin.Invoke(ctx, in.Method, in.Params)
			if err == nil && in.Method == "describe" {
				var view map[string]any
				if err = json.Unmarshal(raw, &view); err == nil {
					view["enabled"] = entry.enabled
					raw, err = json.Marshal(view)
				}
			}
			return raw, err
		}
		if !entry.enabled {
			return nil, errors.New("selected engine is disabled or unavailable")
		}
		var input pluginprotocol.EngineBinding
		if err := decodeServiceParams(in.Params, &input); err != nil {
			return nil, err
		}
		callbacks := &nativeEngineCallbacks{provider: p, gateway: input.GatewayID, ctx: ctx}
		binding := callbacks.binding(input)
		var session agentengine.Session
		if _, ok := entry.factory.(*nativeWuuEngineFactory); ok {
			session = &nativeWuuEngineSession{callbacks: callbacks}
		} else if factory, ok := entry.factory.(agentengine.ThreadBoundFactory); ok {
			session, err = factory.SessionForThread(ctx, binding)
		} else {
			session, err = entry.factory.Resume(ctx, agentengine.ResumeRequest{OpenRequest: agentengine.OpenRequest{ThreadID: input.ThreadID, SessionID: input.ThreadID, RootDir: input.RootDir}, ExternalSessionRef: input.ExternalRef})
		}
		if err != nil {
			return nil, err
		}
		sessionID := rand.Text()
		p.mu.Lock()
		p.sessions[sessionID] = &nativeEngineSession{session: session, entry: entry, callbacks: callbacks, owner: in.Caller, engine: id}
		p.mu.Unlock()
		retained = true
		return json.Marshal(pluginprotocol.EngineSessionParams{SessionID: sessionID})
	default:
		var scope pluginprotocol.EngineSessionParams
		if err := json.Unmarshal(in.Params, &scope); err != nil {
			return nil, err
		}
		p.mu.Lock()
		s := p.sessions[scope.SessionID]
		if s != nil && (s.owner != in.Caller || s.engine != id) {
			s = nil
		}
		if in.Method == "close" && s != nil {
			delete(p.sessions, scope.SessionID)
		}
		p.mu.Unlock()
		if s == nil {
			return nil, errors.New("engine session is unavailable to this caller")
		}
		switch in.Method {
		case "run":
			var input pluginprotocol.EngineRunParams
			if err := decodeServiceParams(in.Params, &input); err != nil {
				return nil, err
			}
			s.callbacks.setContext(ctx)
			result, runErr := s.session.RunTurn(ctx, agentengine.TurnInput{History: modelwire.DriverMessagesFromWire(input.Messages)}, func(event providers.StreamEvent) {
				if err := s.callbacks.event(ctx, event); err != nil {
					s.callbacks.setError(err)
				}
			})
			runErr = errors.Join(runErr, s.callbacks.takeError())
			return json.Marshal(pluginprotocol.EngineRunResult{Result: engineResultToWire(result.Result), Failure: loopdriver.FailureToWire(runErr)})
		case "interrupt":
			var input pluginprotocol.EngineInterruptParams
			if err := decodeServiceParams(in.Params, &input); err != nil {
				return nil, err
			}
			if err := s.session.Interrupt(ctx, input.Reason); err != nil {
				return nil, err
			}
			return json.RawMessage(`{}`), nil
		case "steer":
			var input pluginprotocol.EngineSteerParams
			if err := decodeServiceParams(in.Params, &input); err != nil {
				return nil, err
			}
			steering, ok := s.session.(agentengine.SteeringSession)
			if !ok {
				return nil, agentengine.ErrSteerUnsupported
			}
			messages := modelwire.DriverMessagesFromWire([]pluginprotocol.DriverMessageV1{input.Message})
			ref, err := steering.Steer(ctx, messages[0])
			if err != nil {
				return nil, err
			}
			return json.Marshal(map[string]string{"turn_id": ref})
		case "close":
			err := s.session.Close(ctx)
			p.releaseEntry(s.entry)
			if err != nil {
				return nil, err
			}
			return json.RawMessage(`{}`), nil
		default:
			return nil, errors.New("unknown engine operation")
		}
	}
}

type nativeEngineCallbacks struct {
	provider *nativeEngineProvider
	gateway  string
	mu       sync.Mutex
	ctx      context.Context
	err      error
}

func (c *nativeEngineCallbacks) setContext(ctx context.Context) {
	c.mu.Lock()
	c.ctx = ctx
	c.err = nil
	c.mu.Unlock()
}
func (c *nativeEngineCallbacks) setError(err error) {
	c.mu.Lock()
	c.err = errors.Join(c.err, err)
	c.mu.Unlock()
}
func (c *nativeEngineCallbacks) takeError() error { c.mu.Lock(); defer c.mu.Unlock(); return c.err }
func (c *nativeEngineCallbacks) context() context.Context {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.ctx
}
func (c *nativeEngineCallbacks) call(ctx context.Context, method string, input pluginprotocol.EngineGatewayParams, out any) error {
	input.GatewayID = c.gateway
	raw, err := json.Marshal(input)
	if err != nil {
		return err
	}
	raw, err = c.provider.call(ctx, pluginprotocol.EngineGatewayService, method, raw)
	if err != nil {
		return err
	}
	if out != nil {
		return json.Unmarshal(raw, out)
	}
	return nil
}
func (c *nativeEngineCallbacks) event(ctx context.Context, event providers.StreamEvent) error {
	wire := engineEventToWire(event)
	return c.call(ctx, "event", pluginprotocol.EngineGatewayParams{Event: &wire}, nil)
}
func (c *nativeEngineCallbacks) binding(in pluginprotocol.EngineBinding) agentengine.ThreadBinding {
	result := agentengine.ThreadBinding{ThreadID: in.ThreadID, RootDir: in.RootDir, Model: in.Model, Effort: in.Effort, Speed: in.Speed, ModelOptions: in.ModelOptions, PermissionMode: in.PermissionMode, Instructions: in.Instructions, ExternalRef: in.ExternalRef}
	for _, server := range in.MCPServers {
		result.MCPServers = append(result.MCPServers, agentengine.MCPServer{Name: server.Name, URL: server.URL})
	}
	result.PersistRef = func(ref string) error {
		ctx := c.context()
		if ctx.Err() == nil {
			return c.call(ctx, "persist_reference", pluginprotocol.EngineGatewayParams{Reference: ref}, nil)
		}
		// A confirmed reference is receipt persistence, not new execution. Use a
		// separate bounded admission authenticated by the retained session token.
		cleanup, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		return c.call(cleanup, "settle_reference", pluginprotocol.EngineGatewayParams{Reference: ref}, nil)
	}
	result.PersistTurnRef = func(ref string, pending bool) error {
		if pending {
			return c.call(c.context(), "persist_turn", pluginprotocol.EngineGatewayParams{Reference: ref, Pending: true}, nil)
		}
		cleanup, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		return c.call(cleanup, "settle_turn", pluginprotocol.EngineGatewayParams{Reference: ref}, nil)
	}
	result.RequestApproval = func(ctx context.Context, request agentengine.ApprovalRequest) (agentengine.ApprovalDecision, error) {
		wire, err := engineApprovalToWire(request)
		if err != nil {
			return "", err
		}
		var out agentengine.ApprovalDecision
		err = c.call(ctx, "approval", pluginprotocol.EngineGatewayParams{Approval: &wire}, &out)
		return out, err
	}
	result.RequestUserInput = func(ctx context.Context, request agentengine.UserInputRequest) (map[string][]string, error) {
		wire := engineUserInputRequestToWire(request)
		var out map[string][]string
		err := c.call(ctx, "input", pluginprotocol.EngineGatewayParams{Input: &wire}, &out)
		return out, err
	}
	return result
}

type nativeWuuEngineFactory struct{}

func (*nativeWuuEngineFactory) Descriptor(context.Context) (agentengine.Descriptor, error) {
	return agentengine.Descriptor{ID: agentengine.EngineWuu, Version: "1", Capabilities: []string{"native-tool-loop", "native-session-resume", "canonical-history", "host-managed-continuation", "host-managed-steering", "active-turn-steering"}}, nil
}
func (*nativeWuuEngineFactory) Open(context.Context, agentengine.OpenRequest) (agentengine.Session, error) {
	return nil, errors.New("selected loop engine requires bound execution resources")
}
func (f *nativeWuuEngineFactory) Resume(ctx context.Context, in agentengine.ResumeRequest) (agentengine.Session, error) {
	return f.Open(ctx, in.OpenRequest)
}

type nativeWuuEngineSession struct {
	callbacks *nativeEngineCallbacks
	mu        sync.Mutex
	cancel    context.CancelFunc
	done      chan struct{}
	closed    bool
}

func (s *nativeWuuEngineSession) RunTurn(ctx context.Context, in agentengine.TurnInput, sink agentengine.EventSink) (agentengine.TurnResult, error) {
	ctx, cancel := context.WithCancel(ctx)
	s.mu.Lock()
	if s.closed || s.done != nil {
		s.mu.Unlock()
		cancel()
		return agentengine.TurnResult{}, errors.New("engine turn is already active")
	}
	s.cancel = cancel
	done := make(chan struct{})
	s.done = done
	s.mu.Unlock()
	defer func() {
		cancel()
		s.mu.Lock()
		s.cancel = nil
		s.done = nil
		close(done)
		s.mu.Unlock()
	}()
	var out pluginprotocol.EngineRunResult
	if err := s.callbacks.call(ctx, "run_loop", pluginprotocol.EngineGatewayParams{Messages: modelwire.DriverMessagesToWire(in.History)}, &out); err != nil {
		return agentengine.TurnResult{}, err
	}
	result, err := engineResultFromWire(out.Result)
	if err != nil {
		return agentengine.TurnResult{}, err
	}
	if out.Failure != nil {
		return agentengine.TurnResult{Result: result}, &loopdriver.FailureError{Failure: *out.Failure}
	}
	return agentengine.TurnResult{Result: result}, nil
}
func (s *nativeWuuEngineSession) Interrupt(context.Context, string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.cancel == nil {
		return agentengine.ErrNoActiveTurn
	}
	s.cancel()
	return nil
}
func (s *nativeWuuEngineSession) Close(ctx context.Context) error {
	s.mu.Lock()
	s.closed = true
	cancel, done := s.cancel, s.done
	s.mu.Unlock()
	if cancel != nil {
		cancel()
	}
	if done == nil {
		return nil
	}
	select {
	case <-done:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}
