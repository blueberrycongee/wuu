package runtime

import (
	"sync"

	"github.com/blueberrycongee/wuu/internal/config"
	"github.com/blueberrycongee/wuu/internal/hooks"
	"github.com/blueberrycongee/wuu/internal/modelbudget"
	"github.com/blueberrycongee/wuu/internal/modelroles"
	"github.com/blueberrycongee/wuu/internal/tools"
)

// RuntimePresentation is a consistent copy of the live model and settings.
// Toolkit belongs to the retained graph and remains usable until release.
type RuntimePresentation struct {
	ProviderName, Model, Effort, Variant string
	Roles                                modelroles.Set
	Permissions                          config.ResolvedPermissions
	ProjectModels                        config.ProjectModelsConfig
	ModelBudget                          modelbudget.Budget
	MaxSteps                             int
	Temperature, CompactThresholdPct     float64
	CompactKeepRecentTokens              int
	DisableAutoCompact                   bool
	Toolkit                              *tools.Toolkit
	HookDispatcher                       *hooks.Dispatcher
	ReadinessIssues                      []ReadinessIssue
}

// CaptureRuntimePresentation does not invoke providers under the publication
// lock. The release closure is idempotent and must be called after using Toolkit.
func (s *Session) CaptureRuntimePresentation() (RuntimePresentation, func()) {
	if s == nil {
		return RuntimePresentation{}, func() {}
	}
	s.pluginGenerationMu.Lock()
	generation := s.pluginGeneration
	generation.retain()
	snapshot := RuntimePresentation{
		ProviderName: s.ProviderName, Model: s.Model,
		Roles:       cloneStateModels(StateModels{Roles: s.ModelRoles}).Roles,
		Permissions: s.Permissions, ProjectModels: s.ProjectModels,
		ModelBudget: s.ModelBudget, Toolkit: s.Toolkit, HookDispatcher: s.HookDispatcher,
		ReadinessIssues: append([]ReadinessIssue(nil), s.ReadinessIssues...),
	}
	if runner := s.StreamRunner; runner != nil {
		snapshot.Effort, snapshot.Variant = runner.Effort, runner.Variant
		snapshot.MaxSteps, snapshot.Temperature = runner.MaxSteps, runner.Temperature
		snapshot.CompactThresholdPct, snapshot.CompactKeepRecentTokens = runner.CompactThresholdPct, runner.CompactKeepRecentTokens
		snapshot.DisableAutoCompact = runner.DisableAutoCompact
	}
	s.pluginGenerationMu.Unlock()
	var once sync.Once
	return snapshot, func() { once.Do(func() { _ = s.releasePluginGeneration(generation) }) }
}
