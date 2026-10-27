package runtime

import (
	"context"
	"crypto/rand"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"

	"github.com/blueberrycongee/wuu/internal/composition"
	"github.com/blueberrycongee/wuu/internal/executionauth"
	"github.com/blueberrycongee/wuu/internal/loopdriver"
	"github.com/blueberrycongee/wuu/internal/persistence"
	"github.com/blueberrycongee/wuu/internal/pluginhost"
	"github.com/blueberrycongee/wuu/internal/session"
)

// Each kernel generation owns its scopes. Nothing is published to the process
// catalog, and selecting a scope never rewrites a live execution's bindings.
type compositionService struct {
	parent   *kernelHostServices
	mu       sync.Mutex
	closed   bool
	ctx      context.Context
	cancel   context.CancelFunc
	calls    sync.WaitGroup
	scopes   map[string]*compositionScope
	bindings map[compositionAgent]string
	stopped  map[string]bool
}

type compositionOwner struct{ plugin, thread string }
type compositionAgent struct {
	caller compositionOwner
	agent  string
}
type compositionScope struct {
	id, directory    string
	parentGeneration string
	stateGeneration  string
	stateSource      string
	caller           compositionOwner
	ctx              context.Context
	cancel           context.CancelFunc
	runtime          *Session
	selection        composition.Resolved
	plugins          []pluginhost.CompositionPackage
	productResources *productCompositionBindings
	running          int
	published        bool
	disposed         bool
}

func newCompositionService(parent *kernelHostServices) *compositionService {
	ctx, cancel := context.WithCancel(context.Background())
	return &compositionService{parent: parent, ctx: ctx, cancel: cancel,
		scopes: make(map[string]*compositionScope), bindings: make(map[compositionAgent]string), stopped: make(map[string]bool)}
}

func (c *compositionService) ID() string                { return c.parent.ID() }
func (c *compositionService) Status() pluginhost.Status { return c.parent.Status() }

func (c *compositionService) InvokeService(ctx context.Context, params pluginhost.ServiceInvokeParams) (json.RawMessage, error) {
	caller := compositionOwner{plugin: params.Caller}
	if params.CallerScope != nil {
		caller.thread = params.CallerScope.ThreadID
	}
	c.mu.Lock()
	if c.closed || c.stopped[caller.plugin] {
		c.mu.Unlock()
		return nil, serviceError("generation_closed", "composition owner is closed")
	}
	c.calls.Add(1)
	c.mu.Unlock()
	defer c.calls.Done()
	ctx, cancel := context.WithCancel(ctx)
	stop := context.AfterFunc(c.ctx, cancel)
	defer func() { stop(); cancel() }()
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	switch params.Method {
	case "create":
		var input pluginhost.CompositionCreate
		if err := decodeServiceParams(params.Params, &input); err != nil {
			return nil, err
		}
		return c.create(ctx, caller, input)
	case "inspect":
		var input pluginhost.CompositionInspect
		if err := decodeServiceParams(params.Params, &input); err != nil {
			return nil, err
		}
		c.mu.Lock()
		defer c.mu.Unlock()
		scope, err := c.scope(caller, input.ID)
		if err != nil {
			return nil, err
		}
		return json.Marshal(c.view(scope))
	case "inspectSession":
		var input struct {
			Session string `json:"session"`
		}
		if err := decodeServiceParams(params.Params, &input); err != nil {
			return nil, err
		}
		return c.inspectSession(ctx, input.Session)
	case "bind":
		var input pluginhost.CompositionBind
		if err := decodeServiceParams(params.Params, &input); err != nil {
			return nil, err
		}
		if (strings.TrimSpace(input.Agent) == "") == (strings.TrimSpace(input.Session) == "") || input.Expected == nil {
			return nil, serviceError("invalid_params", "exactly one of agent or session and expected binding are required")
		}
		if input.Session != "" {
			return c.bindSession(ctx, caller, input)
		}
		c.mu.Lock()
		defer c.mu.Unlock()
		key := compositionAgent{caller, input.Agent}
		previous := c.bindings[key]
		if previous != *input.Expected {
			return nil, serviceError("binding_conflict", "agent binding changed; inspect before retrying")
		}
		if input.ID != "" {
			if _, err := c.scope(caller, input.ID); err != nil {
				return nil, err
			}
			c.bindings[key] = input.ID
		} else {
			delete(c.bindings, key)
		}
		return json.Marshal(pluginhost.CompositionBinding{Agent: input.Agent, ID: input.ID, Previous: previous})
	case "run":
		var input pluginhost.CompositionRun
		if err := decodeServiceParams(params.Params, &input); err != nil {
			return nil, err
		}
		return c.run(ctx, caller, input)
	case "dispose":
		var input pluginhost.CompositionInspect
		if err := decodeServiceParams(params.Params, &input); err != nil {
			return nil, err
		}
		c.mu.Lock()
		scope, err := c.scope(caller, input.ID)
		if err != nil {
			c.mu.Unlock()
			return nil, err
		}
		scope.disposed = true
		for key, id := range c.bindings {
			if id == scope.id {
				delete(c.bindings, key)
			}
		}
		draining := scope.running != 0
		if !draining {
			delete(c.scopes, scope.id)
		}
		c.mu.Unlock()
		if !draining {
			if err := scope.close(); err != nil {
				return nil, err
			}
		}
		return json.Marshal(pluginhost.CompositionDisposed{ID: input.ID, Draining: draining})
	default:
		return nil, serviceError("method_not_found", "unknown composition method")
	}
}

// scope and view require c.mu. Caller identity is supplied by the registry.
func (c *compositionService) scope(caller compositionOwner, id string) (*compositionScope, error) {
	scope := c.scopes[id]
	if scope == nil || scope.caller != caller || scope.disposed {
		return nil, serviceError("composition_unavailable", "composition is absent or disposed")
	}
	return scope, nil
}

func (c *compositionService) view(scope *compositionScope) pluginhost.CompositionView {
	agents := []string{}
	for key, id := range c.bindings {
		if id == scope.id {
			agents = append(agents, key.agent)
		}
	}
	sort.Strings(agents)
	return pluginhost.CompositionView{Services: scope.runtime.PluginHost.ServiceRegistry().Snapshot(0), ID: scope.id, Root: scope.runtime.RootDir, State: "active", Running: scope.running,
		Agents: agents, StateSource: scope.stateSource, Selection: scope.selection, Modules: scope.runtime.PluginHost.Statuses(), Plugins: scope.plugins,
		ParentGeneration: scope.parentGeneration, Model: pluginhost.CompositionModel{Provider: scope.runtime.ProviderName,
			Model: scope.runtime.Model, Variant: scope.runtime.StreamRunner.Variant, Effort: scope.runtime.StreamRunner.Effort}}
}

func (c *compositionService) create(ctx context.Context, caller compositionOwner, input pluginhost.CompositionCreate) (_ json.RawMessage, err error) {
	c.parent.mu.RLock()
	owner, generation := c.parent.stateOwner, c.parent.stateGeneration
	c.parent.mu.RUnlock()
	if owner == nil || generation == nil {
		return nil, serviceError("service_unavailable", "composition creation requires an initialized runtime")
	}
	if input.State == "" {
		input.State = "isolated"
	}
	if input.State != "isolated" && input.State != "parent" {
		return nil, serviceError("invalid_params", "state must be isolated or parent")
	}
	authority := compositionStateGeneration(generation)
	if input.Inherit {
		if input.Selection.Profile != "" {
			return nil, serviceError("invalid_params", "inherit and a selection profile are mutually exclusive")
		}
		if input.Selection.Version == 0 {
			input.Selection.Version = composition.Version
		}
		parent := generation.host.ServiceRegistry().Snapshot(0)
		// Reconstruct imported providers against the new candidate context when
		// isolating. Preserve the selected implementations, not the live bridge.
		isolatedProviders := make(map[string]bool)
		if input.State == "isolated" {
			for index, binding := range parent.Services {
				if binding.Provider != parentStateModule {
					continue
				}
				major, _ := pluginhost.ServiceVersionMajor(binding.Version)
				provider, found := authority.host.ServiceRegistry().ProviderPluginIDFor(binding.Consumer, binding.Service, major)
				if !found {
					return nil, serviceError("service_unavailable", "imported state provider is unavailable")
				}
				parent.Services[index].Provider = provider
				isolatedProviders[provider] = true
			}
		}
		if input.Selection.Modules == nil {
			modules := make([]string, 0, len(parent.Composition.Modules))
			for _, module := range parent.Composition.Modules {
				if input.State == "isolated" && module.ID == parentStateModule {
					continue
				}
				modules = append(modules, module.ID)
				delete(isolatedProviders, module.ID)
			}
			for provider := range isolatedProviders {
				modules = append(modules, provider)
			}
			sort.Strings(modules)
			input.Selection.Modules = &modules
		}
		overrides := make(map[composition.ServiceKey]bool, len(input.Selection.Services))
		for _, binding := range input.Selection.Services {
			overrides[composition.ServiceKey{Consumer: binding.Consumer, Service: binding.Service, Major: binding.Major}] = true
		}
		for _, binding := range parent.Services {
			major, _ := pluginhost.ServiceVersionMajor(binding.Version)
			if !overrides[composition.ServiceKey{Consumer: binding.Consumer, Service: binding.Service, Major: major}] {
				input.Selection.Services = append(input.Selection.Services, composition.Binding{Consumer: binding.Consumer, Service: binding.Service, Major: major, Provider: binding.Provider})
			}
		}
	}
	var factories []NativeProviderFactory
	for _, factory := range owner.nativeProviders {
		if factory.ID != parentStateModule {
			factories = append(factories, factory)
		}
	}
	var productParent *kernelHostServices
	var productRouter *PluginSessionRouter
	if input.State == "parent" {
		// This explicit import changes selection in the same graph as every
		// other override; native and process consumers see the bridge provider.
		imported := map[string]bool{persistence.ServiceName: true, session.ServiceName: true, ArtifactServiceName: true}
		// Grants belong to the imported product authority, not to a second
		// provider instantiated against the bridge's general document consumer.
		// Import only the actually selected service; its absence stays absent.
		var executionDescriptor *pluginhost.ServiceDescriptor
		if descriptor, ok := generation.host.ServiceRegistry().ServiceDescriptorFor(executionauth.ConsumerModule, executionauth.ServiceName, 1); ok {
			executionDescriptor = &descriptor
			imported[executionauth.ServiceName] = true
		}
		modules := map[string]bool{parentStateModule: true}
		var removeDelegates []composition.ServiceKey
		parentServices := generation.host.ServiceRegistry().Snapshot(0).Services
		for _, service := range parentServices {
			if imported[service.Service] {
				if service.Consumer != "" {
					major, _ := pluginhost.ServiceVersionMajor(service.Version)
					removeDelegates = append(removeDelegates, composition.ServiceKey{Consumer: service.Consumer, Service: service.Service, Major: major})
				}
				modules[service.Provider] = false
			}
		}
		for _, service := range parentServices {
			if !imported[service.Service] {
				delete(modules, service.Provider)
			}
		}
		modules[parentStateModule] = true
		bindings := []composition.Binding{
			{Service: persistence.ServiceName, Major: 1, Provider: parentStateModule},
			{Service: session.ServiceName, Major: 1, Provider: parentStateModule},
			{Service: ArtifactServiceName, Major: 1, Provider: parentStateModule},
		}
		if executionDescriptor != nil {
			bindings = append(bindings, composition.Binding{Service: executionauth.ServiceName, Major: 1, Provider: parentStateModule})
		}
		input.Selection.Patches = append(input.Selection.Patches, composition.Patch{
			Modules: modules, RemoveServices: removeDelegates, Services: bindings,
		})
		factories = append(factories, NativeProviderFactory{ID: parentStateModule, Build: func(NativeProviderContext) (pluginhost.Client, error) {
			return &compositionStateImport{parent: generation.host, state: pluginhost.StatePrepared, executionDescriptor: executionDescriptor}, nil
		}})
		productParent, productRouter = c.parent, owner.PluginSessionRouter
	}
	selection, err := input.Selection.Resolve()
	if err != nil {
		return nil, err
	}
	catalog := generation.ActivePackages()
	directory, err := os.MkdirTemp("", "wuu-composition-")
	if err != nil {
		return nil, err
	}
	scope := &compositionScope{id: rand.Text(), caller: caller, directory: directory, selection: selection, parentGeneration: generation.id, stateGeneration: authority.id, stateSource: input.State, productResources: owner.productCompositions,
		plugins: []pluginhost.CompositionPackage{}}
	scope.ctx, scope.cancel = context.WithCancel(c.ctx)
	var runtime *Session
	defer func() {
		if err != nil {
			scope.cancel()
			if runtime != nil {
				if _, cleanupErr := runtime.Cleanup(); cleanupErr != nil {
					err = errors.Join(err, fmt.Errorf("candidate state retained at %s: %w", directory, cleanupErr))
					return
				}
			}
			err = errors.Join(err, os.RemoveAll(directory))
		}
	}()
	home := filepath.Join(directory, "state")
	root := input.Root
	if root == "" {
		root = filepath.Join(directory, "workspace")
	} else if !filepath.IsAbs(root) {
		root = filepath.Join(owner.RootDir, root)
	}
	if err = os.MkdirAll(root, 0o700); err != nil {
		return nil, err
	}
	cfg, catalog, err := PrepareCompositionSources(ctx, generation.settings, catalog, home, owner.RootDir, input.Paths)
	if err != nil {
		return nil, err
	}
	cfg.Composition = &input.Selection
	if err = ctx.Err(); err != nil {
		return nil, err
	}
	parentModels := captureGenerationStateModels(generation)
	if parentModels.err != nil {
		return nil, parentModels.err
	}
	main := parentModels.Roles.Main
	model := pluginhost.CompositionModel{Provider: main.Provider, Model: main.Model, Variant: main.Variant, Effort: main.LegacyEffort}
	if input.Model != nil {
		model = *input.Model
	}
	cfg.Agent.Variant, cfg.Agent.Effort = model.Variant, model.Effort
	runtime, err = NewSession(Options{RootDir: root, HomeDir: directory, WuuHome: home, Config: cfg,
		UserSpaceID: owner.UserSpaceID, ProductDomain: owner.ProductDomain, UserHostIsolationMode: owner.UserHostIsolationMode, UserHostIsolationVerifier: owner.userHostIsolationVerifier,
		DiscoveredPlugins: &catalog, NativeProviders: factories, NonInteractive: true, DriverProfile: input.DriverProfile,
		productParent: productParent, productRouter: productRouter, composition: productCompositionBinding{service: c, scope: scope},
		ProviderName: model.Provider, ModelOverride: model.Model})
	if err != nil {
		return nil, err
	}
	for _, status := range runtime.PluginHost.Statuses() {
		if status.State != pluginhost.StateActive {
			return nil, fmt.Errorf("composition module %s is %s: %s", status.ID, status.State, status.Error)
		}
	}
	if len(runtime.ReadinessIssues) != 0 {
		return nil, fmt.Errorf("composition is not ready: %v", runtime.ReadinessIssues)
	}
	if err = ctx.Err(); err != nil {
		return nil, err
	}
	if input.State == "parent" {
		runtime.productCompositions = owner.productCompositions
	}
	scope.runtime = runtime
	for _, item := range runtime.pluginGeneration.active {
		scope.plugins = append(scope.plugins, pluginhost.CompositionPackage{ID: item.ID, Fingerprint: item.Fingerprint})
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.closed || c.stopped[caller.plugin] {
		return nil, serviceError("generation_closed", "composition owner closed during preparation")
	}
	view, err := json.Marshal(c.view(scope))
	if err != nil {
		return nil, err
	}
	scope.published = true
	c.scopes[scope.id] = scope
	scope.productResources.mu.Lock()
	scope.productResources.scopes[scope.id] = runtime.pluginGeneration.composition
	scope.productResources.mu.Unlock()
	return view, nil
}

func (c *compositionService) run(ctx context.Context, caller compositionOwner, input pluginhost.CompositionRun) (_ json.RawMessage, err error) {
	if (input.ID == "") == (input.Agent == "") || strings.TrimSpace(input.Prompt) == "" {
		return nil, serviceError("invalid_params", "provide prompt and exactly one of id or agent")
	}
	c.mu.Lock()
	id := input.ID
	if input.Agent != "" {
		id = c.bindings[compositionAgent{caller, input.Agent}]
	}
	scope, err := c.scope(caller, id)
	if err != nil {
		c.mu.Unlock()
		return nil, err
	}
	// This capture is the admission boundary. Bind/dispose cannot redirect it.
	scope.running++
	c.mu.Unlock()
	defer func() {
		c.mu.Lock()
		scope.running--
		closeScope := scope.disposed && scope.running == 0
		if closeScope {
			delete(c.scopes, scope.id)
		}
		c.mu.Unlock()
		if closeScope {
			err = errors.Join(err, scope.close())
		}
	}()
	// A new execution owns its receipt and service-completion chain. Inheriting
	// the caller's completion chain would make disposal wait on its own RPC.
	// Only cancellation crosses this composition boundary.
	callerCtx := ctx
	runCtx, cancel := context.WithCancelCause(context.Background())
	stopCaller := context.AfterFunc(callerCtx, func() { cancel(context.Cause(callerCtx)) })
	stopScope := context.AfterFunc(scope.ctx, func() { cancel(context.Cause(scope.ctx)) })
	defer func() { stopCaller(); stopScope(); cancel(context.Canceled) }()
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	ctx = runCtx
	executionID := rand.Text()
	if _, err := scope.runtime.Sessions.Create(executionID); err != nil {
		return nil, err
	}
	thread, err := scope.runtime.NewThreadRuntimeForRoot(executionID, scope.runtime.RootDir)
	if err != nil {
		return nil, err
	}
	defer func() {
		if control := thread.AgentControl; control != nil {
			control.BeginShutdown()
			control.StopAll()
			control.YieldWorkerTerminalFinalizations()
			err = errors.Join(err, control.WaitOwnedWorkerExecutions(context.Background()), shutdownAndCleanupAgentControl(control))
		}
		err = errors.Join(err, scope.runtime.releasePluginGeneration(thread.PluginGeneration))
	}()
	ctx = loopdriver.WithExecutionContext(ctx, loopdriver.ExecutionContext{SessionID: executionID, ExecutionID: executionID})
	content, runErr := thread.StreamRunner.Run(ctx, input.Prompt)
	result := pluginhost.CompositionRunResult{ID: scope.id, ExecutionID: executionID, Content: content}
	if runErr != nil {
		result.Error = runErr.Error()
	}
	return json.Marshal(result)
}

func (s *compositionScope) close() error {
	s.productResources.mu.Lock()
	delete(s.productResources.scopes, s.id)
	s.productResources.mu.Unlock()
	s.cancel()
	_, err := s.runtime.Cleanup()
	// Preserve state for diagnosis if shutdown could not complete safely.
	if err != nil {
		return fmt.Errorf("close composition %s (state retained at %s): %w", s.id, s.directory, err)
	}
	return os.RemoveAll(s.directory)
}

func (c *compositionService) close() error {
	c.mu.Lock()
	c.closed = true
	c.cancel()
	c.mu.Unlock()
	c.calls.Wait()
	c.mu.Lock()
	scopes := c.scopes
	c.scopes = make(map[string]*compositionScope)
	c.mu.Unlock()
	var err error
	for _, scope := range scopes {
		err = errors.Join(err, scope.close())
	}
	return err
}

// Caller retirement closes idle scopes and cancels admitted work. The run's
// finalizer retains providers until its physical execution has drained.
func (c *compositionService) stopCaller(plugin string) error {
	c.mu.Lock()
	c.stopped[plugin] = true
	var idle []*compositionScope
	for id, scope := range c.scopes {
		if scope.caller.plugin != plugin {
			continue
		}
		scope.disposed = true
		scope.cancel()
		if scope.running == 0 {
			delete(c.scopes, id)
			idle = append(idle, scope)
		}
	}
	for key := range c.bindings {
		if key.caller.plugin == plugin {
			delete(c.bindings, key)
		}
	}
	c.mu.Unlock()
	var err error
	for _, scope := range idle {
		err = errors.Join(err, scope.close())
	}
	return err
}
