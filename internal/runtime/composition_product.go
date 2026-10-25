package runtime

import (
	"context"
	"crypto/rand"
	"encoding/json"
	"errors"
	"sync"
	"sync/atomic"

	"github.com/blueberrycongee/wuu/internal/agentengine"
	"github.com/blueberrycongee/wuu/internal/persistence"
	"github.com/blueberrycongee/wuu/internal/pluginhost"
	"github.com/blueberrycongee/wuu/internal/session"
)

const parentStateModule = "wuu.composition.parent-state"

// The parent owns this bridge through its composition service. Its dependency
// edges keep both state services alive until child scopes finish cleanup.
// No permanent generation retain is taken: that would form an ownership cycle.
type compositionStateImport struct {
	parent *pluginhost.Host
	mu     sync.Mutex
	state  pluginhost.State
}

func (p *compositionStateImport) ID() string { return parentStateModule }
func (p *compositionStateImport) Status() pluginhost.Status {
	p.mu.Lock()
	defer p.mu.Unlock()
	return pluginhost.Status{ID: p.ID(), State: p.state}
}
func (p *compositionStateImport) ProvidedServices() []pluginhost.ServiceDescriptor {
	services := append((&persistence.Service{}).ProvidedServices(), (&session.Service{}).ProvidedServices()...)
	return append(services, (&artifactService{}).ProvidedServices()...)
}
func (p *compositionStateImport) RequiredServices() []pluginhost.ServiceRequirement { return nil }
func (p *compositionStateImport) Prepare(ctx context.Context) error                 { return ctx.Err() }
func (p *compositionStateImport) Activate(ctx context.Context) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	p.mu.Lock()
	p.state = pluginhost.StateActive
	p.mu.Unlock()
	return nil
}
func (p *compositionStateImport) Close(context.Context) error {
	p.mu.Lock()
	p.state = pluginhost.StateStopped
	p.mu.Unlock()
	return nil
}
func (p *compositionStateImport) InvokeService(ctx context.Context, input pluginhost.ServiceInvokeParams) (json.RawMessage, error) {
	consumer := sessionStateConsumer
	if input.Service == ArtifactServiceName {
		consumer = kernelHostModule
	}
	result, err := p.parent.CallService(ctx, consumer, input.Service, input.Method, input.Params)
	if err != nil {
		return nil, err
	}
	return result, nil
}

// Resources are process-local. Session selection lives exclusively in documents;
// this table cannot choose a provider or erase a persisted unavailable selection.
type productCompositionBindings struct {
	mu     sync.Mutex
	scopes map[string]productCompositionBinding
}
type productCompositionBinding struct {
	service *compositionService
	scope   *compositionScope
}

const productCompositionNamespace = "wuu.session_compositions.v1"

type productCompositionRecord struct {
	ID          string                      `json:"id"`
	Owner       string                      `json:"owner,omitempty"`
	Thread      string                      `json:"thread,omitempty"`
	Composition *pluginhost.CompositionView `json:"composition,omitempty"`
}

func readProductComposition(ctx context.Context, documents persistence.Documents, sessionID string) (productCompositionRecord, persistence.Revision, bool, error) {
	view, err := documents.Read(ctx, persistence.Query{Namespace: productCompositionNamespace, Keys: []string{sessionID}})
	if err != nil {
		return productCompositionRecord{}, 0, false, err
	}
	var record productCompositionRecord
	if len(view.Documents) == 0 {
		return record, view.Revision, false, nil
	}
	if err := json.Unmarshal(view.Documents[0].Value, &record); err != nil {
		return record, 0, false, err
	}
	return record, view.Revision, true, nil
}

// Only definite revision conflicts are retried. Unknown commit outcomes propagate;
// callers inspect the durable selection before deciding whether to write again.
func writeProductComposition(ctx context.Context, documents persistence.Documents, sessionID, expected string, record productCompositionRecord, initialize bool) (string, error) {
	raw, err := json.Marshal(record)
	if err != nil {
		return "", err
	}
	for {
		previous, revision, found, err := readProductComposition(ctx, documents, sessionID)
		if err != nil {
			return "", err
		}
		if initialize && found {
			return previous.ID, nil
		}
		if !initialize && previous.ID != expected {
			return previous.ID, serviceError("binding_conflict", "session binding changed; inspect before retrying")
		}
		_, err = documents.Commit(ctx, persistence.Transaction{ID: rand.Text(), ExpectedRevision: revision,
			Writes: []persistence.Write{{Namespace: productCompositionNamespace, Key: sessionID, Value: raw}}})
		if errors.Is(err, persistence.ErrConflict) {
			continue
		}
		return previous.ID, err
	}
}

func (c *compositionService) bindSession(ctx context.Context, caller compositionOwner, input pluginhost.CompositionBind) (json.RawMessage, error) {
	binding, err := c.parent.retainState()
	if err != nil {
		return nil, err
	}
	defer binding.Close()
	if target, found, err := binding.Sessions.Find(input.Session); err != nil {
		return nil, err
	} else if !found {
		return nil, serviceError("session_not_found", "product session does not exist")
	} else if input.ID != "" && agentengine.NormalizeEngineID(target.EngineID) != agentengine.EngineWuu {
		return nil, serviceError("invalid_params", "product compositions require the native Wuu engine")
	}
	c.parent.mu.RLock()
	owner := c.parent.stateOwner
	c.parent.mu.RUnlock()
	if owner == nil {
		return nil, serviceError("service_unavailable", "product runtime is unavailable")
	}
	record := productCompositionRecord{ID: input.ID, Owner: caller.plugin, Thread: caller.thread}
	// Serialize disposal with publication. Resources are registered before the
	// durable write so even a lost commit response leaves the selection usable.
	c.mu.Lock()
	defer c.mu.Unlock()
	if input.ID != "" {
		scope, err := c.scope(caller, input.ID)
		if err != nil {
			return nil, err
		}
		if scope.stateSource != "parent" {
			return nil, serviceError("invalid_params", "product sessions require state: parent")
		}
		view := c.view(scope)
		record.Composition = &view
		bindings := owner.productCompositions
		bindings.mu.Lock()
		bindings.scopes[scope.id] = productCompositionBinding{service: c, scope: scope}
		bindings.mu.Unlock()
	}
	previous, err := writeProductComposition(ctx, binding.Documents, input.Session, *input.Expected, record, false)
	if err != nil {
		return nil, err
	}
	return json.Marshal(pluginhost.CompositionBinding{Session: input.Session, ID: input.ID, Previous: previous})
}

// SessionCompositionID reads the authoritative selection, including identities
// whose process resources no longer exist. Storage errors never mean "default".
func (s *Session) SessionCompositionID(ctx context.Context, binding *State, sessionID string) (string, error) {
	record, _, _, err := readProductComposition(ctx, binding.Documents, sessionID)
	return record.ID, err
}

func (s *Session) productCompositionResource(id string) productCompositionBinding {
	if s.productCompositions == nil {
		return productCompositionBinding{}
	}
	s.productCompositions.mu.Lock()
	defer s.productCompositions.mu.Unlock()
	return s.productCompositions.scopes[id]
}

func (c *compositionService) inspectSession(ctx context.Context, sessionID string) (json.RawMessage, error) {
	if sessionID == "" {
		return nil, serviceError("invalid_params", "session is required")
	}
	binding, err := c.parent.retainState()
	if err != nil {
		return nil, err
	}
	defer binding.Close()
	record, _, _, err := readProductComposition(ctx, binding.Documents, sessionID)
	if err != nil {
		return nil, err
	}
	view := pluginhost.CompositionSessionView{Session: sessionID, ID: record.ID, State: "default",
		Owner: record.Owner, Thread: record.Thread, Composition: record.Composition}
	if record.ID != "" {
		view.State = "unavailable"
		c.parent.mu.RLock()
		owner := c.parent.stateOwner
		c.parent.mu.RUnlock()
		if owner != nil {
			selected := owner.productCompositionResource(record.ID)
			if selected.scope != nil {
				selected.service.mu.Lock()
				if err := selected.available(binding); err == nil {
					current := selected.service.view(selected.scope)
					view.Composition, view.State = &current, "active"
				}
				selected.service.mu.Unlock()
			}
		}
	}
	return json.Marshal(view)
}

// State imports preserve the captured ancestor authority. A nested scope's
// direct parent remains its diagnostic and resource owner, but is not a new
// durable authority merely because it exposes the same services through a bridge.
func compositionStateGeneration(generation *PluginGeneration) *PluginGeneration {
	for generation != nil && generation.productParent != nil {
		parent := generation.productParent
		parent.mu.RLock()
		generation = parent.stateGeneration
		parent.mu.RUnlock()
	}
	return generation
}

// available requires the owning composition service lock.
func (b productCompositionBinding) available(binding *State) error {
	if b.service.closed || b.scope.disposed || b.service.stopped[b.scope.caller.plugin] {
		return serviceError("composition_unavailable", "selected product composition is disposed")
	}
	authority := compositionStateGeneration(binding.Generation)
	if authority == nil || authority.id != b.scope.stateGeneration {
		return serviceError("composition_state_changed", "recreate the composition against the current product state")
	}
	return nil
}

// CheckSessionComposition fences reuse of cached runtimes as well as creation.
// The returned ID is an admission snapshot, never a second selection authority.
func (s *Session) CheckSessionComposition(ctx context.Context, binding *State, sessionID string) (string, error) {
	id, err := s.SessionCompositionID(ctx, binding, sessionID)
	if err != nil || id == "" {
		return id, err
	}
	selected := s.productCompositionResource(id)
	if selected.scope == nil {
		return id, serviceError("composition_unavailable", "selected product composition has no live resources; inspect and rebind")
	}
	selected.service.mu.Lock()
	defer selected.service.mu.Unlock()
	return id, selected.available(binding)
}

func (s *Session) productThreadRuntime(binding *State, sessionID, root string, selected ThreadModelSelection) (*ThreadRuntime, bool, error) {
	id, err := s.SessionCompositionID(context.Background(), binding, sessionID)
	if err != nil {
		return nil, true, err
	}
	if id == "" {
		return nil, false, nil
	}
	selection := s.productCompositionResource(id)
	if selection.scope == nil {
		return nil, true, serviceError("composition_unavailable", "selected product composition has no live resources; inspect and rebind")
	}
	c, scope := selection.service, selection.scope
	c.mu.Lock()
	if err := selection.available(binding); err != nil {
		c.mu.Unlock()
		return nil, true, err
	}
	c.calls.Add(1)
	scope.running++
	c.mu.Unlock()
	release := func() error {
		defer c.calls.Done()
		c.mu.Lock()
		scope.running--
		closeScope := scope.disposed && scope.running == 0
		if closeScope {
			delete(c.scopes, scope.id)
		}
		c.mu.Unlock()
		if closeScope {
			return scope.close()
		}
		return nil
	}
	// A composition's model choice takes precedence over the conversation's
	// workspace model defaults; permission and speed remain conversation choices.
	captured := selected
	selected.Provider, selected.Model = scope.runtime.ProviderName, scope.runtime.Model
	selected.Variant, selected.Effort = scope.runtime.StreamRunner.Variant, scope.runtime.StreamRunner.Effort
	shadow, err := scope.runtime.cloneForThreadModel()
	if err != nil {
		return nil, true, errors.Join(err, release())
	}
	defer scope.runtime.releasePluginGeneration(shadow.pluginGeneration)
	shadow.StateDir, shadow.SessionDir, shadow.WuuHome = s.StateDir, s.SessionDir, s.WuuHome
	thread, err := shadow.NewThreadRuntimeForRootModel(sessionID, root, selected)
	if err != nil {
		return nil, true, errors.Join(err, release())
	}
	thread.Selection = captured
	thread.CompositionID = scope.id
	thread.compositionBinding = selection
	thread.closeComposition = func() error {
		err := scope.runtime.releasePluginGeneration(thread.PluginGeneration)
		thread.PluginGeneration = nil
		return errors.Join(err, release())
	}
	return thread, true, nil
}

// InheritSessionComposition snapshots the parent's admitted binding when a
// child is created. Later parent edits cannot redirect that child's turns.
// An idle parent contributes its selected binding instead.
func (s *Session) InheritSessionComposition(ctx context.Context, binding *State, parentID, childID string, admitted *ThreadRuntime) error {
	if parentID == "" {
		return nil
	}
	var record productCompositionRecord
	if admitted != nil {
		record.ID = admitted.CompositionID
		if selected := admitted.compositionBinding; selected.scope != nil {
			selected.service.mu.Lock()
			view := selected.service.view(selected.scope)
			record.Owner, record.Thread, record.Composition = selected.scope.caller.plugin, selected.scope.caller.thread, &view
			selected.service.mu.Unlock()
		}
	} else {
		var err error
		record, _, _, err = readProductComposition(ctx, binding.Documents, parentID)
		if err != nil {
			return err
		}
	}
	_, err := writeProductComposition(ctx, binding.Documents, childID, "", record, true)
	return err
}

// A retained execution owns the scope's processes and directory as well as its
// generation. State clones share this lease until their final consumer closes.
type compositionExecutionLease struct {
	refs    atomic.Int32
	binding productCompositionBinding
}

func (l *compositionExecutionLease) release() error {
	if l.refs.Add(-1) != 0 {
		return nil
	}
	c, scope := l.binding.service, l.binding.scope
	defer c.calls.Done()
	c.mu.Lock()
	scope.running--
	closeScope := scope.disposed && scope.running == 0
	if closeScope {
		delete(c.scopes, scope.id)
	}
	c.mu.Unlock()
	if closeScope {
		return scope.close()
	}
	return nil
}

// RetainExecution keeps an admitted invocation's exact implementation alive for
// asynchronous work. Disposal drains continuations of live work; it cannot
// resurrect an idle disposed scope.
func (s *State) RetainExecution() (*State, error) {
	held := s.Retain()
	if held == nil {
		return nil, serviceError("generation_closed", "execution state is closed")
	}
	if held.execution != nil || held.Generation == nil || held.Generation.composition.scope == nil {
		return held, nil
	}
	selected := held.Generation.composition
	c, scope := selected.service, selected.scope
	c.mu.Lock()
	if !scope.published || c.closed || c.stopped[scope.caller.plugin] || (scope.disposed && scope.running == 0) {
		c.mu.Unlock()
		_ = held.Close()
		return nil, serviceError("composition_unavailable", "execution composition is preparing or closed")
	}
	c.calls.Add(1)
	scope.running++
	c.mu.Unlock()
	held.execution = &compositionExecutionLease{binding: selected}
	held.execution.refs.Store(1)
	return held, nil
}

// ExecutionCompositionID identifies the captured implementation, not the latest
// session selection. Empty identifies the product's ordinary generation.
func (s *State) ExecutionCompositionID() string {
	if s.Generation == nil || s.Generation.composition.scope == nil {
		return ""
	}
	return s.Generation.composition.scope.id
}

// RetainCompositionExecution resolves only process resources for an already
// recorded origin. Missing resources never select the product default.
func (s *Session) RetainCompositionExecution(id string) (*State, error) {
	selected := s.productCompositionResource(id)
	if selected.scope == nil {
		return nil, serviceError("composition_unavailable", "request origin composition has no live resources")
	}
	binding, err := selected.scope.runtime.RetainState()
	if err != nil {
		return nil, err
	}
	defer binding.Close()
	return binding.RetainExecution()
}

// RetainSideThreadExecution inherits a running parent's admitted scope. With an
// idle parent it admits the current durable selection, rejecting disposed scopes.
func (s *Session) RetainSideThreadExecution(binding *State, parentID string, admitted *ThreadRuntime) (*State, error) {
	var id string
	if admitted != nil {
		id = admitted.CompositionID
	} else {
		var err error
		id, err = s.SessionCompositionID(context.Background(), binding, parentID)
		if err != nil {
			return nil, err
		}
	}
	if id == "" {
		return binding.RetainExecution()
	}
	held, err := s.RetainCompositionExecution(id)
	if err != nil {
		return nil, err
	}
	if admitted == nil {
		selected := held.Generation.composition
		selected.service.mu.Lock()
		err = selected.available(binding)
		selected.service.mu.Unlock()
		if err != nil {
			return nil, errors.Join(err, held.Close())
		}
	}
	return held, nil
}

// CloseComposition releases the admitted scope after tools, workers and their
// finalizers have drained. Non-composed runtimes use their ordinary owner.
func (t *ThreadRuntime) CloseComposition() error {
	t.compositionCloseOnce.Do(func() {
		if t.closeComposition != nil {
			t.compositionCloseErr = t.closeComposition()
		}
	})
	return t.compositionCloseErr
}
