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
	"time"

	"github.com/blueberrycongee/wuu/internal/composition"
	"github.com/blueberrycongee/wuu/internal/config"
	"github.com/blueberrycongee/wuu/internal/extensions"
	"github.com/blueberrycongee/wuu/internal/loopdriver"
	pluginpkg "github.com/blueberrycongee/wuu/internal/plugin"
	"github.com/blueberrycongee/wuu/internal/pluginhost"
	"github.com/blueberrycongee/wuu/internal/pluginprogram"
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
	id, directory string
	caller        compositionOwner
	ctx           context.Context
	cancel        context.CancelFunc
	runtime       *Session
	selection     composition.Resolved
	plugins       []pluginhost.CompositionPackage
	running       int
	disposed      bool
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
	case "bind":
		var input pluginhost.CompositionBind
		if err := decodeServiceParams(params.Params, &input); err != nil {
			return nil, err
		}
		if strings.TrimSpace(input.Agent) == "" || input.Expected == nil {
			return nil, serviceError("invalid_params", "agent and expected binding are required")
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
	return pluginhost.CompositionView{ID: scope.id, Root: scope.runtime.RootDir, State: "active", Running: scope.running,
		Agents: agents, Selection: scope.selection, Modules: scope.runtime.PluginHost.Statuses(), Plugins: scope.plugins}
}

func (c *compositionService) create(ctx context.Context, caller compositionOwner, input pluginhost.CompositionCreate) (_ json.RawMessage, err error) {
	selection, err := input.Selection.Resolve()
	if err != nil {
		return nil, err
	}
	c.parent.mu.RLock()
	owner, generation := c.parent.stateOwner, c.parent.stateGeneration
	c.parent.mu.RUnlock()
	if owner == nil || generation == nil {
		return nil, serviceError("service_unavailable", "composition creation requires an initialized runtime")
	}
	// Clone the owning generation's configuration, never its mutable state or
	// shell-specific factories. Explicit selection resolves normal public providers.
	encoded, err := json.Marshal(generation.settings)
	if err != nil {
		return nil, err
	}
	var cfg config.Config
	if err = json.Unmarshal(encoded, &cfg); err != nil {
		return nil, err
	}
	cfg.Composition = &input.Selection
	if cfg.Extensions == nil {
		cfg.Extensions = &extensions.Settings{}
	}
	generation.revokedMu.Lock()
	catalog := make([]pluginpkg.Plugin, 0, len(generation.active))
	for _, item := range generation.active {
		if !generation.revokedPlugins[item.ID] {
			catalog = append(catalog, item)
		}
	}
	generation.revokedMu.Unlock()
	directory, err := os.MkdirTemp("", "wuu-composition-")
	if err != nil {
		return nil, err
	}
	var runtime *Session
	defer func() {
		if err != nil {
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
	for _, source := range input.Paths {
		if err = ctx.Err(); err != nil {
			return nil, err
		}
		if !filepath.IsAbs(source) {
			source = filepath.Join(owner.RootDir, source)
		}
		cleanup := func() {}
		if filepath.Ext(source) == ".ts" {
			source, cleanup, err = pluginprogram.PrepareSingleFile(ctx, source)
			if err != nil {
				return nil, err
			}
		}
		installed, installErr := pluginpkg.InstallPackage(home, source)
		cleanup()
		if installErr != nil {
			return nil, installErr
		}
		item := installed.Plugin
		if err = cfg.Extensions.RecordGrant(extensions.Grant{SubjectID: item.SubjectID, Fingerprint: item.Fingerprint,
			Scope: extensions.GrantScopeSession, Permissions: item.EffectivePermissions, ApprovedAt: time.Now().UTC()}); err != nil {
			return nil, err
		}
		cfg.Extensions.SetEnabled(item.SubjectID, true)
		replaced := false
		for i := range catalog {
			if catalog[i].ID == item.ID {
				catalog[i], replaced = item, true
				break
			}
		}
		if !replaced {
			catalog = append(catalog, item)
		}
	}
	if err = ctx.Err(); err != nil {
		return nil, err
	}
	runtime, err = NewSession(Options{RootDir: root, HomeDir: directory, WuuHome: home, Config: cfg,
		DiscoveredPlugins: &catalog, NonInteractive: true, DriverProfile: input.DriverProfile,
		ProviderName: owner.ProviderName, ModelOverride: owner.Model})
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
	scope := &compositionScope{id: rand.Text(), caller: caller, directory: directory, runtime: runtime, selection: selection,
		plugins: []pluginhost.CompositionPackage{}}
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
	scope.ctx, scope.cancel = context.WithCancel(c.ctx)
	c.scopes[scope.id] = scope
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
