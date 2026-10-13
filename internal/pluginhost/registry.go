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
)

// The service registry is the only addressable layer for plugin-provided
// services. Plugins cannot discover, connect to, or address one another; a
// consumer emits one host-mediated frame (service.call) and the registry
// resolves the provider by (service name, required major version), enforces
// the consumer's explicit declaration, and routes the call. The registry is
// built from one generation's initialize results and only accepts calls while
// that generation is active; closing it revokes every address and notifies
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

// ServiceConflict describes a rejected provider registration. The first
// provider of a (name, major) wins; later duplicates surface as diagnostics.
type ServiceConflict struct {
	PluginID string
	Service  string
	Major    int
	Message  string
}

type serviceKey struct {
	name  string
	major int
}

type serviceProvider struct {
	descriptor ServiceDescriptor
	pluginID   string
	invoker    ServiceInvoker
	kernel     bool
}

type serviceExecution struct {
	ctx          context.Context
	cancel       context.CancelFunc
	refs         int
	participants map[string]struct{}
	autonomous   bool
}

// ServiceRegistry resolves and routes service calls for one generation.
type ServiceRegistry struct {
	mu                   sync.RWMutex
	active               bool
	open                 bool
	providers            map[serviceKey]serviceProvider
	consumers            map[string]map[serviceKey]struct{}
	notifiers            map[string]ServiceChangedNotifier
	kernelLifecycles     []kernelServiceLifecycle
	executions           map[string]*serviceExecution
	executionChanged     chan struct{}
	nextExecution        uint64
	scopeTracker         *ExecutionTracker
	backgroundStopped    bool
	cleanupOwners        map[string]int
	retiringProviders    map[string]bool
	rootExecutionContext func(string, string) (context.Context, bool, *HostServiceError)
}

// ServiceRegistration is the common provider entry consumed by the registry.
// Kernel and plugin providers both become these entries before routing starts.
type ServiceRegistration struct {
	Descriptor ServiceDescriptor
	Invoker    ServiceInvoker
	Kernel     bool
}

type kernelServiceClient interface {
	ServiceClient
	KernelServiceRegistrations() []ServiceRegistration
}

type kernelServiceLifecycle interface {
	ActivateKernelServices()
	CloseKernelServices()
}

func NewServiceRegistry() *ServiceRegistry {
	return &ServiceRegistry{
		open: true, providers: make(map[serviceKey]serviceProvider),
		consumers:  make(map[string]map[serviceKey]struct{}),
		notifiers:  make(map[string]ServiceChangedNotifier),
		executions: make(map[string]*serviceExecution), executionChanged: make(chan struct{}),
	}
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
	var conflicts []ServiceConflict
	for _, client := range clients {
		declared, ok := client.(ServiceClient)
		if !ok {
			continue
		}
		if notifier, ok := client.(ServiceChangedNotifier); ok {
			registry.notifiers[declared.ID()] = notifier
		}
		kernelClient, kernel := declared.(kernelServiceClient)
		registrations := make([]ServiceRegistration, 0, len(declared.ProvidedServices()))
		if kernel {
			registrations = append(registrations, kernelClient.KernelServiceRegistrations()...)
		} else if invoker, ok := client.(ServiceInvoker); ok {
			for _, descriptor := range declared.ProvidedServices() {
				registrations = append(registrations, ServiceRegistration{Descriptor: descriptor, Invoker: invoker})
			}
		}
		if lifecycle, ok := client.(kernelServiceLifecycle); kernel && ok {
			registry.kernelLifecycles = append(registry.kernelLifecycles, lifecycle)
		}
		for _, registration := range registrations {
			descriptor := registration.Descriptor
			major, ok := ServiceVersionMajor(descriptor.Version)
			if !ok {
				continue
			}
			key := serviceKey{name: strings.TrimSpace(descriptor.Name), major: major}
			if existing, duplicate := registry.providers[key]; duplicate {
				conflicts = append(conflicts, ServiceConflict{
					PluginID: declared.ID(),
					Service:  key.name,
					Major:    key.major,
					Message:  fmt.Sprintf("service %s@%d is already provided by plugin %q; keeping the first registration", key.name, key.major, existing.pluginID),
				})
				continue
			}
			if registration.Invoker == nil {
				conflicts = append(conflicts, ServiceConflict{
					PluginID: declared.ID(),
					Service:  key.name,
					Major:    key.major,
					Message:  fmt.Sprintf("service %s@%d cannot receive host-routed calls", key.name, key.major),
				})
				continue
			}
			registry.providers[key] = serviceProvider{descriptor: descriptor, pluginID: declared.ID(), invoker: registration.Invoker, kernel: kernel || registration.Kernel}
		}
		registry.consumers[declared.ID()] = make(map[serviceKey]struct{})
		for _, requirement := range declared.RequiredServices() {
			key := serviceKey{name: strings.TrimSpace(requirement.Name), major: requirement.MajorVersion}
			registry.consumers[declared.ID()][key] = struct{}{}
		}
	}
	return conflicts
}

// AllowPreflight grants only read-phase kernel calls while initialize runs.
func (r *ServiceRegistry) AllowPreflight(pluginID string, requirements []ServiceRequirement) {
	r.mu.Lock()
	defer r.mu.Unlock()
	allowed := make(map[serviceKey]struct{}, len(requirements))
	for _, requirement := range requirements {
		allowed[serviceKey{name: strings.TrimSpace(requirement.Name), major: requirement.MajorVersion}] = struct{}{}
	}
	r.consumers[pluginID] = allowed
}

// Activate opens the registry for calls. The runtime activates the registry
// together with its generation so prepare-phase candidates stay unroutable.
func (r *ServiceRegistry) Activate() {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.active = true
	for _, lifecycle := range r.kernelLifecycles {
		lifecycle.ActivateKernelServices()
	}
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
	for _, lifecycle := range r.kernelLifecycles {
		lifecycle.CloseKernelServices()
	}
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
		if provider.pluginID == pluginID && !provider.kernel {
			delete(r.providers, key)
			revoked = append(revoked, key)
		}
	}
	delete(r.consumers, pluginID)
	delete(r.notifiers, pluginID)
	for _, execution := range r.executions {
		if _, involved := execution.participants[pluginID]; involved {
			execution.cancel()
		}
	}
	r.mu.Unlock()
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
	return caller == "kernel" || allowed
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

func (r *ServiceRegistry) acquireExecution(ctx context.Context, requestedID, caller string, key serviceKey, provider string, canStart bool) (context.Context, string, func(), *HostServiceError) {
	callerContext := ctx
	if err := callerContext.Err(); err != nil {
		return nil, "", nil, &HostServiceError{Code: "request_cancelled", Message: "calling transport was cancelled"}
	}
	r.mu.Lock()
	// Call reads declarations before checking provider readiness. Revalidate
	// identity at admission so revocation cannot leave a stale caller window.
	if _, registered := r.consumers[caller]; caller != "kernel" && !registered {
		r.mu.Unlock()
		return nil, "", nil, &HostServiceError{Code: "service_not_authorized", Message: "service caller was revoked"}
	}
	current, registered := r.providers[key]
	if !registered || current.pluginID != provider {
		r.mu.Unlock()
		return nil, "", nil, &HostServiceError{Code: "service_unavailable", Message: "service provider was revoked"}
	}
	cleanup := InShutdownCleanup(ctx) || r.cleanupOwners[caller] > 0
	if r.retiringProviders[provider] && !cleanup {
		r.mu.Unlock()
		return nil, "", nil, &HostServiceError{Code: "generation_closed", Message: "service provider is retiring"}
	}
	id := strings.TrimSpace(requestedID)
	if execution := r.executions[id]; id != "" && execution != nil {
		if _, allowed := execution.participants[caller]; caller != "kernel" && !allowed {
			r.mu.Unlock()
			return nil, "", nil, &HostServiceError{Code: "service_unavailable", Message: "service execution is unavailable"}
		}
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
		return callContext, id, func() { stopCaller(); cancel(); release() }, nil
	}
	scoped := false
	if r.scopeTracker != nil && id != "" && caller != "kernel" {
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
	if r.backgroundStopped && !scoped && !cleanup && caller != "kernel" {
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
	if requestedID != "" && caller != "kernel" && r.rootExecutionContext != nil {
		rootContext, handled, err := r.rootExecutionContext(caller, id)
		if err != nil {
			r.mu.Unlock()
			return nil, "", nil, err
		}
		if handled {
			parentContext = rootContext
		}
	}
	execCtx, cancelRoot := context.WithCancel(parentContext)
	stopCaller := context.AfterFunc(callerContext, cancelRoot)
	cancel := func() { stopCaller(); cancelRoot() }
	r.executions[id] = &serviceExecution{
		ctx: execCtx, cancel: cancel, refs: 1,
		participants: map[string]struct{}{caller: {}, provider: {}},
		autonomous:   !scoped && !cleanup && caller != "kernel",
	}
	r.mu.Unlock()
	return execCtx, id, r.releaseExecution(id), nil
}

func (r *ServiceRegistry) releaseExecution(id string) func() {
	var once sync.Once
	return func() {
		once.Do(func() {
			r.mu.Lock()
			execution := r.executions[id]
			if execution != nil {
				execution.refs--
				if execution.refs == 0 {
					delete(r.executions, id)
					execution.cancel()
					close(r.executionChanged)
					r.executionChanged = make(chan struct{})
				}
			}
			r.mu.Unlock()
		})
	}
}

// invokeProvider gives each plugin callback its own generic execution scope.
// The caller's scope supplies cancellation and attribution, never tool authority
// or an execution ID owned by a different plugin. Kernel calls keep the incoming
// scope because their scoped services validate the original plugin caller.
func (r *ServiceRegistry) invokeProvider(ctx context.Context, provider serviceProvider, params ServiceInvokeParams) (json.RawMessage, error) {
	r.mu.RLock()
	tracker := r.scopeTracker
	r.mu.RUnlock()
	cleanup := InShutdownCleanup(ctx)
	if provider.kernel || tracker == nil || cleanup {
		return provider.invoker.InvokeService(ctx, params)
	}
	callbackParent, cancel := context.WithCancel(ctx)
	var executionID string
	var callbackCtx context.Context
	for {
		executionID, callbackCtx = tracker.begin(provider.pluginID, callbackParent, ToolExecuteInput{}, false)
		r.mu.Lock()
		// Kernel-originated service IDs can share the tracker prefix. Never
		// replace an already-live routing record with a callback's new scope.
		if r.executions[executionID] == nil {
			r.executions[executionID] = &serviceExecution{
				ctx: callbackCtx, cancel: cancel, refs: 1,
				participants: map[string]struct{}{provider.pluginID: {}},
			}
			r.mu.Unlock()
			break
		}
		r.mu.Unlock()
		tracker.End(executionID)
	}
	release := r.releaseExecution(executionID)
	defer func() {
		tracker.End(executionID)
		cancel()
		release()
	}()
	params.ExecutionID = executionID
	return provider.invoker.InvokeService(callbackCtx, params)
}

// HasProvider reports whether a provider exists for (name, major).
func (r *ServiceRegistry) HasProvider(name string, major int) bool {
	r.mu.RLock()
	defer r.mu.RUnlock()
	_, ok := r.providers[serviceKey{name: strings.TrimSpace(name), major: major}]
	return ok
}

// ProviderPluginID reports which plugin provides one service major, so
// kernel-side resolution can stamp execution ownership (remote drivers).
func (r *ServiceRegistry) ProviderPluginID(name string, major int) (string, bool) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	provider, ok := r.providers[serviceKey{name: strings.TrimSpace(name), major: major}]
	if !ok {
		return "", false
	}
	return provider.pluginID, true
}

// ProviderMajors returns the provided majors for one service name, for
// diagnostics and version-mismatch errors.
func (r *ServiceRegistry) ProviderMajors(name string) []int {
	r.mu.RLock()
	defer r.mu.RUnlock()
	name = strings.TrimSpace(name)
	var majors []int
	for key := range r.providers {
		if key.name == name {
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
func (r *ServiceRegistry) Call(ctx context.Context, consumerPluginID string, params ServiceCallParams) (json.RawMessage, *HostServiceError) {
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
	if !active && !provider.kernel && !continuation {
		return nil, &HostServiceError{Code: "service_unavailable", Message: fmt.Sprintf("service %s is unavailable during generation prepare", service)}
	}
	methodDeclared := false
	for _, declared := range provider.descriptor.Methods {
		if declared.Name == method {
			methodDeclared = true
			break
		}
	}
	if !methodDeclared {
		return nil, &HostServiceError{Code: "method_not_found", Message: fmt.Sprintf("service %s does not declare method %q", service, method)}
	}
	if state := provider.invoker.Status().State; state != StateActive && !(provider.kernel && !active) {
		go r.notifyConsumers(context.Background(), key, "provider_unavailable")
		return nil, &HostServiceError{Code: "service_unavailable", Message: fmt.Sprintf("service %s provider %q is %s", service, provider.pluginID, state)}
	}
	// A cancelled foreground or background callback must still be able to
	// release its generation-owned lock through its unscoped lifecycle host.
	// This grants cleanup only, never another acquisition or execution scope.
	if provider.kernel && service == KernelFileLockService && method == "release" {
		ctx = context.WithValue(ctx, serviceCleanupKey{}, true)
	}
	execCtx, executionID, release, executionErr := r.acquireExecution(ctx, params.ExecutionID, consumerPluginID, key, provider.pluginID, active || provider.kernel)
	if executionErr != nil {
		return nil, executionErr
	}
	defer release()
	result, err := r.invokeProvider(execCtx, provider, ServiceInvokeParams{
		Service:     service,
		Method:      method,
		Caller:      consumerPluginID,
		Params:      params.Params,
		ExecutionID: executionID,
	})
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

// CallProvider is the kernel-caller entry point: the kernel itself is the
// authority and needs no consumer declaration, but provider existence,
// activation state, and the declared method set are still enforced, and the
// execution id rides the invoke so long-lived kernel-initiated calls (loop
// drivers) cancel through the execution plane.
func (r *ServiceRegistry) CallProvider(ctx context.Context, service string, major int, method string, params json.RawMessage, executionID string) (json.RawMessage, *HostServiceError) {
	service = strings.TrimSpace(service)
	method = strings.TrimSpace(method)
	if service == "" || method == "" {
		return nil, &HostServiceError{Code: "invalid_request", Message: "kernel service call requires non-empty service and method"}
	}
	key := serviceKey{name: service, major: major}
	r.mu.RLock()
	provider, found := r.providers[key]
	active := r.active
	r.mu.RUnlock()
	if !found {
		return nil, &HostServiceError{Code: "service_unavailable", Message: fmt.Sprintf("no provider for service %s major version %d (provided majors: %v)", service, major, r.ProviderMajors(service))}
	}
	continuation := r.executionContinuation(executionID, "kernel")
	if !active && !provider.kernel && !continuation {
		return nil, &HostServiceError{Code: "service_unavailable", Message: fmt.Sprintf("service %s is unavailable during generation prepare", service)}
	}
	methodDeclared := false
	for _, declared := range provider.descriptor.Methods {
		if declared.Name == method {
			methodDeclared = true
			break
		}
	}
	if !methodDeclared {
		return nil, &HostServiceError{Code: "method_not_found", Message: fmt.Sprintf("service %s does not declare method %q", service, method)}
	}
	if state := provider.invoker.Status().State; state != StateActive && !(provider.kernel && !active) {
		return nil, &HostServiceError{Code: "service_unavailable", Message: fmt.Sprintf("service %s provider %q is %s", service, provider.pluginID, state)}
	}
	execCtx, executionID, release, executionErr := r.acquireExecution(ctx, executionID, "kernel", key, provider.pluginID, active || provider.kernel)
	if executionErr != nil {
		return nil, executionErr
	}
	defer release()
	result, err := r.invokeProvider(execCtx, provider, ServiceInvokeParams{
		Service:     service,
		Method:      method,
		Caller:      "kernel",
		Params:      params,
		ExecutionID: executionID,
	})
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

// ServiceRegistryEntry describes one provided service in a registry snapshot.
type ServiceRegistryEntry struct {
	Service  string   `json:"service"`
	Version  string   `json:"version"`
	Provider string   `json:"provider"`
	Kernel   bool     `json:"kernel,omitempty"`
	Methods  []string `json:"methods"`
}

// ServiceRegistrySnapshot is the machine-readable answer to "which services
// exist, at what version, provided by whom, in which generation". It carries
// registration metadata only — never configuration values, credentials, or
// another plugin's private data.
type ServiceRegistrySnapshot struct {
	Generation uint64                 `json:"generation"`
	Services   []ServiceRegistryEntry `json:"services"`
}

// Snapshot returns a deterministic view of the currently registered
// providers. The generation identity is supplied by the caller because the
// registry itself is generation-scoped but does not own the durable epoch.
func (r *ServiceRegistry) Snapshot(generation uint64) ServiceRegistrySnapshot {
	r.mu.RLock()
	defer r.mu.RUnlock()
	entries := make([]ServiceRegistryEntry, 0, len(r.providers))
	for _, provider := range r.providers {
		methods := make([]string, 0, len(provider.descriptor.Methods))
		for _, method := range provider.descriptor.Methods {
			methods = append(methods, method.Name)
		}
		sort.Strings(methods)
		entries = append(entries, ServiceRegistryEntry{
			Service:  provider.descriptor.Name,
			Version:  provider.descriptor.Version,
			Provider: provider.pluginID,
			Kernel:   provider.kernel,
			Methods:  methods,
		})
	}
	sort.Slice(entries, func(i, j int) bool {
		if entries[i].Service != entries[j].Service {
			return entries[i].Service < entries[j].Service
		}
		return entries[i].Version < entries[j].Version
	})
	return ServiceRegistrySnapshot{Generation: generation, Services: entries}
}

// notifyConsumers delivers service.changed to every consumer of (name,
// major), best effort. Notifications never block the caller's result.
func (r *ServiceRegistry) notifyConsumers(ctx context.Context, key serviceKey, reason string) {
	r.mu.RLock()
	targets := make([]ServiceChangedNotifier, 0, len(r.consumers))
	for consumerID, requirements := range r.consumers {
		if _, ok := requirements[key]; !ok {
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
