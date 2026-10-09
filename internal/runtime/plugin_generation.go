package runtime

import (
	"context"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/blueberrycongee/wuu/internal/agent"
	"github.com/blueberrycongee/wuu/internal/config"
	"github.com/blueberrycongee/wuu/internal/hooks"
	"github.com/blueberrycongee/wuu/internal/mcp"
	pluginpkg "github.com/blueberrycongee/wuu/internal/plugin"
	"github.com/blueberrycongee/wuu/internal/pluginhost"
	"github.com/blueberrycongee/wuu/internal/providers"
	"github.com/blueberrycongee/wuu/internal/session"
	"github.com/blueberrycongee/wuu/internal/skills"
	"github.com/blueberrycongee/wuu/internal/tools"
)

// PluginGeneration is a complete replacement for the plugin-owned surfaces of a
// Session. It owns every process and private package snapshot. Conversations
// adopt published updates at their next idle turn boundary; active turns and
// background work retain their generation until they settle. Committed removal
// or disable revokes that plugin across all retained generations immediately.
// Close retires the remaining resources in reverse ownership order and records
// a structured revocation report.
type PluginGeneration struct {
	id                string
	settings          config.Config
	plugins           []pluginpkg.Plugin
	active            []pluginpkg.Plugin
	host              *pluginhost.Host
	hooks             *hooks.Dispatcher
	skills            []skills.Skill
	pluginSkills      pluginSkillSnapshot
	mcp               *mcp.Manager
	mcpBinding        map[string]tools.MCPActivityBinding
	requestTransforms *agent.RequestTransformChain
	systemPrompts     *agent.SystemPromptAssembler
	compactions       *agent.CompactionRegistry
	ownedRoots        []string
	revocation        *GenerationRevocationReport
	// driverGateways is this generation's remote-driver gateway routing
	// table; executions registered here route only to this generation's
	// kernel services.
	driverGateways *driverGatewayTable
	// refs counts the live Session plus any ThreadRuntime still bound to this
	// generation. A retired generation stays open until the last conversation
	// that started against it is released.
	refs           atomic.Int32
	revocationMu   sync.Mutex
	revokedMu      sync.Mutex
	revokedPlugins map[string]bool
}

// PreflightExtensions discovers and builds a replacement without changing the
// live Session.
func (s *Session) PreflightExtensions(cfg config.Config) (*PluginGeneration, error) {
	if s == nil {
		return nil, errors.New("runtime is not initialized")
	}
	return s.buildPluginGeneration(cfg, discoverPlugins(s.RootDir, s.WuuHome), nil, nil, startPluginClient)
}

// PluginGenerationNeedsRecovery reports a runtime process that reached active
// state and subsequently failed. Startup failures are excluded so one broken
// optional plugin does not force a rebuild before every turn.
func (s *Session) PluginGenerationNeedsRecovery() bool {
	if s == nil {
		return false
	}
	s.pluginGenerationMu.Lock()
	host := s.PluginHost
	s.pluginGenerationMu.Unlock()
	if host == nil {
		return false
	}
	for _, status := range host.Statuses() {
		if status.State == pluginhost.StateFailed && !status.StartedAt.IsZero() {
			return true
		}
	}
	return false
}

// RevokeFailedPluginDependencies stops required consumers of failed runtimes
// in the current and retained generations. Each generation keeps its immutable
// snapshots and its own admission mask; a crash in an older implementation does
// not revoke the same package ID in a healthy replacement. Call outside thread
// admission locks because shutdown may synchronously call host services.
func (s *Session) RevokeFailedPluginDependencies() bool {
	if s == nil {
		return false
	}
	s.pluginTransitionMu.Lock()
	defer s.pluginTransitionMu.Unlock()
	s.pluginGenerationMu.Lock()
	captured := false
	if s.pluginGeneration == nil && s.PluginHost != nil {
		s.pluginGeneration = s.capturePluginGeneration()
		captured = true
	}
	current := s.pluginGeneration
	var generations []*PluginGeneration
	if current != nil {
		current.retain()
		generations = append(generations, current)
	}
	for generation := range s.retiredPluginGenerations {
		if generation == current {
			continue
		}
		for refs := generation.refs.Load(); refs > 0; refs = generation.refs.Load() {
			if generation.refs.CompareAndSwap(refs, refs+1) {
				generations = append(generations, generation)
				break
			}
		}
	}
	s.pluginGenerationMu.Unlock()
	// Embedded sessions may acquire their first generation here. Production
	// toolkits are already bound at generation construction/publication.
	if captured && s.Toolkit != nil {
		s.Toolkit.SetSkillsWithAvailability(current.skills, current.skillAvailable)
	}
	changed := false
	for _, generation := range generations {
		failures := generation.host.PackageDependencyFailures()
		activeIDs := make(map[string]bool, len(generation.active))
		invalidated := false
		generation.revokedMu.Lock()
		for _, item := range generation.active {
			activeIDs[item.ID] = failures[item.ID] == nil
			if failures[item.ID] != nil && !generation.revokedPlugins[item.ID] {
				invalidated = true
			}
		}
		generation.revokedMu.Unlock()
		if invalidated {
			generation.revokeMissingPlugins(activeIDs, s.TitleClient, s.Model)
			if generation == current && s.HookDispatcher != nil && s.HookDispatcher != generation.hooks {
				s.HookDispatcher.Replace(generation.hooks)
			}
			changed = true
		}
		s.releasePluginGeneration(generation)
	}
	return changed
}

// PreflightExtensionPolicy builds a replacement from the current package set
// without persisting the proposed grant/enable decisions.
func (s *Session) PreflightExtensionPolicy(cfg config.Config) (*PluginGeneration, error) {
	if s == nil {
		return nil, errors.New("runtime is not initialized")
	}
	return s.buildPluginGeneration(cfg, s.Plugins, nil, nil, startPluginClient)
}

// PreflightPluginRemoval builds the generation that will remain after one
// installed user package is removed, while the current package is still
// available for rollback.
func (s *Session) PreflightPluginRemoval(cfg config.Config, id string) (*PluginGeneration, error) {
	if s == nil {
		return nil, errors.New("runtime is not initialized")
	}
	id = strings.TrimSpace(id)
	if id == "" {
		return nil, errors.New("plugin id is required")
	}
	discovered := make([]pluginpkg.Plugin, 0, len(s.Plugins))
	found := false
	for _, item := range s.Plugins {
		if item.Source == "user" && item.ID == id {
			found = true
			continue
		}
		discovered = append(discovered, item)
	}
	if !found {
		return nil, fmt.Errorf("installed user plugin %q was not found", id)
	}
	return s.buildPluginGeneration(cfg, discovered, nil, nil, startPluginClient)
}

// PreflightPluginUpdate builds an exact approved pending package as part of a
// complete replacement generation. The private snapshot keeps registrations
// valid after the pending directory is published and removed.
func (s *Session) PreflightPluginUpdate(cfg config.Config, id, fingerprint, packageRoot, manifestPath string) (*PluginGeneration, error) {
	if s == nil {
		return nil, errors.New("runtime is not initialized")
	}
	id = strings.TrimSpace(id)
	fingerprint = strings.TrimSpace(fingerprint)
	if id == "" || fingerprint == "" {
		return nil, errors.New("plugin id and fingerprint are required")
	}
	snapshot, err := snapshotPluginPackage(packageRoot)
	if err != nil {
		return nil, fmt.Errorf("snapshot plugin %q candidate: %w", id, err)
	}
	cleanupSnapshot := true
	defer func() {
		if cleanupSnapshot {
			_ = os.RemoveAll(snapshot)
		}
	}()

	manifest, err := packageManifestPath(snapshot, manifestPath)
	if err != nil {
		return nil, fmt.Errorf("resolve plugin %q candidate manifest: %w", id, err)
	}
	candidate, err := pluginpkg.LoadManifestWithOptions(manifest, pluginpkg.LoadOptions{Source: "user"})
	if err != nil {
		return nil, fmt.Errorf("load plugin %q candidate: %w", id, err)
	}
	if candidate.ID != id || candidate.Fingerprint != fingerprint {
		return nil, fmt.Errorf("plugin %q candidate changed during activation", id)
	}

	discovered := append([]pluginpkg.Plugin(nil), s.Plugins...)
	found := false
	for index := range discovered {
		if discovered[index].ID != id {
			continue
		}
		if discovered[index].Source != "user" || discovered[index].SubjectID != candidate.SubjectID {
			return nil, fmt.Errorf("plugin %q candidate does not replace the installed user package", id)
		}
		discovered[index] = candidate
		found = true
		break
	}
	if !found {
		return nil, fmt.Errorf("installed user plugin %q was not found", id)
	}
	sort.Slice(discovered, func(i, j int) bool { return discovered[i].ID < discovered[j].ID })

	required := map[string]bool{id: true}
	if s.PluginHost != nil {
		for _, status := range s.PluginHost.Statuses() {
			if status.State == pluginhost.StateActive {
				required[status.ID] = true
			}
		}
	}
	generation, err := s.buildPluginGeneration(cfg, discovered, required, []string{snapshot}, startPluginClient)
	if err != nil {
		return nil, err
	}
	if !containsPluginGeneration(generation.active, id, fingerprint) {
		_ = generation.close()
		s.persistRevocationReport(generation)
		return nil, fmt.Errorf("plugin %q candidate is not approved for its exact fingerprint", id)
	}
	cleanupSnapshot = false
	return generation, nil
}

func (s *Session) buildPluginGeneration(cfg config.Config, discovered []pluginpkg.Plugin, required map[string]bool, ownedRoots []string, start pluginClientStarter) (*PluginGeneration, error) {
	var active []pluginpkg.Plugin
	if !s.SafeMode {
		activationPlan, err := ResolvePluginActivationPlan(cfg, discovered)
		if err != nil {
			return nil, err
		}
		active = activationPlan.Plugins
	}
	var snapshotRoots []string
	var snapshotErr error
	active, snapshotRoots, snapshotErr = snapshotMutableExecutionPackages(active)
	if snapshotErr != nil {
		return nil, snapshotErr
	}
	ownedRoots = append(ownedRoots, snapshotRoots...)
	// A refresh may isolate a newly broken optional runtime, but must not
	// replace an already working runtime with a failed candidate. Requirements
	// only apply to runtimes in the candidate, so intentional removals and
	// disables remain possible.
	requiredRuntimes := make(map[string]bool, len(required))
	for id, value := range required {
		requiredRuntimes[id] = value
	}
	if s.PluginHost != nil {
		for _, status := range s.PluginHost.Statuses() {
			if status.State == pluginhost.StateActive {
				requiredRuntimes[status.ID] = true
			}
		}
	}
	host, kernel, err := buildPluginHost(active, s.RootDir, s.WorkspaceID, s.WuuHome, s.StateDir, requiredRuntimes, start, s.PluginSessionRouter, s.UserQuestions)
	if err != nil {
		for _, root := range ownedRoots {
			_ = os.RemoveAll(root)
		}
		return nil, err
	}
	// Service negotiation can reject a prepared runtime after process startup.
	// Treat that exactly like startup failure for required replacements.
	for _, status := range host.Statuses() {
		if requiredRuntimes[status.ID] && status.State == pluginhost.StateFailed {
			ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
			closeErr := host.Close(ctx)
			cancel()
			for _, root := range ownedRoots {
				_ = os.RemoveAll(root)
			}
			return nil, pluginActivationError(status.ID, errors.New(status.Error), closeErr)
		}
	}
	active = availablePluginPackages(active, host)
	systemPrompts, compactions, err := buildPluginAgentCapabilities(context.Background(), host, s.ProviderName, s.Model, s.RootDir)
	if err != nil {
		ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
		closeErr := host.Close(ctx)
		cancel()
		for _, root := range ownedRoots {
			_ = os.RemoveAll(root)
		}
		return nil, errors.Join(err, closeErr)
	}
	pluginSkills := discoverPluginSkills(active)
	generation := &PluginGeneration{
		id:                newPluginGenerationID(s.WuuHome),
		settings:          cfg,
		plugins:           append([]pluginpkg.Plugin(nil), discovered...),
		active:            append([]pluginpkg.Plugin(nil), active...),
		host:              host,
		hooks:             buildHookDispatcher(cfg, active, s.TitleClient, s.Model, nil),
		skills:            discoverSkillsWithPlugins(s.RootDir, s.HomeDir, s.WuuHome, pluginSkills, cfg.Skills),
		pluginSkills:      pluginSkills,
		mcpBinding:        mcpActivityBindingsFromPlugins(active),
		requestTransforms: buildPluginRequestTransforms(host, s.ProviderName, "", s.RootDir),
		systemPrompts:     systemPrompts,
		compactions:       compactions,
		ownedRoots:        append([]string(nil), ownedRoots...),
		driverGateways:    kernel.driverGateways,
	}
	generation.mcp, err = startMCPManager(cfg, active, required)
	if err != nil {
		_ = generation.close()
		s.persistRevocationReport(generation)
		return nil, err
	}
	// Activation acquires the Session reference. A prepared candidate has no
	// live owners and must not keep an extra reference after publication.
	return generation, nil
}

// PluginGenerationWasCommitted distinguishes post-commit activation failure
// from preparation or persistence failure. Callers must finish durable cleanup
// and report the new generation's diagnostics instead of rolling policy back.
func PluginGenerationWasCommitted(err error) bool {
	var committed *pluginGenerationCommittedError
	return errors.As(err, &committed)
}

type pluginGenerationCommittedError struct{ cause error }

func (e *pluginGenerationCommittedError) Error() string {
	return fmt.Sprintf("plugin change was committed, but runtime activation failed: %v", e.cause)
}
func (e *pluginGenerationCommittedError) Unwrap() error { return e.cause }

// ActivatePluginGeneration persists a prepared candidate before opening its
// effectful lifecycle. Persistence failure preserves the current generation
// without invoking candidate activation. After commit, activation failure is a
// degraded published generation, exposed through status inventory and a
// PluginGenerationWasCommitted error; external effects cannot be rolled back.
// Active work retains same-ID implementations until its idle boundary, while
// removal or disable revokes that plugin across all retained generations.
func (s *Session) ActivatePluginGeneration(candidate *PluginGeneration, commit func() error) error {
	if s == nil {
		return errors.New("runtime is not initialized")
	}
	if candidate == nil || candidate.host == nil || candidate.hooks == nil {
		return errors.New("plugin candidate generation is not initialized")
	}
	s.pluginTransitionMu.Lock()
	defer s.pluginTransitionMu.Unlock()
	s.pluginGenerationMu.Lock()
	old := s.pluginGeneration
	if old == nil {
		old = s.capturePluginGeneration()
	}
	if commit != nil {
		if err := commit(); err != nil {
			s.pluginGenerationMu.Unlock()
			commitErr := errors.Join(err, candidate.close())
			s.persistRevocationReport(candidate)
			return commitErr
		}
	}
	s.pluginGenerationMu.Unlock()
	// Activation may synchronously call host services. Keep old bindings
	// available until activation settles, without holding their lookup mutex.
	activationErr := activatePluginHost(context.Background(), candidate.host)
	available := availablePluginPackages(candidate.active, candidate.host)
	if len(available) != len(candidate.active) {
		activeIDs := make(map[string]bool, len(available))
		for _, item := range available {
			activeIDs[item.ID] = true
		}
		candidate.revokeMissingPlugins(activeIDs, s.TitleClient, s.Model)
		candidate.active = available
		candidate.pluginSkills = discoverPluginSkills(available)
		candidate.skills = discoverSkillsWithPlugins(s.RootDir, s.HomeDir, s.WuuHome, candidate.pluginSkills, candidate.settings.Skills)
		candidate.mcpBinding = mcpActivityBindingsFromPlugins(available)
	}
	// Preparation materializes native contributions before the effectful
	// lifecycle opens. Failed runtimes must not leave cached prompts or win
	// decision/transform dispatch in the degraded generation. Keep their status
	// diagnostics and independent declarative package features intact.
	for _, status := range candidate.host.Statuses() {
		if status.State != pluginhost.StateFailed {
			continue
		}
		if candidate.systemPrompts != nil {
			candidate.systemPrompts.RemoveByPlugin(status.ID)
		}
		if candidate.compactions != nil {
			candidate.compactions.RemoveByPlugin(status.ID)
		}
		if candidate.requestTransforms != nil {
			candidate.requestTransforms.RemoveByPlugin(status.ID)
		}
	}
	s.pluginGenerationMu.Lock()
	s.applyPluginGeneration(candidate)
	s.pluginGeneration = candidate
	candidate.retain()
	// Conversations that already pinned the previous generation keep using it
	// until they rebuild. The Session reference is released here; remaining
	// thread runtimes keep the processes and tools alive.
	if s.retiredPluginGenerations == nil {
		s.retiredPluginGenerations = make(map[*PluginGeneration]struct{})
	}
	s.retiredPluginGenerations[old] = struct{}{}
	activeIDs := make(map[string]bool, len(candidate.active))
	for _, item := range candidate.active {
		activeIDs[item.ID] = true
	}
	var retired []*PluginGeneration
	for generation := range s.retiredPluginGenerations {
		// A thread-model shadow can release its final reference independently
		// of this Session. Never resurrect that generation while it closes.
		for refs := generation.refs.Load(); ; refs = generation.refs.Load() {
			if refs <= 0 {
				delete(s.retiredPluginGenerations, generation)
				break
			}
			if generation.refs.CompareAndSwap(refs, refs+1) {
				retired = append(retired, generation)
				break
			}
		}
	}
	s.pluginGenerationMu.Unlock()
	// Plugin shutdown can call host services. Never hold the Session mutex
	// across those callbacks; temporary references keep the old hosts alive.
	s.releasePluginGeneration(old)
	for _, generation := range retired {
		generation.revokeMissingPlugins(activeIDs, s.TitleClient, s.Model)
		s.releasePluginGeneration(generation)
	}
	if activationErr != nil {
		return &pluginGenerationCommittedError{cause: activationErr}
	}
	return nil
}

// revokeMissingPlugins stops removed or disabled implementations even when a
// conversation still pins this generation. Updates with the same ID retain
// their old implementation until outstanding work releases it.
func (g *PluginGeneration) revokeMissingPlugins(activeIDs map[string]bool, client providers.Client, model string) {
	g.revocationMu.Lock()
	defer g.revocationMu.Unlock()
	g.revokedMu.Lock()
	// Same-ID replacements can change their requirements. A pinned generation
	// still needs its own providers; never apply the replacement's graph to it.
	retainedIDs := make(map[string]bool, len(g.active))
	for _, item := range g.active {
		retainedIDs[item.ID] = activeIDs[item.ID] && !g.revokedPlugins[item.ID]
	}
	for changed := true; changed; {
		changed = false
		for _, item := range g.active {
			if !retainedIDs[item.ID] {
				continue
			}
			for _, required := range item.RequiredPluginIDs() {
				if !retainedIDs[required] {
					retainedIDs[item.ID] = false
					changed = true
					break
				}
			}
		}
	}
	activeIDs = retainedIDs
	var remaining []pluginpkg.Plugin
	for _, item := range g.active {
		if activeIDs[item.ID] && !g.revokedPlugins[item.ID] {
			remaining = append(remaining, item)
		}
	}
	var retiring []pluginpkg.Plugin
	// g.active is provider-first. Consumers must release their resources while
	// provider services are still available, including in pinned generations.
	for index := len(g.active) - 1; index >= 0; index-- {
		item := g.active[index]
		if activeIDs[item.ID] && !g.revokedPlugins[item.ID] {
			continue
		}
		if g.revokedPlugins[item.ID] {
			continue
		}
		if g.revokedPlugins == nil {
			g.revokedPlugins = make(map[string]bool)
		}
		g.revokedPlugins[item.ID] = true
		retiring = append(retiring, item)
	}
	// Publish the entire mask before any shutdown callback. Such callbacks may
	// read skills or inventory, so no mask lock may span an external effect.
	g.revokedMu.Unlock()
	if len(retiring) == 0 {
		return
	}
	dependencyFailures := make(map[string]error)
	if g.host != nil {
		dependencyFailures = g.host.PackageDependencyFailures()
	}
	if g.hooks != nil {
		g.hooks.Replace(buildHookDispatcher(g.settings, remaining, client, model, nil))
	}
	for _, item := range retiring {
		if g.host != nil {
			ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
			outcome, _ := g.host.RetirePlugin(ctx, item.ID, &pluginhost.UserQuestionError{Code: "plugin_disabled", Message: "plugin was removed or disabled"})
			cancel()
			if failure := dependencyFailures[item.ID]; failure != nil {
				g.host.Add(pluginhost.Failed(item.ID, failure))
			}
			if outcome.Err != nil {
				providers.DebugLogf("retire disabled plugin %q: %v", item.ID, outcome.Err)
			}
		}
		if g.mcp != nil {
			for localName := range item.MCPServers {
				name := PluginMCPServerName(item.ID, localName)
				if err := g.mcp.Revoke(name); err != nil {
					providers.DebugLogf("revoke disabled plugin MCP server %q: %v", name, err)
				}
			}
		}
		if g.systemPrompts != nil {
			g.systemPrompts.RemoveByPlugin(item.ID)
		}
		if g.compactions != nil {
			g.compactions.RemoveByPlugin(item.ID)
		}
		if g.requestTransforms != nil {
			g.requestTransforms.RemoveByPlugin(item.ID)
		}
	}

}

func (g *PluginGeneration) retain() {
	if g == nil {
		return
	}
	g.refs.Add(1)
}

// Release drops one conversation or Session reference. It returns true when
// this call retired the generation. Revocation persistence belongs to the
// Session helper when a live Session is available.
func (g *PluginGeneration) Release() bool {
	if g == nil {
		return false
	}
	remaining := g.refs.Add(-1)
	if remaining > 0 {
		return false
	}
	if remaining < 0 {
		g.refs.Store(0)
	}
	if err := g.close(); err != nil {
		providers.DebugLogf("plugin generation cleanup: %v", err)
	}
	return true
}

// IsCurrentPluginGeneration reports whether a conversation is bound to the
// currently published generation. Catalog changes and rejected candidates do
// not change this identity, unlike the durable mutation epoch.
func (s *Session) IsCurrentPluginGeneration(generation *PluginGeneration) bool {
	if s == nil {
		return false
	}
	s.pluginGenerationMu.Lock()
	defer s.pluginGenerationMu.Unlock()
	return s.pluginGeneration == generation
}

func (s *Session) RetainPluginGeneration() *PluginGeneration {
	return s.retainPluginGeneration()
}

func (s *Session) retainPluginGeneration() *PluginGeneration {
	if s == nil {
		return nil
	}
	s.pluginGenerationMu.Lock()
	defer s.pluginGenerationMu.Unlock()
	generation := s.pluginGeneration
	if generation == nil {
		return nil
	}
	generation.retain()
	return generation
}

func (s *Session) ReleasePluginGeneration(generation *PluginGeneration) {
	s.releasePluginGeneration(generation)
}

func (s *Session) releasePluginGeneration(generation *PluginGeneration) {
	if s == nil || generation == nil {
		return
	}
	if generation.Release() {
		s.pluginGenerationMu.Lock()
		delete(s.retiredPluginGenerations, generation)
		s.pluginGenerationMu.Unlock()
		s.persistRevocationReport(generation)
	}
}

func (s *Session) capturePluginGeneration() *PluginGeneration {
	var manager *mcp.Manager
	if s.Toolkit != nil {
		manager = s.Toolkit.MCPManager()
	}
	hookSnapshot := hooks.NewDispatcher(nil)
	hookSnapshot.Replace(s.HookDispatcher)
	generation := &PluginGeneration{
		id:                newPluginGenerationID(s.WuuHome),
		settings:          config.Config{Extensions: s.ExtensionSettings},
		plugins:           append([]pluginpkg.Plugin(nil), s.Plugins...),
		active:            append([]pluginpkg.Plugin(nil), s.ActivePlugins...),
		host:              s.PluginHost,
		hooks:             hookSnapshot,
		skills:            append([]skills.Skill(nil), s.Skills...),
		pluginSkills:      s.pluginSkills,
		mcp:               manager,
		mcpBinding:        mcpActivityBindingsFromPlugins(s.ActivePlugins),
		requestTransforms: buildPluginRequestTransforms(s.PluginHost, s.ProviderName, "", s.RootDir),
		systemPrompts:     s.systemPrompts,
	}
	if s.StreamRunner != nil {
		generation.compactions = s.StreamRunner.CompactionRegistry
	}
	generation.retain()
	return generation
}

func (s *Session) applyPluginGeneration(generation *PluginGeneration) {
	if generation == nil {
		return
	}
	s.Plugins = append([]pluginpkg.Plugin(nil), generation.plugins...)
	s.ActivePlugins = append([]pluginpkg.Plugin(nil), generation.active...)
	s.ExtensionSettings = generation.settings.Extensions
	s.PluginHost = generation.host
	s.systemPrompts = generation.systemPrompts
	if s.StreamRunner != nil {
		s.StreamRunner.Tools = replacePluginToolHost(s.StreamRunner.Tools, generation.host, "", s.RootDir)
		s.StreamRunner.BeforeModelStep = pluginPreStepInjector(generation.host, s.ProviderName, s.Model, "", s.RootDir)
		s.StreamRunner.BeforeRequest = pluginRequestInterceptorWithTransforms(generation.host, generation.requestTransforms, s.ProviderName, "", s.RootDir)
		s.StreamRunner.CompactionRegistry = generation.compactions
	}
	s.HookDispatcher = generation.hooks
	s.Skills = append([]skills.Skill(nil), generation.skills...)
	s.pluginSkills = generation.pluginSkills
	if s.Toolkit != nil {
		s.Toolkit.SetPluginManager(s.pluginManager(generation, runPluginManagementCommand))
		s.Toolkit.SetSkillsWithAvailability(s.Skills, generation.skillAvailable)
		s.Toolkit.SetMCPActivityBindings(generation.mcpBinding)
		s.Toolkit.SetMCPManager(generation.mcp)
		configureToolkitSecurityExtensions(s.Toolkit, generation.host.ServiceRegistry())
	}
	s.refreshSystemPromptWithSkills(s.ProviderName, s.Model, generation.filterSkills(generation.skills))
}

// close retires every generation-owned resource in reverse ownership order
// and records the structured revocation report on the generation. The report
// attributes failures by plugin, resource, and phase; close still returns the
// joined error for callers that only need success or failure.
func (g *PluginGeneration) close() error {
	if g == nil {
		return nil
	}
	g.revokedMu.Lock()
	if g.revokedPlugins == nil {
		g.revokedPlugins = make(map[string]bool)
	}
	for _, item := range g.active {
		g.revokedPlugins[item.ID] = true
	}
	g.revokedMu.Unlock()
	report := &GenerationRevocationReport{GenerationID: g.id, RetiredAt: time.Now().UTC()}
	g.revocation = report
	var err error
	if g.mcp != nil {
		mcpErr := g.mcp.Close()
		report.record("", "mcp-manager", RevocationPhaseShutdown, mcpErr)
		err = errors.Join(err, mcpErr)
		g.mcp = nil
	}
	if g.systemPrompts != nil {
		g.systemPrompts.Clear()
		report.record("", "system-prompts", RevocationPhaseCleanup, nil)
		g.systemPrompts = nil
	}
	if g.compactions != nil {
		g.compactions.Clear()
		report.record("", "compaction-registry", RevocationPhaseCleanup, nil)
		g.compactions = nil
	}
	if g.host != nil {
		g.host.CancelExecutions(&pluginhost.UserQuestionError{Code: "generation_closed", Message: "plugin generation retired"})
		report.record("", "plugin-executions", RevocationPhaseCancelExecutions, nil)
		ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
		for _, outcome := range g.host.CloseWithOutcomes(ctx) {
			report.record(outcome.PluginID, "plugin-process", RevocationPhaseShutdown, outcome.Err)
			if outcome.Err != nil {
				err = errors.Join(err, fmt.Errorf("close plugin %q: %w", outcome.PluginID, outcome.Err))
			}
		}
		// Shutdown hooks need host storage and session cancellation to release
		// plugin-owned background work before service routing is revoked.
		if registry := g.host.ServiceRegistry(); registry != nil {
			registry.Close(ctx)
			report.record("", "service-registry", RevocationPhaseRevokeServices, nil)
		}

		cancel()
		g.host = nil
	}
	g.requestTransforms = nil
	for index := len(g.ownedRoots) - 1; index >= 0; index-- {
		rootErr := os.RemoveAll(g.ownedRoots[index])
		report.recordDetail("", "package-snapshot", RevocationPhaseCleanup, g.ownedRoots[index], rootErr)
		err = errors.Join(err, rootErr)
	}
	g.ownedRoots = nil
	return err
}

// revocationReport returns the structured retirement record after close.
func (g *PluginGeneration) revocationReport() *GenerationRevocationReport {
	if g == nil {
		return nil
	}
	return g.revocation
}

// persistRevocationReport retains a retired or rejected generation's
// revocation report so cleanup failures stay visible after the generation is
// gone. Persistence failure is only debug-logged: it must not block the
// lifecycle transition the report describes.
func (s *Session) persistRevocationReport(g *PluginGeneration) {
	if s == nil || g == nil || g.revocation == nil {
		return
	}
	if err := appendGenerationRevocation(s.WuuHome, g.revocation); err != nil {
		providers.DebugLogf("persist plugin generation revocation: %v", err)
	}
}

// PluginGenerationRevocations returns retained generation retirement reports,
// newest first. limit bounds the result; zero returns everything retained.
func (s *Session) PluginGenerationRevocations(limit int) ([]GenerationRevocationReport, error) {
	if s == nil {
		return nil, errors.New("runtime is not initialized")
	}
	return readGenerationRevocations(s.WuuHome, limit)
}

func containsPluginGeneration(plugins []pluginpkg.Plugin, id, fingerprint string) bool {
	for _, item := range plugins {
		if item.ID == id && item.Fingerprint == fingerprint {
			return true
		}
	}
	return false
}

func packageManifestPath(root, manifestPath string) (string, error) {
	manifestPath = filepath.Clean(strings.TrimSpace(manifestPath))
	if manifestPath == "." || filepath.IsAbs(manifestPath) || manifestPath == ".." || strings.HasPrefix(manifestPath, ".."+string(filepath.Separator)) {
		return "", errors.New("manifest path escapes the package")
	}
	return filepath.Join(root, manifestPath), nil
}

func snapshotPluginPackage(source string) (string, error) {
	source = filepath.Clean(strings.TrimSpace(source))
	info, err := os.Stat(source)
	if err != nil {
		return "", err
	}
	if !info.IsDir() {
		return "", errors.New("candidate package root is not a directory")
	}
	destination, err := os.MkdirTemp("", "wuu-plugin-generation-")
	if err != nil {
		return "", err
	}
	if err := filepath.WalkDir(source, func(path string, entry fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		rel, err := filepath.Rel(source, path)
		if err != nil || rel == "." {
			return err
		}
		target := filepath.Join(destination, rel)
		info, err := entry.Info()
		if err != nil {
			return err
		}
		if entry.IsDir() {
			return os.MkdirAll(target, info.Mode().Perm())
		}
		if !info.Mode().IsRegular() {
			return fmt.Errorf("candidate package contains unsupported entry %s", rel)
		}
		if err := os.MkdirAll(filepath.Dir(target), 0o755); err != nil {
			return err
		}
		in, err := os.Open(path)
		if err != nil {
			return err
		}
		out, openErr := os.OpenFile(target, os.O_CREATE|os.O_EXCL|os.O_WRONLY, info.Mode().Perm())
		if openErr == nil {
			_, openErr = io.Copy(out, in)
		}
		closeErr := in.Close()
		if out != nil {
			closeErr = errors.Join(closeErr, out.Close())
		}
		return errors.Join(openErr, closeErr)
	}); err != nil {
		_ = os.RemoveAll(destination)
		return "", err
	}
	return destination, nil
}

// PluginServiceRegistrySnapshot introspects the service registry of the
// active plugin generation: which services exist, at what version, provided
// by whom, tagged with the durable generation epoch.
func (s *Session) PluginServiceRegistrySnapshot() (pluginhost.ServiceRegistrySnapshot, error) {
	if s == nil {
		return pluginhost.ServiceRegistrySnapshot{}, errors.New("runtime is not initialized")
	}
	host := s.PluginHost
	if host == nil {
		return pluginhost.ServiceRegistrySnapshot{}, errors.New("plugin host is not initialized")
	}
	registry := host.ServiceRegistry()
	if registry == nil {
		return pluginhost.ServiceRegistrySnapshot{}, errors.New("service registry is not active")
	}
	epoch, err := session.ReadPluginGenerationEpoch(s.WuuHome)
	if err != nil {
		epoch = 0
	}
	return registry.Snapshot(epoch), nil
}

// PluginExecutionSnapshots returns the live execution table of the active
// plugin host: which tool/capability executions are open right now, owned by
// which plugin, with their latest self-reported progress. Read-only; each
// entry is authored by the owning plugin about its own execution.
func (s *Session) PluginExecutionSnapshots() []pluginhost.ExecutionSnapshot {
	if s == nil || s.PluginHost == nil {
		return nil
	}
	return s.PluginHost.ExecutionSnapshots()
}

// snapshotMutableExecutionPackages detaches executable and lazy declarative paths
// from the replaceable installed/development catalog. Callers serialize the copy with
// publication; the returned roots belong to the generation, not discovery.
func snapshotMutableExecutionPackages(active []pluginpkg.Plugin) ([]pluginpkg.Plugin, []string, error) {
	result := append([]pluginpkg.Plugin(nil), active...)
	var roots []string
	fail := func(err error) ([]pluginpkg.Plugin, []string, error) {
		for _, root := range roots {
			_ = os.RemoveAll(root)
		}
		return nil, nil, err
	}
	for i, item := range active {
		if item.Source != "dev" && item.Source != "user" {
			continue
		}
		root, err := snapshotPluginPackage(item.Root)
		if err != nil {
			return fail(fmt.Errorf("snapshot mutable plugin %q: %w", item.ID, err))
		}
		roots = append(roots, root)
		relative, err := filepath.Rel(item.Root, item.ManifestPath)
		if err != nil {
			return fail(err)
		}
		manifest, err := packageManifestPath(root, relative)
		if err != nil {
			return fail(err)
		}
		copied, err := pluginpkg.LoadManifestWithOptions(manifest, pluginpkg.LoadOptions{Source: item.Source, Official: item.Official, WorkspaceID: item.WorkspaceID})
		if err != nil {
			return fail(err)
		}
		// Normalization rebases execution paths; package-relative fingerprints
		// must still match the exact catalog identity before retaining its trust.
		if copied.SubjectID != item.SubjectID || copied.Fingerprint != item.Fingerprint {
			return fail(fmt.Errorf("plugin %q changed while its execution package was being snapshotted", item.ID))
		}
		copied.AuthorizedDev = item.AuthorizedDev
		copied.EffectivePermissions = append([]string(nil), item.EffectivePermissions...)
		result[i] = copied
	}
	return result, roots, nil
}
