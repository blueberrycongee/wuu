package runtime

import (
	"context"
	"encoding/json"
	"strings"
	"sync"

	pluginpkg "github.com/blueberrycongee/wuu/internal/plugin"
	"github.com/blueberrycongee/wuu/internal/pluginhost"
	"github.com/blueberrycongee/wuu/internal/version"
)

const defaultShellModule = "wuu.default-shell"
const defaultSettingsModule = "wuu.default-settings"
const defaultNavigationModule = "wuu.default-navigation"

// ApplicationShellBinding identifies the renderer contribution selected by the
// same service registry as native and process implementations. Nil disables it.
type ApplicationShellBinding struct {
	PluginID   string `json:"plugin_id"`
	Generation string `json:"generation"`
	ProviderID string `json:"provider_id"`
}

type RendererModule struct {
	ID, SubjectID, Fingerprint, Entry string
	Services                          []pluginhost.ServiceDescriptor
}

func applicationShellDescriptor() pluginhost.ServiceDescriptor {
	return pluginhost.ServiceDescriptor{Name: "ui.shell", Version: "1.0.0", Methods: []pluginhost.ServiceMethodDescriptor{{
		Name: "describe", InputSchema: "ui.shell.describe.input.v1", OutputSchema: "ui.shell.describe.output.v1",
	}}}
}

func nativeRendererModules() []RendererModule {
	return []RendererModule{{ID: defaultShellModule, SubjectID: defaultShellModule,
		Fingerprint: "ui-shell-v1:" + version.Info().Version + ":" + version.Info().Commit,
		Entry:       "wuu-builtin:default-shell", Services: []pluginhost.ServiceDescriptor{applicationShellDescriptor()}},
		{ID: defaultSettingsModule, SubjectID: defaultSettingsModule,
			Fingerprint: "ui-settings-v1:" + version.Info().Version + ":" + version.Info().Commit,
			Entry:       "wuu-builtin:default-settings", Services: []pluginhost.ServiceDescriptor{{Name: "ui.settings", Version: "1.0.0", Methods: []pluginhost.ServiceMethodDescriptor{{Name: "describe", InputSchema: "ui.settings.describe.input.v1", OutputSchema: "ui.settings.describe.output.v1"}}}}},
		{ID: defaultNavigationModule, SubjectID: defaultNavigationModule,
			Fingerprint: "ui-navigation-v1:" + version.Info().Version + ":" + version.Info().Commit,
			Entry:       "wuu-builtin:default-navigation", Services: []pluginhost.ServiceDescriptor{{Name: "ui.navigation", Version: "1.0.0", Methods: []pluginhost.ServiceMethodDescriptor{{Name: "describe", InputSchema: "ui.navigation.describe.input.v1", OutputSchema: "ui.navigation.describe.output.v1"}}}}}}
}

// rendererServiceClient adds a renderer transport to a package's graph node.
// A mixed package retains its process owner and requirements; a desktop-only
// package owns this lifecycle directly. The renderer loads only this binding.
type rendererServiceClient struct {
	mu           sync.Mutex
	module       RendererModule
	requirements []pluginhost.ServiceRequirement
	owner        pluginhost.Client
	state        pluginhost.State
}

func newRendererService(module RendererModule, requirements []pluginhost.ServiceRequirement, owner pluginhost.Client) *rendererServiceClient {
	if services, ok := owner.(pluginhost.ServiceClient); ok {
		requirements = append(append([]pluginhost.ServiceRequirement(nil), services.RequiredServices()...), requirements...)
	}
	return &rendererServiceClient{module: module, requirements: requirements, owner: owner, state: pluginhost.StatePrepared}
}

func (r *rendererServiceClient) ID() string { return r.module.ID }
func (r *rendererServiceClient) Status() pluginhost.Status {
	if r.owner != nil {
		return r.owner.Status()
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	return pluginhost.Status{ID: r.ID(), State: r.state}
}
func (r *rendererServiceClient) ProvidedServices() []pluginhost.ServiceDescriptor {
	return r.module.Services
}
func (r *rendererServiceClient) RequiredServices() []pluginhost.ServiceRequirement {
	return r.requirements
}
func (r *rendererServiceClient) Activate(ctx context.Context) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	r.state = pluginhost.StateActive
	return nil
}
func (r *rendererServiceClient) Close(context.Context) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.state = pluginhost.StateStopped
	return nil
}
func (r *rendererServiceClient) InvokeService(_ context.Context, params pluginhost.ServiceInvokeParams) (json.RawMessage, error) {
	declared := false
	for _, service := range r.module.Services {
		if service.Name == params.Service {
			declared = true
			break
		}
	}
	if !declared || params.Method != "describe" {
		return nil, &pluginhost.HostServiceError{Code: "method_not_found", Message: "renderer service method is unavailable"}
	}
	return json.Marshal(ApplicationShellBinding{PluginID: r.module.SubjectID, Generation: r.module.Fingerprint, ProviderID: params.Service})
}

func rendererModule(item pluginpkg.Plugin) RendererModule {
	return RendererModule{ID: item.ID, SubjectID: item.SubjectID, Fingerprint: item.Fingerprint,
		Entry: item.Desktop.Entry, Services: item.Desktop.Services}
}

// NativeRendererModules returns only modules admitted by this generation.
// Their source transport differs from file modules; selection and lifecycle do not.
func (s *Session) NativeRendererModules() []RendererModule {
	s.pluginGenerationMu.Lock()
	defer s.pluginGenerationMu.Unlock()
	if s.PluginHost == nil {
		return nil
	}
	var result []RendererModule
	for _, module := range nativeRendererModules() {
		for _, status := range s.PluginHost.Statuses() {
			if status.ID == module.ID && status.State == pluginhost.StateActive {
				result = append(result, module)
			}
		}
	}
	return result
}

func (s *Session) ApplicationShellBinding() *ApplicationShellBinding {
	shell, _ := s.ApplicationBindings()
	return shell
}

// ApplicationBindings captures shell and independently selected surfaces from
// one generation. Discoverable packages never override the committed graph.
func (s *Session) ApplicationBindings() (*ApplicationShellBinding, map[string]ApplicationShellBinding) {
	surfaces := make(map[string]ApplicationShellBinding)
	if s == nil {
		return nil, surfaces
	}
	s.pluginGenerationMu.Lock()
	defer s.pluginGenerationMu.Unlock()
	if s.PluginHost == nil || s.PluginHost.ServiceRegistry() == nil {
		return nil, surfaces
	}
	registry := s.PluginHost.ServiceRegistry()
	active := make(map[string]bool)
	for _, status := range s.PluginHost.Statuses() {
		active[status.ID] = status.State == pluginhost.StateActive
	}
	modules := nativeRendererModules()
	for _, item := range s.ActivePlugins {
		if item.Desktop != nil {
			modules = append(modules, rendererModule(item))
		}
	}
	var shell *ApplicationShellBinding
	for _, module := range modules {
		if !active[module.ID] {
			continue
		}
		for _, service := range module.Services {
			if !strings.HasPrefix(service.Name, "ui.") {
				continue
			}
			provider, exists := registry.ProviderPluginID(service.Name, 1)
			if !exists || provider != module.ID {
				continue
			}
			binding := ApplicationShellBinding{PluginID: module.SubjectID, Generation: module.Fingerprint, ProviderID: service.Name}
			if service.Name == "ui.shell" {
				shell = &binding
			} else {
				surfaces[service.Name] = binding
			}
		}
	}
	return shell, surfaces
}
