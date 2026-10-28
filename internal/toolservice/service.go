package toolservice

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"sync"

	"github.com/blueberrycongee/wuu/internal/agent"
	"github.com/blueberrycongee/wuu/internal/modelwire"
	"github.com/blueberrycongee/wuu/internal/pluginhost"
	"github.com/blueberrycongee/wuu/internal/pluginprotocol"
	"github.com/blueberrycongee/wuu/internal/providers"
	"github.com/blueberrycongee/wuu/internal/toolresult"
	"github.com/blueberrycongee/wuu/internal/tools"
)

// Backend is initialized only by the selected native provider. Executor includes
// default tool contributions; Toolkit supplies native profile/discovery state.
type Backend struct {
	Executor agent.ToolExecutor
	Toolkit  *tools.Toolkit
	Close    func(context.Context) error
}

// Scope carries an inert factory for one authenticated conversation/actor.
// Allocating it must not initialize tools, MCP connections or Code Mode.
type Scope struct {
	factory func(context.Context) (Backend, error)
}

func NewScope(factory func(context.Context) (Backend, error)) *Scope { return &Scope{factory: factory} }

type scopeKey struct{}

// WithScope is host-only: provider payloads cannot select another actor's scope.
func WithScope(ctx context.Context, scope *Scope) context.Context {
	return context.WithValue(ctx, scopeKey{}, scope)
}

type backendEntry struct {
	ready   chan struct{}
	backend Backend
	err     error
}

// Service owns scoped default backend initialization and teardown. External
// selections never invoke these factories and never inherit this tool catalog.
type Service struct {
	mu        sync.Mutex
	state     pluginhost.State
	backends  map[*Scope]*backendEntry
	closeDone chan struct{}
	closeErr  error
}

func NewService() *Service {
	return &Service{state: pluginhost.StatePrepared, backends: make(map[*Scope]*backendEntry)}
}
func (*Service) ID() string { return DefaultModule }
func (s *Service) Status() pluginhost.Status {
	s.mu.Lock()
	defer s.mu.Unlock()
	return pluginhost.Status{ID: s.ID(), State: s.state}
}
func (*Service) ProvidedServices() []pluginhost.ServiceDescriptor {
	return []pluginhost.ServiceDescriptor{Descriptor()}
}
func (*Service) RequiredServices() []pluginhost.ServiceRequirement { return nil }
func (s *Service) Activate(ctx context.Context) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.state != pluginhost.StatePrepared {
		return fmt.Errorf("tool provider cannot activate from %s", s.state)
	}
	s.state = pluginhost.StateActive
	return nil
}

// Close stops admission immediately. A canceled graph drain may leave factories
// running; cleanup retains ownership of their eventual results independently of
// the caller's deadline. Repeated Close calls observe the same cleanup outcome.
func (s *Service) Close(ctx context.Context) error {
	s.mu.Lock()
	if s.closeDone == nil {
		s.state = pluginhost.StateStopped
		s.closeDone = make(chan struct{})
		backends := s.backends
		s.backends = nil
		// Late resources need a live cleanup context even after the graph's drain
		// deadline. Never restart or repeat a cleanup callback after cancellation.
		cleanupCtx := context.WithoutCancel(ctx)
		go func() {
			outcomes := make(chan error, len(backends))
			for _, entry := range backends {
				go func() {
					var err error
					defer func() {
						// Preserve the host lifecycle panic boundary after moving
						// cleanup out of its synchronous Close call stack.
						if value := recover(); value != nil {
							err = fmt.Errorf("tool backend cleanup panicked: %v", value)
						}
						outcomes <- err
					}()
					<-entry.ready
					if entry.backend.Close != nil {
						err = entry.backend.Close(cleanupCtx)
					}
				}()
			}
			var errs []error
			for range backends {
				errs = append(errs, <-outcomes)
			}
			s.mu.Lock()
			s.closeErr = errors.Join(errs...)
			close(s.closeDone)
			s.mu.Unlock()
		}()
	}
	done := s.closeDone
	s.mu.Unlock()
	select {
	case <-done:
		s.mu.Lock()
		defer s.mu.Unlock()
		return s.closeErr
	case <-ctx.Done():
		return ctx.Err()
	}
}
func (s *Service) backend(ctx context.Context) (Backend, error) {
	scope, _ := ctx.Value(scopeKey{}).(*Scope)
	if scope == nil || scope.factory == nil {
		return Backend{}, &pluginhost.HostServiceError{Code: "invalid_execution_scope", Message: "tool service requires a host-bound scope"}
	}
	s.mu.Lock()
	if s.state != pluginhost.StateActive {
		s.mu.Unlock()
		return Backend{}, &pluginhost.HostServiceError{Code: "service_unavailable", Message: "tool provider is not active"}
	}
	entry, exists := s.backends[scope]
	if !exists {
		entry = &backendEntry{ready: make(chan struct{})}
		s.backends[scope] = entry
	}
	s.mu.Unlock()
	if exists {
		select {
		case <-ctx.Done():
			return Backend{}, ctx.Err()
		case <-entry.ready:
			return s.initializedBackend(entry)
		}
	}
	// Factories can call other selected services; never hold the provider lock
	// across them. Concurrent calls share this one outcome, including failure.
	entry.backend, entry.err = scope.factory(ctx)
	if entry.err == nil && entry.backend.Executor == nil {
		entry.err = errors.New("tool backend factory returned no executor")
	}
	close(entry.ready)
	return s.initializedBackend(entry)
}

// Initialization is not dispatch admission after the provider has stopped.
func (s *Service) initializedBackend(entry *backendEntry) (Backend, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.state != pluginhost.StateActive {
		return Backend{}, &pluginhost.HostServiceError{Code: "service_unavailable", Message: "tool provider stopped during initialization"}
	}
	return entry.backend, entry.err
}
func (s *Service) InvokeService(ctx context.Context, p pluginhost.ServiceInvokeParams) (json.RawMessage, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if p.Service != ServiceName {
		return nil, &pluginhost.HostServiceError{Code: "service_not_found", Message: "unknown tool service"}
	}
	switch p.Method {
	case "list", "resolve", "execute", "freeze", "unfreeze", "prepare", "finalize":
	default:
		return nil, &pluginhost.HostServiceError{Code: "method_not_found", Message: "unknown tool service method"}
	}
	var input CallRequest
	var preparation PrepareRequest
	var finalization FinalizeRequest
	if p.Method == "resolve" || p.Method == "execute" {
		if err := decode(p.Params, &input); err != nil {
			return nil, err
		}
		if input.Call.Name == "" {
			return nil, &pluginhost.HostServiceError{Code: "invalid_request", Message: "tool name is required"}
		}
	} else if p.Method == "finalize" {
		if err := decode(p.Params, &finalization); err != nil {
			return nil, err
		}
		if finalization.Call.Name == "" || finalization.Result == nil {
			return nil, &pluginhost.HostServiceError{Code: "invalid_request", Message: "finalize requires call and result"}
		}
		if err := finalization.Result.Validate(); err != nil {
			return nil, err
		}
	} else if p.Method == "prepare" {
		if err := decode(p.Params, &preparation); err != nil {
			return nil, err
		}
		if preparation.History == nil {
			return nil, &pluginhost.HostServiceError{Code: "invalid_request", Message: "prepare requires a history array"}
		}
	} else if err := decode(p.Params, &struct{}{}); err != nil {
		return nil, err
	}
	if p.Method == "execute" {
		// The default implementation has no private-scope shortcut: only its own
		// admitted callback may execute tools, just like an external provider.
		if _, err := p.Execution(ctx, s.ID(), true); err != nil {
			return nil, err
		}
	}
	backend, err := s.backend(ctx)
	if err != nil {
		return nil, err
	}
	executor := backend.Executor
	var result any
	switch p.Method {
	case "finalize":
		settled := agent.FinalizeToolOutcome(executor, finalization.Call, *finalization.Result)
		result = ExecutionResult{Result: settled.Result, DiscoveredTools: settled.DiscoveredTools, AdditionalContext: settled.AdditionalContext}
	case "prepare":
		prepared := PrepareResult{Messages: []pluginprotocol.ModelMessageV1{}}
		if preparer, ok := executor.(agent.ModelStepPreparer); ok {
			history := make([]providers.ChatMessage, 0, len(preparation.History))
			for _, message := range preparation.History {
				history = append(history, modelwire.ChatMessageFromWire(message))
			}
			messages, prepareErr := preparer.PrepareModelStep(ctx, history)
			if prepareErr != nil {
				return nil, prepareErr
			}
			for _, message := range messages {
				prepared.Messages = append(prepared.Messages, modelwire.ChatMessageToWire(message))
			}
		}
		result = prepared
	case "freeze", "unfreeze":
		if freezer, ok := executor.(interface {
			FreezeToolSurface()
			UnfreezeToolSurface()
		}); ok {
			if p.Method == "freeze" {
				freezer.FreezeToolSurface()
			} else {
				freezer.UnfreezeToolSurface()
			}
		}
		result = struct{}{}
	case "list":
		definitions := executor.Definitions()
		view := ListResult{Tools: make([]Definition, 0, len(definitions)), Catalog: []tools.DeferredToolCatalogEntry{}}
		for _, d := range definitions {
			view.Tools = append(view.Tools, Definition{Name: d.Name, Description: d.Description, InputSchema: d.InputSchema, Freeform: d.Freeform, DirectOnly: d.DirectOnly, DeferLoading: d.DeferLoading, CacheStable: d.CacheStable})
		}
		if kit := backend.Toolkit; kit != nil {
			view.Catalog = kit.DeferredToolCatalogEntries()
			if view.Catalog == nil {
				view.Catalog = []tools.DeferredToolCatalogEntry{}
			}
			view.CatalogPrompt, err = kit.DeferredToolCatalogSystemSection()
			if err != nil {
				return nil, err
			}
			view.Surface = kit.ActiveSurface()
			view.SystemFragment = view.Surface.SystemFragment
		}
		result = view
	case "resolve":
		resolved := ResolveResult{}
		if provider, ok := executor.(agent.ToolMetadataProvider); ok {
			m, found := provider.ToolMetadata(input.Call)
			resolved.Found = found
			if found {
				resolved.Metadata = &Metadata{Orchestrator: m.Orchestrator, ReadOnly: m.ReadOnly, ConcurrencySafe: m.ConcurrencySafe, Destructive: m.Destructive, Risk: m.Risk, Reason: m.Reason}
			}
		}
		if provider, ok := executor.(agent.ToolSupportProvider); ok {
			resolved.Supports = provider.SupportsTool(input.Call.Name)
		} else {
			resolved.Supports = resolved.Found
		}
		if provider, ok := executor.(agent.ToolDisplayProvider); ok {
			if display, found := provider.ToolDisplay(input.Call); found {
				resolved.Display = &display
			}
		}
		result = resolved
	case "execute":
		var value toolresult.Result
		if rich, ok := executor.(agent.RichToolExecutor); ok {
			value, err = rich.ExecuteResult(ctx, input.Call)
		} else {
			var text string
			text, err = executor.Execute(ctx, input.Call)
			value = toolresult.FromText(text)
		}
		if err != nil {
			return nil, err
		}
		execution := ExecutionResult{Result: value}
		if discovery, ok := executor.(agent.ToolDiscoveryProvider); ok {
			execution.DiscoveredTools = discovery.DiscoveredTools(input.Call)
		}
		if provider, ok := executor.(agent.ToolCallContextProvider); ok {
			execution.AdditionalContext = provider.TakeAdditionalContext(input.Call)
		}
		result = execution
	}
	return json.Marshal(result)
}
func decode(raw json.RawMessage, target any) error {
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(target); err != nil {
		return &pluginhost.HostServiceError{Code: "invalid_request", Message: err.Error()}
	}
	if err := decoder.Decode(new(any)); err != io.EOF {
		return &pluginhost.HostServiceError{Code: "invalid_request", Message: "expected one JSON value"}
	}
	return nil
}
