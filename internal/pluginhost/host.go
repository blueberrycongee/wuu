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
	mu              sync.RWMutex
	clients         []Client
	tools           map[string]RegisteredTool
	toolOrder       []string
	capabilities    []RegisteredCapability
	diagnostics     map[string]map[string]string
	serviceRegistry *ServiceRegistry
	executions      *ExecutionTracker
	materialize     func(context.Context, ToolExecutionScope, *toolresult.Result) error
}

type ContributionDiagnostic struct {
	Contribution string `json:"contribution"`
	Message      string `json:"message"`
}

func New(clients ...Client) *Host {
	host := &Host{tools: make(map[string]RegisteredTool), diagnostics: make(map[string]map[string]string), executions: NewExecutionTracker()}
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
	return &failedClient{status: Status{ID: id, State: StateFailed, Error: message}}
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
	defer h.mu.Unlock()
	h.serviceRegistry = registry
	for _, conflict := range conflicts {
		if h.diagnostics[conflict.PluginID] == nil {
			h.diagnostics[conflict.PluginID] = make(map[string]string)
		}
		h.diagnostics[conflict.PluginID][fmt.Sprintf("service:%s@%d", conflict.Service, conflict.Major)] = conflict.Message
	}
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
	if client == nil {
		return
	}
	h.clients = append(h.clients, client)
	if capabilityClient, ok := client.(CapabilityClient); ok && capabilityClient.ProtocolVersion() >= CapabilityProtocolVersion {
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

// InvokeCapability invokes one exact plugin registration.
func (h *Host) InvokeCapability(ctx context.Context, capability RegisteredCapability, input, output any) error {
	if capability.client == nil {
		return fmt.Errorf("plugin %q capability %q is not active", capability.PluginID, capability.Descriptor.ID)
	}
	inputJSON, err := json.Marshal(input)
	if err != nil {
		return fmt.Errorf("marshal capability %q input: %w", capability.Descriptor.ID, err)
	}
	outputJSON, err := json.Marshal(output)
	if err != nil {
		return fmt.Errorf("marshal capability %q output: %w", capability.Descriptor.ID, err)
	}
	h.mu.RLock()
	admitted := false
	for _, current := range h.capabilities {
		if current.PluginID == capability.PluginID && current.Descriptor.ID == capability.Descriptor.ID && current.client == capability.client && capabilityStateReady(current.client.Status().State) {
			admitted = true
			break
		}
	}
	if !admitted {
		h.mu.RUnlock()
		return fmt.Errorf("plugin %q capability %q is not active", capability.PluginID, capability.Descriptor.ID)
	}
	executionID := h.executions.Begin(capability.PluginID)
	h.mu.RUnlock()
	defer h.executions.End(executionID)
	result, err := capability.client.InvokeCapability(ctx, CapabilityInvokeParams{
		Capability:  capability.Descriptor.ID,
		ExecutionID: executionID,
		Input:       inputJSON,
		Output:      outputJSON,
	})
	if err != nil {
		return fmt.Errorf("plugin %q capability %q: %w", capability.PluginID, capability.Descriptor.ID, err)
	}
	decoder := json.NewDecoder(bytes.NewReader(result.Output))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(output); err != nil {
		return fmt.Errorf("plugin %q capability %q returned invalid output: %w", capability.PluginID, capability.Descriptor.ID, err)
	}
	var trailing any
	if err := decoder.Decode(&trailing); !errors.Is(err, io.EOF) {
		if err == nil {
			err = errors.New("multiple JSON values")
		}
		return fmt.Errorf("plugin %q capability %q returned invalid output: %w", capability.PluginID, capability.Descriptor.ID, err)
	}
	return nil
}

// HandleCapabilityError applies the negotiated descriptor policy to one
// capability failure. A nil return means the caller must skip this
// contribution and continue dispatch with its previous value.
func (h *Host) HandleCapabilityError(capability RegisteredCapability, err error) error {
	if err == nil {
		return nil
	}
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
		out = append(out, ContributionDiagnostic{Contribution: contribution, Message: byContribution[contribution]})
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
			Description: tool.Registration.Description,
			InputSchema: cloneSchema(tool.Registration.InputSchema),
		})
	}
	return definitions
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
	h.mu.RLock()
	tool, ok := h.tools[name]
	if !ok || tool.client.Status().State != StateActive {
		h.mu.RUnlock()
		return toolresult.Result{}, fmt.Errorf("plugin tool %q is not registered", name)
	}
	input.Tool = name
	input.ExecutionID = h.executions.BeginTool(tool.PluginID, ctx, input)
	h.mu.RUnlock()
	defer h.executions.End(input.ExecutionID)
	response, err := tool.client.ExecuteTool(ctx, ToolExecuteParams{
		ToolExecuteInput: input,
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
		if err := materialize(ctx, scope, &response.Result); err != nil {
			return toolresult.Result{}, fmt.Errorf("plugin %q tool %q materialize artifacts: %w", tool.PluginID, name, err)
		}
		if err := response.Result.Validate(); err != nil {
			return toolresult.Result{}, fmt.Errorf("plugin %q tool %q returned invalid materialized result: %w", tool.PluginID, name, err)
		}
	}
	return response.Result.Clone(), nil
}

func capabilityStateReady(state State) bool {
	return state == StatePrepared || state == StateActive
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

func (h *Host) CancelExecutions(cause error) {
	if h != nil && h.executions != nil {
		h.executions.CancelAll(cause)
	}
}

// RetirePlugin revokes one plugin's executable paths and closes only its
// client. Unrelated clients and contributions remain active so runtimes can
// replace plugin generations independently.
func (h *Host) RetirePlugin(ctx context.Context, pluginID string, cause error) (ClientShutdownOutcome, bool) {
	if h == nil {
		return ClientShutdownOutcome{}, false
	}
	pluginID = strings.TrimSpace(pluginID)
	if pluginID == "" {
		return ClientShutdownOutcome{}, false
	}
	h.mu.Lock()
	var client Client
	keptClients := h.clients[:0]
	for _, candidate := range h.clients {
		if candidate.ID() == pluginID && client == nil {
			client = candidate
			continue
		}
		keptClients = append(keptClients, candidate)
	}
	if client == nil {
		h.mu.Unlock()
		return ClientShutdownOutcome{}, false
	}
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
	if h.executions != nil {
		h.executions.CancelPlugin(pluginID, cause)
	}
	// Contributions and executions are already detached. Keep declared host
	// services available while the plugin persists state and cancels its work.
	closeErr := client.Close(ctx)
	if registry != nil {
		registry.RevokePlugin(ctx, pluginID)
	}
	return ClientShutdownOutcome{PluginID: pluginID, Err: closeErr}, true
}

// ExecutionSnapshots returns the host's live execution table for diagnostics.
func (h *Host) ExecutionSnapshots() []ExecutionSnapshot {
	if h == nil || h.executions == nil {
		return nil
	}
	return h.executions.Snapshot()
}

// Activate starts prepared runtimes so the generation can be validated before
// publication. Failures are isolated to the affected runtime and remain
// visible through status inventory.
func (h *Host) Activate(ctx context.Context) error {
	if h == nil {
		return nil
	}
	h.mu.RLock()
	clients := append([]Client(nil), h.clients...)
	h.mu.RUnlock()
	var err error
	for _, client := range clients {
		lifecycle, ok := client.(lifecycleClient)
		if !ok || client.Status().State != StatePrepared {
			continue
		}
		if activateErr := lifecycle.Activate(ctx); activateErr != nil {
			err = errors.Join(err, fmt.Errorf("activate plugin %q: %w", client.ID(), activateErr))
		}
	}
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
		out = append(out, client.Status())
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

// CloseWithOutcomes shuts clients down in reverse start order and reports one
// structured outcome per client instead of collapsing everything into a
// single joined error.
func (h *Host) CloseWithOutcomes(ctx context.Context) []ClientShutdownOutcome {
	if h == nil {
		return nil
	}
	h.CancelExecutions(&UserQuestionError{Code: "generation_closed", Message: "plugin generation retired"})
	h.mu.Lock()
	clients := append([]Client(nil), h.clients...)
	h.clients = nil
	h.tools = nil
	h.toolOrder = nil
	h.capabilities = nil
	h.mu.Unlock()

	outcomes := make([]ClientShutdownOutcome, 0, len(clients))
	for i := len(clients) - 1; i >= 0; i-- {
		outcomes = append(outcomes, ClientShutdownOutcome{PluginID: clients[i].ID(), Err: clients[i].Close(ctx)})
	}
	return outcomes
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
