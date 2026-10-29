package runtime

import (
	"errors"
	"strings"

	"github.com/blueberrycongee/wuu/internal/config"
	"github.com/blueberrycongee/wuu/internal/modelbudget"
	"github.com/blueberrycongee/wuu/internal/modelroles"
)

// ErrStaleAdvancedRuntime means that the state used to resolve an advanced
// settings update was superseded before publication. The saved configuration
// may already have changed; callers must re-read it before retrying.
var ErrStaleAdvancedRuntime = errors.New("advanced runtime selection changed before publication")

// AdvancedRuntimeBasis identifies the exact workspace model and publication
// against which advanced settings may be resolved.
type AdvancedRuntimeBasis struct {
	ProviderName string
	Model        string
	Effort       string
	Variant      string

	owner              *Session
	generation         *PluginGeneration
	version            uint64
	propagationVersion uint64
}

// CaptureAdvancedRuntime captures the state needed to resolve advanced
// settings without holding the publication lock across provider calls.
func (s *Session) CaptureAdvancedRuntime() (AdvancedRuntimeBasis, error) {
	if s == nil {
		return AdvancedRuntimeBasis{}, errors.New("runtime is not initialized")
	}
	s.pluginGenerationMu.Lock()
	defer s.pluginGenerationMu.Unlock()
	basis := AdvancedRuntimeBasis{
		ProviderName: strings.TrimSpace(s.ProviderName), Model: strings.TrimSpace(s.Model),
		owner: s, generation: s.pluginGeneration, version: s.runtimePublicationVersion.Load(), propagationVersion: s.advancedPropagationVersion.Load(),
	}
	if s.StreamRunner != nil {
		basis.Effort = strings.TrimSpace(s.StreamRunner.Effort)
		basis.Variant = strings.TrimSpace(s.StreamRunner.Variant)
	}
	return basis, nil
}

// PublishAdvancedRuntime atomically replaces all advanced values used by state
// captures and thread construction. Preparation and provider calls occur before
// this method. A stale basis is rejected without mutating live state.
func (s *Session) PublishAdvancedRuntime(basis AdvancedRuntimeBasis, cfg config.AgentConfig, roles modelroles.Set, modelBudget, workerBudget modelbudget.Budget) (AdvancedRuntimeBasis, error) {
	if s == nil {
		return AdvancedRuntimeBasis{}, errors.New("runtime is not initialized")
	}
	// Provider options are mutable maps; detach them before publishing.
	roles = cloneStateModels(StateModels{Roles: roles}).Roles
	// Activation can synchronously reenter a settings provider. Never wait for
	// that transition here; the caller can re-read and retry after it settles.
	if !s.pluginTransitionMu.TryLock() {
		return AdvancedRuntimeBasis{}, ErrStaleAdvancedRuntime
	}
	defer s.pluginTransitionMu.Unlock()
	s.pluginGenerationMu.Lock()
	defer s.pluginGenerationMu.Unlock()
	if basis.owner != s || basis.generation != s.pluginGeneration || basis.version != s.runtimePublicationVersion.Load() || basis.ProviderName != strings.TrimSpace(s.ProviderName) || basis.Model != strings.TrimSpace(s.Model) {
		return AdvancedRuntimeBasis{}, ErrStaleAdvancedRuntime
	}
	s.ModelRoles = roles
	s.ProjectModels = cfg.ProjectModels
	s.ModelBudget = modelBudget
	s.WorkerModelBudget = workerBudget
	if runner := s.StreamRunner; runner != nil {
		runner.MaxSteps = cfg.MaxSteps
		runner.Temperature = cfg.Temperature
		runner.CompactThresholdPct = cfg.CompactThresholdPct
		runner.CompactKeepRecentTokens = cfg.CompactKeepRecentTokens
		runner.DisableAutoCompact = cfg.DisableAutoCompact
		runner.ContextWindowOverride = modelBudget.ContextWindowTokens
		runner.MaxInputTokens = modelBudget.InputLimitTokens
		runner.OutputReserveTokens = modelBudget.OutputReserveTokens
		runner.CompactThresholdTokens = modelBudget.CompactThresholdTokens
	}
	basis.version = s.runtimePublicationVersion.Add(1)
	basis.propagationVersion = s.advancedPropagationVersion.Add(1)
	return basis, nil
}
