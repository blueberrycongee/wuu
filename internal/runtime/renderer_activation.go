package runtime

import (
	"errors"

	"github.com/blueberrycongee/wuu/internal/config"
	pluginpkg "github.com/blueberrycongee/wuu/internal/plugin"
	"github.com/blueberrycongee/wuu/internal/pluginhost"
)

// ReportRendererActivation updates transport readiness on the exact committed
// graph. A source fingerprint alone cannot identify a graph rebuilt with the
// same code and different service bindings.
func (s *Session) ReportRendererActivation(graph, subject, fingerprint, renderer string, report pluginhost.RendererActivationReport) error {
	if s == nil {
		return errors.New("runtime is not initialized")
	}
	s.pluginGenerationMu.Lock()
	defer s.pluginGenerationMu.Unlock()
	generation := s.pluginGeneration
	if generation == nil || generation.id != graph {
		generation = nil
		if report.Phase == "cleanup" {
			for retired := range s.retiredPluginGenerations {
				if retired.id == graph {
					generation = retired
					break
				}
			}
		}
	}
	if generation == nil {
		return errors.New("renderer graph is no longer available")
	}
	generation.revokedMu.Lock()
	host := generation.host
	generation.revokedMu.Unlock()
	if host == nil || host.ServiceRegistry() == nil {
		return errors.New("renderer graph is no longer available")
	}
	modules := nativeRendererModules()
	for _, item := range generation.active {
		if item.Desktop != nil {
			modules = append(modules, rendererModule(item))
		}
	}
	for _, module := range modules {
		if module.SubjectID == subject && module.Fingerprint == fingerprint {
			return host.ServiceRegistry().ReportRenderer(module.ID, fingerprint, renderer, report)
		}
	}
	return errors.New("renderer source does not belong to this graph")
}

func (s *Session) ForgetRenderer(renderer string) {
	if s == nil {
		return
	}
	s.pluginGenerationMu.Lock()
	defer s.pluginGenerationMu.Unlock()
	forget := func(g *PluginGeneration) {
		if g == nil {
			return
		}
		g.revokedMu.Lock()
		host := g.host
		g.revokedMu.Unlock()
		if host != nil && host.ServiceRegistry() != nil {
			host.ServiceRegistry().ForgetRenderer(renderer)
		}
	}
	forget(s.pluginGeneration)
	for generation := range s.retiredPluginGenerations {
		forget(generation)
	}
}

// ResolveExtensionPlan includes application-provided native factories when
// projecting the configured graph for management and diagnostics.
func (s *Session) ResolveExtensionPlan(cfg config.Config, discovered []pluginpkg.Plugin) (pluginpkg.ActivationPlan, error) {
	return ResolvePluginActivationPlan(cfg, discovered, s.nativeProviders...)
}
