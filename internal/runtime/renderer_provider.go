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

const defaultApplicationModule = "wuu.default-application"
const defaultShellModule = "wuu.default-shell"
const defaultSettingsModule = "wuu.default-settings"
const defaultNavigationModule = "wuu.default-navigation"
const defaultConversationModule = "wuu.default-conversation"
const defaultPreviewModule = "wuu.default-preview"
const defaultComposerModule = "wuu.default-composer"

// ApplicationShellBinding identifies the renderer contribution selected by the
// same service registry as native and process implementations. Nil disables it.
type ApplicationShellBinding struct {
	PluginID   string `json:"plugin_id"`
	Generation string `json:"generation"`
	ProviderID string `json:"provider_id"`
}

type RendererModule struct {
	ID, SubjectID, Fingerprint, Entry string
	Name, Description                 string
	Services                          []pluginhost.ServiceDescriptor
	Requirements                      []pluginhost.ServiceRequirement
}

func applicationShellDescriptor() pluginhost.ServiceDescriptor {
	return pluginhost.ServiceDescriptor{Name: "ui.shell", Version: "1.0.0", Methods: []pluginhost.ServiceMethodDescriptor{{
		Name: "describe", InputSchema: "ui.shell.describe.input.v1", OutputSchema: "ui.shell.describe.output.v1",
	}}}
}

func nativeRendererModules() []RendererModule {
	modules := []RendererModule{{ID: defaultShellModule, SubjectID: defaultShellModule,
		Name: "Wuu App Layout", Description: "Application frame and layout for selected interface modules.",
		Fingerprint: "ui-shell-v1:" + version.Info().Version + ":" + version.Info().Commit,
		Entry:       "wuu-builtin:default-shell", Services: []pluginhost.ServiceDescriptor{applicationShellDescriptor()}},
		{ID: defaultSettingsModule, SubjectID: defaultSettingsModule,
			Name: "Wuu Settings", Description: "Application settings and extension management.",
			Fingerprint: "ui-settings-v1:" + version.Info().Version + ":" + version.Info().Commit,
			Entry:       "wuu-builtin:default-settings", Services: []pluginhost.ServiceDescriptor{{Name: "ui.settings", Version: "1.0.0", Methods: []pluginhost.ServiceMethodDescriptor{{Name: "describe", InputSchema: "ui.settings.describe.input.v1", OutputSchema: "ui.settings.describe.output.v1"}}}}},
		{ID: defaultNavigationModule, SubjectID: defaultNavigationModule,
			Name: "Wuu Navigation", Description: "Workspace and conversation navigation.",
			Fingerprint: "ui-navigation-v1:" + version.Info().Version + ":" + version.Info().Commit,
			Entry:       "wuu-builtin:default-navigation", Services: []pluginhost.ServiceDescriptor{{Name: "ui.navigation", Version: "1.0.0", Methods: []pluginhost.ServiceMethodDescriptor{{Name: "describe", InputSchema: "ui.navigation.describe.input.v1", OutputSchema: "ui.navigation.describe.output.v1"}}}}},
		{ID: defaultConversationModule, SubjectID: defaultConversationModule,
			Name: "Wuu Conversations", Description: "Conversation messages and activity.",
			Fingerprint: "ui-conversation-v1:" + version.Info().Version + ":" + version.Info().Commit,
			Entry:       "wuu-builtin:default-conversation", Services: []pluginhost.ServiceDescriptor{{Name: "ui.conversation", Version: "1.0.0", Methods: []pluginhost.ServiceMethodDescriptor{{Name: "describe", InputSchema: "ui.conversation.describe.input.v1", OutputSchema: "ui.conversation.describe.output.v1"}}}}},
		{ID: defaultComposerModule, SubjectID: defaultComposerModule,
			Name: "Wuu Message Composer", Description: "Message drafts, attachments, and submission controls.",
			Fingerprint: "ui-composer-v1:" + version.Info().Version + ":" + version.Info().Commit,
			Entry:       "wuu-builtin:default-composer", Services: []pluginhost.ServiceDescriptor{{Name: "ui.composer", Version: "1.0.0", Methods: []pluginhost.ServiceMethodDescriptor{{Name: "describe", InputSchema: "ui.composer.describe.input.v1", OutputSchema: "ui.composer.describe.output.v1"}}}}},
		{ID: defaultPreviewModule, SubjectID: defaultPreviewModule,
			Name: "Wuu File Preview", Description: "Workspace files, documents, and previews.",
			Fingerprint: "ui-preview-v1:" + version.Info().Version + ":" + version.Info().Commit,
			Entry:       "wuu-builtin:default-preview", Services: []pluginhost.ServiceDescriptor{{Name: "ui.preview", Version: "1.0.0", Methods: []pluginhost.ServiceMethodDescriptor{{Name: "describe", InputSchema: "ui.preview.describe.input.v1", OutputSchema: "ui.preview.describe.output.v1"}}}}}}
	for index := range modules {
		modules[index].Requirements = []pluginhost.ServiceRequirement{{Name: "ui.application", MajorVersion: 1, Required: true}}
	}
	return append(modules, RendererModule{ID: defaultApplicationModule, SubjectID: defaultApplicationModule,
		Name: "Wuu Application", Description: "Application state, actions and background work.",
		Fingerprint: "ui-application-v1:" + version.Info().Version + ":" + version.Info().Commit,
		Entry:       "wuu-builtin:default-application", Services: []pluginhost.ServiceDescriptor{{Name: "ui.application", Version: "1.0.0", Methods: []pluginhost.ServiceMethodDescriptor{{Name: "describe", InputSchema: "ui.application.describe.input.v1", OutputSchema: "ui.application.describe.output.v1"}}}}})

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
	requirements = append(append([]pluginhost.ServiceRequirement(nil), module.Requirements...), requirements...)
	if services, ok := owner.(pluginhost.ServiceClient); ok {
		requirements = append(append([]pluginhost.ServiceRequirement(nil), services.RequiredServices()...), requirements...)
	}
	// A mixed package has one graph identity even when both transports require
	// the same service. Preserve the strongest requirement; incompatible majors
	// remain distinct so ordinary negotiation reports the conflict.
	type requirementKey struct {
		name  string
		major int
	}
	indices := make(map[requirementKey]int)
	merged := make([]pluginhost.ServiceRequirement, 0, len(requirements))
	for _, requirement := range requirements {
		key := requirementKey{requirement.Name, requirement.MajorVersion}
		if index, exists := indices[key]; exists {
			merged[index].Required = merged[index].Required || requirement.Required
		} else {
			indices[key] = len(merged)
			merged = append(merged, requirement)
		}
	}
	return &rendererServiceClient{module: module, requirements: merged, owner: owner, state: pluginhost.StatePrepared}
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
	return s.ExtensionSnapshot().NativeRendererModules()
}

// NativeRendererModules resolves metadata against this snapshot's host, so
// inventory, asset loading, and retained diagnostic requests share admission.
func (snapshot ExtensionSnapshot) NativeRendererModules() []RendererModule {
	if snapshot.PluginHost == nil {
		return nil
	}
	statuses := snapshot.PluginHost.Statuses()
	var result []RendererModule
	for _, module := range nativeRendererModules() {
		for _, status := range statuses {
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
