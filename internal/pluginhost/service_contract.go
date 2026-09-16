package pluginhost

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"regexp"
	"strings"
)

// This file defines the provide/consume service contract: plugins publish a
// versioned service under a stable name, other plugins consume it by name and
// major version, and every call is routed and validated by the host. Service
// declarations ride the same initialize handshake as capabilities and are
// collected during the prepare phase; calls only flow once both generations
// are active.
const (
	// ServiceCallMethod is the plugin -> host gateway frame for consuming a
	// registered service. It is advertised alongside host services but is
	// validated and routed through the service registry, not the fixed host
	// service table.
	ServiceCallMethod HostServiceMethod = "host.service.call"

	// ServiceInvokeMethod is the host -> plugin request delivering one
	// validated call to the service provider.
	ServiceInvokeMethod = "service.invoke"

	// ServiceChangedMethod notifies consumers that a provided service was
	// republished or revoked, so they can re-resolve on their next call.
	ServiceChangedMethod = "service.changed"

	KernelServiceMethod = "call"

	KernelStorageGetService             = "host.storage.get"
	KernelStorageSetService             = "host.storage.set"
	KernelStorageDeleteService          = "host.storage.delete"
	KernelStorageKeysService            = "host.storage.keys"
	KernelStorageCompareExchangeService = "host.storage.compare-exchange"
	KernelSettingsGetService            = "host.settings.get"
	KernelSettingsListService           = "host.settings.list"
	KernelSessionCreateService          = "host.session.create"
	KernelSessionSendService            = "host.session.send"
	KernelSessionListService            = "host.session.list"
	KernelSessionCancelService          = "host.session.cancel"
	KernelSessionInspectService         = "host.session.inspect"
	KernelSessionControlService         = "host.session.control"
	KernelSessionHistoryReadService     = "host.session.history.read"
	KernelSessionHistorySearchService   = "host.session.history.search"
	KernelWorkspaceStatusService        = "host.workspace.status"
	KernelWorkspaceApplyService         = "host.workspace.apply"
	KernelWorkspaceDiscardService       = "host.workspace.discard"
	KernelUserQuestionAskService        = "host.user-question.ask"
	KernelUserQuestionOfferService      = "host.user-question.offer"
	KernelArtifactImportService         = "host.artifact.import"

	// KernelRegistryIntrospectService is the kernel's read-only registry
	// introspection service: which services exist, at what version, provided
	// by whom, in which generation.
	KernelRegistryIntrospectService = "registry.introspect"

	// KernelExecutionUpdateService is the kernel's execution progress
	// endpoint; the constant lives with the execution scope vocabulary.
	KernelExecutionUpdateService = ExecutionUpdateService

	// KernelDriverModelLoopService is the kernel gateway endpoint a remote
	// loop driver executes model loops through; calls are routed to the
	// gateway registered for the execution id in the params.
	KernelDriverModelLoopService = "driver.model-loop"

	// KernelDriverCheckpointService is the kernel gateway endpoint a remote
	// loop driver persists checkpoints through.
	KernelDriverCheckpointService = "driver.checkpoint"
)

// KernelServiceDescriptors are the host-provided services available to every
// plugin generation. They use the same registry contract as plugin providers.
func KernelServiceDescriptors() []ServiceDescriptor {
	names := []string{
		KernelStorageGetService, KernelStorageSetService, KernelStorageDeleteService,
		KernelStorageKeysService, KernelStorageCompareExchangeService,
		KernelSettingsGetService, KernelSettingsListService,
		KernelSessionCreateService, KernelSessionSendService, KernelSessionListService,
		KernelSessionCancelService, KernelSessionInspectService,
		KernelSessionHistoryReadService, KernelSessionHistorySearchService,
		KernelWorkspaceStatusService, KernelWorkspaceApplyService, KernelWorkspaceDiscardService,
		KernelSessionControlService,
	}
	descriptors := make([]ServiceDescriptor, 0, len(names))
	for _, name := range names {
		schema := strings.ReplaceAll(name, "-", ".")
		descriptors = append(descriptors, ServiceDescriptor{
			Name: name, Version: "1.0.0",
			Methods: []ServiceMethodDescriptor{{Name: KernelServiceMethod, InputSchema: schema + ".input.v1", OutputSchema: schema + ".output.v1"}},
		})
	}
	return descriptors
}

func KernelServiceRequirements(names ...string) []ServiceRequirement {
	requirements := make([]ServiceRequirement, 0, len(names))
	for _, name := range names {
		requirements = append(requirements, ServiceRequirement{Name: name, MajorVersion: 1, Required: true})
	}
	return requirements
}

func KernelPreflightRequirements() []ServiceRequirement {
	return KernelServiceRequirements(
		KernelStorageGetService, KernelStorageKeysService,
		KernelSettingsGetService, KernelSettingsListService,
		KernelSessionListService, KernelSessionInspectService,
		KernelSessionHistoryReadService, KernelSessionHistorySearchService, KernelWorkspaceStatusService,
	)
}

// KernelRegistryIntrospectDescriptor is the descriptor the kernel registers
// for registry introspection. It rides the same registry contract as every
// other kernel service.
func KernelRegistryIntrospectDescriptor() ServiceDescriptor {
	return ServiceDescriptor{
		Name: KernelRegistryIntrospectService, Version: "1.0.0",
		Methods: []ServiceMethodDescriptor{{Name: KernelServiceMethod, InputSchema: "registry.introspect.input.v1", OutputSchema: "registry.introspect.output.v1"}},
	}
}

// KernelExecutionUpdateDescriptor is the descriptor the kernel registers for
// execution progress. The complementary host-initiated frames (open riding
// the invoke params, execution.cancel mid-flight, close riding the invoke
// response) complete the execution scope vocabulary without being callable
// services.
func KernelExecutionUpdateDescriptor() ServiceDescriptor {
	return ServiceDescriptor{
		Name: ExecutionUpdateService, Version: "1.0.0",
		Methods: []ServiceMethodDescriptor{{Name: KernelServiceMethod, InputSchema: "execution.update.request.v1", OutputSchema: "execution.update.response.v1"}},
	}
}

func KernelUserQuestionAskDescriptor() ServiceDescriptor {
	return ServiceDescriptor{
		Name: KernelUserQuestionAskService, Version: "1.0.0",
		Methods: []ServiceMethodDescriptor{{Name: KernelServiceMethod, InputSchema: "host.user-question.ask.input.v1", OutputSchema: "host.user-question.ask.output.v1"}},
	}
}

func KernelUserQuestionOfferDescriptor() ServiceDescriptor {
	return ServiceDescriptor{
		Name: KernelUserQuestionOfferService, Version: "1.0.0",
		Methods: []ServiceMethodDescriptor{{Name: KernelServiceMethod, InputSchema: "host.user-question.offer.input.v1", OutputSchema: "host.user-question.offer.output.v1"}},
	}
}

func KernelArtifactImportDescriptor() ServiceDescriptor {
	return ServiceDescriptor{
		Name: KernelArtifactImportService, Version: "1.0.0",
		Methods: []ServiceMethodDescriptor{{Name: KernelServiceMethod, InputSchema: "host.artifact.import.input.v1", OutputSchema: "host.artifact.import.output.v1"}},
	}
}

// KernelDriverModelLoopDescriptor is the descriptor the kernel registers for
// the remote-driver model-loop gateway. The method set is the single kernel
// verb; execution scoping rides the params.
func KernelDriverModelLoopDescriptor() ServiceDescriptor {
	return ServiceDescriptor{
		Name: KernelDriverModelLoopService, Version: "1.0.0",
		Methods: []ServiceMethodDescriptor{{Name: KernelServiceMethod, InputSchema: "driver.model-loop.request.v1", OutputSchema: "driver.model-loop.response.v1"}},
	}
}

// KernelDriverCheckpointDescriptor is the descriptor the kernel registers
// for the remote-driver checkpoint gateway.
func KernelDriverCheckpointDescriptor() ServiceDescriptor {
	return ServiceDescriptor{
		Name: KernelDriverCheckpointService, Version: "1.0.0",
		Methods: []ServiceMethodDescriptor{{Name: KernelServiceMethod, InputSchema: "driver.checkpoint.request.v1", OutputSchema: "driver.checkpoint.response.v1"}},
	}
}

var (
	serviceNamePattern    = regexp.MustCompile(`^[a-z][a-z0-9-]*(\.[a-z][a-z0-9-]*)+$`)
	serviceMethodPattern  = regexp.MustCompile(`^[a-z][a-z0-9-]*(\.[a-z][a-z0-9-]*)*$`)
	serviceSchemaPattern  = regexp.MustCompile(`^[a-z][a-z0-9-]*(\.[a-z0-9-]+)+$`)
	serviceVersionPattern = regexp.MustCompile(`^(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)$`)
)

// ServiceMethodDescriptor declares one typed method of a provided service.
type ServiceMethodDescriptor struct {
	Name         string `json:"name"`
	InputSchema  string `json:"input_schema"`
	OutputSchema string `json:"output_schema"`
}

// ServiceDescriptor declares a versioned service a plugin provides. The name
// is stable across versions; consumers resolve by name and major version.
type ServiceDescriptor struct {
	Name    string                    `json:"name"`
	Version string                    `json:"version"`
	Methods []ServiceMethodDescriptor `json:"methods"`
}

// ServiceRequirement declares a consumer's need for one service. Authority to
// call a service is exactly this explicit declaration; the host never infers
// it from plugin identity or session knowledge.
type ServiceRequirement struct {
	Name         string `json:"name"`
	MajorVersion int    `json:"major_version"`
	Required     bool   `json:"required,omitempty"`
}

// ServiceCallParams is the payload of the service.call gateway frame.
type ServiceCallParams struct {
	Service     string          `json:"service"`
	Method      string          `json:"method"`
	ExecutionID string          `json:"execution_id,omitempty"`
	Params      json.RawMessage `json:"params,omitempty"`
}

// ServiceInvokeParams is the payload of the host -> provider service.invoke
// request. Caller carries the validated consumer plugin ID.
type ServiceInvokeParams struct {
	Service string          `json:"service"`
	Method  string          `json:"method"`
	Caller  string          `json:"caller"`
	Params  json.RawMessage `json:"params,omitempty"`
	// ExecutionID, when set, binds the invoke to one execution scope so
	// cross-process cancellation propagates through the execution plane.
	ExecutionID string `json:"execution_id,omitempty"`
}

// ServiceChangedParams notifies a consumer that a service resolution changed.
type ServiceChangedParams struct {
	Service string `json:"service"`
	Reason  string `json:"reason,omitempty"`
}

// ServiceRouter routes validated service.call frames into the active
// generation's service registry. The runtime implements it; the pluginhost
// package depends only on this narrow surface.
type ServiceRouter interface {
	RouteServiceCall(ctx context.Context, pluginID string, params ServiceCallParams) (json.RawMessage, *HostServiceError)
}

// ServiceVersionMajor parses the declared semver and returns its major. The
// descriptor is only usable after ValidateServiceDescriptor accepts it.
func ServiceVersionMajor(version string) (int, bool) {
	match := serviceVersionPattern.FindStringSubmatch(strings.TrimSpace(version))
	if match == nil {
		return 0, false
	}
	major := 0
	for _, digit := range match[1] {
		major = major*10 + int(digit-'0')
	}
	return major, true
}

func ValidateServiceDescriptor(descriptor ServiceDescriptor) error {
	name := strings.TrimSpace(descriptor.Name)
	if !serviceNamePattern.MatchString(name) {
		return fmt.Errorf("service name %q must be a dotted lowercase identifier", descriptor.Name)
	}
	if _, ok := ServiceVersionMajor(descriptor.Version); !ok {
		return fmt.Errorf("service %s: version %q must be strict MAJOR.MINOR.PATCH semver", name, descriptor.Version)
	}
	if len(descriptor.Methods) == 0 {
		return fmt.Errorf("service %s must declare at least one method", name)
	}
	seen := make(map[string]struct{}, len(descriptor.Methods))
	for _, method := range descriptor.Methods {
		methodName := strings.TrimSpace(method.Name)
		if !serviceMethodPattern.MatchString(methodName) {
			return fmt.Errorf("service %s: method name %q must be a lowercase identifier", name, method.Name)
		}
		if _, exists := seen[methodName]; exists {
			return fmt.Errorf("service %s: duplicate method %q", name, methodName)
		}
		seen[methodName] = struct{}{}
		if !serviceSchemaPattern.MatchString(strings.TrimSpace(method.InputSchema)) {
			return fmt.Errorf("service %s.%s: input schema %q must be a dotted versioned identifier", name, methodName, method.InputSchema)
		}
		if !serviceSchemaPattern.MatchString(strings.TrimSpace(method.OutputSchema)) {
			return fmt.Errorf("service %s.%s: output schema %q must be a dotted versioned identifier", name, methodName, method.OutputSchema)
		}
	}
	return nil
}

func ValidateServiceRequirement(requirement ServiceRequirement) error {
	name := strings.TrimSpace(requirement.Name)
	if !serviceNamePattern.MatchString(name) {
		return fmt.Errorf("required service name %q must be a dotted lowercase identifier", requirement.Name)
	}
	if requirement.MajorVersion < 0 {
		return fmt.Errorf("required service %s: major version cannot be negative", name)
	}
	return nil
}

// ValidateServiceNegotiation validates the service declarations of one
// initialize response. Cross-plugin satisfaction is checked by the runtime
// once every client of a generation has completed initialize; there is no
// dependency solver — an unsatisfied required service simply blocks that
// consumer's activation with a diagnostic.
func ValidateServiceNegotiation(result CapabilityInitializeResult) error {
	if len(result.ProvidedServices) == 0 && len(result.RequiredServices) == 0 {
		return nil
	}
	version := result.ProtocolVersion
	if version == 0 {
		version = ProtocolVersion
	}
	if version < 3 {
		return errors.New("service provide/consume declarations require capability protocol v3")
	}
	provided := make(map[string]struct{}, len(result.ProvidedServices))
	for _, descriptor := range result.ProvidedServices {
		if err := ValidateServiceDescriptor(descriptor); err != nil {
			return err
		}
		name := strings.TrimSpace(descriptor.Name)
		major, _ := ServiceVersionMajor(descriptor.Version)
		key := fmt.Sprintf("%s@%d", name, major)
		if _, exists := provided[key]; exists {
			return fmt.Errorf("duplicate provided service %s major version %d", name, major)
		}
		provided[key] = struct{}{}
	}
	required := make(map[string]struct{}, len(result.RequiredServices))
	for _, requirement := range result.RequiredServices {
		if err := ValidateServiceRequirement(requirement); err != nil {
			return err
		}
		name := strings.TrimSpace(requirement.Name)
		if _, exists := required[name]; exists {
			return fmt.Errorf("duplicate required service %q", name)
		}
		required[name] = struct{}{}
	}
	for _, descriptor := range result.ProvidedServices {
		name := strings.TrimSpace(descriptor.Name)
		if _, conflict := required[name]; conflict {
			return fmt.Errorf("service %s cannot be both provided and required by one plugin", name)
		}
	}
	return nil
}
