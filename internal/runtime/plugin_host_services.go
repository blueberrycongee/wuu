package runtime

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"sort"
	"strings"
	"sync"

	pluginpkg "github.com/blueberrycongee/wuu/internal/plugin"
	"github.com/blueberrycongee/wuu/internal/pluginhost"
	"github.com/blueberrycongee/wuu/internal/pluginsettings"
	"github.com/blueberrycongee/wuu/internal/providers"
)

// pluginHostServices is bound to exactly one installed plugin generation. It
// never accepts a caller-supplied plugin ID or filesystem path: ownership and
// roots are captured from the validated plugin inventory at activation time.
type pluginHostServices struct {
	mu          sync.RWMutex
	active      bool
	open        bool
	pluginID    string
	subjectID   string
	fingerprint string
	projectRoot string
	wuuHome     string
	settings    map[string]pluginpkg.SettingDefinition
	turnRouter  *PluginSessionRouter
}

func newPluginHostServices(item pluginpkg.Plugin, projectRoot, wuuHome string, turnRouter *PluginSessionRouter) *pluginHostServices {
	settings := make(map[string]pluginpkg.SettingDefinition, len(item.Settings))
	for key, definition := range item.Settings {
		settings[key] = definition
	}
	services := &pluginHostServices{
		open: true, pluginID: item.ID, subjectID: strings.TrimSpace(item.SubjectID),
		fingerprint: item.Fingerprint, projectRoot: projectRoot, wuuHome: wuuHome,
		settings: settings, turnRouter: turnRouter,
	}
	return services
}

func (s *pluginHostServices) close() {
	s.mu.Lock()
	s.open = false
	s.active = false
	s.mu.Unlock()
}

func (s *pluginHostServices) activate() {
	s.mu.Lock()
	if s.open {
		s.active = true
	}
	s.mu.Unlock()
}

func (s *pluginHostServices) invoke(ctx context.Context, method pluginhost.HostServiceMethod, raw json.RawMessage) (json.RawMessage, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	if !s.open {
		return nil, serviceError("generation_closed", "plugin generation is no longer active")
	}
	if !s.active && !kernelServiceReadOnly(method) {
		return nil, serviceError("service_unavailable", "host service is unavailable during generation prepare")
	}
	if strings.TrimSpace(s.subjectID) == "" || strings.TrimSpace(s.fingerprint) == "" {
		return nil, serviceError("generation_invalid", "plugin generation has no stable package identity")
	}

	switch method {
	case pluginhost.HostServiceSessionControl:
		if s.turnRouter == nil {
			return nil, serviceError("service_unavailable", "session service is unavailable")
		}
		var params pluginhost.SessionControlParams
		if err := decodeServiceParams(raw, &params); err != nil {
			return nil, err
		}
		result, err := s.turnRouter.Control(ctx, s.pluginID, params)
		if err != nil {
			return nil, err
		}
		return marshalServiceResult(result)
	case pluginhost.HostServiceSessionCreate:
		if s.turnRouter == nil {
			return nil, serviceError("service_unavailable", "session service is unavailable")
		}
		var params pluginhost.SessionCreateParams
		if err := decodeServiceParams(raw, &params); err != nil {
			return nil, err
		}
		result, err := s.turnRouter.Create(ctx, s.pluginID, params)
		if err != nil {
			return nil, err
		}
		return marshalServiceResult(result)
	case pluginhost.HostServiceSessionSend:
		if s.turnRouter == nil {
			return nil, serviceError("service_unavailable", "session service is unavailable")
		}
		var params pluginhost.SessionSendParams
		if err := decodeServiceParams(raw, &params); err != nil {
			return nil, err
		}
		result, err := s.turnRouter.Send(ctx, s.pluginID, params)
		if err != nil {
			return nil, err
		}
		return marshalServiceResult(result)
	case pluginhost.HostServiceSessionList:
		if s.turnRouter == nil {
			return nil, serviceError("service_unavailable", "session service is unavailable")
		}
		var params pluginhost.SessionListParams
		if err := decodeServiceParams(raw, &params); err != nil {
			return nil, err
		}
		result, err := s.turnRouter.List(ctx, s.pluginID, params)
		if err != nil {
			return nil, err
		}
		return marshalServiceResult(result)
	case pluginhost.HostServiceSessionCancel:
		if s.turnRouter == nil {
			return nil, serviceError("service_unavailable", "session service is unavailable")
		}
		var params pluginhost.SessionCancelParams
		if err := decodeServiceParams(raw, &params); err != nil {
			return nil, err
		}
		result, err := s.turnRouter.Cancel(ctx, s.pluginID, params)
		if err != nil {
			return nil, err
		}
		return marshalServiceResult(result)
	case pluginhost.HostServiceSessionInspect:
		if s.turnRouter == nil {
			return nil, serviceError("service_unavailable", "session service is unavailable")
		}
		var params pluginhost.SessionInspectParams
		if err := decodeServiceParams(raw, &params); err != nil {
			return nil, err
		}
		result, err := s.turnRouter.Inspect(ctx, s.pluginID, params)
		if err != nil {
			return nil, err
		}
		return marshalServiceResult(result)
	case pluginhost.HostServiceSessionHistoryRead:
		var params pluginhost.SessionHistoryReadParams
		if err := decodeServiceParams(raw, &params); err != nil {
			return nil, err
		}
		result, err := readPluginSessionHistory(ctx, s.wuuHome, s.pluginID, params)
		if err != nil {
			return nil, err
		}
		return marshalServiceResult(result)
	case pluginhost.HostServiceSessionHistorySearch:
		var params pluginhost.SessionHistorySearchParams
		if err := decodeServiceParams(raw, &params); err != nil {
			return nil, err
		}
		result, err := searchPluginSessionHistory(ctx, s.wuuHome, s.pluginID, params)
		if err != nil {
			return nil, err
		}
		return marshalServiceResult(result)
	case pluginhost.HostServiceWorkspaceStatus:
		if s.turnRouter == nil {
			return nil, serviceError("service_unavailable", "workspace service is unavailable")
		}
		var params pluginhost.WorkspaceStatusParams
		if err := decodeServiceParams(raw, &params); err != nil {
			return nil, err
		}
		result, err := s.turnRouter.WorkspaceStatus(ctx, s.pluginID, params)
		if err != nil {
			return nil, err
		}
		return marshalServiceResult(result)
	case pluginhost.HostServiceWorkspaceApply:
		if s.turnRouter == nil {
			return nil, serviceError("service_unavailable", "workspace service is unavailable")
		}
		var params pluginhost.WorkspaceApplyParams
		if err := decodeServiceParams(raw, &params); err != nil {
			return nil, err
		}
		result, err := s.turnRouter.WorkspaceApply(ctx, s.pluginID, params)
		if err != nil {
			return nil, err
		}
		return marshalServiceResult(result)
	case pluginhost.HostServiceWorkspaceDiscard:
		if s.turnRouter == nil {
			return nil, serviceError("service_unavailable", "workspace service is unavailable")
		}
		var params pluginhost.WorkspaceDiscardParams
		if err := decodeServiceParams(raw, &params); err != nil {
			return nil, err
		}
		result, err := s.turnRouter.WorkspaceDiscard(ctx, s.pluginID, params)
		if err != nil {
			return nil, err
		}
		return marshalServiceResult(result)
	case pluginhost.HostServiceSettingsGet:
		return s.settingsGet(raw)
	case pluginhost.HostServiceSettingsList:
		if err := decodeServiceParams(raw, &struct{}{}); err != nil {
			return nil, err
		}
		return s.settingsList()
	case pluginhost.HostServiceStorageGet:
		var params pluginhost.StorageGetParams
		if err := decodeServiceParams(raw, &params); err != nil {
			return nil, err
		}
		return s.storageGet(params)
	case pluginhost.HostServiceStorageSet:
		var params pluginhost.StorageSetParams
		if err := decodeServiceParams(raw, &params); err != nil {
			return nil, err
		}
		return s.storageSet(params)
	case pluginhost.HostServiceStorageDelete:
		var params pluginhost.StorageDeleteParams
		if err := decodeServiceParams(raw, &params); err != nil {
			return nil, err
		}
		return s.storageDelete(params)
	case pluginhost.HostServiceStorageKeys:
		var params pluginhost.StorageKeysParams
		if err := decodeServiceParams(raw, &params); err != nil {
			return nil, err
		}
		return s.storageKeys(params)
	case pluginhost.HostServiceStorageCompareExchange:
		var params pluginhost.StorageCompareExchangeParams
		if err := decodeServiceParams(raw, &params); err != nil {
			return nil, err
		}
		return s.storageCompareExchange(params)
	default:
		return nil, serviceError("method_not_found", fmt.Sprintf("host service %q is not implemented", method))
	}
}

func (s *pluginHostServices) settingsGet(raw json.RawMessage) (json.RawMessage, error) {
	var params pluginhost.SettingsGetParams
	if err := decodeServiceParams(raw, &params); err != nil {
		return nil, err
	}
	localKey, definition, err := s.ownedSetting(params.Key)
	if err != nil {
		return nil, err
	}
	value, err := s.settingValue(localKey, definition)
	if err != nil {
		return nil, err
	}
	return marshalServiceResult(pluginhost.SettingsGetResult{Value: value})
}

func (s *pluginHostServices) settingsList() (json.RawMessage, error) {
	entries := make(map[string]json.RawMessage, len(s.settings))
	keys := make([]string, 0, len(s.settings))
	for qualified := range s.settings {
		prefix := s.pluginID + "."
		if strings.HasPrefix(qualified, prefix) {
			keys = append(keys, strings.TrimPrefix(qualified, prefix))
		}
	}
	sort.Strings(keys)
	for _, key := range keys {
		_, definition, err := s.ownedSetting(key)
		if err != nil {
			return nil, err
		}
		value, err := s.settingValue(key, definition)
		if err != nil {
			return nil, err
		}
		entries[key] = value
	}
	return marshalServiceResult(pluginhost.SettingsListResult{Entries: entries})
}

func (s *pluginHostServices) ownedSetting(key string) (string, pluginpkg.SettingDefinition, error) {
	key = strings.TrimSpace(key)
	if key == "" || strings.Contains(key, "/") || strings.Contains(key, "\\") || strings.Contains(key, "..") {
		return "", pluginpkg.SettingDefinition{}, serviceError("invalid_params", "setting key must be a plugin-local key")
	}
	definition, ok := s.settings[s.pluginID+"."+key]
	if !ok {
		return "", pluginpkg.SettingDefinition{}, serviceError("setting_not_owned", fmt.Sprintf("plugin does not own setting %q", key))
	}
	return key, definition, nil
}

func (s *pluginHostServices) settingValue(key string, definition pluginpkg.SettingDefinition) (json.RawMessage, error) {
	scope := pluginsettings.Scope(definition.Scope)
	// Settings are intentionally read-only on this channel. Persisted values
	// survive plugin upgrades, so a document fingerprint may differ from this
	// generation's fingerprint. Reading never rewrites it; Desktop's existing
	// fingerprint-gated direct API remains the sole settings writer.
	document, err := pluginsettings.Read(s.wuuHome, s.projectRoot, s.subjectID, scope)
	if err != nil {
		return nil, err
	}
	value, ok := document.Values[key]
	if !ok {
		value, err = json.Marshal(definition.Default)
		if err != nil {
			return nil, fmt.Errorf("encode setting default: %w", err)
		}
	}
	if err := validateRuntimeSettingValue(definition, value); err != nil {
		return nil, fmt.Errorf("persisted setting %q: %w", key, err)
	}
	return append(json.RawMessage(nil), value...), nil
}

// HandleHostService remains an internal semantic test seam; transport routing
// enters through the kernel registry above it.
func (s *pluginHostServices) HandleHostService(ctx context.Context, method pluginhost.HostServiceMethod, raw json.RawMessage) (json.RawMessage, error) {
	return s.invoke(ctx, method, raw)
}

func (s *pluginHostServices) CloseHostServices() { s.close() }

func kernelServiceReadOnly(method pluginhost.HostServiceMethod) bool {
	switch method {
	case pluginhost.HostServiceStorageGet, pluginhost.HostServiceStorageKeys,
		pluginhost.HostServiceSettingsGet, pluginhost.HostServiceSettingsList,
		pluginhost.HostServiceSessionList, pluginhost.HostServiceSessionInspect,
		pluginhost.HostServiceSessionHistoryRead, pluginhost.HostServiceSessionHistorySearch,
		pluginhost.HostServiceWorkspaceStatus:
		return true
	default:
		return false
	}
}

// executionUpdateRecorder is the kernel's view into the host's live execution
// table; *pluginhost.Host satisfies it in production.
type executionUpdateRecorder interface {
	RecordExecutionUpdate(callerPluginID string, params pluginhost.ExecutionUpdateParams) *pluginhost.HostServiceError
	ResolveToolExecution(callerPluginID, executionID string) (pluginhost.ToolExecutionScope, *pluginhost.HostServiceError)
}

type kernelHostServices struct {
	mu                sync.RWMutex
	active            bool
	services          map[string]*pluginHostServices
	registry          *pluginhost.ServiceRegistry
	generation        func() uint64
	executions        executionUpdateRecorder
	userQuestions     *pluginhost.UserQuestionBroker
	driverGateways    *driverGatewayTable
	workspaceStateDir string
	wuuHome           string
}

func newKernelHostServices(generation func() uint64, executions executionUpdateRecorder) *kernelHostServices {
	return &kernelHostServices{services: make(map[string]*pluginHostServices), generation: generation, executions: executions, driverGateways: newDriverGatewayTable()}
}

// bindWorkspaceStateDir sets the workspace state directory used by read-only
// kernel data services. It is set once during generation construction.
func (k *kernelHostServices) bindWorkspaceStateDir(dir string) {
	k.mu.Lock()
	k.workspaceStateDir = strings.TrimSpace(dir)
	k.mu.Unlock()
}

func (k *kernelHostServices) bindWuuHome(dir string) {
	k.mu.Lock()
	k.wuuHome = strings.TrimSpace(dir)
	k.mu.Unlock()
}

func (k *kernelHostServices) bindUserQuestions(broker *pluginhost.UserQuestionBroker) {
	k.mu.Lock()
	k.userQuestions = broker
	k.mu.Unlock()
}

func (k *kernelHostServices) add(pluginID string, services *pluginHostServices) {
	k.mu.Lock()
	k.services[pluginID] = services
	k.mu.Unlock()
}

// bindRegistry late-binds the registry built from this kernel's registrations
// so the introspection invoker can answer from the live provider table.
func (k *kernelHostServices) bindRegistry(registry *pluginhost.ServiceRegistry) {
	k.mu.Lock()
	k.registry = registry
	k.mu.Unlock()
}

func (k *kernelHostServices) ID() string { return "kernel" }

func (k *kernelHostServices) Status() pluginhost.Status {
	k.mu.RLock()
	defer k.mu.RUnlock()
	state := pluginhost.StatePrepared
	if k.active {
		state = pluginhost.StateActive
	}
	return pluginhost.Status{ID: k.ID(), State: state}
}

func (k *kernelHostServices) Close(context.Context) error { return nil }
func (k *kernelHostServices) ProvidedServices() []pluginhost.ServiceDescriptor {
	return pluginhost.KernelServiceDescriptors()
}
func (k *kernelHostServices) RequiredServices() []pluginhost.ServiceRequirement { return nil }
func (k *kernelHostServices) KernelServiceRegistrations() []pluginhost.ServiceRegistration {
	descriptors := pluginhost.KernelServiceDescriptors()
	methods := []pluginhost.HostServiceMethod{
		pluginhost.HostServiceStorageGet, pluginhost.HostServiceStorageSet,
		pluginhost.HostServiceStorageDelete, pluginhost.HostServiceStorageKeys,
		pluginhost.HostServiceStorageCompareExchange, pluginhost.HostServiceSettingsGet,
		pluginhost.HostServiceSettingsList, pluginhost.HostServiceSessionCreate,
		pluginhost.HostServiceSessionSend, pluginhost.HostServiceSessionList,
		pluginhost.HostServiceSessionCancel, pluginhost.HostServiceSessionInspect,
		pluginhost.HostServiceSessionHistoryRead, pluginhost.HostServiceSessionHistorySearch,
		pluginhost.HostServiceWorkspaceStatus, pluginhost.HostServiceWorkspaceApply,
		pluginhost.HostServiceWorkspaceDiscard,
		pluginhost.HostServiceSessionControl,
	}
	registrations := make([]pluginhost.ServiceRegistration, 0, len(descriptors)+8)
	for index, descriptor := range descriptors {
		registrations = append(registrations, pluginhost.ServiceRegistration{
			Descriptor: descriptor, Invoker: &kernelServiceInvoker{parent: k, method: methods[index]}, Kernel: true,
		})
	}
	registrations = append(registrations,
		pluginhost.ServiceRegistration{
			Descriptor: pluginhost.KernelRegistryIntrospectDescriptor(),
			Invoker:    &registryIntrospectInvoker{parent: k}, Kernel: true,
		},
		pluginhost.ServiceRegistration{
			Descriptor: pluginhost.KernelExecutionUpdateDescriptor(),
			Invoker:    &executionUpdateInvoker{parent: k}, Kernel: true,
		},
		pluginhost.ServiceRegistration{
			Descriptor: pluginhost.KernelInvokeToolDescriptor(),
			Invoker:    &nestedToolInvoker{parent: k}, Kernel: true,
		},
		pluginhost.ServiceRegistration{
			Descriptor: pluginhost.KernelUserQuestionAskDescriptor(),
			Invoker:    &userQuestionAskInvoker{parent: k}, Kernel: true,
		},
		pluginhost.ServiceRegistration{
			Descriptor: pluginhost.KernelUserQuestionOfferDescriptor(),
			Invoker:    &userQuestionOfferInvoker{parent: k}, Kernel: true,
		},
		pluginhost.ServiceRegistration{
			Descriptor: pluginhost.KernelArtifactImportDescriptor(),
			Invoker:    &artifactImportInvoker{parent: k}, Kernel: true,
		},
		pluginhost.ServiceRegistration{
			Descriptor: pluginhost.KernelDataQueryDescriptor(),
			Invoker:    &dataQueryInvoker{parent: k}, Kernel: true,
		},
		pluginhost.ServiceRegistration{
			Descriptor: pluginhost.KernelDriverModelLoopDescriptor(),
			Invoker:    &driverModelLoopInvoker{parent: k}, Kernel: true,
		},
		pluginhost.ServiceRegistration{
			Descriptor: pluginhost.KernelDriverCheckpointDescriptor(),
			Invoker:    &driverCheckpointInvoker{parent: k}, Kernel: true,
		},
	)
	return registrations
}

func (k *kernelHostServices) ActivateKernelServices() {
	k.mu.Lock()
	k.active = true
	services := make([]*pluginHostServices, 0, len(k.services))
	for _, service := range k.services {
		services = append(services, service)
	}
	k.mu.Unlock()
	for _, service := range services {
		service.activate()
	}
}

func (k *kernelHostServices) CloseKernelServices() {
	k.mu.Lock()
	k.active = false
	services := make([]*pluginHostServices, 0, len(k.services))
	for _, service := range k.services {
		services = append(services, service)
	}
	k.mu.Unlock()
	for _, service := range services {
		service.close()
	}
}

type kernelServiceInvoker struct {
	parent *kernelHostServices
	method pluginhost.HostServiceMethod
}

func (k *kernelServiceInvoker) ID() string                { return k.parent.ID() }
func (k *kernelServiceInvoker) Status() pluginhost.Status { return k.parent.Status() }
func (k *kernelServiceInvoker) InvokeService(ctx context.Context, params pluginhost.ServiceInvokeParams) (json.RawMessage, error) {
	k.parent.mu.RLock()
	services := k.parent.services[params.Caller]
	k.parent.mu.RUnlock()
	if services == nil {
		return nil, serviceError("service_unavailable", "kernel service caller is unavailable")
	}
	if params.Method != pluginhost.KernelServiceMethod {
		return nil, serviceError("method_not_found", "kernel service method is unavailable")
	}
	return services.invoke(ctx, k.method, params.Params)
}

// registryIntrospectInvoker answers registry.introspect from the live
// registry. The answer is identical for every caller; authority was already
// enforced by the registry's declaration check before routing here.
type registryIntrospectInvoker struct {
	parent *kernelHostServices
}

func (k *registryIntrospectInvoker) ID() string                { return k.parent.ID() }
func (k *registryIntrospectInvoker) Status() pluginhost.Status { return k.parent.Status() }
func (k *registryIntrospectInvoker) InvokeService(ctx context.Context, params pluginhost.ServiceInvokeParams) (json.RawMessage, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if params.Method != pluginhost.KernelServiceMethod {
		return nil, serviceError("method_not_found", "kernel service method is unavailable")
	}
	k.parent.mu.RLock()
	registry := k.parent.registry
	generation := k.parent.generation
	k.parent.mu.RUnlock()
	if registry == nil {
		return nil, serviceError("service_unavailable", "registry introspection is unavailable")
	}
	var epoch uint64
	if generation != nil {
		epoch = generation()
	}
	return marshalServiceResult(registry.Snapshot(epoch))
}

// executionUpdateInvoker answers execution.update against the host's live
// execution table. Ownership and liveness are enforced by the tracker; the
// typed error reaches the caller through the registry unchanged.
type executionUpdateInvoker struct {
	parent *kernelHostServices
}

func (k *executionUpdateInvoker) ID() string                { return k.parent.ID() }
func (k *executionUpdateInvoker) Status() pluginhost.Status { return k.parent.Status() }
func (k *executionUpdateInvoker) InvokeService(ctx context.Context, params pluginhost.ServiceInvokeParams) (json.RawMessage, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if params.Method != pluginhost.KernelServiceMethod {
		return nil, serviceError("method_not_found", "kernel service method is unavailable")
	}
	k.parent.mu.RLock()
	executions := k.parent.executions
	k.parent.mu.RUnlock()
	if executions == nil {
		return nil, serviceError("service_unavailable", "execution scope is unavailable")
	}
	var update pluginhost.ExecutionUpdateParams
	if err := json.Unmarshal(params.Params, &update); err != nil {
		return nil, serviceError("invalid_request", fmt.Sprintf("decode execution.update params: %v", err))
	}
	if err := executions.RecordExecutionUpdate(params.Caller, update); err != nil {
		return nil, err
	}
	message := strings.TrimSpace(update.Message)
	if message != "" {
		providers.DebugLogf("plugin %q execution %s: %s", params.Caller, update.ExecutionID, message)
	}
	return json.RawMessage(`{}`), nil
}

type userQuestionAskInvoker struct {
	parent *kernelHostServices
}

func (k *userQuestionAskInvoker) ID() string                { return k.parent.ID() }
func (k *userQuestionAskInvoker) Status() pluginhost.Status { return k.parent.Status() }
func (k *userQuestionAskInvoker) InvokeService(ctx context.Context, params pluginhost.ServiceInvokeParams) (json.RawMessage, error) {
	return invokeUserQuestion(ctx, k.parent, params, false)
}

type userQuestionOfferInvoker struct {
	parent *kernelHostServices
}

func (k *userQuestionOfferInvoker) ID() string                { return k.parent.ID() }
func (k *userQuestionOfferInvoker) Status() pluginhost.Status { return k.parent.Status() }
func (k *userQuestionOfferInvoker) InvokeService(ctx context.Context, params pluginhost.ServiceInvokeParams) (json.RawMessage, error) {
	return invokeUserQuestion(ctx, k.parent, params, true)
}

func invokeUserQuestion(ctx context.Context, parent *kernelHostServices, params pluginhost.ServiceInvokeParams, offer bool) (json.RawMessage, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if params.Method != pluginhost.KernelServiceMethod {
		return nil, serviceError("method_not_found", "kernel service method is unavailable")
	}
	parent.mu.RLock()
	broker := parent.userQuestions
	executions := parent.executions
	parent.mu.RUnlock()
	if broker == nil {
		return nil, serviceError("service_unavailable", "user interaction is unavailable")
	}
	if executions == nil {
		return nil, serviceError("service_unavailable", "execution scope is unavailable")
	}
	scope, scopeErr := executions.ResolveToolExecution(params.Caller, params.ExecutionID)
	if scopeErr != nil {
		return nil, scopeErr
	}
	var input pluginhost.UserQuestionAskParams
	if err := decodeServiceParams(params.Params, &input); err != nil {
		return nil, serviceError("invalid_request", "invalid user question parameters")
	}
	owner := pluginhost.UserQuestionOwner{
		PluginID: scope.PluginID, ExecutionID: scope.ID, SessionID: scope.SessionID,
		ThreadID: scope.ThreadID, TurnID: scope.TurnID, ActorID: scope.ActorID, CallID: scope.CallID,
	}
	if offer {
		result, err := broker.Offer(owner, input)
		if err != nil {
			return nil, userQuestionServiceError(err)
		}
		return marshalServiceResult(result)
	}
	answer, err := broker.Ask(scope.Context, owner, input)
	if err != nil {
		return nil, userQuestionServiceError(err)
	}
	return marshalServiceResult(answer)
}

func userQuestionServiceError(err error) error {
	var questionErr *pluginhost.UserQuestionError
	if errors.As(err, &questionErr) {
		return serviceError(questionErr.Code, questionErr.Message)
	}
	return err
}

func (s *pluginHostServices) storageGet(params pluginhost.StorageGetParams) (json.RawMessage, error) {
	scope, err := storageScope(params.Scope)
	if err != nil {
		return nil, err
	}
	if err := pluginsettings.ValidateStateKey(params.Key); err != nil {
		return nil, serviceError("invalid_params", err.Error())
	}
	document, err := pluginsettings.ReadState(s.wuuHome, s.projectRoot, s.subjectID, scope)
	if err != nil {
		return nil, err
	}
	value, ok := document.Values[params.Key]
	if !ok {
		return marshalServiceResult(pluginhost.StorageGetResult{})
	}
	return marshalServiceResult(pluginhost.StorageGetResult{Value: &value})
}

func (s *pluginHostServices) storageSet(params pluginhost.StorageSetParams) (json.RawMessage, error) {
	scope, err := storageScope(params.Scope)
	if err != nil {
		return nil, err
	}
	if err := pluginsettings.ValidateStateKey(params.Key); err != nil {
		return nil, serviceError("invalid_params", err.Error())
	}
	if len([]byte(params.Value)) > pluginsettings.MaxStateValueBytes {
		return nil, serviceError("limit_exceeded", fmt.Sprintf("plugin storage value exceeds %d bytes", pluginsettings.MaxStateValueBytes))
	}
	_, err = pluginsettings.UpdateState(s.wuuHome, s.projectRoot, s.subjectID, scope, func(values map[string]string) error {
		values[params.Key] = params.Value
		return nil
	})
	if err != nil {
		return nil, err
	}
	return marshalServiceResult(struct{}{})
}

func (s *pluginHostServices) storageDelete(params pluginhost.StorageDeleteParams) (json.RawMessage, error) {
	scope, err := storageScope(params.Scope)
	if err != nil {
		return nil, err
	}
	if err := pluginsettings.ValidateStateKey(params.Key); err != nil {
		return nil, serviceError("invalid_params", err.Error())
	}
	_, err = pluginsettings.UpdateState(s.wuuHome, s.projectRoot, s.subjectID, scope, func(values map[string]string) error {
		delete(values, params.Key)
		return nil
	})
	if err != nil {
		return nil, err
	}
	return marshalServiceResult(struct{}{})
}

func (s *pluginHostServices) storageKeys(params pluginhost.StorageKeysParams) (json.RawMessage, error) {
	scope, err := storageScope(params.Scope)
	if err != nil {
		return nil, err
	}
	document, err := pluginsettings.ReadState(s.wuuHome, s.projectRoot, s.subjectID, scope)
	if err != nil {
		return nil, err
	}
	keys := make([]string, 0, len(document.Values))
	for key := range document.Values {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	return marshalServiceResult(pluginhost.StorageKeysResult{Keys: keys})
}

func (s *pluginHostServices) storageCompareExchange(params pluginhost.StorageCompareExchangeParams) (json.RawMessage, error) {
	scope, err := storageScope(params.Scope)
	if err != nil {
		return nil, err
	}
	if err := pluginsettings.ValidateStateKey(params.Key); err != nil {
		return nil, serviceError("invalid_params", err.Error())
	}
	if params.Value != nil && len([]byte(*params.Value)) > pluginsettings.MaxStateValueBytes {
		return nil, serviceError("limit_exceeded", fmt.Sprintf("plugin storage value exceeds %d bytes", pluginsettings.MaxStateValueBytes))
	}
	result := pluginhost.StorageCompareExchangeResult{}
	_, err = pluginsettings.UpdateState(s.wuuHome, s.projectRoot, s.subjectID, scope, func(values map[string]string) error {
		current, exists := values[params.Key]
		matches := params.Expected != nil && exists && current == *params.Expected
		if params.Expected == nil {
			matches = !exists
		}
		if !matches {
			if exists {
				currentCopy := current
				result.Value = &currentCopy
			}
			return nil
		}
		result.Swapped = true
		if params.Value == nil {
			delete(values, params.Key)
			return nil
		}
		values[params.Key] = *params.Value
		valueCopy := *params.Value
		result.Value = &valueCopy
		return nil
	})
	if err != nil {
		return nil, err
	}
	return marshalServiceResult(result)
}

func storageScope(value string) (pluginsettings.Scope, error) {
	switch pluginsettings.Scope(strings.TrimSpace(value)) {
	case pluginsettings.ScopeUser:
		return pluginsettings.ScopeUser, nil
	case pluginsettings.ScopeWorkspace:
		return pluginsettings.ScopeWorkspace, nil
	default:
		return "", serviceError("invalid_params", "storage scope must be user or workspace")
	}
}

func decodeServiceParams(raw json.RawMessage, target any) error {
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(target); err != nil {
		return serviceError("invalid_params", "invalid host service parameters")
	}
	var trailing any
	if err := decoder.Decode(&trailing); !errors.Is(err, io.EOF) {
		return serviceError("invalid_params", "invalid host service parameters")
	}
	return nil
}

func marshalServiceResult(value any) (json.RawMessage, error) {
	raw, err := json.Marshal(value)
	if err != nil {
		return nil, fmt.Errorf("encode host service result: %w", err)
	}
	return raw, nil
}

func serviceError(code, message string) error {
	return &pluginhost.HostServiceError{Code: code, Message: message}
}

func validateRuntimeSettingValue(definition pluginpkg.SettingDefinition, raw json.RawMessage) error {
	if len(raw) == 0 || len(raw) > 64<<10 || !json.Valid(raw) {
		return errors.New("value must be valid JSON within 65536 bytes")
	}
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.UseNumber()
	var value any
	if err := decoder.Decode(&value); err != nil {
		return errors.New("value is invalid")
	}
	valid := false
	switch definition.Type {
	case pluginpkg.SettingTypeBoolean:
		_, valid = value.(bool)
	case pluginpkg.SettingTypeString:
		_, valid = value.(string)
	case pluginpkg.SettingTypeNumber:
		_, valid = value.(json.Number)
	case pluginpkg.SettingTypeEnum:
		text, ok := value.(string)
		for _, candidate := range definition.Enum {
			valid = valid || (ok && text == candidate)
		}
	}
	if !valid {
		return fmt.Errorf("value does not match declared type %q", definition.Type)
	}
	return nil
}
