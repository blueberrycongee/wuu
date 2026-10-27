package pluginhost

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/blueberrycongee/wuu/internal/composition"
)

// The service registry is the only addressable layer for plugin-provided
// services. Plugins cannot choose provider identities or transports in calls;
// a consumer emits one host-mediated frame (service.call) and the registry
// resolves its configured (consumer, service name, required major) route, enforces
// the consumer's explicit declaration, and routes the call. The registry is
// built from one generation's initialize results. Preparation permits only
// explicitly marked methods; active generations admit other calls. Closing it
// revokes every address and notifies
// consumers so stale resolutions cannot leak across generations.

// ServiceInvoker is the provider-side surface the registry routes calls to.
// ProcessClient implements it over the service.invoke host -> plugin frame.
type ServiceInvoker interface {
	ID() string
	Status() Status
	InvokeService(ctx context.Context, params ServiceInvokeParams) (json.RawMessage, error)
}

// ServiceChangedNotifier receives best-effort service.changed notifications.
type ServiceChangedNotifier interface {
	NotifyServiceChanged(ctx context.Context, params ServiceChangedParams) error
}

// ServiceClient exposes the provide/consume declarations captured during
// initialize. ProcessClient implements it; failed clients do not.
type ServiceClient interface {
	Client
	ProvidedServices() []ServiceDescriptor
	RequiredServices() []ServiceRequirement
}

// RuntimeServiceRequirements preserves declaration origin when a host-owned
// mixed-transport client combines its process and renderer requirements. All
// ordinary native/process declarations originate in the runtime. The optional
// method is internal host metadata, never a field accepted from plugin wire data.
func RuntimeServiceRequirements(client ServiceClient) []ServiceRequirement {
	if mixed, ok := client.(interface{ RuntimeRequiredServices() []ServiceRequirement }); ok {
		return mixed.RuntimeRequiredServices()
	}
	return client.RequiredServices()
}

type serviceConsumption struct {
	runtime bool // False only for requirements originating exclusively in a renderer.
}

// ServiceConflict describes a rejected provider registration. An ambiguous
// service stays unavailable unless the composition explicitly selects a provider.
type ServiceConflict struct {
	PluginID string
	Service  string
	Major    int
	Message  string
}

type serviceKey struct {
	consumer string // Empty for the default route and for requirement declarations.
	name     string
	major    int
}

type serviceProvider struct {
	descriptor ServiceDescriptor
	pluginID   string
	invoker    ServiceInvoker
}

type callerScopeKey struct {
	provider  string
	execution string
}

type callerScopeResource struct {
	ctx    context.Context
	cancel context.CancelFunc
}

type serviceExecution struct {
	retained     bool // Opaque resources retain this execution until physical cleanup.
	ctx          context.Context
	cancel       context.CancelFunc
	refs         int
	participants map[string]struct{}
	autonomous   bool
}

type serviceCompletionKey struct{}

type serviceCompletion struct {
	mu         sync.Mutex
	references int
	closed     bool
	finalizers []func() error
}

// DeferServiceCompletion releases native invocation resources after the entire
// authenticated service chain has left the registry. It adds no wire authority.
// False means there is no live completion boundary; the caller must release now.
func DeferServiceCompletion(ctx context.Context, finalize func() error) bool {
	completion, _ := ctx.Value(serviceCompletionKey{}).(*serviceCompletion)
	if completion == nil {
		return false
	}
	completion.mu.Lock()
	defer completion.mu.Unlock()
	if completion.closed {
		return false
	}
	completion.finalizers = append(completion.finalizers, finalize)
	return true
}

func retainServiceCompletion(ctx context.Context) context.Context {
	if completion, _ := ctx.Value(serviceCompletionKey{}).(*serviceCompletion); completion != nil {
		completion.mu.Lock()
		if !completion.closed {
			completion.references++
			completion.mu.Unlock()
			return ctx
		}
		completion.mu.Unlock()
	}
	return context.WithValue(ctx, serviceCompletionKey{}, &serviceCompletion{references: 1})
}

func (c *serviceCompletion) release() error {
	c.mu.Lock()
	c.references--
	if c.references != 0 {
		c.mu.Unlock()
		return nil
	}
	c.closed = true
	finalizers := c.finalizers
	c.finalizers = nil
	c.mu.Unlock()
	var err error
	for i := len(finalizers) - 1; i >= 0; i-- {
		err = errors.Join(err, finalizers[i]())
	}
	return err
}

func serviceCompletionError(callErr *HostServiceError, cleanupErr error) *HostServiceError {
	if cleanupErr == nil {
		return callErr
	}
	code := "service_unavailable"
	if callErr != nil {
		code = callErr.Code
		cleanupErr = errors.Join(callErr, cleanupErr)
	}
	return &HostServiceError{Code: code, Message: fmt.Sprintf("service completion failed: %v", cleanupErr)}
}

// ServiceRegistry resolves and routes service calls for one generation.
type ServiceRegistry struct {
	renderers            map[string]*rendererRegistration
	mu                   sync.RWMutex
	active               bool
	open                 bool
	providers            map[serviceKey]serviceProvider
	bindings             map[serviceKey]string
	bindingSources       map[serviceKey]string
	ambiguous            map[serviceKey]string
	invalidDeclarations  map[string]string
	consumers            map[string]map[serviceKey]serviceConsumption
	requirements         map[string][]ServiceRequirement
	notifiers            map[string]ServiceChangedNotifier
	executions           map[string]*serviceExecution
	executionChanged     chan struct{}
	nextExecution        uint64
	scopeTracker         *ExecutionTracker
	callerScopes         map[callerScopeKey]*callerScopeResource
	backgroundStopped    bool
	cleanupOwners        map[string]int
	preparing            map[string]bool
	retiringProviders    map[string]bool
	rootExecutionContext func(string, string) (context.Context, bool, *HostServiceError)
	graphGeneration      string
	composition          *CompositionSnapshot
	moduleStatuses       func() []Status
	dependencies         func() map[string][]Dependency
}

func NewServiceRegistry() *ServiceRegistry {
	return &ServiceRegistry{
		open: true, providers: make(map[serviceKey]serviceProvider),
		bindings: make(map[serviceKey]string), bindingSources: make(map[serviceKey]string), ambiguous: make(map[serviceKey]string),
		consumers:    make(map[string]map[serviceKey]serviceConsumption),
		requirements: make(map[string][]ServiceRequirement),
		notifiers:    make(map[string]ServiceChangedNotifier),
		executions:   make(map[string]*serviceExecution), executionChanged: make(chan struct{}),
	}
}

// NewServiceRegistryWithBindings prepares explicit provider selections before
// any initialization calls can resolve a service.
func NewServiceRegistryWithBindings(bindings []composition.Binding) (*ServiceRegistry, error) {
	if err := (composition.Config{Version: composition.Version, Services: bindings}).Validate(); err != nil {
		return nil, err
	}
	registry := NewServiceRegistry()
	for _, binding := range bindings {
		key := serviceKey{consumer: binding.Consumer, name: binding.Service, major: binding.Major}
		registry.bindings[key] = binding.Provider
		source := binding.Source
		if source == "" {
			source = "composition"
		}
		registry.bindingSources[key] = source
	}
	return registry, nil
}

// ValidateBindings refuses a candidate whose explicit choices cannot be met.
// Call after all providers have initialized and before committing configuration.
func (r *ServiceRegistry) ValidateBindings() error {
	r.mu.RLock()
	defer r.mu.RUnlock()
	var failures []string
	for _, message := range r.invalidDeclarations {
		failures = append(failures, message)
	}
	for key, selected := range r.bindings {
		if key.consumer != "" {
			declaration := serviceKey{name: key.name, major: key.major}
			if _, declared := r.consumers[key.consumer][declaration]; !declared {
				failures = append(failures, fmt.Sprintf("composition consumer %q does not declare service %s@%d", key.consumer, key.name, key.major))
			}
			if key.consumer == selected && r.consumers[key.consumer][declaration].runtime {
				failures = append(failures, fmt.Sprintf("composition consumer %q delegates %s@%d to itself", key.consumer, key.name, key.major))
			}
		}
		provider, ok := r.providers[key]
		if !ok || provider.pluginID != selected {
			failures = append(failures, fmt.Sprintf("composition selects unavailable provider %q for %s@%d (consumer %q)", selected, key.name, key.major, key.consumer))
		}
		if ok && key.consumer != "" {
			// A same-contract adapter cannot silently reinterpret the delegate's
			// schemas or preparation/cleanup authority for a shared method.
			for route, adapter := range r.providers {
				if route.name != key.name || route.major != key.major || adapter.pluginID != key.consumer {
					continue
				}
				for _, method := range adapter.descriptor.Methods {
					for _, target := range provider.descriptor.Methods {
						if method.Name == target.Name && method != target {
							failures = append(failures, fmt.Sprintf("composition delegate %q has incompatible contract for %s@%d.%s consumed by %q", selected, key.name, key.major, method.Name, key.consumer))
						}
					}
				}
			}
		}
	}
	for _, diagnostic := range r.ambiguous {
		failures = append(failures, diagnostic)
	}
	if len(failures) == 0 {
		return nil
	}
	sort.Strings(failures)
	return errors.New(strings.Join(failures, "; "))
}

// bindRootExecutionContext connects a new plugin-initiated service chain to
// its originating host dispatch. Nested providers reuse the admitted chain
// after participant validation rather than pretending to own the root tool.
func (r *ServiceRegistry) bindRootExecutionContext(resolve func(string, string) (context.Context, bool, *HostServiceError)) {
	r.mu.Lock()
	r.rootExecutionContext = resolve
	r.mu.Unlock()
}

// BuildServiceRegistry collects provide/consume declarations from one
// generation's clients. Providers that cannot receive service.invoke are
// rejected as conflicts.
func BuildServiceRegistry(clients ...Client) (*ServiceRegistry, []ServiceConflict) {
	registry := NewServiceRegistry()
	return registry, registry.RegisterClients(clients...)
}

// RegisterClients adds prepared providers and consumers before activation.
func (registry *ServiceRegistry) RegisterClients(clients ...Client) []ServiceConflict {
	registry.mu.Lock()
	defer registry.mu.Unlock()
	var conflicts []ServiceConflict
	for _, client := range clients {
		declared, ok := client.(ServiceClient)
		if !ok {
			continue
		}
		if err := ValidateServiceNegotiation(CapabilityInitializeResult{ProtocolVersion: 3, ProvidedServices: declared.ProvidedServices(), RequiredServices: declared.RequiredServices()}); err != nil {
			message := fmt.Sprintf("provider %q has invalid service declarations: %v", declared.ID(), err)
			if registry.invalidDeclarations == nil {
				registry.invalidDeclarations = map[string]string{}
			}
			registry.invalidDeclarations[declared.ID()] = message
			for key, provider := range registry.providers {
				if provider.pluginID == declared.ID() {
					delete(registry.providers, key)
				}
			}
			delete(registry.consumers, declared.ID())
			delete(registry.requirements, declared.ID())
			delete(registry.notifiers, declared.ID())
			conflicts = append(conflicts, ServiceConflict{PluginID: declared.ID(), Message: message})
			continue
		}
		delete(registry.invalidDeclarations, declared.ID())
		if notifier, ok := client.(ServiceChangedNotifier); ok {
			registry.notifiers[declared.ID()] = notifier
		}
		invoker, _ := client.(ServiceInvoker)
		for _, descriptor := range declared.ProvidedServices() {
			major, ok := ServiceVersionMajor(descriptor.Version)
			if !ok {
				continue
			}
			key := serviceKey{name: strings.TrimSpace(descriptor.Name), major: major}
			routes := []serviceKey{key}
			for route, selected := range registry.bindings {
				if route.consumer != "" && route.name == key.name && route.major == key.major && selected == declared.ID() {
					routes = append(routes, route)
				}
			}
			for _, route := range routes {
				if selected := registry.bindings[route]; selected != "" && selected != declared.ID() {
					continue
				}
				if existing, duplicate := registry.providers[route]; duplicate || registry.ambiguous[route] != "" {
					message := registry.ambiguous[route]
					if message == "" {
						message = fmt.Sprintf("service %s@%d is provided by both %q and %q; select a provider in composition.services", route.name, route.major, existing.pluginID, declared.ID())
					}
					registry.ambiguous[route] = message
					delete(registry.providers, route)
					conflicts = append(conflicts, ServiceConflict{PluginID: declared.ID(), Service: route.name, Major: route.major, Message: message})
					continue
				}
				if invoker == nil {
					conflicts = append(conflicts, ServiceConflict{PluginID: declared.ID(), Service: route.name, Major: route.major,
						Message: fmt.Sprintf("service %s@%d cannot receive host-routed calls", route.name, route.major)})
					continue
				}
				descriptor.Methods = append([]ServiceMethodDescriptor(nil), descriptor.Methods...)
				registry.providers[route] = serviceProvider{descriptor: descriptor, pluginID: declared.ID(), invoker: invoker}
			}
		}
		registry.consumers[declared.ID()] = make(map[serviceKey]serviceConsumption)
		registry.requirements[declared.ID()] = append([]ServiceRequirement(nil), declared.RequiredServices()...)
		for _, requirement := range registry.requirements[declared.ID()] {
			key := serviceKey{name: strings.TrimSpace(requirement.Name), major: requirement.MajorVersion}
			registry.consumers[declared.ID()][key] = serviceConsumption{}
		}
		for _, requirement := range RuntimeServiceRequirements(declared) {
			key := serviceKey{name: strings.TrimSpace(requirement.Name), major: requirement.MajorVersion}
			if _, declared := registry.consumers[client.ID()][key]; declared {
				registry.consumers[client.ID()][key] = serviceConsumption{runtime: true}
			}
		}
	}
	return conflicts
}

// RuntimeConsumesService distinguishes backend delegation from a renderer using
// its own package's runtime. Both remain authorized under the same package ID;
// only runtime declarations can establish a provider delegation chain.
func (r *ServiceRegistry) RuntimeConsumesService(consumer, service string, major int) bool {
	r.mu.RLock()
	defer r.mu.RUnlock()
	return r.consumers[consumer][serviceKey{name: strings.TrimSpace(service), major: major}].runtime
}

type servicePreparationKey struct{}

// Preparation authority belongs to one lifecycle callback and its admitted
// descendants. Initialize cannot call services before declarations are bound.
func (r *ServiceRegistry) allowPreparation(pluginID string) func() {
	if r == nil {
		return func() {}
	}
	r.mu.Lock()
	if r.preparing == nil {
		r.preparing = make(map[string]bool)
	}
	r.preparing[pluginID] = true
	r.mu.Unlock()
	return func() {
		r.mu.Lock()
		delete(r.preparing, pluginID)
		r.mu.Unlock()
	}
}

// Activate opens the registry for calls. The runtime activates the registry
// together with its generation so prepare-phase candidates stay unroutable.
func (r *ServiceRegistry) Activate() {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.active = true
}

// Close rejects new executions, drains calls already admitted by this
// generation, then revokes every address. If the deadline expires, all live
// execution contexts are canceled before process shutdown continues.
func (r *ServiceRegistry) Close(ctx context.Context) {
	r.mu.Lock()
	if !r.open {
		r.mu.Unlock()
		return
	}
	r.active = false
	r.open = false
	keys := make([]serviceKey, 0, len(r.providers))
	for key := range r.providers {
		keys = append(keys, key)
	}
	for len(r.executions) != 0 {
		changed := r.executionChanged
		r.mu.Unlock()
		select {
		case <-changed:
			r.mu.Lock()
		case <-ctx.Done():
			r.mu.Lock()
			for _, execution := range r.executions {
				execution.cancel()
			}
			r.mu.Unlock()
			goto drained
		}
	}
	r.mu.Unlock()

drained:
	r.cancelCallerScopes("")
	for _, key := range keys {
		r.notifyConsumers(ctx, key, "provider_closed")
	}
}

// RevokePlugin removes one plugin's provider and consumer bindings without
// closing unrelated services. New calls are rejected immediately; executions
// involving the retired plugin are canceled before its process is closed.
func (r *ServiceRegistry) RevokePlugin(ctx context.Context, pluginID string) {
	if r == nil {
		return
	}
	pluginID = strings.TrimSpace(pluginID)
	if pluginID == "" {
		return
	}
	r.mu.Lock()
	var revoked []serviceKey
	for key, provider := range r.providers {
		if provider.pluginID == pluginID {
			delete(r.providers, key)
			revoked = append(revoked, key)
		}
	}
	delete(r.invalidDeclarations, pluginID)
	delete(r.consumers, pluginID)
	delete(r.notifiers, pluginID)
	for _, execution := range r.executions {
		if _, involved := execution.participants[pluginID]; involved {
			execution.cancel()
		}
	}
	r.mu.Unlock()
	r.cancelCallerScopes(pluginID)
	for _, key := range revoked {
		r.notifyConsumers(ctx, key, "provider_closed")
	}
}

// stopProviderAdmission seals incoming work before the host takes its
// cancellation snapshot. Declared shutdown cleanup remains routable until
// RevokePlugin removes the consumer and provider bindings after Close returns.
func (r *ServiceRegistry) stopProviderAdmission(pluginID string) {
	if r == nil {
		return
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.retiringProviders == nil {
		r.retiringProviders = make(map[string]bool)
	}
	r.retiringProviders[pluginID] = true
	for _, execution := range r.executions {
		if _, involved := execution.participants[pluginID]; involved {
			execution.cancel()
		}
	}
}

func (r *ServiceRegistry) executionContinuation(executionID, caller string) bool {
	executionID = strings.TrimSpace(executionID)
	if executionID == "" {
		return false
	}
	r.mu.RLock()
	defer r.mu.RUnlock()
	execution := r.executions[executionID]
	if execution == nil {
		return false
	}
	_, allowed := execution.participants[caller]
	return allowed
}

// A shutdown callback may persist through its declared services while normal
// autonomous admission is closed. These calls never mint model/tool authority.
type serviceCleanupKey struct{}

// InShutdownCleanup marks inherited persistence/cancellation work, not a new
// autonomous entry point. Kernel services retain their normal owner checks.
func InShutdownCleanup(ctx context.Context) bool {
	cleanup, _ := ctx.Value(serviceCleanupKey{}).(bool)
	return cleanup
}

func (r *ServiceRegistry) allowShutdownCleanup(pluginID string) func() {
	if r == nil {
		return func() {}
	}
	r.mu.Lock()
	if r.cleanupOwners == nil {
		r.cleanupOwners = make(map[string]int)
	}
	r.cleanupOwners[pluginID]++
	r.mu.Unlock()
	return sync.OnceFunc(func() {
		r.mu.Lock()
		r.cleanupOwners[pluginID]--
		if r.cleanupOwners[pluginID] == 0 {
			delete(r.cleanupOwners, pluginID)
		}
		r.mu.Unlock()
	})
}

func (r *ServiceRegistry) acquireExecution(ctx context.Context, requestedID, caller string, key serviceKey, provider string, canStart bool) (context.Context, string, func() error, *HostServiceError) {
	callerContext := ctx
	if err := callerContext.Err(); err != nil {
		return nil, "", nil, &HostServiceError{Code: "request_cancelled", Message: "calling transport was cancelled"}
	}
	r.mu.Lock()
	// Call reads declarations before checking provider readiness. Revalidate
	// identity at admission so revocation cannot leave a stale caller window.
	if _, registered := r.consumers[caller]; !registered {
		r.mu.Unlock()
		return nil, "", nil, &HostServiceError{Code: "service_not_authorized", Message: "service caller was revoked"}
	}
	current, registered := r.providers[key]
	if !registered || current.pluginID != provider {
		r.mu.Unlock()
		return nil, "", nil, &HostServiceError{Code: "service_unavailable", Message: "service provider was revoked"}
	}
	id := strings.TrimSpace(requestedID)
	execution := r.executions[id]
	if marker, ok := ctx.Value(inheritedInvocationKey{}).(*nativeInvocation); ok && marker != nil {
		if marker.registry != r || marker.owner != caller || marker.executionID != id || !marker.live || (execution == nil && !marker.root) {
			r.mu.Unlock()
			return nil, "", nil, &HostServiceError{Code: "execution_not_found", Message: "native callback execution is no longer live"}
		}
	}
	cleanup := InShutdownCleanup(ctx) || r.cleanupOwners[caller] > 0
	if execution != nil {
		if _, allowed := execution.participants[caller]; !allowed {
			r.mu.Unlock()
			return nil, "", nil, &HostServiceError{Code: "service_unavailable", Message: "service execution is unavailable"}
		}
		// A process transports the execution ID, not the Go context. Continue
		// the authenticated cleanup chain across provider callbacks.
		cleanup = cleanup || InShutdownCleanup(execution.ctx)
	}
	if r.retiringProviders[provider] && !cleanup {
		r.mu.Unlock()
		return nil, "", nil, &HostServiceError{Code: "generation_closed", Message: "service provider is retiring"}
	}
	if execution != nil {
		if execution.ctx.Err() != nil {
			r.mu.Unlock()
			return nil, "", nil, &HostServiceError{Code: "request_cancelled", Message: "owning execution was cancelled"}
		}
		execution.refs++
		execution.participants[provider] = struct{}{}
		r.mu.Unlock()
		// A callback's transport can disappear while its shared root remains live.
		// Cancel this invocation without revoking the other chain participants.
		callContext, cancel := context.WithCancel(execution.ctx)
		stopCaller := context.AfterFunc(callerContext, cancel)
		release := r.releaseExecution(id)
		return callContext, id, func() error { stopCaller(); cancel(); return release() }, nil
	}
	scoped := false
	if r.scopeTracker != nil && id != "" {
		scope, scopeErr := r.scopeTracker.Resolve(caller, id)
		if scopeErr != nil {
			r.mu.Unlock()
			return nil, "", nil, scopeErr
		}
		ctx, scoped = scope.Context, true
	}
	if err := ctx.Err(); err != nil {
		r.mu.Unlock()
		return nil, "", nil, &HostServiceError{Code: "request_cancelled", Message: "owning execution was cancelled"}
	}
	if cleanup {
		ctx = context.WithValue(ctx, serviceCleanupKey{}, true)
	}
	if r.backgroundStopped && !scoped && !cleanup {
		r.mu.Unlock()
		return nil, "", nil, &HostServiceError{Code: "generation_closed", Message: "generation is not accepting autonomous service calls"}
	}
	if !r.open || !canStart {
		r.mu.Unlock()
		return nil, "", nil, &HostServiceError{Code: "service_unavailable", Message: "service registry is not accepting new executions"}
	}
	if id == "" {
		r.nextExecution++
		id = fmt.Sprintf("service-%d", r.nextExecution)
	}
	parentContext := ctx
	if requestedID != "" && r.rootExecutionContext != nil {
		rootContext, handled, err := r.rootExecutionContext(caller, id)
		if err != nil {
			r.mu.Unlock()
			return nil, "", nil, err
		}
		if handled {
			parentContext = rootContext
		}
	}
	execCtx, cancelRoot := context.WithCancel(retainServiceCompletion(parentContext))
	stopCaller := context.AfterFunc(callerContext, cancelRoot)
	cancel := func() { stopCaller(); cancelRoot() }
	r.executions[id] = &serviceExecution{
		retained: ctx.Value(retainedServiceScopeAdmissionKey{}) == true, ctx: execCtx, cancel: cancel, refs: 1,
		participants: map[string]struct{}{caller: {}, provider: {}},
		autonomous:   !scoped && !cleanup,
	}
	r.mu.Unlock()
	return execCtx, id, r.releaseExecution(id), nil
}

func (r *ServiceRegistry) releaseExecution(id string) func() error {
	var once sync.Once
	var releaseErr error
	return func() error {
		once.Do(func() {
			r.mu.Lock()
			var completion *serviceCompletion
			execution := r.executions[id]
			if execution != nil {
				execution.refs--
				if execution.refs == 0 {
					delete(r.executions, id)
					execution.cancel()
					close(r.executionChanged)
					r.executionChanged = make(chan struct{})
					completion, _ = execution.ctx.Value(serviceCompletionKey{}).(*serviceCompletion)
				}
			}
			r.mu.Unlock()
			// Finalizers may close this registry. Remove all chain executions
			// and drop the mutex before allowing the final owner to do so.
			if completion != nil {
				releaseErr = completion.release()
			}
		})
		return releaseErr
	}
}

type nativeInvocationKey struct{}
type inheritedInvocationKey struct{}

// The marker is private to one callback, registry and authenticated owner.
// Its lifetime is guarded by the registry mutex, including admission checks.
type nativeInvocation struct {
	registry           *ServiceRegistry
	owner, executionID string
	live               bool
	root               bool // Host-authenticated capability root, resolved by scopeTracker.
}

func (r *ServiceRegistry) invokeNativeContext(ctx context.Context, provider serviceProvider, params ServiceInvokeParams) (json.RawMessage, error) {
	marker := &nativeInvocation{registry: r, owner: provider.pluginID, executionID: params.ExecutionID, live: true}
	defer func() { r.mu.Lock(); marker.live = false; r.mu.Unlock() }()
	ctx = context.WithValue(ctx, inheritedInvocationKey{}, (*nativeInvocation)(nil))
	return provider.invoker.InvokeService(context.WithValue(ctx, nativeInvocationKey{}, marker), params)
}

// invokeProvider gives each callback its own identity. Caller metadata never
// confers tool authority: only a native tool dispatch can explicitly delegate
// it to this callback. Nested ordinary service calls remain generic callbacks.
func (r *ServiceRegistry) invokeProvider(ctx context.Context, provider serviceProvider, params ServiceInvokeParams, toolScope *ToolExecuteInput) (result json.RawMessage, invokeErr error) {
	r.mu.RLock()
	tracker := r.scopeTracker
	r.mu.RUnlock()
	cleanup := InShutdownCleanup(ctx)
	if tracker != nil && strings.HasPrefix(params.ExecutionID, "exec-") && !cleanup {
		scope, err := tracker.Resolve(params.Caller, params.ExecutionID)
		if err != nil {
			return nil, err
		}
		if err := r.bindCallerScope(provider.pluginID, &scope); err != nil {
			return nil, err
		}
		params.CallerScope = &scope
	}
	if tracker == nil || cleanup {
		return r.invokeNativeContext(ctx, provider, params)
	}
	callbackParent, cancel := context.WithCancel(retainServiceCompletion(ctx))
	input := ToolExecuteInput{}
	if toolScope != nil {
		input = *toolScope
	}
	executionID, callbackCtx := tracker.begin(provider.pluginID, callbackParent, input, toolScope != nil)
	r.mu.Lock()
	r.executions[executionID] = &serviceExecution{
		ctx: callbackCtx, cancel: cancel, refs: 1,
		participants: map[string]struct{}{provider.pluginID: {}},
	}
	r.mu.Unlock()
	release := r.releaseExecution(executionID)
	defer func() {
		tracker.End(executionID)
		cancel()
		invokeErr = errors.Join(invokeErr, release())
	}()
	params.ExecutionID = executionID
	return r.invokeNativeContext(callbackCtx, provider, params)
}

// Caller resources are effects owned by the provider lifecycle, bounded also
// by their originating invocation. Repeated callbacks share one lifetime.
func (r *ServiceRegistry) bindCallerScope(provider string, scope *ExecutionScope) *HostServiceError {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.retiringProviders[provider] {
		return &HostServiceError{Code: "generation_closed", Message: "service provider is retiring"}
	}
	key := callerScopeKey{provider: provider, execution: scope.ID}
	resource := r.callerScopes[key]
	if resource == nil {
		ctx, cancel := context.WithCancel(scope.Context)
		resource = &callerScopeResource{ctx: ctx, cancel: cancel}
		if r.callerScopes == nil {
			r.callerScopes = make(map[callerScopeKey]*callerScopeResource)
		}
		r.callerScopes[key] = resource
		context.AfterFunc(ctx, func() {
			r.mu.Lock()
			if r.callerScopes[key] == resource {
				delete(r.callerScopes, key)
			}
			r.mu.Unlock()
		})
	}
	scope.Context = resource.ctx
	return nil
}

func (r *ServiceRegistry) cancelCallerScopes(provider string) {
	if r == nil {
		return
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	for key, resource := range r.callerScopes {
		if provider == "" || key.provider == provider {
			delete(r.callerScopes, key)
			resource.cancel()
		}
	}
}

// providerKeyLocked uses the consumer's explicit binding even when its target
// was revoked. Only the absence of a scoped selection permits the global route.
// The same key is captured by dispatch, admission, dependencies and scopes.
func (r *ServiceRegistry) providerKeyLocked(consumer string, key serviceKey) serviceKey {
	scoped := serviceKey{consumer: consumer, name: key.name, major: key.major}
	if _, selected := r.bindings[scoped]; selected {
		return scoped
	}
	return serviceKey{name: key.name, major: key.major}
}

// HasProvider reports whether a provider exists for (name, major).
func (r *ServiceRegistry) HasProvider(name string, major int) bool {
	return r.HasProviderFor("", name, major)
}

// HasProviderFor probes the same binding used by this consumer's calls. A
// missing explicit scoped target never probes the global implementation.
func (r *ServiceRegistry) HasProviderFor(consumer, name string, major int) bool {
	_, found := r.ProviderPluginIDFor(consumer, name, major)
	return found
}

// ProviderPluginID reports which plugin provides one service major, so
// callers can attribute resources to the selected implementation.
func (r *ServiceRegistry) ProviderPluginID(name string, major int) (string, bool) {
	return r.ProviderPluginIDFor("", name, major)
}

// ProviderPluginIDFor reports the same explicit route used by an authenticated
// consumer's calls. This is attribution only; it grants no invocation authority.
func (r *ServiceRegistry) ProviderPluginIDFor(consumer, name string, major int) (string, bool) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	key := r.providerKeyLocked(consumer, serviceKey{name: strings.TrimSpace(name), major: major})
	provider, ok := r.providers[key]
	if !ok {
		return "", false
	}
	return provider.pluginID, true
}

// ServiceDescriptorFor returns an owned copy of the selected route's contract.
// Diagnostic snapshots may also contain other consumers' explicit routes.
func (r *ServiceRegistry) ServiceDescriptorFor(consumer, name string, major int) (ServiceDescriptor, bool) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	key := r.providerKeyLocked(consumer, serviceKey{name: name, major: major})
	provider, found := r.providers[key]
	descriptor := provider.descriptor
	descriptor.Methods = append([]ServiceMethodDescriptor(nil), descriptor.Methods...)
	return descriptor, found
}

// ProviderMajors returns the provided majors for one service name, for
// diagnostics and version-mismatch errors.
func (r *ServiceRegistry) ProviderMajors(name string) []int {
	r.mu.RLock()
	defer r.mu.RUnlock()
	name = strings.TrimSpace(name)
	var majors []int
	for key := range r.providers {
		if key.consumer == "" && key.name == name {
			majors = append(majors, key.major)
		}
	}
	return majors
}

// CheckSatisfaction returns a deterministic error when a required service has
// no provider. There is no dependency solver: the consumer is refused
// activation with this diagnostic.
func (r *ServiceRegistry) CheckSatisfaction(requirements []ServiceRequirement) error {
	for _, requirement := range requirements {
		if !requirement.Required {
			continue
		}
		name := strings.TrimSpace(requirement.Name)
		if r.HasProvider(name, requirement.MajorVersion) {
			continue
		}
		if majors := r.ProviderMajors(name); len(majors) > 0 {
			return fmt.Errorf("required service %s major version %d has no provider (provided majors: %v)", name, requirement.MajorVersion, majors)
		}
		return fmt.Errorf("required service %s has no provider", name)
	}
	return nil
}

// Call validates and routes one service.call frame from a consumer plugin.
// Authority is exactly the consumer's explicit declaration captured during
// initialize; the not-authorized check runs before existence checks so an
// undeclared consumer cannot probe which services exist.
func (r *ServiceRegistry) Call(ctx context.Context, consumerPluginID string, params ServiceCallParams) (result json.RawMessage, callErr *HostServiceError) {
	return r.call(ctx, consumerPluginID, params, nil)
}

// toolScope is a host-only delegation; no wire field can grant it.
func (r *ServiceRegistry) call(ctx context.Context, consumerPluginID string, params ServiceCallParams, toolScope *ToolExecuteInput) (result json.RawMessage, callErr *HostServiceError) {
	if params.ExecutionID == "" {
		if marker, ok := ctx.Value(nativeInvocationKey{}).(*nativeInvocation); ok {
			if marker.registry != r || marker.owner != consumerPluginID {
				return nil, &HostServiceError{Code: "service_not_authorized", Message: "native callback identity does not match service caller"}
			}
			params.ExecutionID = marker.executionID
			ctx = context.WithValue(ctx, inheritedInvocationKey{}, marker)
		}
	}
	service := strings.TrimSpace(params.Service)
	method := strings.TrimSpace(params.Method)
	if service == "" || method == "" {
		return nil, &HostServiceError{Code: "invalid_request", Message: "service.call requires non-empty service and method"}
	}
	key := serviceKey{name: service}
	r.mu.RLock()
	active := r.active
	open := r.open
	var declaredMajor *int
	if requirements, ok := r.consumers[consumerPluginID]; ok {
		for consumerKey := range requirements {
			if consumerKey.name == service {
				major := consumerKey.major
				declaredMajor = &major
				break
			}
		}
	}
	r.mu.RUnlock()
	continuation := r.executionContinuation(params.ExecutionID, consumerPluginID)
	if !open && !continuation {
		return nil, &HostServiceError{Code: "service_unavailable", Message: "service registry is not active"}
	}
	if declaredMajor == nil {
		return nil, &HostServiceError{Code: "service_not_authorized", Message: fmt.Sprintf("plugin %q did not declare service %s in required services", consumerPluginID, service)}
	}
	key.major = *declaredMajor
	r.mu.RLock()
	key = r.providerKeyLocked(consumerPluginID, key)
	provider, found := r.providers[key]
	nameExists := false
	if !found {
		for providerKey := range r.providers {
			if providerKey.name == service {
				nameExists = true
				break
			}
		}
	}
	r.mu.RUnlock()
	if !found {
		if !nameExists {
			return nil, &HostServiceError{Code: "service_unavailable", Message: fmt.Sprintf("no provider for service %s", service)}
		}
		return nil, &HostServiceError{Code: "service_unavailable", Message: fmt.Sprintf("no provider for service %s major version %d (provided majors: %v)", service, key.major, r.ProviderMajors(service))}
	}
	r.mu.RLock()
	preparing := r.preparing[consumerPluginID]
	if execution := r.executions[params.ExecutionID]; execution != nil {
		if _, participant := execution.participants[consumerPluginID]; participant {
			inherited, _ := execution.ctx.Value(servicePreparationKey{}).(bool)
			preparing = preparing || inherited
		}
	}
	r.mu.RUnlock()
	if preparing {
		ctx = context.WithValue(ctx, servicePreparationKey{}, true)
	}
	methodDeclared, preparationMethod, cleanupMethod := false, false, false
	for _, declared := range provider.descriptor.Methods {
		if declared.Name == method {
			methodDeclared, preparationMethod, cleanupMethod = true, declared.Preparation, declared.Cleanup
			break
		}
	}
	if !methodDeclared {
		return nil, &HostServiceError{Code: "method_not_found", Message: fmt.Sprintf("service %s does not declare method %q", service, method)}
	}
	if params.Cleanup && (!cleanupMethod || params.ExecutionID != "") {
		return nil, &HostServiceError{Code: "invalid_cleanup", Message: "cleanup requires a declared resource-release method and no borrowed execution identity"}
	}
	if preparing && !preparationMethod || !active && !preparing && !continuation {
		return nil, &HostServiceError{Code: "service_unavailable", Message: fmt.Sprintf("service %s method %q is unavailable during generation prepare", service, method)}
	}
	if state := provider.invoker.Status().State; state != StateActive && !(preparing && state == StatePrepared) {
		go r.notifyConsumers(context.Background(), key, "provider_unavailable")
		return nil, &HostServiceError{Code: "service_unavailable", Message: fmt.Sprintf("service %s provider %q is %s", service, provider.pluginID, state)}
	}
	// Providers declare resource-release methods independently of transport.
	// Cleanup grants no new execution or tool authority.
	if cleanupMethod {
		ctx = context.WithValue(ctx, serviceCleanupKey{}, true)
	}
	execCtx, executionID, release, executionErr := r.acquireExecution(ctx, params.ExecutionID, consumerPluginID, key, provider.pluginID, active || preparing)
	if executionErr != nil {
		return nil, executionErr
	}
	defer func() { callErr = serviceCompletionError(callErr, release()) }()
	result, err := r.invokeProvider(execCtx, provider, ServiceInvokeParams{
		Admission:   serviceAdmission(ctx, consumerPluginID, service, method, params.Params),
		Service:     service,
		Method:      method,
		Caller:      consumerPluginID,
		Params:      params.Params,
		ExecutionID: executionID,
	}, toolScope)
	if err != nil {
		var hostErr *HostServiceError
		if errors.As(err, &hostErr) && hostErr.Code != "" {
			return nil, hostErr
		}
		var remoteErr *remoteCallError
		if errors.As(err, &remoteErr) && remoteErr.code != "" {
			return nil, &HostServiceError{Code: remoteErr.code, Message: remoteErr.message}
		}
		return nil, &HostServiceError{Code: "service_unavailable", Message: fmt.Sprintf("service %s call failed: %v", service, err)}
	}
	if len(result) == 0 || !json.Valid(result) {
		return nil, &HostServiceError{Code: "service_unavailable", Message: fmt.Sprintf("service %s provider returned an invalid result", service)}
	}
	return result, nil
}

func (r *ServiceRegistry) RouteServiceCall(ctx context.Context, pluginID string, params ServiceCallParams) (json.RawMessage, *HostServiceError) {
	return r.Call(ctx, pluginID, params)
}

// ServiceRegistryEntry describes one provided service in a registry snapshot.
type ServiceRegistryEntry struct {
	Consumer      string            `json:"consumer,omitempty"`
	Descriptor    ServiceDescriptor `json:"descriptor"`
	Service       string            `json:"service"`
	Version       string            `json:"version"`
	Provider      string            `json:"provider"`
	Methods       []string          `json:"methods"`
	BindingSource string            `json:"binding_source"`
}

// ServiceRegistrySnapshot is the machine-readable answer to "which services
// exist, at what version, provided by whom, in which generation". It carries
// registration metadata only — never configuration values, credentials, or
// another plugin's private data.
type ServiceRegistrySnapshot struct {
	// GraphGeneration identifies this immutable runtime, independently of the
	// mutable catalog epoch shared by concurrent and retained compositions.
	GraphGeneration string `json:"graph_generation,omitempty"`
	// Dependencies retain the lifecycle graph after provider retirement.
	Dependencies map[string][]Dependency `json:"dependencies"`
	// Requirements include unresolved optional and required declarations.
	Requirements map[string][]ServiceRequirement `json:"requirements"`
	Generation   uint64                          `json:"generation"`
	Services     []ServiceRegistryEntry          `json:"services"`
	Composition  *CompositionSnapshot            `json:"composition,omitempty"`
}

// CompositionSnapshot records selected modules without exposing configuration
// values. Version is the package version, or product build for native modules.
// Declarative-only packages have no runtime State.
type CompositionSnapshot struct {
	Profile      string                      `json:"profile,omitempty"`
	ModuleSource string                      `json:"module_source"`
	Layers       []string                    `json:"layers,omitempty"`
	Modules      []CompositionModuleSnapshot `json:"modules"`
}

type CompositionModuleSnapshot struct {
	Renderer *RendererActivationSnapshot `json:"renderer,omitempty"`
	ID       string                      `json:"id"`
	Source   string                      `json:"source"`
	Version  string                      `json:"version,omitempty"`
	State    State                       `json:"state,omitempty"`
	Error    string                      `json:"error,omitempty"`
}

// SetGraphGeneration binds the runtime identity before effectful activation.
// A retained registry keeps this identity even after the catalog epoch changes.
func (r *ServiceRegistry) SetGraphGeneration(identity string) {
	r.mu.Lock()
	r.graphGeneration = identity
	r.mu.Unlock()
}

// SetComposition binds diagnostic metadata before candidate preparation.
// Runtime states come from the owning Host's authoritative lifecycle graph.
func (r *ServiceRegistry) SetComposition(snapshot CompositionSnapshot) {
	snapshot.Layers = append([]string(nil), snapshot.Layers...)
	snapshot.Modules = append([]CompositionModuleSnapshot{}, snapshot.Modules...)
	sort.Slice(snapshot.Modules, func(i, j int) bool { return snapshot.Modules[i].ID < snapshot.Modules[j].ID })
	r.mu.Lock()
	r.composition = &snapshot
	r.mu.Unlock()
}

// Snapshot returns a deterministic view of the currently registered
// providers. The generation identity is supplied by the caller because the
// registry itself is generation-scoped but does not own the durable epoch.
func (r *ServiceRegistry) Snapshot(generation uint64) ServiceRegistrySnapshot {
	r.mu.RLock()
	var composition *CompositionSnapshot
	if r.composition != nil {
		copy := *r.composition
		copy.Layers = append([]string(nil), copy.Layers...)
		copy.Modules = append([]CompositionModuleSnapshot{}, copy.Modules...)
		composition = &copy
		for i := range composition.Modules {
			composition.Modules[i].Renderer = r.rendererSnapshot(composition.Modules[i].ID)
		}
	}
	graphGeneration := r.graphGeneration
	moduleStatuses := r.moduleStatuses
	dependencies := r.dependencies
	requirements := make(map[string][]ServiceRequirement, len(r.requirements))
	for id, declared := range r.requirements {
		requirements[id] = append([]ServiceRequirement{}, declared...)
	}
	entries := make([]ServiceRegistryEntry, 0, len(r.providers))
	for route, provider := range r.providers {
		methods := make([]string, 0, len(provider.descriptor.Methods))
		for _, method := range provider.descriptor.Methods {
			methods = append(methods, method.Name)
		}
		sort.Strings(methods)
		source := "unique-provider"
		if selectedSource := r.bindingSources[route]; selectedSource != "" {
			source = selectedSource
		}
		descriptor := provider.descriptor
		descriptor.Methods = append([]ServiceMethodDescriptor{}, descriptor.Methods...)
		sort.Slice(descriptor.Methods, func(i, j int) bool { return descriptor.Methods[i].Name < descriptor.Methods[j].Name })
		entries = append(entries, ServiceRegistryEntry{
			Consumer:      route.consumer,
			Descriptor:    descriptor,
			Service:       provider.descriptor.Name,
			Version:       provider.descriptor.Version,
			Provider:      provider.pluginID,
			Methods:       methods,
			BindingSource: source,
		})
	}
	r.mu.RUnlock()
	if composition != nil && moduleStatuses != nil {
		statuses := make(map[string]Status)
		for _, status := range moduleStatuses() {
			statuses[status.ID] = status
		}
		for index := range composition.Modules {
			module := &composition.Modules[index]
			status := statuses[module.ID]
			module.State, module.Error = status.State, status.Error
		}
	}
	sort.Slice(entries, func(i, j int) bool {
		if entries[i].Service != entries[j].Service {
			return entries[i].Service < entries[j].Service
		}
		if entries[i].Version != entries[j].Version {
			return entries[i].Version < entries[j].Version
		}
		return entries[i].Consumer < entries[j].Consumer
	})
	graph := make(map[string][]Dependency)
	if dependencies != nil {
		graph = dependencies()
	}
	return ServiceRegistrySnapshot{GraphGeneration: graphGeneration, Generation: generation, Services: entries, Composition: composition, Dependencies: graph, Requirements: requirements}
}

// notifyConsumers delivers service.changed to every consumer of (name,
// major), best effort. Notifications never block the caller's result.
func (r *ServiceRegistry) notifyConsumers(ctx context.Context, key serviceKey, reason string) {
	r.mu.RLock()
	targets := make([]ServiceChangedNotifier, 0, len(r.consumers))
	for consumerID, requirements := range r.consumers {
		if r.providerKeyLocked(consumerID, key) != key {
			continue
		}
		if _, ok := requirements[serviceKey{name: key.name, major: key.major}]; !ok {
			continue
		}
		if notifier, ok := r.notifiers[consumerID]; ok {
			targets = append(targets, notifier)
		}
	}
	r.mu.RUnlock()
	for _, notifier := range targets {
		notifyCtx, cancel := context.WithTimeout(ctx, 2*time.Second)
		_ = notifier.NotifyServiceChanged(notifyCtx, ServiceChangedParams{Service: key.name, Reason: reason})
		cancel()
	}
}
