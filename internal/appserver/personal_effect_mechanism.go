package appserver

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"

	"github.com/blueberrycongee/wuu/internal/access"
	"github.com/blueberrycongee/wuu/internal/approvefor"
	"github.com/blueberrycongee/wuu/internal/pluginhost"
	"github.com/blueberrycongee/wuu/internal/principal"
	"github.com/blueberrycongee/wuu/internal/providers"
	"github.com/blueberrycongee/wuu/internal/runtime"
	"github.com/blueberrycongee/wuu/internal/toolresult"
	"github.com/blueberrycongee/wuu/internal/tools"
)

const PersonalEffectService = "host.personal-effects"
const PersonalEffectModule = "wuu.personal-effects"

type PersonalPreparedFile struct {
	EffectID string           `json:"effect_id"`
	File     tools.FileAction `json:"file"`
}
type PersonalEffectReference struct {
	EffectID string `json:"effect_id"`
}
type PersonalEffectResult struct {
	ToolResult toolresult.Result `json:"tool_result"`
}

type personalEffectScopeKey struct{}
type personalEffectScope struct {
	bridge    *PersonalEffectBridge
	server    *Server
	state     *runtime.State
	kit       *tools.Toolkit
	authority context.Context
}

// PersonalEffectBridge owns host effect resources across compatible coordinator
// generations. It does not decide whether an effect should be approved. The
// runtime owner must Close it before disposing its retained service graphs.
type PersonalEffectBridge struct {
	mu        sync.Mutex
	resources map[string]*personalEffectResource
	closed    bool
	stopped   map[personalEffectOwner]bool
}

type personalEffectOwner struct {
	registry *pluginhost.ServiceRegistry
	owner    string
}

func NewPersonalEffectBridge() *PersonalEffectBridge {
	return &PersonalEffectBridge{resources: make(map[string]*personalEffectResource), stopped: make(map[personalEffectOwner]bool)}
}

// Context installs host-only provenance; wire params never select a server,
// toolkit, principal, or service graph. The state must remain live for the call.
func (b *PersonalEffectBridge) Context(ctx context.Context, server *Server, state *runtime.State, kit *tools.Toolkit) context.Context {
	return context.WithValue(ctx, personalEffectScopeKey{}, &personalEffectScope{bridge: b, server: server, state: state, kit: kit, authority: ctx})
}
func personalEffectScopeForCall(ctx context.Context, call pluginhost.ServiceInvokeParams) *personalEffectScope {
	scope, _ := ctx.Value(personalEffectScopeKey{}).(*personalEffectScope)
	if scope == nil && call.CallerScope != nil && call.CallerScope.Context != nil {
		scope, _ = call.CallerScope.Context.Value(personalEffectScopeKey{}).(*personalEffectScope)
	}
	return scope
}
func (b *PersonalEffectBridge) ProviderFactory() runtime.NativeProviderFactory {
	return runtime.NativeProviderFactory{ID: PersonalEffectModule, DefaultEnabled: true, Build: func(input runtime.NativeProviderContext) (pluginhost.Client, error) {
		if input.Registry == nil {
			return nil, errors.New("personal effects require a captured graph")
		}
		return &personalEffectMechanisms{bridge: b, registry: input.Registry}, nil
	}}
}

type personalEffectMechanisms struct {
	bridge   *PersonalEffectBridge
	registry *pluginhost.ServiceRegistry
	active   atomic.Bool
	closed   atomic.Bool
}

func (*personalEffectMechanisms) ID() string { return PersonalEffectModule }
func (m *personalEffectMechanisms) Status() pluginhost.Status {
	state := pluginhost.StatePrepared
	if m.active.Load() {
		state = pluginhost.StateActive
	}
	if m.closed.Load() {
		state = pluginhost.StateStopped
	}
	return pluginhost.Status{ID: m.ID(), State: state}
}
func (m *personalEffectMechanisms) Activate(context.Context) error {
	if m.closed.Load() {
		return errors.New("personal effects stopped")
	}
	m.active.Store(true)
	return nil
}

// Generation disposal never closes shared resources belonging to a successor.
func (m *personalEffectMechanisms) Close(context.Context) error {
	m.closed.Store(true)
	m.active.Store(false)
	return nil
}
func (*personalEffectMechanisms) RequiredServices() []pluginhost.ServiceRequirement { return nil }
func (*personalEffectMechanisms) ProvidedServices() []pluginhost.ServiceDescriptor {
	return []pluginhost.ServiceDescriptor{{Name: PersonalEffectService, Version: "1.0.0", Methods: []pluginhost.ServiceMethodDescriptor{
		{Name: "prepare-file", InputSchema: "host.personal-effects.prepare-file.input.v1", OutputSchema: "host.personal-effects.prepare-file.output.v1"},
		{Name: "execute", InputSchema: "host.personal-effects.reference.v1", OutputSchema: "host.personal-effects.execute.output.v1"},
		{Name: "release", InputSchema: "host.personal-effects.reference.v1", OutputSchema: "host.personal-effects.release.output.v1", Cleanup: true},
	}}}
}
func (m *personalEffectMechanisms) InvokeService(ctx context.Context, call pluginhost.ServiceInvokeParams) (json.RawMessage, error) {
	if !m.active.Load() && call.Method != "release" {
		return nil, errors.New("personal effects stopped")
	}
	owner, ok := m.registry.ProviderPluginIDFor("wuu.personal-client", "personal.coordination", 1)
	if !ok || owner != call.Caller {
		return nil, errors.New("personal effects require the selected coordinator")
	}
	if call.Method == "release" {
		var input PersonalEffectReference
		if err := json.Unmarshal(call.Params, &input); err != nil {
			return nil, err
		}
		if err := m.bridge.release(input.EffectID, owner); err != nil {
			return nil, err
		}
		return json.RawMessage(`{}`), nil
	}
	scope := personalEffectScopeForCall(ctx, call)
	if scope == nil || scope.bridge != m.bridge || scope.server == nil || scope.state == nil || scope.state.Host == nil || scope.state.Host.ServiceRegistry() != m.registry {
		return nil, errors.New("personal effect requires captured host scope in this graph")
	}
	held := scope.state.Retain()
	if held == nil {
		return nil, errors.New("personal effect host scope has expired")
	}
	defer held.Close()
	caller, ok := principal.FromContext(scope.authority)
	if !ok {
		return nil, errors.New("personal effect requires authenticated principal")
	}
	if err := caller.Check(ctx); err != nil {
		return nil, err
	}
	ctx = principal.WithContext(ctx, caller)
	if delegation := access.DelegationFromContext(scope.authority); delegation != nil {
		ctx = access.WithDelegation(ctx, *delegation)
	}
	ctx = runtime.WithExecutionAuthorization(ctx, runtime.CaptureExecutionAuthorization(scope.authority))
	ctx = runtime.WithInvocationState(ctx, held)
	switch call.Method {
	case "prepare-file":
		if scope.kit == nil {
			return nil, errors.New("file effect requires captured toolkit")
		}
		var input providers.ToolCall
		if err := json.Unmarshal(call.Params, &input); err != nil {
			return nil, err
		}
		file, err := scope.kit.PrepareFileAction(ctx, input)
		if err != nil {
			return nil, err
		}
		if err = personalEffectSafeFile(scope.server, file); err != nil {
			return nil, err
		}
		kit, err := scope.kit.CloneForRoot(scope.kit.RootDir())
		if err != nil {
			return nil, err
		}
		state := held.Retain()
		if state == nil {
			return nil, errors.New("personal effect state released")
		}
		r := newPersonalEffectResource()
		r.owner, r.registry, r.server, r.state = owner, m.registry, scope.server, state
		r.use = func(ctx context.Context) (toolresult.Result, error) {
			ctx = runtime.WithInvocationState(ctx, state)
			metadata, found, err := state.Sessions.Find(kit.SessionID())
			if err != nil {
				return toolresult.Result{}, err
			}
			if !found || metadata.ArchivedAt != nil || metadata.UserSpaceID != scope.server.rt.UserBinding().UserSpaceID {
				return toolresult.Result{}, errors.New("personal effect session is unavailable in this user space")
			}
			if err := scope.server.AuthorizeResource(ctx, "personal/file.execute", access.Resource{Kind: "user-space", ID: scope.server.rt.UserBinding().UserSpaceID}, access.Resource{Kind: "session", ID: kit.SessionID()}); err != nil {
				return toolresult.Result{}, err
			}
			if err := personalEffectSafeFile(scope.server, file); err != nil {
				return toolresult.Result{}, err
			}
			return kit.ExecuteFileAction(ctx, file)
		}
		id, err := m.bridge.add(r)
		if err != nil {
			r.finish(context.Background())
			return nil, err
		}
		return json.Marshal(PersonalPreparedFile{EffectID: id, File: file})
	case "execute":
		var input PersonalEffectReference
		if err := json.Unmarshal(call.Params, &input); err != nil {
			return nil, err
		}
		result, err := m.bridge.execute(ctx, input.EffectID, owner, scope.server)
		if err != nil {
			return nil, err
		}
		return json.Marshal(PersonalEffectResult{ToolResult: result})
	default:
		return nil, errors.New("unknown personal effect method")
	}
}

type personalEffectResource struct {
	owner         string
	registry      *pluginhost.ServiceRegistry
	server        *Server
	state         *runtime.State
	ctx           context.Context
	cancel        context.CancelFunc
	use           func(context.Context) (toolresult.Result, error)
	running       bool // bridge.mu
	used          bool // bridge.mu
	done          chan struct{}
	once          sync.Once
	unregister    func()
	stateReleased atomic.Bool
}

func newPersonalEffectResource() *personalEffectResource {
	ctx, cancel := context.WithCancel(context.Background())
	return &personalEffectResource{ctx: ctx, cancel: cancel, done: make(chan struct{})}
}
func (r *personalEffectResource) finish(ctx context.Context) {
	r.once.Do(func() {
		r.cancel()
		if r.unregister != nil {
			r.unregister()
		}
		// Quiescence precedes reference disposal: the final State.Close may itself
		// synchronously start host shutdown and reenter this resource's owner hook.
		close(r.done)
	})
	// Do not hold sync.Once across reference disposal. A concurrently started
	// retirement hook must observe quiescence without waiting on State.Close.
	if r.state != nil && r.stateReleased.CompareAndSwap(false, true) && !pluginhost.DeferServiceCompletion(ctx, r.state.Close) {
		_ = r.state.Close()
	}
}

func (b *PersonalEffectBridge) add(r *personalEffectResource) (string, error) {
	var random [24]byte
	if _, err := rand.Read(random[:]); err != nil {
		return "", err
	}
	id := hex.EncodeToString(random[:])
	b.mu.Lock()
	defer b.mu.Unlock()
	if b.closed || b.stopped[personalEffectOwner{r.registry, r.owner}] {
		return "", errors.New("personal effects closed")
	}
	if r.registry != nil {
		unregister, err := r.registry.RegisterRetirementCleanup(r.owner, func(ctx context.Context) error { return b.releaseContext(ctx, id, r.owner) })
		if err != nil {
			return "", err
		}
		r.unregister = unregister
	}
	b.resources[id] = r
	return id, nil
}
func (b *PersonalEffectBridge) execute(ctx context.Context, id, owner string, server *Server) (toolresult.Result, error) {
	b.mu.Lock()
	r := b.resources[id]
	if r == nil || r.owner != owner || r.server != server || r.used {
		b.mu.Unlock()
		return toolresult.Result{}, errors.New("personal effect unavailable or already consumed")
	}
	r.used, r.running = true, true
	b.mu.Unlock()
	defer func() {
		r.finish(context.Background())
		b.mu.Lock()
		if b.resources[id] == r {
			delete(b.resources, id)
		}
		b.mu.Unlock()
	}()
	ctx, cancel := context.WithCancel(ctx)
	stop := context.AfterFunc(r.ctx, cancel)
	defer stop()
	defer cancel()
	if err := r.ctx.Err(); err != nil {
		return toolresult.Result{}, err
	}
	return r.use(ctx)
}
func (b *PersonalEffectBridge) release(id, owner string) error {
	return b.releaseContext(context.Background(), id, owner)
}
func (b *PersonalEffectBridge) releaseContext(ctx context.Context, id, owner string) error {
	b.mu.Lock()
	r := b.resources[id]
	if r == nil {
		b.mu.Unlock()
		return nil
	}
	if r.owner != owner {
		b.mu.Unlock()
		return errors.New("personal effect belongs to another coordinator")
	}
	r.used = true
	running := r.running
	b.mu.Unlock()
	r.cancel()
	if !running {
		r.finish(ctx)
	}
	<-r.done
	b.mu.Lock()
	if b.resources[id] == r {
		delete(b.resources, id)
	}
	b.mu.Unlock()
	return nil
}

// RegisterConnector exposes only this synchronous, already-bound broker effect.
// The caller must defer release, even if coordinator invocation fails. Its
// cancellation terminates execution; the coordinator cannot detach account use.
func (b *PersonalEffectBridge) RegisterConnector(ctx context.Context, state *runtime.State, use func(context.Context) error) (string, func(), error) {
	scope := personalEffectScopeForCall(ctx, pluginhost.ServiceInvokeParams{})
	if scope == nil || scope.bridge != b || scope.server == nil || state == nil || scope.state == nil || state.Host == nil || scope.state.Host != state.Host {
		return "", nil, errors.New("connector effect requires captured host scope")
	}
	owner, ok := state.Host.ServiceRegistry().ProviderPluginIDFor("wuu.personal-client", "personal.coordination", 1)
	if !ok {
		return "", nil, errors.New("personal coordinator unavailable")
	}
	held := state.Retain()
	if held == nil {
		return "", nil, errors.New("connector effect state expired")
	}
	r := newPersonalEffectResource()
	r.owner, r.registry, r.server, r.state = owner, state.Host.ServiceRegistry(), scope.server, held
	r.use = func(effectCtx context.Context) (toolresult.Result, error) {
		return toolresult.Result{}, use(runtime.WithInvocationState(effectCtx, held))
	}
	id, err := b.add(r)
	if err != nil {
		r.finish(context.Background())
		return "", nil, err
	}
	stop := context.AfterFunc(ctx, r.cancel)
	return id, func() { stop(); _ = b.release(id, owner) }, nil
}

// ReleaseCoordinator is for disable/crash, not ordinary compatible publication.
// The runtime must invoke it before waiting for generation-retained references.
func (b *PersonalEffectBridge) ReleaseCoordinator(registry *pluginhost.ServiceRegistry, owner string) {
	b.mu.Lock()
	b.stopped[personalEffectOwner{registry, owner}] = true
	b.mu.Unlock()
	b.drain(func(r *personalEffectResource) bool { return r.registry == registry && r.owner == owner }, false)
}
func (b *PersonalEffectBridge) Close() {
	b.drain(func(*personalEffectResource) bool { return true }, true)
}
func (b *PersonalEffectBridge) drain(match func(*personalEffectResource) bool, closeBridge bool) {
	b.mu.Lock()
	if closeBridge {
		b.closed = true
	}
	var waiting []*personalEffectResource
	var idle []*personalEffectResource
	for _, r := range b.resources {
		if match(r) {
			r.used = true
			waiting = append(waiting, r)
			if !r.running {
				idle = append(idle, r)
			}
		}
	}
	b.mu.Unlock()
	// Cancel every accepted operation before waiting for any of them.
	for _, r := range waiting {
		r.cancel()
	}
	for _, r := range idle {
		r.finish(context.Background())
	}
	for _, r := range waiting {
		<-r.done
	}
	b.mu.Lock()
	for id, r := range b.resources {
		if match(r) && r.used {
			delete(b.resources, id)
		}
	}
	b.mu.Unlock()
}

func personalEffectSafeFile(s *Server, a tools.FileAction) error {
	root := s.rt.RootDir
	rel, err := filepath.Rel(root, a.Target)
	if err != nil || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
		return errors.New("Personal file action must stay inside its own files workspace")
	}
	stateRoot := s.rt.ProductDataRoot()
	if stateRoot == "" {
		stateRoot = filepath.Dir(s.rt.SessionDir)
	}
	protectedRoots := []string{stateRoot, s.rt.SessionDir}
	if s.rt.WuuHome != "" && filepath.Clean(root) != filepath.Clean(s.rt.UserBinding().Root(s.rt.WuuHome)) {
		protectedRoots = append(protectedRoots, s.rt.WuuHome)
	}
	for _, protected := range protectedRoots {
		if protected == "" {
			continue
		}
		relative, err := filepath.Rel(protected, a.Target)
		if err == nil && relative != ".." && !strings.HasPrefix(relative, ".."+string(filepath.Separator)) {
			return errors.New("Personal file actions cannot change application state or Coding settings")
		}
	}
	if reason := approvefor.HardDenyReason(approvefor.Request{PermissionMode: "standard", Tool: approvefor.Tool{Name: a.Operation}, Arguments: string(a.Arguments), CWD: a.CWD, ReferencedPaths: []string{a.Target}}); reason != "" {
		return errors.New(reason)
	}
	return nil
}
