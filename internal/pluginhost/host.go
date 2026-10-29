package pluginhost

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"sort"
	"strings"
	"sync"
	"unicode"

	"github.com/blueberrycongee/wuu/internal/providers"
	"github.com/blueberrycongee/wuu/internal/toolresult"
	cordis "github.com/tr1v3r/cordis-go"
)

// Client is one initialized plugin runtime. Implementations may host plugins
// in subprocesses or in-process tests, but share the same typed wire contract.
type Client interface {
	ID() string
	Status() Status
	Close(context.Context) error
}

// ToolClient extends a plugin client with its initialized tool registrations
// and the typed execution method for those tools.
type ToolClient interface {
	Client
	Tools() []ToolRegistration
	ExecuteTool(context.Context, ToolExecuteParams) (ToolExecuteResult, error)
}

// CapabilityClient is a protocol-v2 client with negotiated capability calls.
type CapabilityClient interface {
	Client
	ProtocolVersion() int
	Capabilities() []CapabilityDescriptor
	InvokeCapability(context.Context, CapabilityInvokeParams) (CapabilityInvokeResult, error)
}

type preparationClient interface {
	Prepare(context.Context) error
}

type lifecycleClient interface {
	Activate(context.Context) error
}

// RegisteredCapability is the host-owned view of one negotiated capability.
type RegisteredCapability struct {
	PluginID   string
	Descriptor CapabilityDescriptor
	client     CapabilityClient
}

// RegisteredTool is the host-owned public view of a plugin tool registration.
type RegisteredTool struct {
	PublicName   string
	PluginID     string
	Registration ToolRegistration
	client       ToolClient
}

// Host owns the active plugin clients and their negotiated contributions.
type Host struct {
	readCursors     readCursorStore
	modelReceipts   sync.Map
	modelMetrics    map[string]*modelMetricsDelivery
	sensitiveOwners sync.Map

	lifecycleMu         sync.Mutex
	lifecycleRoot       *cordis.Context
	lifetimes           map[string]*clientLifetime
	closed              bool
	shutdown            *hostShutdown
	shutdownDone        chan struct{}
	preparationErr      error
	mu                  sync.RWMutex
	clients             []Client
	tools               map[string]RegisteredTool
	toolOrder           []string
	capabilities        []RegisteredCapability
	diagnostics         map[string]map[string]string
	serviceRegistry     *ServiceRegistry
	dependencies        map[string][]Dependency
	dependencyErrors    map[string]error
	executions          *ExecutionTracker
	background          map[string]*backgroundJob
	backgroundCompleted []string
	backgroundClosed    bool
	materialize         func(context.Context, ToolExecutionScope, *toolresult.Result) error
}

type ContributionDiagnostic struct {
	Contribution string `json:"contribution"`
	Message      string `json:"message"`
}

func New(clients ...Client) *Host {
	host := &Host{tools: make(map[string]RegisteredTool), diagnostics: make(map[string]map[string]string), executions: NewExecutionTracker(), shutdownDone: make(chan struct{})}
	for _, client := range clients {
		host.addLocked(client)
	}
	return host
}

// SetToolResultMaterializer installs the kernel boundary that turns transient
// plugin file references into thread-owned artifacts before execution closes.
func (h *Host) SetToolResultMaterializer(materialize func(context.Context, ToolExecutionScope, *toolresult.Result) error) {
	if h == nil {
		return
	}
	h.mu.Lock()
	h.materialize = materialize
	h.mu.Unlock()
}

// Failed preserves a plugin load failure in the runtime inventory without
// registering any interception points.
func Failed(id string, err error) Client {
	message := "plugin failed to start"
	if err != nil {
		message = err.Error()
	}
	return FailedWithStatus(Status{ID: id, Error: message})
}

// FailedWithStatus retains the original process identity and start time after
// cleanup. Recovery must distinguish a crashed active module from a load failure.
func FailedWithStatus(status Status) Client {
	status.State = StateFailed
	return &failedClient{status: status}
}

func (h *Host) Add(client Client) {
	if h == nil || client == nil {
		return
	}
	h.mu.Lock()
	defer h.mu.Unlock()
	h.addLocked(client)
}

// AttachServiceRegistry binds the generation-scoped service registry to this
// host and records each rejected provider registration as a contribution
// diagnostic.
func (h *Host) AttachServiceRegistry(registry *ServiceRegistry, conflicts []ServiceConflict) {
	if h == nil {
		return
	}
	h.mu.Lock()
	h.serviceRegistry = registry
	if registry != nil {
		registry.bindRootExecutionContext(h.ResolveExecutionContext)
		registry.mu.Lock()
		registry.scopeTracker = h.executions
		registry.moduleStatuses = h.Statuses
		registry.dependencies = h.Dependencies
		registry.mu.Unlock()
	}
	for _, conflict := range conflicts {
		if h.diagnostics[conflict.PluginID] == nil {
			h.diagnostics[conflict.PluginID] = make(map[string]string)
		}
		h.diagnostics[conflict.PluginID][fmt.Sprintf("service:%s@%d", conflict.Service, conflict.Major)] = conflict.Message
	}
	h.mu.Unlock()
	h.resolveServiceDependencies(registry)
}

// ServiceRegistry returns the registry attached at generation build time, or
// nil when the generation declares no service surface.
func (h *Host) ServiceRegistry() *ServiceRegistry {
	if h == nil {
		return nil
	}
	h.mu.RLock()
	defer h.mu.RUnlock()
	return h.serviceRegistry
}

func (h *Host) addLocked(client Client) {
	if client == nil || h.shutdown != nil {
		return
	}
	h.clients = append(h.clients, client)
	// Capabilities were introduced in v2; v3 adds service declarations without
	// invalidating capability-only runtimes.
	if capabilityClient, ok := client.(CapabilityClient); ok && capabilityClient.ProtocolVersion() >= 2 {
		for _, descriptor := range capabilityClient.Capabilities() {
			h.capabilities = append(h.capabilities, RegisteredCapability{
				PluginID: client.ID(), Descriptor: descriptor, client: capabilityClient,
			})
		}
	}
	toolClient, ok := client.(ToolClient)
	if !ok {
		return
	}
	registrations := toolClient.Tools()
	if validateToolRegistrations(registrations) != nil {
		// Process clients reject invalid declarations before activation. Keeping
		// this guard makes in-process clients fail closed without affecting other
		// initialized plugins.
		return
	}
	if h.tools == nil {
		h.tools = make(map[string]RegisteredTool)
	}
	for _, registration := range registrations {
		publicName := h.availablePublicToolName(client.ID(), registration.ID)
		registration = cloneToolRegistration(registration)
		if registration.Display == nil {
			registration.Display = &providers.ToolCallDisplay{}
		}
		if strings.TrimSpace(registration.Display.Label) == "" {
			registration.Display.Label = registration.ID
		}
		h.tools[publicName] = RegisteredTool{
			PublicName:   publicName,
			PluginID:     client.ID(),
			Registration: registration,
			client:       toolClient,
		}
		h.toolOrder = append(h.toolOrder, publicName)
	}
}

// ValidateCapabilities verifies generation-wide dependencies and conflicts.
func (h *Host) ValidateCapabilities() error {
	if h == nil {
		return nil
	}
	h.mu.RLock()
	registered := append([]RegisteredCapability(nil), h.capabilities...)
	h.mu.RUnlock()
	if err := h.validateModelAdapters(); err != nil {
		return err
	}
	present := make(map[string]struct{}, len(registered))
	for _, capability := range registered {
		present[capability.Descriptor.ID] = struct{}{}
	}
	for _, capability := range registered {
		for _, dependency := range capability.Descriptor.DependsOn {
			if _, ok := present[dependency]; !ok {
				return fmt.Errorf("plugin %q capability %q requires missing capability %q", capability.PluginID, capability.Descriptor.ID, dependency)
			}
		}
		for _, conflict := range capability.Descriptor.Conflicts {
			if _, ok := present[conflict]; ok {
				return fmt.Errorf("plugin %q capability %q conflicts with capability %q", capability.PluginID, capability.Descriptor.ID, conflict)
			}
		}
	}
	return nil
}

// Capabilities returns active negotiated capabilities in deterministic
// priority order. Ties preserve plugin discovery order.
func (h *Host) Capabilities(id string) []RegisteredCapability {
	if h == nil {
		return nil
	}
	h.mu.RLock()
	defer h.mu.RUnlock()
	var out []RegisteredCapability
	for _, capability := range h.capabilities {
		if capability.Descriptor.ID != id || !capabilityStateReady(capability.client.Status().State) {
			continue
		}
		copy := capability
		copy.Descriptor = cloneCapabilityDescriptors([]CapabilityDescriptor{capability.Descriptor})[0]
		out = append(out, copy)
	}
	sort.SliceStable(out, func(i, j int) bool {
		return out[i].Descriptor.Priority > out[j].Descriptor.Priority
	})
	return out
}

// Capability returns one active capability owned by the exact plugin.
func (h *Host) Capability(pluginID, id string) (RegisteredCapability, bool) {
	if h == nil {
		return RegisteredCapability{}, false
	}
	pluginID, id = strings.TrimSpace(pluginID), strings.TrimSpace(id)
	h.mu.RLock()
	defer h.mu.RUnlock()
	for _, capability := range h.capabilities {
		if capability.PluginID == pluginID && capability.Descriptor.ID == id && capabilityStateReady(capability.client.Status().State) {
			return capability, true
		}
	}
	return RegisteredCapability{}, false
}

// ErrCapabilityRetired identifies an explicitly removed registration, distinct
// from a runtime failure in a still-registered capability.
var ErrCapabilityRetired = errors.New("capability registration was retired")

// InvokeCapability invokes one exact plugin registration.
func (h *Host) InvokeCapability(ctx context.Context, capability RegisteredCapability, input, output any) (returnErr error) {
	if capability.client == nil {
		return fmt.Errorf("plugin %q capability %q is not active", capability.PluginID, capability.Descriptor.ID)
	}
	inputJSON, err := json.Marshal(input)
	if err != nil {
		return fmt.Errorf("marshal capability %q input: %w", capability.Descriptor.ID, err)
	}
	defer clear(inputJSON)
	if sensitiveModelValidation(capability.Descriptor.ID, inputJSON) {
		h.markSensitiveOwner(capability.PluginID)
	}
	defer func() { returnErr = h.ownerDiagnosticError(capability.PluginID, returnErr) }()
	outputJSON, err := json.Marshal(output)
	if err != nil {
		return fmt.Errorf("marshal capability %q output: %w", capability.Descriptor.ID, err)
	}
	h.mu.RLock()
	registered, admitted := false, false
	for _, current := range h.capabilities {
		if current.PluginID == capability.PluginID && current.Descriptor.ID == capability.Descriptor.ID && current.client == capability.client {
			registered = true
			admitted = capabilityStateReady(current.client.Status().State)
			break
		}
	}
	if !admitted {
		h.mu.RUnlock()
		if !registered {
			return fmt.Errorf("plugin %q capability %q is not active: %w", capability.PluginID, capability.Descriptor.ID, ErrCapabilityRetired)
		}
		return fmt.Errorf("plugin %q capability %q is not active", capability.PluginID, capability.Descriptor.ID)
	}
	executionID, executionCtx := h.executions.begin(capability.PluginID, ctx, ToolExecuteInput{}, false)
	h.mu.RUnlock()
	defer h.executions.End(executionID)
	// Native capabilities inherit the same authenticated callback identity that
	// external capabilities carry in execution_id frames. Authority expires when
	// this callback returns; cached clients cannot retain it for later calls.
	if registry := h.ServiceRegistry(); registry != nil {
		marker := &nativeInvocation{registry: registry, owner: capability.PluginID, executionID: executionID, live: true, root: true}
		defer func() { registry.mu.Lock(); marker.live = false; registry.mu.Unlock() }()
		executionCtx = context.WithValue(executionCtx, inheritedInvocationKey{}, (*nativeInvocation)(nil))
		executionCtx = context.WithValue(executionCtx, nativeInvocationKey{}, marker)
	}
	result, err := capability.client.InvokeCapability(executionCtx, CapabilityInvokeParams{
		Capability:  capability.Descriptor.ID,
		ExecutionID: executionID,
		Input:       inputJSON,
		Output:      outputJSON,
	})
	var invokeErr error
	if err != nil {
		invokeErr = fmt.Errorf("plugin %q capability %q: %w", capability.PluginID, capability.Descriptor.ID, err)
	}
	if invokeErr != nil && (capability.Descriptor.ID != CapabilityModelAdapter || len(result.Output) == 0) {
		return invokeErr
	}
	decoder := json.NewDecoder(bytes.NewReader(result.Output))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(output); err != nil {
		return errors.Join(invokeErr, fmt.Errorf("plugin %q capability %q returned invalid output: %w", capability.PluginID, capability.Descriptor.ID, err))
	}
	var trailing any
	if err := decoder.Decode(&trailing); !errors.Is(err, io.EOF) {
		if err == nil {
			err = errors.New("multiple JSON values")
		}
		return errors.Join(invokeErr, fmt.Errorf("plugin %q capability %q returned invalid output: %w", capability.PluginID, capability.Descriptor.ID, err))
	}
	return invokeErr
}

// HandleCapabilityError applies the negotiated descriptor policy to one
// capability failure. A nil return means the caller must skip this
// contribution and continue dispatch with its previous value.
func (h *Host) HandleCapabilityError(capability RegisteredCapability, err error) error {
	if err == nil {
		return nil
	}
	err = h.ownerDiagnosticError(capability.PluginID, err)
	switch EffectiveErrorPolicy(capability.Descriptor) {
	case ErrorPolicyIsolate:
		h.recordDiagnostic(capability.PluginID, capability.Descriptor.ID, err.Error())
		providers.DebugLogf("plugin %q contribution %q isolated after failure: %v", capability.PluginID, capability.Descriptor.ID, err)
		return nil
	case ErrorPolicyIgnore:
		return nil
	default:
		return err
	}
}

func (h *Host) recordDiagnostic(pluginID, contribution, message string) {
	h.mu.Lock()
	defer h.mu.Unlock()
	byContribution := h.diagnostics[pluginID]
	if byContribution == nil {
		byContribution = make(map[string]string)
		h.diagnostics[pluginID] = byContribution
	}
	if _, sensitive := h.sensitiveOwners.Load(pluginID); sensitive {
		message = sensitiveDiagnostic
	}
	byContribution[contribution] = message
}

func (h *Host) ContributionDiagnostics(pluginID string) []ContributionDiagnostic {
	if h == nil {
		return nil
	}
	h.mu.RLock()
	defer h.mu.RUnlock()
	byContribution := h.diagnostics[strings.TrimSpace(pluginID)]
	contributions := make([]string, 0, len(byContribution))
	for contribution := range byContribution {
		contributions = append(contributions, contribution)
	}
	sort.Strings(contributions)
	out := make([]ContributionDiagnostic, 0, len(contributions))
	for _, contribution := range contributions {
		message := byContribution[contribution]
		if _, sensitive := h.sensitiveOwners.Load(strings.TrimSpace(pluginID)); sensitive {
			message = sensitiveDiagnostic
		}
		out = append(out, ContributionDiagnostic{Contribution: contribution, Message: message})
	}
	return out
}

// ToolDefinitions returns plugin tools in deterministic client and declaration
// order, ready to append to the executor definition surface sent to providers.
func (h *Host) ToolDefinitions() []providers.ToolDefinition {
	if h == nil {
		return nil
	}
	h.mu.RLock()
	defer h.mu.RUnlock()
	definitions := make([]providers.ToolDefinition, 0, len(h.toolOrder))
	for _, name := range h.toolOrder {
		tool := h.tools[name]
		if tool.client.Status().State != StateActive {
			continue
		}
		definitions = append(definitions, providers.ToolDefinition{
			Name:        name,
			DirectOnly:  tool.Registration.DirectOnly,
			Description: tool.Registration.Description,
			InputSchema: cloneSchema(tool.Registration.InputSchema),
		})
	}
	return definitions
}

// DeclaredTool returns an initialized tool while its generation is being prepared.
func (h *Host) DeclaredTool(pluginID, localID string) (RegisteredTool, bool) {
	if h == nil {
		return RegisteredTool{}, false
	}
	h.mu.RLock()
	defer h.mu.RUnlock()
	for _, name := range h.toolOrder {
		tool := h.tools[name]
		if tool.PluginID == pluginID && tool.Registration.ID == localID {
			state := tool.client.Status().State
			if state == StateFailed || state == StateStopped {
				return RegisteredTool{}, false
			}
			tool.Registration = cloneToolRegistration(tool.Registration)
			return tool, true
		}
	}
	return RegisteredTool{}, false
}

// Tool returns a copy of the registration behind a public tool name.
func (h *Host) Tool(name string) (RegisteredTool, bool) {
	if h == nil {
		return RegisteredTool{}, false
	}
	h.mu.RLock()
	defer h.mu.RUnlock()
	tool, ok := h.tools[name]
	if !ok || tool.client.Status().State != StateActive {
		return RegisteredTool{}, false
	}
	tool.Registration = cloneToolRegistration(tool.Registration)
	return tool, true
}

func (h *Host) SupportsTool(name string) bool {
	_, ok := h.Tool(name)
	return ok
}

// ExecuteTool routes a public tool call to its owning plugin and validates the
// structured result before returning it to the runtime.
func (h *Host) ExecuteTool(ctx context.Context, name string, input ToolExecuteInput) (toolresult.Result, error) {
	return h.executeTool(ctx, name, input, "")
}

// ExecuteToolWithVisibleCWD retains the authenticated host scope while sending
// a separately captured environment root to the plugin process.
func (h *Host) ExecuteToolWithVisibleCWD(ctx context.Context, name string, input ToolExecuteInput, cwd string) (toolresult.Result, error) {
	if strings.TrimSpace(cwd) == "" {
		return toolresult.Result{}, errors.New("visible execution root is required")
	}
	return h.executeTool(ctx, name, input, cwd)
}

func (h *Host) executeTool(ctx context.Context, name string, input ToolExecuteInput, visibleCWD string) (toolresult.Result, error) {
	h.mu.RLock()
	tool, ok := h.tools[name]
	if !ok || tool.client.Status().State != StateActive {
		h.mu.RUnlock()
		return toolresult.Result{}, fmt.Errorf("plugin tool %q is not registered", name)
	}
	input.Tool = name
	executionID, executionCtx := h.executions.begin(tool.PluginID, ctx, input, true)
	input.ExecutionID = executionID
	h.mu.RUnlock()
	defer h.executions.End(input.ExecutionID)
	wireInput := input
	if visibleCWD != "" {
		wireInput.CWD = visibleCWD
	}
	response, err := tool.client.ExecuteTool(executionCtx, ToolExecuteParams{
		ToolExecuteInput: wireInput,
		ToolID:           tool.Registration.ID,
	})
	if err != nil {
		return toolresult.Result{}, fmt.Errorf("plugin %q tool %q: %w", tool.PluginID, name, err)
	}
	if err := response.Result.Validate(); err != nil {
		return toolresult.Result{}, fmt.Errorf("plugin %q tool %q returned invalid result: %w", tool.PluginID, name, err)
	}
	h.mu.RLock()
	materialize := h.materialize
	h.mu.RUnlock()
	if materialize != nil {
		scope, scopeErr := h.executions.ResolveTool(tool.PluginID, input.ExecutionID)
		if scopeErr != nil {
			return toolresult.Result{}, scopeErr
		}
		if err := materialize(executionCtx, scope, &response.Result); err != nil {
			return toolresult.Result{}, fmt.Errorf("plugin %q tool %q materialize artifacts: %w", tool.PluginID, name, err)
		}
		if err := response.Result.Validate(); err != nil {
			return toolresult.Result{}, fmt.Errorf("plugin %q tool %q returned invalid materialized result: %w", tool.PluginID, name, err)
		}
	}
	return response.Result.Clone(), nil
}

func capabilityStateReady(state State) bool {
	return state == StateInitialized || state == StatePrepared || state == StateActive
}

// RecordExecutionUpdate is the kernel-side entry for execution.update: the
// caller identity was already declaration-checked by the registry; ownership
// and liveness are enforced by the tracker.
func (h *Host) RecordExecutionUpdate(callerPluginID string, params ExecutionUpdateParams) *HostServiceError {
	if h == nil || h.executions == nil {
		return &HostServiceError{Code: "service_unavailable", Message: "execution scope is unavailable"}
	}
	return h.executions.RecordUpdate(callerPluginID, params)
}

func (h *Host) ResolveToolExecution(callerPluginID, executionID string) (ToolExecutionScope, *HostServiceError) {
	if h == nil || h.executions == nil {
		return ToolExecutionScope{}, &HostServiceError{Code: "service_unavailable", Message: "execution scope is unavailable"}
	}
	return h.executions.ResolveTool(callerPluginID, executionID)
}

// ResolveExecutionContext validates a root tool/capability dispatch owner.
// Registry-owned service IDs keep their separately routed execution context.
func (h *Host) ResolveExecutionContext(callerPluginID, executionID string) (context.Context, bool, *HostServiceError) {
	if h == nil || h.executions == nil {
		return nil, true, &HostServiceError{Code: "service_unavailable", Message: "execution scope is unavailable"}
	}
	return h.executions.ResolveContext(callerPluginID, executionID)
}

func (h *Host) ResolveExecution(callerPluginID, executionID string) (ExecutionScope, *HostServiceError) {
	if h == nil || h.executions == nil {
		return ExecutionScope{}, &HostServiceError{Code: "service_unavailable", Message: "execution scope is unavailable"}
	}
	return h.executions.Resolve(callerPluginID, executionID)
}

func (h *Host) CancelExecutions(cause error) {
	if h != nil && h.executions != nil {
		h.executions.CancelAll(cause)
	}
}

// RetirePlugin stops required consumers before closing their provider. Other
// clients remain active. Cleanup retains access to providers until it returns.
func (h *Host) RetirePlugin(ctx context.Context, pluginID string, cause error) (ClientShutdownOutcome, bool) {
	return h.retirePluginWithFailure(ctx, pluginID, cause, nil)
}

// RetireFailedPlugin detaches executable authority while preserving the failure
// in status inventory, including during cleanup callbacks. The replacement is
// diagnostic only and cannot serve tools, capabilities, or service calls.
func (h *Host) RetireFailedPlugin(ctx context.Context, status Status, cause error) (ClientShutdownOutcome, bool) {
	status.State = StateFailed
	return h.retirePluginWithFailure(ctx, status.ID, cause, &status)
}

func (h *Host) retirePluginWithFailure(ctx context.Context, pluginID string, cause error, failure *Status) (ClientShutdownOutcome, bool) {
	if h == nil {
		return ClientShutdownOutcome{}, false
	}
	type retirementResult struct {
		outcome ClientShutdownOutcome
		found   bool
	}
	done := make(chan retirementResult, 1)
	// The waiter does not own physical cleanup. Retained scopes keep the same
	// graph alive after a deadline until transports actually finish.
	go func() {
		h.lifecycleMu.Lock()
		defer h.lifecycleMu.Unlock()
		outcome, found := h.retirePluginAndConsumers(context.WithoutCancel(ctx), pluginID, cause, failure)
		done <- retirementResult{outcome, found}
	}()
	select {
	case result := <-done:
		return result.outcome, result.found
	case <-ctx.Done():
		return ClientShutdownOutcome{PluginID: pluginID, Err: fmt.Errorf("retirement cleanup pending: %w", ctx.Err())}, true
	}
}

func (h *Host) retirePluginAndConsumers(ctx context.Context, pluginID string, cause error, failure *Status) (ClientShutdownOutcome, bool) {
	pluginID = strings.TrimSpace(pluginID)
	retiring := map[string]bool{pluginID: true}
	dependencies := h.Dependencies()
	for changed := true; changed; {
		changed = false
		for id, edges := range dependencies {
			if retiring[id] {
				continue
			}
			for _, edge := range edges {
				if edge.Required && retiring[edge.Provider] {
					retiring[id], changed = true, true
					break
				}
			}
		}
	}
	clients := h.orderedClients()
	for _, client := range clients {
		if retiring[client.ID()] {
			h.serviceRegistry.stopProviderAdmission(client.ID())
		}
	}
	var result ClientShutdownOutcome
	var found bool
	var cleanupErr error
	for i := len(clients) - 1; i >= 0; i-- {
		id := clients[i].ID()
		if !retiring[id] {
			continue
		}
		var diagnostic *Status
		if id == pluginID {
			diagnostic = failure
		} else if failure != nil {
			status := clients[i].Status()
			if status.State == StateFailed {
				diagnostic = &status
			}
		}
		outcome, retired := h.retirePluginKeepingFailure(ctx, id, cause, diagnostic)
		cleanupErr = errors.Join(cleanupErr, outcome.Err)
		if id == pluginID {
			result, found = outcome, retired
		} else if retired && diagnostic == nil {
			h.Add(Failed(id, fmt.Errorf("plugin %q requires retired plugin %q", id, pluginID)))
		}
	}
	result.Err = cleanupErr
	return result, found
}

func (h *Host) retirePlugin(ctx context.Context, pluginID string, cause error) (ClientShutdownOutcome, bool) {
	return h.retirePluginKeepingFailure(ctx, pluginID, cause, nil)
}

func (h *Host) retirePluginKeepingFailure(ctx context.Context, pluginID string, cause error, failure *Status) (ClientShutdownOutcome, bool) {
	if h == nil {
		return ClientShutdownOutcome{}, false
	}
	pluginID = strings.TrimSpace(pluginID)
	if pluginID == "" {
		return ClientShutdownOutcome{}, false
	}
	if err := h.ownClients(h.orderedClients()); err != nil {
		return ClientShutdownOutcome{PluginID: pluginID, Err: err}, false
	}
	h.mu.Lock()
	var client Client
	keptClients := h.clients[:0]
	for _, candidate := range h.clients {
		if candidate.ID() == pluginID && client == nil {
			client = candidate
			if failure != nil {
				keptClients = append(keptClients, FailedWithStatus(*failure))
			}
			continue
		}
		keptClients = append(keptClients, candidate)
	}
	if client == nil {
		h.mu.Unlock()
		return ClientShutdownOutcome{}, false
	}
	h.retireReadCursors(pluginID)
	h.clients = keptClients
	for name, tool := range h.tools {
		if tool.PluginID == pluginID {
			delete(h.tools, name)
		}
	}
	keptTools := h.toolOrder[:0]
	for _, name := range h.toolOrder {
		if tool, ok := h.tools[name]; ok && tool.PluginID != pluginID {
			keptTools = append(keptTools, name)
		}
	}
	h.toolOrder = keptTools
	keptCapabilities := h.capabilities[:0]
	for _, capability := range h.capabilities {
		if capability.PluginID != pluginID {
			keptCapabilities = append(keptCapabilities, capability)
		}
	}
	h.capabilities = keptCapabilities
	delete(h.diagnostics, pluginID)
	registry := h.serviceRegistry
	h.mu.Unlock()

	if cause == nil {
		cause = &UserQuestionError{Code: "generation_closed", Message: "plugin generation retired"}
	}
	registry.stopProviderAdmission(pluginID)
	h.stopModelMetrics(pluginID)
	if h.executions != nil {
		h.executions.CancelPlugin(pluginID, cause)
	}
	// Contributions and executions are already detached. Keep declared host
	// services available while the plugin persists state and cancels its work.
	closeErr := h.closeClient(ctx, client)
	if registry != nil {
		registry.RevokePlugin(ctx, pluginID)
	}
	return ClientShutdownOutcome{PluginID: pluginID, Err: closeErr}, true
}

// CallService dispatches a native consumer callback through its declared service
// dependencies. Like tool and capability callbacks, it owns a fresh execution
// bounded by ctx and retirement of the consumer. Domain IDs belong in params;
// they never confer execution authority.
func (h *Host) CallService(ctx context.Context, consumer, service, method string, params json.RawMessage) (json.RawMessage, *HostServiceError) {
	return h.callService(ctx, consumer, service, method, params, nil)
}

// CallToolService delegates one native tool dispatch to its selected service.
// Input must come from the host's admitted execution, not provider request data.
// The selected callback receives its own tool identity; ordinary downstream
// service calls do not inherit that authority.
func (h *Host) CallToolService(ctx context.Context, consumer, service, method string, params json.RawMessage, input ToolExecuteInput) (json.RawMessage, *HostServiceError) {
	return h.callService(ctx, consumer, service, method, params, &input)
}

func (h *Host) callService(ctx context.Context, consumer, service, method string, params json.RawMessage, toolScope *ToolExecuteInput) (json.RawMessage, *HostServiceError) {
	if h == nil {
		return nil, &HostServiceError{Code: "service_unavailable", Message: "service host is unavailable"}
	}
	h.mu.RLock()
	if toolScope == nil && InShutdownCleanup(ctx) && h.serviceRegistry != nil {
		registry := h.serviceRegistry
		h.mu.RUnlock()
		// Like ordinary CallService, cleanup starts a native consumer call.
		// A bridge may carry another graph's callback marker; it cannot lend
		// that marker to this consumer or constrain this fresh invocation.
		ctx = context.WithValue(ctx, nativeInvocationKey{}, struct{}{})
		return registry.Call(ctx, consumer, ServiceCallParams{Service: service, Method: method, Params: params})
	}
	if h.shutdown != nil {
		h.mu.RUnlock()
		return nil, &HostServiceError{Code: "generation_closed", Message: "plugin generation is closing"}
	}
	var admitted bool
	for _, client := range h.clients {
		if client.ID() == consumer && client.Status().State == StateActive {
			admitted = true
			break
		}
	}
	registry := h.serviceRegistry
	if !admitted || registry == nil {
		h.mu.RUnlock()
		return nil, &HostServiceError{Code: "service_unavailable", Message: fmt.Sprintf("service consumer %q is not active", consumer)}
	}
	input := ToolExecuteInput{}
	if toolScope != nil {
		input = *toolScope
	}
	executionID, executionCtx := h.executions.begin(consumer, ctx, input, toolScope != nil)
	h.mu.RUnlock()
	defer h.executions.End(executionID)
	return registry.call(executionCtx, consumer, ServiceCallParams{
		Service: service, Method: method, Params: params, ExecutionID: executionID,
	}, toolScope)
}

// ExecutionSnapshots returns the host's live execution table for diagnostics.
func (h *Host) ExecutionSnapshots() []ExecutionSnapshot {
	if h == nil || h.executions == nil {
		return nil
	}
	return h.executions.Snapshot()
}

// Prepare validates a candidate in dependency order before durable commit.
// Preparation methods may read selected providers but must not publish effects.
func (h *Host) Prepare(ctx context.Context) error {
	h.lifecycleMu.Lock()
	defer h.lifecycleMu.Unlock()
	return h.prepare(ctx, nil)
}

// PrepareAvailable quarantines optional preparation failures and their required
// consumers while preserving independent clients. A non-nil required set names
// the clients that must remain usable; nil preserves strict Prepare behavior.
// Explicit service bindings remain mandatory regardless of the required set.
func (h *Host) PrepareAvailable(ctx context.Context, required map[string]bool) error {
	h.lifecycleMu.Lock()
	defer h.lifecycleMu.Unlock()
	return h.prepare(ctx, required)
}

func (h *Host) prepare(ctx context.Context, required map[string]bool) (err error) {
	h.mu.RLock()
	closing := h.shutdown != nil
	h.mu.RUnlock()
	if h.closed || closing {
		return errors.New("plugin generation is closed")
	}
	if h.preparationErr != nil {
		return h.preparationErr
	}
	defer func() {
		if err != nil {
			h.preparationErr = err
		}
	}()
	if _, err := h.DependencyOrder(); err != nil {
		return err
	}
	h.blockFailedDependencies(ctx)
	clients := h.orderedClients()
	if err := h.ownClients(clients); err != nil {
		return err
	}
	dependencies := h.Dependencies()
	blocked := h.DependencyFailures()
	for _, client := range clients {
		if blocked[client.ID()] != nil || client.Status().State == StateFailed || client.Status().State == StateStopped {
			continue
		}
		if err := h.prepareClient(ctx, client, dependencies[client.ID()]); err != nil {
			failure := fmt.Errorf("prepare plugin %q: %w", client.ID(), err)
			if required == nil || required[client.ID()] || ctx.Err() != nil {
				return failure
			}
			// Close consumers before providers and retain diagnostic-only clients.
			// This shares activation's cleanup barrier: snapshot owners cannot
			// release a failed runtime's resources while its cleanup is pending.
			outcome, _ := h.retirePluginAndConsumers(ctx, client.ID(), failure, nil)
			h.Add(Failed(client.ID(), errors.Join(failure, outcome.Err)))
			h.blockFailedDependencies(ctx)
			blocked = h.DependencyFailures()
		}
	}
	if required != nil {
		for _, status := range h.Statuses() {
			if required[status.ID] && (status.State == StateFailed || status.State == StateStopped) {
				return fmt.Errorf("prepare required plugin %q: %s", status.ID, status.Error)
			}
		}
		if h.serviceRegistry != nil {
			return h.serviceRegistry.ValidateBindings()
		}
	}
	return nil
}

// Activate opens the effectful lifecycle of prepared runtimes. The caller owns
// the durable commit and publication boundary; activation effects cannot be
// rolled back. Failures remain visible through status inventory.
func (h *Host) Activate(ctx context.Context) error {
	if h == nil {
		return nil
	}
	h.lifecycleMu.Lock()
	defer h.lifecycleMu.Unlock()
	if err := h.prepare(ctx, nil); err != nil {
		return err
	}
	if h.serviceRegistry != nil {
		h.serviceRegistry.Activate()
	}
	clients := h.orderedClients()
	dependencies := h.Dependencies()
	var err error
	blocked := h.DependencyFailures()
	for _, client := range clients {
		if blocked[client.ID()] != nil || client.Status().State == StateFailed || client.Status().State == StateStopped {
			continue
		}
		if activateErr := h.activateClient(ctx, client, dependencies[client.ID()]); activateErr != nil {
			err = errors.Join(err, fmt.Errorf("activate plugin %q: %w", client.ID(), activateErr))
			outcome, _ := h.retirePluginAndConsumers(ctx, client.ID(), activateErr, nil)
			err = errors.Join(err, outcome.Err)
			h.Add(Failed(client.ID(), activateErr))
			h.blockFailedDependencies(ctx)
			blocked = h.DependencyFailures()
		}
	}
	h.blockFailedDependencies(ctx)
	return err
}

func (h *Host) Statuses() []Status {
	if h == nil {
		return nil
	}
	h.mu.RLock()
	clients := append([]Client(nil), h.clients...)
	h.mu.RUnlock()
	out := make([]Status, 0, len(clients))
	for _, client := range clients {
		status := client.Status()
		if _, sensitive := h.sensitiveOwners.Load(client.ID()); sensitive && status.Error != "" {
			status.Error = sensitiveDiagnostic
		}
		out = append(out, status)
	}
	return out
}

// ClientShutdownOutcome reports how one plugin client shut down when its
// owning generation retired, so failures stay attributable to the plugin.
type ClientShutdownOutcome struct {
	PluginID string
	Err      error
}

func (h *Host) Close(ctx context.Context) error {
	var firstErr error
	for _, outcome := range h.CloseWithOutcomes(ctx) {
		if outcome.Err != nil && firstErr == nil {
			firstErr = fmt.Errorf("close plugin %q: %w", outcome.PluginID, outcome.Err)
		}
	}
	return firstErr
}

// CloseWithOutcomes shuts clients down in reverse dependency order and reports one
// structured outcome per client instead of collapsing everything into a
// single joined error.
type hostShutdown struct {
	clients  []Client
	outcomes []ClientShutdownOutcome
}

// ShutdownDone closes only when consumers, their dependencies and the owning
// lifecycle graph have actually finished cleanup. A Close deadline does not
// close this channel. Generation owners must retain registry and package roots
// until this barrier closes.
func (h *Host) ShutdownDone() <-chan struct{} {
	if h == nil {
		done := make(chan struct{})
		close(done)
		return done
	}
	h.mu.Lock()
	defer h.mu.Unlock()
	if h.shutdownDone == nil {
		h.shutdownDone = make(chan struct{})
	}
	return h.shutdownDone
}

func (h *Host) CloseWithOutcomes(ctx context.Context) []ClientShutdownOutcome {
	if h == nil {
		return nil
	}
	h.mu.Lock()
	if h.shutdownDone == nil {
		h.shutdownDone = make(chan struct{})
	}
	done := h.shutdownDone
	start := h.shutdown == nil
	if start {
		h.closeReadCursors()
		h.shutdown = &hostShutdown{clients: h.orderedClientsLocked()}
		h.tools = nil
		h.toolOrder = nil
		h.capabilities = nil
	}
	shutdown, registry := h.shutdown, h.serviceRegistry
	h.mu.Unlock()
	if start {
		// Seal every incoming edge before cancellation snapshots or callbacks.
		for _, client := range shutdown.clients {
			registry.stopProviderAdmission(client.ID())
		}
		h.StopBackground()
		h.CancelExecutions(&UserQuestionError{Code: "generation_closed", Message: "plugin generation retired"})
		go h.finishShutdown(context.WithoutCancel(ctx), shutdown, done)
	}
	select {
	case <-done:
		h.mu.RLock()
		defer h.mu.RUnlock()
		return append([]ClientShutdownOutcome(nil), shutdown.outcomes...)
	case <-ctx.Done():
		h.mu.RLock()
		defer h.mu.RUnlock()
		outcomes := append([]ClientShutdownOutcome(nil), shutdown.outcomes...)
		completed := make(map[string]bool, len(outcomes))
		for _, outcome := range outcomes {
			completed[outcome.PluginID] = true
		}
		for i := len(shutdown.clients) - 1; i >= 0; i-- {
			id := shutdown.clients[i].ID()
			if !completed[id] {
				outcomes = append(outcomes, ClientShutdownOutcome{PluginID: id, Err: ctx.Err()})
			}
		}
		if len(outcomes) == 0 {
			outcomes = append(outcomes, ClientShutdownOutcome{Err: ctx.Err()})
		}
		return outcomes
	}
}
func (h *Host) finishShutdown(ctx context.Context, shutdown *hostShutdown, done chan struct{}) {
	h.stopModelMetrics("")
	h.lifecycleMu.Lock()
	defer h.lifecycleMu.Unlock()
	defer close(done)
	h.closed = true
	// A concurrent single-plugin retirement may have finished while this worker
	// waited for the lifecycle lock. Never recreate or close its lifetime twice.
	h.mu.Lock()
	shutdown.clients = h.orderedClientsLocked()
	h.mu.Unlock()
	if err := h.ownClients(shutdown.clients); err != nil {
		h.mu.Lock()
		shutdown.outcomes = append(shutdown.outcomes, ClientShutdownOutcome{Err: err})
		h.mu.Unlock()
		return
	}
	for i := len(shutdown.clients) - 1; i >= 0; i-- {
		client := shutdown.clients[i]
		outcome := ClientShutdownOutcome{PluginID: client.ID(), Err: h.ownerDiagnosticError(client.ID(), h.closeClient(ctx, client))}
		h.mu.Lock()
		shutdown.outcomes = append(shutdown.outcomes, outcome)
		h.mu.Unlock()
	}
	h.lifecycleRoot.Fiber().Dispose()
	h.mu.Lock()
	h.clients = nil
	h.mu.Unlock()
}

func (h *Host) availablePublicToolName(pluginID, localID string) string {
	for attempt := 0; ; attempt++ {
		name := publicToolName(pluginID, localID, attempt)
		if _, exists := h.tools[name]; !exists {
			return name
		}
	}
}

func publicToolName(pluginID, localID string, attempt int) string {
	pluginPart := toolNamePart(pluginID)
	localPart := toolNamePart(localID)
	seed := fmt.Sprintf("%s\x00%s\x00%d", pluginID, localID, attempt)
	digest := fmt.Sprintf("%x", sha256.Sum256([]byte(seed)))[:16]
	return "plugin_" + pluginPart + "_" + localPart + "_" + digest
}

func toolNamePart(value string) string {
	var b strings.Builder
	for _, r := range strings.ToLower(strings.TrimSpace(value)) {
		if unicode.IsLetter(r) || unicode.IsDigit(r) {
			if r <= unicode.MaxASCII {
				b.WriteRune(r)
			} else {
				b.WriteByte('_')
			}
		} else {
			b.WriteByte('_')
		}
		if b.Len() >= 19 {
			break
		}
	}
	part := strings.Trim(b.String(), "_")
	if part == "" {
		return "unnamed"
	}
	return part
}

func cloneToolRegistration(registration ToolRegistration) ToolRegistration {
	clone := registration
	clone.InputSchema = cloneSchema(registration.InputSchema)
	clone.ExecutionScopes = append([]string(nil), registration.ExecutionScopes...)
	clone.ReplacementContracts = append([]string(nil), registration.ReplacementContracts...)
	if registration.Activity != nil {
		activity := *registration.Activity
		clone.Activity = &activity
	}
	clone.Display = registration.Display.Clone()
	return clone
}

func cloneSchema(schema map[string]any) map[string]any {
	if schema == nil {
		return nil
	}
	raw, err := json.Marshal(schema)
	if err != nil {
		return nil
	}
	var clone map[string]any
	if err := json.Unmarshal(raw, &clone); err != nil {
		return nil
	}
	return clone
}

type failedClient struct{ status Status }

func (c *failedClient) ID() string                  { return c.status.ID }
func (c *failedClient) Status() Status              { return c.status }
func (c *failedClient) Close(context.Context) error { return nil }
