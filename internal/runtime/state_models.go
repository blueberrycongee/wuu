package runtime

import (
	"errors"
	"strings"

	"github.com/blueberrycongee/wuu/internal/config"
	"github.com/blueberrycongee/wuu/internal/modelroles"
	"github.com/blueberrycongee/wuu/internal/modelvariant"
	"github.com/blueberrycongee/wuu/internal/providers"
)

// StateModels keeps inference clients, effective selections and execution
// defaults in the authority retained by State. Provider configuration and
// capability records are immutable model metadata; request options are copied.
type StateModels struct {
	Main                    providers.StreamClient
	Title                   providers.Client
	Roles                   modelroles.Set
	Temperature             float64
	CompactThresholdPct     float64
	CompactKeepRecentTokens int
	DisableAutoCompact      bool
	err                     error
}

// Models returns this capture's inference bindings. Keep State alive until the
// inference and its journal cleanup finish; a closed capture cannot admit work.
func (s *State) Models() (StateModels, error) {
	if s == nil {
		return StateModels{}, errors.New("model state is unavailable")
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closed {
		return StateModels{}, errors.New("model state is closed")
	}
	if s.models == nil {
		return StateModels{}, errors.New("captured state has no model runtime")
	}
	if s.models.err != nil {
		return StateModels{}, s.models.err
	}
	return cloneStateModels(*s.models), nil
}

func cloneStateModels(models StateModels) StateModels {
	for _, role := range []*modelroles.Selection{&models.Roles.Main, &models.Roles.Title, &models.Roles.Worker, &models.Roles.Verification, &models.Roles.Review, &models.Roles.Compact, &models.Roles.Fallback} {
		role.ProviderOptions = modelvariant.CloneOptions(role.ProviderOptions)
	}
	return models
}

// The caller holds Session's publication lock, including for embedded runtimes
// whose clients and runner options are explicitly injected by their owner.
func captureSessionStateModels(s *Session) *StateModels {
	if s == nil || (s.StreamRunner == nil && s.TitleClient == nil) {
		return nil
	}
	models := StateModels{Title: s.TitleClient, Roles: s.ModelRoles}
	main := &models.Roles.Main
	main.Provider, main.Model = s.ProviderName, s.Model
	if runner := s.StreamRunner; runner != nil {
		models.Main = runner.Client
		if strings.TrimSpace(runner.ProviderName) != "" {
			main.Provider = runner.ProviderName
		}
		if strings.TrimSpace(runner.Model) != "" {
			main.Model = runner.Model
		}
		main.APIModel, main.LegacyEffort, main.Variant = runner.APIModel, runner.Effort, runner.Variant
		main.ProviderOptions = runner.ProviderOptions
		models.Temperature, models.CompactThresholdPct = runner.Temperature, runner.CompactThresholdPct
		models.CompactKeepRecentTokens, models.DisableAutoCompact = runner.CompactKeepRecentTokens, runner.DisableAutoCompact
	}
	if strings.TrimSpace(main.APIModel) == "" {
		main.APIModel = main.Model
	}
	copy := cloneStateModels(models)
	return &copy
}

func captureGenerationStateModels(generation *PluginGeneration) *StateModels {
	if generation == nil {
		return nil
	}
	generation.revokedMu.Lock()
	defer generation.revokedMu.Unlock()
	if generation.models == nil {
		return nil
	}
	binding := generation.models
	defaults := binding.config.Agent
	models := cloneStateModels(StateModels{
		Main: binding.main, Title: binding.title, Roles: binding.roles,
		Temperature: defaults.Temperature, CompactThresholdPct: defaults.CompactThresholdPct,
		CompactKeepRecentTokens: defaults.CompactKeepRecentTokens, DisableAutoCompact: defaults.DisableAutoCompact,
		err: binding.err,
	})
	return &models
}

// Initial graph construction already created selected clients. Preserve those
// instances without reopening credentials or running provider construction.
func captureGenerationModelBindings(s *Session) *generationModelBindings {
	models := captureSessionStateModels(s)
	if models == nil {
		return nil
	}
	return &generationModelBindings{
		main: models.Main, title: models.Title, worker: s.WorkerClient, roles: models.Roles, err: models.err,
		config: config.Config{Agent: config.AgentConfig{
			Temperature: models.Temperature, CompactThresholdPct: models.CompactThresholdPct,
			CompactKeepRecentTokens: models.CompactKeepRecentTokens, DisableAutoCompact: models.DisableAutoCompact,
		}},
	}
}
