package runtime

import (
	"context"
	"errors"

	"github.com/blueberrycongee/wuu/internal/agent"
)

// HandoffBriefForState admits one inference against the captured generation.
// A stale capture fails before planning or model calls. Keep binding alive until
// both planning and the returned fork finish, including inference cleanup.
func (s *Session) HandoffBriefForState(binding *State) (agent.HandoffBriefProvider, string, agent.CompactionNoteFork, error) {
	shadow, err := s.cloneForThreadModel(binding)
	if err != nil {
		return nil, "", nil, err
	}
	defer s.releasePluginGeneration(shadow.pluginGeneration)
	models, err := binding.Models()
	if err != nil {
		return nil, "", nil, err
	}
	runner := shadow.StreamRunner
	if runner == nil || models.Main == nil {
		return nil, "", nil, errors.New("handoff brief generation requires a model runtime")
	}
	provider, err := runner.HandoffProvider(context.Background())
	if err != nil {
		return nil, "", nil, err
	}
	if provider == nil {
		return nil, "", nil, errors.New("handoff brief generator is unavailable")
	}
	selection := models.Roles.Main
	runner.Client, runner.ProviderName = models.Main, selection.Provider
	runner.Model, runner.APIModel = selection.Model, selection.APIModel
	runner.Temperature, runner.Effort = models.Temperature, selection.LegacyEffort
	runner.ProviderOptions = selection.ProviderOptions
	// Publication mutates the root toolkit. The fork must capture definitions
	// from the private clone and the same retained host.
	if shadow.Toolkit != nil {
		runner.Tools = newPluginAwareToolExecutor(shadow.Toolkit, shadow.PluginHost, shadow.HookDispatcher, "", "", shadow.RootDir)
	}
	return provider, selection.Model, runner.CompactionNoteFork(), nil
}
