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

	"github.com/blueberrycongee/wuu/internal/config"
	"github.com/blueberrycongee/wuu/internal/credentialstore"
	"github.com/blueberrycongee/wuu/internal/persistence"
	pluginpkg "github.com/blueberrycongee/wuu/internal/plugin"
	"github.com/blueberrycongee/wuu/internal/pluginhost"
	"github.com/blueberrycongee/wuu/internal/pluginsettings"
	"github.com/blueberrycongee/wuu/internal/providers"
	"github.com/blueberrycongee/wuu/internal/session"
	"github.com/blueberrycongee/wuu/internal/toolbatch"
	"github.com/blueberrycongee/wuu/internal/userspace"
)

// pluginHostServices is bound to exactly one installed plugin generation. It
// never accepts a caller-supplied plugin ID or absolute filesystem path:
// ownership and roots are captured from the validated inventory at activation.
type pluginHostServices struct {
	ownerHost              *pluginhost.Host
	binding                userspace.Binding
	artifacts              *ArtifactClient
	mu                     sync.RWMutex
	active                 bool
	open                   bool
	pluginID               string
	subjectID              string
	fingerprint            string
	projectRoot            string
	wuuHome                string
	settings               map[string]pluginpkg.SettingDefinition
	sessions               *session.Store
	credentials            credentialstore.Store
	settingsClient         *pluginsettings.Client
	settingsMu             sync.Mutex
	settingsBaseline       map[string]json.RawMessage
	settingsOperations     map[string]map[string]json.RawMessage
	settingsOperationStops map[string]func() bool
	turnRouter             *PluginSessionRouter
	fileLocks              map[string]func()
	lockContext            context.Context
	lockCancel             context.CancelFunc
	processMu              sync.Mutex
	processes              map[string]*pluginProcess
	processStarts          map[string]*pluginProcessAdmission
	processStartOrder      []string
	processActive          int
	processesClosed        bool
	closeCompositions      func() error
}

func newPluginHostServices(item pluginpkg.Plugin, projectRoot, wuuHome string, turnRouter *PluginSessionRouter) *pluginHostServices {
	settings := make(map[string]pluginpkg.SettingDefinition, len(item.Settings))
	for key, definition := range item.Settings {
		settings[key] = definition
	}
	lockContext, lockCancel := context.WithCancel(context.Background())
	services := &pluginHostServices{
		open: true, pluginID: item.ID, subjectID: strings.TrimSpace(item.SubjectID),
		fingerprint: item.Fingerprint, projectRoot: projectRoot, wuuHome: wuuHome,
		settings: settings, turnRouter: turnRouter,
		fileLocks: make(map[string]func()), lockContext: lockContext, lockCancel: lockCancel,
	}
	return services
}

func (s *pluginHostServices) close() {
	// Cancel in-flight I/O before waiting for invoke's read lock.
	if s.lockCancel != nil {
		s.lockCancel()
	}
	s.mu.Lock()
	s.open = false
	s.active = false
	locks := s.fileLocks
	s.fileLocks = make(map[string]func())
	closeCompositions := s.closeCompositions
	s.closeCompositions = nil
	s.mu.Unlock()
	if closeCompositions != nil {
		if err := closeCompositions(); err != nil {
			providers.DebugLogf("close plugin compositions: %v", err)
		}
	}
	for _, release := range locks {
		release()
	}
	s.closeSettings()
	s.closeProcesses()
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
	case pluginhost.HostServiceExecutionAuthorization:
		return s.executionAuthorizationService(ctx, raw)
	case pluginhost.HostServicePersonal:
		return s.personalService(ctx, raw)
	case pluginhost.HostServiceSessionContext:
		var params struct {
			SessionID              string                                      `json:"session_id"`
			ExecutionAuthorization *pluginhost.ExecutionAuthorizationReference `json:"execution_authorization,omitempty"`
		}
		if err := decodeServiceParams(raw, &params); err != nil {
			return nil, err
		}
		if strings.TrimSpace(params.SessionID) == "" {
			return nil, serviceError("invalid_params", "session_id is required")
		}
		if s.sessions == nil {
			return nil, serviceError("service_unavailable", "session context is unavailable")
		}
		ctx, accessErr := s.authorizeSessionResource(ctx, params.SessionID, params.ExecutionAuthorization, "context")
		if accessErr != nil {
			return nil, accessErr
		}
		metadata, found, err := s.sessions.WithContext(ctx).Find(params.SessionID)
		if err != nil {
			return nil, err
		}
		if !found {
			return nil, session.ErrSessionNotFound
		}
		binding := userspace.Binding{UserSpaceID: metadata.UserSpaceID, ProductDomain: metadata.ProductDomain}
		if !s.binding.Matches(binding) {
			return nil, serviceError("domain_denied", "session belongs to another user space or product domain")
		}
		return marshalServiceResult(map[string]string{"session_id": metadata.ID, "user_space_id": metadata.UserSpaceID, "product_domain": binding.Domain(), "agent_id": metadata.AgentID, "parent_session_id": metadata.ParentID})
	case pluginhost.HostServiceSessionRoute:
		if s.turnRouter == nil {
			return nil, serviceError("service_unavailable", "session service is unavailable")
		}
		var params pluginhost.SessionRouteRequest
		if err := decodeServiceParams(raw, &params); err != nil {
			return nil, err
		}
		result, err := s.turnRouter.Route(ctx, s.pluginID, params)
		if err != nil {
			return nil, err
		}
		return marshalServiceResult(result)
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
	case pluginhost.HostServiceSessionAttachmentRead:
		var params pluginhost.SessionAttachmentReadParams
		if err := decodeServiceParams(raw, &params); err != nil {
			return nil, err
		}
		ctx, accessErr := s.authorizeSessionResource(ctx, params.SessionID, params.ExecutionAuthorization, "attachment.read")
		if accessErr != nil {
			return nil, accessErr
		}
		result, err := readPluginSessionAttachment(ctx, s.sessions, s.artifacts, s.pluginID, s.binding, params)
		if err != nil {
			return nil, err
		}
		return marshalServiceResult(result)
	case pluginhost.HostServiceSessionHistoryRead:
		var params pluginhost.SessionHistoryReadParams
		if err := decodeServiceParams(raw, &params); err != nil {
			return nil, err
		}
		ctx, accessErr := s.authorizeSessionResource(ctx, params.SessionID, params.ExecutionAuthorization, "history.read")
		if accessErr != nil {
			return nil, accessErr
		}
		result, err := readPluginSessionHistory(ctx, s.sessions, s.pluginID, s.binding, params)
		if err != nil {
			return nil, err
		}
		return marshalServiceResult(result)
	case pluginhost.HostServiceSessionHistorySearch:
		var params pluginhost.SessionHistorySearchParams
		if err := decodeServiceParams(raw, &params); err != nil {
			return nil, err
		}
		ctx, accessErr := s.authorizeSessionResource(ctx, params.SessionID, params.ExecutionAuthorization, "history.search")
		if accessErr != nil {
			return nil, accessErr
		}
		result, err := searchPluginSessionHistory(ctx, s.sessions, s.pluginID, s.binding, params)
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
	case pluginhost.HostServiceCredentialsRequest:
		return s.credentialRequest(ctx, raw)
	case pluginhost.HostServiceSettingsGet:
		return s.settingsGet(ctx, raw)
	case pluginhost.HostServiceSettingsList:
		var params pluginhost.SettingsListParams
		if err := decodeServiceParams(raw, &params); err != nil {
			return nil, err
		}
		view, err := s.settingsSnapshot(ctx, params.Scope)
		if err != nil {
			return nil, err
		}
		return marshalServiceResult(view)
	case pluginhost.HostServiceStorageGet:
		var params pluginhost.StorageGetParams
		if err := decodeServiceParams(raw, &params); err != nil {
			return nil, err
		}
		return s.storageGet(ctx, params)
	case pluginhost.HostServiceStorageSet:
		var params pluginhost.StorageSetParams
		if err := decodeServiceParams(raw, &params); err != nil {
			return nil, err
		}
		return s.storageSet(ctx, params)
	case pluginhost.HostServiceStorageDelete:
		var params pluginhost.StorageDeleteParams
		if err := decodeServiceParams(raw, &params); err != nil {
			return nil, err
		}
		return s.storageDelete(ctx, params)
	case pluginhost.HostServiceStorageKeys:
		var params pluginhost.StorageKeysParams
		if err := decodeServiceParams(raw, &params); err != nil {
			return nil, err
		}
		return s.storageKeys(ctx, params)
	case pluginhost.HostServiceStorageCompareExchange:
		var params pluginhost.StorageCompareExchangeParams
		if err := decodeServiceParams(raw, &params); err != nil {
			return nil, err
		}
		return s.storageCompareExchange(ctx, params)
	default:
		return nil, serviceError("method_not_found", fmt.Sprintf("host service %q is not implemented", method))
	}
}

func (s *pluginHostServices) settingsGet(ctx context.Context, raw json.RawMessage) (json.RawMessage, error) {
	var params pluginhost.SettingsGetParams
	if err := decodeServiceParams(raw, &params); err != nil {
		return nil, err
	}
	localKey, definition, err := s.ownedSetting(params.Key)
	if err != nil {
		return nil, err
	}
	// Secrets never enter the ordinary settings read path.
	if definition.Type == pluginpkg.SettingTypeSecret {
		return marshalServiceResult(pluginhost.SettingsGetResult{Value: json.RawMessage(`""`)})
	}
	view, err := s.settingsSnapshot(ctx, "")
	if err != nil {
		return nil, err
	}
	return marshalServiceResult(pluginhost.SettingsGetResult{Value: view.Entries[localKey]})
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
		pluginhost.HostServiceSessionList, pluginhost.HostServiceSessionInspect, pluginhost.HostServiceSessionContext,
		pluginhost.HostServiceSessionHistoryRead, pluginhost.HostServiceSessionHistorySearch, pluginhost.HostServiceSessionAttachmentRead,
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

const kernelHostModule = "kernel"

type kernelHostServices struct {
	sessionRouter     *PluginSessionRouter
	requiredPlugins   map[string]bool
	stateGeneration   *PluginGeneration
	stateOwner        *Session
	mu                sync.RWMutex
	active            bool
	closed            bool
	services          map[string]*pluginHostServices
	registry          *pluginhost.ServiceRegistry
	generation        func() uint64
	executions        executionUpdateRecorder
	userQuestions     *pluginhost.UserQuestionBroker
	engineGateways    *engineGatewayTable
	driverGateways    *driverGatewayTable
	workspaceStateDir string
	wuuHome           string
	modelSession      *Session
	modelHost         *pluginhost.Host
	state             *runtimeState
	selectedServices  *runtimeServices
	providers         map[string]nativeServiceRegistration
	compositions      *compositionService
}

type nativeServiceRegistration struct {
	Descriptor pluginhost.ServiceDescriptor
	Invoker    pluginhost.ServiceInvoker
}

func newKernelHostServices(generation func() uint64, executions executionUpdateRecorder) *kernelHostServices {
	k := &kernelHostServices{services: make(map[string]*pluginHostServices), generation: generation, executions: executions, engineGateways: newEngineGatewayTable(), driverGateways: newDriverGatewayTable(), providers: make(map[string]nativeServiceRegistration)}
	k.compositions = newCompositionService(k)
	for _, registration := range k.serviceRegistrations() {
		k.providers[registration.Descriptor.Name] = registration
	}
	return k
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
	services.closeCompositions = func() error { return k.compositions.stopCaller(pluginID) }
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

func (k *kernelHostServices) ID() string { return kernelHostModule }

func (k *kernelHostServices) Status() pluginhost.Status {
	k.mu.RLock()
	defer k.mu.RUnlock()
	state := pluginhost.StatePrepared
	if k.active {
		state = pluginhost.StateActive
	}
	if k.closed {
		state = pluginhost.StateStopped
	}
	return pluginhost.Status{ID: k.ID(), State: state}
}

func (k *kernelHostServices) ProvidedServices() []pluginhost.ServiceDescriptor {
	descriptors := make([]pluginhost.ServiceDescriptor, 0, len(k.providers))
	for _, registration := range k.providers {
		descriptors = append(descriptors, registration.Descriptor)
	}
	sort.Slice(descriptors, func(i, j int) bool { return descriptors[i].Name < descriptors[j].Name })
	return descriptors
}

func (k *kernelHostServices) InvokeService(ctx context.Context, params pluginhost.ServiceInvokeParams) (json.RawMessage, error) {
	registration, ok := k.providers[params.Service]
	if !ok {
		return nil, serviceError("service_unavailable", "native service is unavailable")
	}
	return registration.Invoker.InvokeService(ctx, params)
}
func (k *kernelHostServices) RequiredServices() []pluginhost.ServiceRequirement {
	return []pluginhost.ServiceRequirement{
		// These adapters are lazy. Selected dependencies still participate in
		// lifecycle ordering; invoking an omitted service fails at the router.
		{Name: credentialstore.ServiceName, MajorVersion: 1},
		{Name: pluginsettings.ServiceName, MajorVersion: 1},
		{Name: config.ServiceName, MajorVersion: 1, Required: true},
		{Name: ArtifactServiceName, MajorVersion: 1},
		{Name: persistence.ServiceName, MajorVersion: 1},
		{Name: session.ServiceName, MajorVersion: 1},
	}
}
func (k *kernelHostServices) serviceRegistrations() []nativeServiceRegistration {
	descriptors := pluginhost.KernelServiceDescriptors()

	registrations := make([]nativeServiceRegistration, 0, len(descriptors)+8)
	for _, descriptor := range descriptors {
		method := pluginhost.HostServiceMethod(descriptor.Name)
		// The original storage gateway used an underscore; the service name is hyphenated.
		if descriptor.Name == pluginhost.KernelStorageCompareExchangeService {
			method = pluginhost.HostServiceStorageCompareExchange
		}
		registrations = append(registrations,
			nativeServiceRegistration{Descriptor: engineGatewayDescriptor(), Invoker: &engineGatewayInvoker{parent: k}}, nativeServiceRegistration{
				Descriptor: descriptor, Invoker: &kernelServiceInvoker{parent: k, method: method},
			})
	}
	registrations = append(registrations,
		nativeServiceRegistration{Descriptor: desktopGatewayDescriptor(), Invoker: &desktopSessionGateway{parent: k}},
		nativeServiceRegistration{Descriptor: pluginhost.KernelClientInvalidationDescriptor(), Invoker: &clientInvalidationInvoker{parent: k}},
		nativeServiceRegistration{Descriptor: pluginhost.KernelModelReceiptDescriptor(), Invoker: &modelReceiptInvoker{parent: k}},
		nativeServiceRegistration{Descriptor: pluginhost.CompositionDescriptor(), Invoker: k.compositions},
		nativeServiceRegistration{
			Descriptor: pluginhost.KernelModelInvokeDescriptor(),
			Invoker:    &modelInvokeInvoker{parent: k, store: newModelInvocationStore()},
		},
		nativeServiceRegistration{
			Descriptor: pluginhost.KernelModelEmitDescriptor(),
			Invoker:    &modelEmitInvoker{parent: k},
		},
		nativeServiceRegistration{
			Descriptor: pluginhost.KernelProcessDescriptor(),
			Invoker:    &processServiceInvoker{parent: k},
		},
		nativeServiceRegistration{
			Descriptor: pluginhost.KernelBackgroundDescriptor(),
			Invoker:    &backgroundInvoker{parent: k},
		},
		nativeServiceRegistration{
			Descriptor: pluginhost.KernelModelRequestDescriptor(),
			Invoker:    &modelRequestInvoker{parent: k},
		},
		nativeServiceRegistration{
			Descriptor: pluginhost.KernelRegistryIntrospectDescriptor(),
			Invoker:    &registryIntrospectInvoker{parent: k},
		},
		nativeServiceRegistration{
			Descriptor: pluginhost.KernelExecutionUpdateDescriptor(),
			Invoker:    &executionUpdateInvoker{parent: k},
		},
		nativeServiceRegistration{
			Descriptor: pluginhost.KernelInvokeToolDescriptor(),
			Invoker:    &nestedToolInvoker{parent: k},
		},
		nativeServiceRegistration{
			Descriptor: pluginhost.KernelUserQuestionAskDescriptor(),
			Invoker:    &userQuestionAskInvoker{parent: k},
		},
		nativeServiceRegistration{
			Descriptor: pluginhost.KernelUserQuestionOfferDescriptor(),
			Invoker:    &userQuestionOfferInvoker{parent: k},
		},
		nativeServiceRegistration{
			Descriptor: pluginhost.KernelFileLockDescriptor(),
			Invoker:    &fileLockInvoker{parent: k},
		},
		nativeServiceRegistration{
			Descriptor: pluginhost.KernelArtifactImportDescriptor(),
			Invoker:    &artifactImportInvoker{parent: k},
		},
		nativeServiceRegistration{Descriptor: toolbatch.ResourceDescriptor(), Invoker: &toolBatchResourceInvoker{parent: k}},
		nativeServiceRegistration{
			Descriptor: pluginhost.DriverTurnDescriptor(),
			Invoker:    &driverTurnInvoker{parent: k},
		},
		nativeServiceRegistration{
			Descriptor: pluginhost.KernelDriverCheckpointDescriptor(),
			Invoker:    &driverCheckpointInvoker{parent: k},
		},
	)
	return registrations
}

// Prepare captures structural settings before any dependent plugin preparation.
// Failed validation rejects the candidate graph, leaving the admitted graph intact.
func (k *kernelHostServices) Prepare(ctx context.Context) error {
	ctx = context.WithValue(ctx, kernelServicePreparationKey{}, true)
	k.mu.RLock()
	services := make([]*pluginHostServices, 0, len(k.services))
	for _, service := range k.services {
		services = append(services, service)
	}
	k.mu.RUnlock()
	for _, service := range services {
		if err := service.prepareSettings(ctx); err != nil {
			return fmt.Errorf("plugin %s settings: %w", service.pluginID, err)
		}
	}
	return nil
}

func (k *kernelHostServices) Activate(ctx context.Context) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	k.mu.Lock()
	if k.closed {
		k.mu.Unlock()
		return errors.New("native host services are closed")
	}
	k.active = true
	services := make([]*pluginHostServices, 0, len(k.services))
	for _, service := range k.services {
		services = append(services, service)
	}
	k.mu.Unlock()
	for _, service := range services {
		service.activate()
	}
	return nil
}

// Close completes resource cleanup even when the generation's context is cancelled.
func (k *kernelHostServices) Close(context.Context) error {
	k.mu.Lock()
	k.active = false
	k.closed = true
	services := make([]*pluginHostServices, 0, len(k.services))
	for _, service := range k.services {
		services = append(services, service)
	}
	k.mu.Unlock()
	k.engineGateways.close()
	err := k.compositions.close()
	for _, service := range services {
		service.close()
	}
	return err
}

type kernelServiceInvoker struct {
	parent *kernelHostServices
	method pluginhost.HostServiceMethod
}

func (k *kernelServiceInvoker) ID() string                { return k.parent.ID() }
func (k *kernelServiceInvoker) Status() pluginhost.Status { return k.parent.Status() }
func (k *kernelServiceInvoker) InvokeService(ctx context.Context, params pluginhost.ServiceInvokeParams) (result json.RawMessage, callErr error) {
	k.parent.mu.RLock()
	services := k.parent.services[params.Caller]
	k.parent.mu.RUnlock()
	if services == nil {
		return nil, serviceError("service_unavailable", "kernel service caller is unavailable")
	}
	if params.Method != pluginhost.KernelServiceMethod {
		return nil, serviceError("method_not_found", "kernel service method is unavailable")
	}
	ctx = context.WithValue(ctx, kernelServiceExecutionKey{}, params.ExecutionID)
	if params.CallerScope != nil {
		scope, err := params.CallerExecution(false)
		if err != nil {
			return nil, err
		}
		ctx = context.WithValue(ctx, pluginSettingsOperationKey{}, &scope)
	}
	ctx, releaseState := k.parent.captureInvocationState(ctx)
	defer func() { callErr = errors.Join(callErr, releaseState()) }()
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
	parent.mu.RUnlock()
	if broker == nil {
		return nil, serviceError("service_unavailable", "user interaction is unavailable")
	}
	scope, scopeErr := params.CallerExecution(true)
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

func (s *pluginHostServices) storageGet(ctx context.Context, params pluginhost.StorageGetParams) (json.RawMessage, error) {
	scope, err := storageScope(params.Scope)
	if err != nil {
		return nil, err
	}
	if err := pluginsettings.ValidateStateKey(params.Key); err != nil {
		return nil, serviceError("invalid_params", err.Error())
	}
	document, err := s.settingsClient.ReadState(ctx, s.subjectID, scope)
	if err != nil {
		return nil, err
	}
	value, ok := document.Values[params.Key]
	if !ok {
		return marshalServiceResult(pluginhost.StorageGetResult{})
	}
	return marshalServiceResult(pluginhost.StorageGetResult{Value: &value})
}

func (s *pluginHostServices) storageSet(ctx context.Context, params pluginhost.StorageSetParams) (json.RawMessage, error) {
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
	_, err = s.settingsClient.UpdateState(ctx, s.subjectID, scope, func(values map[string]string) error {
		values[params.Key] = params.Value
		return nil
	})
	if err != nil {
		return nil, err
	}
	return marshalServiceResult(struct{}{})
}

func (s *pluginHostServices) storageDelete(ctx context.Context, params pluginhost.StorageDeleteParams) (json.RawMessage, error) {
	scope, err := storageScope(params.Scope)
	if err != nil {
		return nil, err
	}
	if err := pluginsettings.ValidateStateKey(params.Key); err != nil {
		return nil, serviceError("invalid_params", err.Error())
	}
	_, err = s.settingsClient.UpdateState(ctx, s.subjectID, scope, func(values map[string]string) error {
		delete(values, params.Key)
		return nil
	})
	if err != nil {
		return nil, err
	}
	return marshalServiceResult(struct{}{})
}

func (s *pluginHostServices) storageKeys(ctx context.Context, params pluginhost.StorageKeysParams) (json.RawMessage, error) {
	scope, err := storageScope(params.Scope)
	if err != nil {
		return nil, err
	}
	document, err := s.settingsClient.ReadState(ctx, s.subjectID, scope)
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

func (s *pluginHostServices) storageCompareExchange(ctx context.Context, params pluginhost.StorageCompareExchangeParams) (json.RawMessage, error) {
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
	_, err = s.settingsClient.UpdateState(ctx, s.subjectID, scope, func(values map[string]string) error {
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
