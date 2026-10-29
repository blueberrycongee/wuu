package runtime

import "github.com/blueberrycongee/wuu/internal/config"

// PrepareAdvancedThreadDerivation resolves one thread's own pinned selection
// against the captured graph. It keeps that graph alive while construction can
// call providers, without holding the publication lock across those calls.
func (s *Session) PrepareAdvancedThreadDerivation(basis AdvancedRuntimeBasis, cfg config.Config, selection ThreadModelSelection) (ThreadModelDerivation, error) {
	if s == nil || basis.owner != s {
		return ThreadModelDerivation{}, ErrStaleAdvancedRuntime
	}
	s.pluginGenerationMu.Lock()
	if basis.propagationVersion != s.advancedPropagationVersion.Load() || basis.generation != s.pluginGeneration {
		s.pluginGenerationMu.Unlock()
		return ThreadModelDerivation{}, ErrStaleAdvancedRuntime
	}
	generation := s.pluginGeneration
	generation.retain()
	shadow := &Session{ProviderName: basis.ProviderName, Model: basis.Model, PluginHost: s.PluginHost, credentials: s.credentials, chatGPTManager: s.chatGPTManager}
	s.pluginGenerationMu.Unlock()
	defer s.releasePluginGeneration(generation)
	derivation, err := shadow.DeriveThreadModel(cfg, selection)
	if err != nil {
		return ThreadModelDerivation{}, err
	}
	if s.advancedPropagationVersion.Load() != basis.propagationVersion {
		return ThreadModelDerivation{}, ErrStaleAdvancedRuntime
	}
	return derivation, nil
}

// AdvancedRuntimeBasisCurrent checks the exact publication without invoking
// providers or acquiring the publication lock.
func (s *Session) AdvancedRuntimeBasisCurrent(basis AdvancedRuntimeBasis) bool {
	return s != nil && basis.owner == s && basis.propagationVersion == s.advancedPropagationVersion.Load()
}

// AdvancedRuntimeCurrent checks the exact publication and thread generation.
// Callers also hold the owning thread's mutex and ensure that later updates to
// that thread acquire it before applying their own publication.
func (s *Session) AdvancedRuntimeCurrent(basis AdvancedRuntimeBasis, thread *ThreadRuntime) bool {
	return thread != nil && s.AdvancedRuntimeBasisCurrent(basis) && thread.PluginGeneration == basis.generation
}
