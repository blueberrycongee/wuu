package runtime

import (
	"context"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"reflect"
	"sort"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/blueberrycongee/wuu/internal/activity"
	"github.com/blueberrycongee/wuu/internal/agent"
	"github.com/blueberrycongee/wuu/internal/codemode"
	"github.com/blueberrycongee/wuu/internal/config"
	"github.com/blueberrycongee/wuu/internal/credentialstore"
	"github.com/blueberrycongee/wuu/internal/hooks"
	"github.com/blueberrycongee/wuu/internal/mcp"
	pluginpkg "github.com/blueberrycongee/wuu/internal/plugin"
	"github.com/blueberrycongee/wuu/internal/pluginhost"
	"github.com/blueberrycongee/wuu/internal/prompt"
	"github.com/blueberrycongee/wuu/internal/promptservice"
	"github.com/blueberrycongee/wuu/internal/providers"
	"github.com/blueberrycongee/wuu/internal/session"
	"github.com/blueberrycongee/wuu/internal/siwc"
	"github.com/blueberrycongee/wuu/internal/skills"
	"github.com/blueberrycongee/wuu/internal/tools"
)

// PluginGeneration is a complete replacement for the plugin-owned surfaces of a
// Session. It owns every process and private package snapshot. Conversations
// adopt published updates at their next idle turn boundary. Active foreground
// turns retain their generation; publication cancels its background jobs and
// local UI control. Committed removal or disable revokes that plugin across all
// retained generations immediately.
// Close retires the remaining resources in reverse ownership order and records
// a structured revocation report.
type PluginGeneration struct {
	productParent     *kernelHostServices
	composition       productCompositionBinding
	systemPrompt      string
	promptSections    []prompt.SectionInfo
	tools             agent.ToolExecutor
	toolkit           *tools.Toolkit
	codeMode          *codemode.Service
	modelSelection    *generationModelSelection
	modelToolLoading  *generationToolLoading
	models            *generationModelBindings
	id                string
	state             *runtimeState
	credentials       credentialstore.Store
	configClient      *config.Client
	chatGPTManager    *siwc.Manager
	artifactClient    *ArtifactClient
	mcpOAuth          *mcp.OAuthManager
	requiredPlugins   map[string]bool
	selectedPluginIDs []string
	settings          config.Config
	plugins           []pluginpkg.Plugin
	active            []pluginpkg.Plugin
	host              *pluginhost.Host
	hooks             *hooks.Dispatcher
	skills            []skills.Skill
	pluginSkills      pluginSkillSnapshot
	mcp               *mcp.Manager
	mcpBinding        map[string]tools.MCPActivityBinding
	activityRegistry  *activity.Registry
	requestTransforms *agent.RequestTransformChain
	systemPrompts     *agent.SystemPromptAssembler
	compactions       *agent.CompactionRegistry
	ownedRoots        []string
	revocation        *GenerationRevocationReport
	closeErr          error
	// driverGateways is this generation's remote-driver gateway routing
	// table; executions registered here route only to this generation's
	// kernel services.
	engineGateways *engineGatewayTable
	driverGateways *driverGatewayTable
	// The callback retains this generation's kernel authority for each turn.
	beginModelReceipts agent.BeginModelReceiptsFunc
	// refs counts the live Session plus any ThreadRuntime still bound to this
	// generation. A retired generation stays open until the last conversation
	// that started against it is released. A negative count seals retirement;
	// zero is reserved for a prepared candidate that has not been published.
	refs         atomic.Int32
	revocationMu sync.Mutex
	// Admission closes under modelMu before root shutdown starts Wait.
	modelMu            sync.Mutex
	modelStopped       bool
	modelOwners        map[*modelInvocationOwner]struct{}
	modelBodies        sync.WaitGroup
	modelInvocations   sync.WaitGroup
	modelCompletionErr error
	// revokedMu also protects host detachment from read-only diagnostics.
	revokedMu      sync.Mutex
	revokedPlugins map[string]bool
}

// Keep only the first durability or authority-release failure. Provider
// rejections and ordinary cancellation belong to the model result, not shutdown.
func (g *PluginGeneration) recordModelCompletionError(err error) {
	if err == nil {
		return
	}
	g.modelMu.Lock()
	if g.modelCompletionErr == nil {
		g.modelCompletionErr = err
	}
	g.modelMu.Unlock()
}

const modelShutdownTimeout = 3 * time.Second

// Root shutdown drains model effects separately from passive State references.
// A retained document reader must not prevent exit or lose its provider early.
func (s *Session) stopPluginModelInvocations() (err error) {
	s.pluginGenerationMu.Lock()
	var generations []*PluginGeneration
	current := s.pluginGeneration
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
	defer func() {
		for _, generation := range generations {
			err = errors.Join(err, s.releasePluginGeneration(generation))
		}
	}()
	ctx, cancel := context.WithTimeout(context.Background(), modelShutdownTimeout)
	defer cancel()
	var modelOwners []*modelInvocationOwner
	for _, generation := range generations {
		generation.modelMu.Lock()
		generation.modelStopped = true
		for owner := range generation.modelOwners {
			modelOwners = append(modelOwners, owner)
		}
		generation.modelMu.Unlock()
	}
	for _, generation := range generations {
		generation.host.StopBackground()
	}
	for _, owner := range modelOwners {
		context.AfterFunc(ctx, func() { owner.abort(context.Cause(ctx)) })
		owner.cancel()
	}
	bodiesDone := make(chan struct{})
	go func() {
		for _, generation := range generations {
			generation.modelBodies.Wait()
		}
		close(bodiesDone)
	}()
	select {
	case <-bodiesDone:
	case <-ctx.Done():
		err = fmt.Errorf("drain model invocations: %w", ctx.Err())
	}
	// Parent callbacks may await their execution's cancellation before returning.
	// Revoke them even after a failed drain, then join deferred State releases
	// with the remaining time from the same shutdown deadline.
	for _, generation := range generations {
		generation.host.CancelExecutions(context.Canceled)
	}
	releasesDone := make(chan struct{})
	go func() {
		for _, generation := range generations {
			generation.modelInvocations.Wait()
		}
		close(releasesDone)
	}()
	select {
	case <-releasesDone:
	case <-ctx.Done():
		if err == nil {
			err = fmt.Errorf("drain model invocations: %w", ctx.Err())
		}
	}
	for _, generation := range generations {
		generation.modelMu.Lock()
		completionErr := generation.modelCompletionErr
		generation.modelMu.Unlock()
		if completionErr != nil {
			err = errors.Join(err, fmt.Errorf("model completion in generation %q: %w", generation.id, completionErr))
		}
	}
	return err
}

// PreflightExtensions discovers and builds a replacement without changing the
// live Session.
func (s *Session) PreflightExtensions(cfg config.Config) (*PluginGeneration, error) {
	if s == nil {
		return nil, errors.New("runtime is not initialized")
	}
	return s.PreflightDiscoveredExtensions(cfg, discoverPlugins(s.RootDir, s.WuuHome))
}

// PreflightDiscoveredExtensions builds the already-observed catalog, allowing
// refresh admission to fingerprint its exact inputs before starting processes.
// Callers must serialize discovery and preparation with catalog publication.
func (s *Session) PreflightDiscoveredExtensions(cfg config.Config, discovered []pluginpkg.Plugin, requiredIDs ...string) (*PluginGeneration, error) {
	if s == nil {
		return nil, errors.New("runtime is not initialized")
	}
	required := s.requiredPluginMutationConsumers(requiredIDs...)
	return s.buildPluginGeneration(cfg, discovered, required, nil, startPluginClient)
}

// Explicit installation/update must preserve the healthy consumers affected by
// its targets. Capture the committed graph before candidate dependency pruning:
// declarative packages and service-only consumers need the same atomicity as
// process runtimes. Unrelated and already unavailable addons remain optional.
func (s *Session) requiredPluginMutationConsumers(targets ...string) map[string]bool {
	required := make(map[string]bool, len(targets))
	for _, id := range targets {
		required[id] = true
	}
	if len(required) == 0 {
		return required
	}
	current := s.retainPluginGeneration()
	defer s.releasePluginGeneration(current)
	if current == nil || current.host == nil {
		return required
	}
	available := current.availableModuleIDs()
	dependencies := current.host.Dependencies()
	for changed := true; changed; {
		changed = false
		for id, edges := range dependencies {
			if required[id] || !available[id] {
				continue
			}
			for _, edge := range edges {
				if edge.Required && required[edge.Provider] {
					required[id], changed = true, true
					break
				}
			}
		}
	}
	return required
}

// PreflightObservedExtensions prepares automatic catalog observations without
// restarting unchanged failed implementations. A new source/configuration or
// an explicit Refresh still gets a real preparation attempt. Required selections
// pass through the same host checks and never gain a fallback through quarantine.
func (s *Session) PreflightObservedExtensions(cfg config.Config, discovered []pluginpkg.Plugin) (*PluginGeneration, error) {
	prepared, err := s.PrepareObservedPluginCatalog(cfg, discovered)
	if err != nil {
		return nil, err
	}
	return prepared.Build(context.Background())
}

// PrepareObservedPluginCatalog copies executable inputs while the caller owns
// the catalog read lease. Build may then initialize those immutable bytes after
// releasing the lease, so another process can publish a cancellation or repair.
func (s *Session) PrepareObservedPluginCatalog(cfg config.Config, discovered []pluginpkg.Plugin) (*PluginCatalogPreparation, error) {
	if s == nil {
		return nil, errors.New("runtime is not initialized")
	}
	current := s.retainPluginGeneration()
	defer s.releasePluginGeneration(current)
	failed := make(map[string]error)
	if current != nil && current.host != nil && reflect.DeepEqual(current.settings, cfg) {
		unchanged := make(map[string]bool)
		for _, item := range discovered {
			for _, previous := range current.plugins {
				if item.ID == previous.ID && item.SubjectID == previous.SubjectID && item.Fingerprint == previous.Fingerprint && item.Source == previous.Source && item.Official == previous.Official {
					unchanged[item.ID] = true
					break
				}
			}
		}
		dependencyFailures := current.host.DependencyFailures()
		for _, status := range current.host.Statuses() {
			// Dependent failures are recomputed so repairing a provider also
			// restores its unchanged consumers. Only direct failures are sticky.
			if unchanged[status.ID] && status.State == pluginhost.StateFailed && dependencyFailures[status.ID] == nil {
				failed[status.ID] = errors.New(status.Error)
			}
		}
	}
	return s.preparePluginCatalog(cfg, discovered, nil, nil, func(ctx context.Context, process pluginhost.ProcessConfig) (pluginhost.Client, error) {
		if failure := failed[process.ID]; failure != nil {
			if process.OnClose != nil {
				process.OnClose()
			}
			return nil, failure
		}
		return startPluginClient(ctx, process)
	})
}

// ObservesPluginCatalog reports whether the committed graph represents these
// exact inputs, including failed or dependency-blocked optional packages.
func (s *Session) ObservesPluginCatalog(cfg config.Config, discovered []pluginpkg.Plugin) bool {
	current := s.retainPluginGeneration()
	defer s.releasePluginGeneration(current)
	return current != nil && reflect.DeepEqual(current.settings, cfg) && reflect.DeepEqual(current.plugins, discovered)
}

// RetainAdditivePluginBase proves that preparing this observation cannot change
// the committed graph's authority: only new inferred addons are present. The
// caller must release the returned generation and revalidate before publication.
func (s *Session) RetainAdditivePluginBase(cfg config.Config, discovered []pluginpkg.Plugin) *PluginGeneration {
	if s == nil || s.SafeMode || cfg.Composition != nil {
		return nil
	}
	base := s.retainPluginGeneration()
	if base == nil {
		return nil
	}
	valid := s.PluginGenerationAcceptsAdditivePreparation(base) && reflect.DeepEqual(base.settings, cfg) && len(discovered) > len(base.plugins)
	byID := make(map[string]pluginpkg.Plugin, len(discovered))
	for _, item := range discovered {
		if _, duplicate := byID[item.ID]; duplicate {
			valid = false
		}
		byID[item.ID] = item
	}
	for _, previous := range base.plugins {
		if current, found := byID[previous.ID]; !found || !reflect.DeepEqual(previous, current) {
			valid = false
			break
		}
	}
	if !valid {
		s.releasePluginGeneration(base)
		return nil
	}
	return base
}

// PluginGenerationAcceptsAdditivePreparation checks the committed graph's
// required roots without provider calls. Quarantined optional diagnostics do
// not make healthy selected services unavailable; required failures always do.
func (s *Session) PluginGenerationAcceptsAdditivePreparation(base *PluginGeneration) bool {
	if base == nil || base.host == nil || base.requiredPlugins == nil || !s.IsCurrentPluginGeneration(base) || s.PluginGenerationNeedsRecovery() {
		return false
	}
	for id := range base.host.DependencyFailures() {
		if base.requiredPlugins[id] {
			return false
		}
	}
	for _, status := range base.host.Statuses() {
		if base.requiredPlugins[status.ID] && (status.State == pluginhost.StateFailed || status.State == pluginhost.StateStopped) {
			return false
		}
	}
	return true
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
// beforeRevoke runs once, before the first retirement effect, so callers can
// fence admission before failed clients temporarily disappear from the host.
func (s *Session) RevokeFailedPluginDependencies(beforeRevoke func()) bool {
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
		activeIDs := generation.availableModuleIDs()
		invalidated := false
		generation.revokedMu.Lock()
		for id, available := range activeIDs {
			if !available && !generation.revokedPlugins[id] {
				invalidated = true
			}
		}
		generation.revokedMu.Unlock()
		if invalidated {
			if !changed && beforeRevoke != nil {
				beforeRevoke()
			}
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
// without persisting the proposed grant/enable decisions. Enabled, trusted
// candidates named in requiredIDs must prepare successfully.
func (s *Session) PreflightExtensionPolicy(cfg config.Config, requiredIDs ...string) (*PluginGeneration, error) {
	if s == nil {
		return nil, errors.New("runtime is not initialized")
	}
	required := s.requiredActivePluginRuntimes()
	for _, id := range requiredIDs {
		required[id] = true
	}
	return s.buildPluginGeneration(cfg, s.Plugins, required, nil, startPluginClient)
}

// An explicit policy transaction must keep its remaining healthy consumers
// usable. This requirement is scoped to that transaction, never inherited by
// ordinary catalog observation or optional implementation replacement.
func (s *Session) requiredActivePluginRuntimes() map[string]bool {
	required := make(map[string]bool)
	if s.PluginHost != nil {
		for _, status := range s.PluginHost.Statuses() {
			if status.State == pluginhost.StateActive {
				required[status.ID] = true
			}
		}
	}
	return required
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
	// Removing an inferred addon intentionally retires its dependent closure.
	// Explicit composition selections and native service roots remain required
	// by host construction; unrelated catalog addons must not veto removal.
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
	defer os.RemoveAll(snapshot)

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
	var installedRoot string
	found := false
	for index := range discovered {
		if discovered[index].ID != id {
			continue
		}
		if discovered[index].Source != "user" || discovered[index].SubjectID != candidate.SubjectID {
			return nil, fmt.Errorf("plugin %q candidate does not replace the installed user package", id)
		}
		installedRoot = discovered[index].Root
		discovered[index] = candidate
		found = true
		break
	}
	if !found {
		return nil, fmt.Errorf("installed user plugin %q was not found", id)
	}
	sort.Slice(discovered, func(i, j int) bool { return discovered[i].ID < discovered[j].ID })

	required := s.requiredPluginMutationConsumers(id)
	generation, err := s.buildPluginGeneration(cfg, discovered, required, nil, startPluginClient)
	if err != nil {
		return nil, err
	}
	if cfg.Extensions == nil || !pluginManagementTrusted(*cfg.Extensions, candidate) || (cfg.Extensions.IsEnabled(candidate.SubjectID, candidate.EnabledByDefault()) && !containsPluginGeneration(generation.active, id, fingerprint)) {
		_ = generation.close()
		s.persistRevocationReport(generation)
		return nil, fmt.Errorf("plugin %q candidate is not approved for its exact fingerprint", id)
	}
	// Catalog paths identify the durable installation; only generation.active
	// owns executable snapshots. A later publication must never borrow this
	// preparation directory after its owner has retired.
	candidate.Root = installedRoot
	candidate.ManifestPath = filepath.Join(installedRoot, manifestPath)
	if candidate.RuntimePath != "" {
		runtime := *candidate.Runtime
		runtime.Command = filepath.Join(installedRoot, candidate.RuntimePath)
		candidate.Runtime = &runtime
	}
	candidate.Commands = append([]pluginpkg.ResolvedCommand(nil), candidate.Commands...)
	for index := range candidate.Commands {
		if candidate.Commands[index].ResolvedPrompt != nil {
			prompt := *candidate.Commands[index].ResolvedPrompt
			prompt.Path = filepath.Join(installedRoot, prompt.RelPath)
			candidate.Commands[index].ResolvedPrompt = &prompt
		}
	}
	for index := range generation.plugins {
		if generation.plugins[index].ID == id {
			generation.plugins[index] = candidate
			break
		}
	}
	return generation, nil
}

// PluginCatalogPreparation owns immutable, unpublished package inputs between
// catalog observation and cancellable process initialization.
type PluginCatalogPreparation struct {
	storageTransfers  []pluginStorageTransfer
	session           *Session
	settings          config.Config
	discovered        []pluginpkg.Plugin
	active            []pluginpkg.Plugin
	selectedPluginIDs []string
	required          map[string]bool
	ownedRoots        []string
	factories         []NativeProviderFactory
	start             pluginClientStarter
	modelSelection    *generationModelSelection
}

// Close discards a snapshot that has not been consumed by Build.
func (p *PluginCatalogPreparation) Close() {
	for _, root := range p.ownedRoots {
		_ = os.RemoveAll(root)
	}
	p.ownedRoots = nil
}

func (s *Session) buildPluginGeneration(cfg config.Config, discovered []pluginpkg.Plugin, required map[string]bool, ownedRoots []string, start pluginClientStarter) (*PluginGeneration, error) {
	prepared, err := s.preparePluginCatalog(cfg, discovered, required, ownedRoots, start)
	if err != nil {
		return nil, err
	}
	return prepared.Build(context.Background())
}

func (s *Session) preparePluginCatalog(cfg config.Config, discovered []pluginpkg.Plugin, required map[string]bool, ownedRoots []string, start pluginClientStarter) (*PluginCatalogPreparation, error) {
	var err error
	cfg, err = s.applyRuntimeConfig(cfg)
	if err != nil {
		return nil, err
	}
	factories := withConfigurationProvider(s.nativeProviders, s.ConfigPath, s.RootDir, cfg, s.ConfigLoadMode)
	var active []pluginpkg.Plugin
	var selectedPluginIDs []string
	if !s.SafeMode {
		activationPlan, err := ResolvePluginActivationPlan(cfg, discovered, factories...)
		if err != nil {
			return nil, err
		}
		for _, item := range discovered {
			if !required[item.ID] {
				continue
			}
			for _, issue := range activationPlan.Issues[item.ID] {
				if issue.Kind == pluginpkg.ActivationIssueMissingRequirement || issue.Kind == pluginpkg.ActivationIssueVersionMismatch {
					return nil, fmt.Errorf("plugin %q cannot activate: required plugin %q (%s)", item.ID, issue.RelatedPluginID, issue.Kind)
				}
			}
		}
		active = activationPlan.Plugins
		for _, item := range active {
			selectedPluginIDs = append(selectedPluginIDs, item.ID)
		}
		// Dependency-pruned replacements remain selected. Their old healthy
		// implementations must not look explicitly removed to accepted work.
		for id := range activationPlan.Issues {
			selectedPluginIDs = append(selectedPluginIDs, id)
		}
	}
	// Policy and unrelated-package updates reuse admitted bytes, not mutable
	// installations. Pin their owner through copying; the replacement then owns
	// independent snapshots and cannot prolong or delete its predecessor's files.
	current := s.retainPluginGeneration()
	defer s.releasePluginGeneration(current)
	if current != nil {
		for index, item := range active {
			for _, admitted := range current.active {
				if admitted.SubjectID == item.SubjectID && admitted.Fingerprint == item.Fingerprint && admitted.Source == item.Source && admitted.Official == item.Official {
					active[index] = admitted
					break
				}
			}
		}
	}
	storageTransfers, err := resolveStorageTransfers(cfg, discovered, active, s.UserBinding())
	if err != nil {
		return nil, err
	}
	var snapshotRoots []string
	var snapshotErr error
	active, snapshotRoots, snapshotErr = snapshotMutableExecutionPackages(active)
	if snapshotErr != nil {
		return nil, snapshotErr
	}
	ownedRoots = append(ownedRoots, snapshotRoots...)
	return &PluginCatalogPreparation{
		storageTransfers: storageTransfers, session: s, settings: cfg, discovered: append([]pluginpkg.Plugin(nil), discovered...), active: active,
		selectedPluginIDs: selectedPluginIDs, required: required, ownedRoots: ownedRoots, factories: factories, start: start,
	}, nil
}

// Build consumes the immutable snapshot exactly once. Failure transfers its
// cleanup to the host drain barrier; success transfers it to the new generation.
func (p *PluginCatalogPreparation) Build(ctx context.Context) (*PluginGeneration, error) {
	s, cfg, discovered, active := p.session, p.settings, p.discovered, p.active
	required, ownedRoots, factories, start := p.required, p.ownedRoots, p.factories, p.start
	p.ownedRoots = nil
	// Targeted mutations must validate their target. Ordinary inferred addons
	// remain optional even after a previous implementation was active: a bad
	// replacement is diagnosed in the new graph, not a global admission fence.
	requiredRuntimes := make(map[string]bool, len(required))
	for id, value := range required {
		requiredRuntimes[id] = value
	}
	selection := productComposition(cfg.Composition, s.DesktopExecutionDefaults, active, factories)
	host, kernel, err := buildPluginHostForBinding(ctx, active, s.RootDir, s.WorkspaceID, s.WuuHome, s.StateDir, requiredRuntimes, start, s.PluginSessionRouter, s.UserQuestions, runtimeComposition(selection, s.SafeMode), cfg.Composition != nil, s.DriverProfile, s.UserBinding(), p.storageTransfers, factories...)
	if err != nil {
		releaseFailedSnapshots(err, ownedRoots)
		return nil, err
	}
	if err := validateToolReplacements(s.RootDir, host, cfg.Agent.ToolReplacements); err != nil {
		closeErr := (&PluginGeneration{host: host, ownedRoots: ownedRoots}).close()
		return nil, errors.Join(err, closeErr)
	}
	kernel.bindModelSession(s, host)
	// Service negotiation can reject a prepared runtime after process startup.
	// Treat that exactly like startup failure for required replacements.
	for _, status := range host.Statuses() {
		if requiredRuntimes[status.ID] && status.State == pluginhost.StateFailed {
			closeErr := (&PluginGeneration{host: host, ownedRoots: ownedRoots}).close()
			return nil, pluginActivationError(status.ID, errors.New(status.Error), closeErr)
		}
	}
	active = availablePluginPackages(active, host)
	var promptProvider, promptModel string
	if p.modelSelection != nil {
		promptProvider, promptModel = p.modelSelection.provider, p.modelSelection.model
	} else {
		basis, err := s.CaptureAdvancedRuntime()
		if err != nil {
			closeErr := (&PluginGeneration{host: host, ownedRoots: ownedRoots}).close()
			return nil, errors.Join(err, closeErr)
		}
		promptProvider, promptModel = basis.ProviderName, basis.Model
	}
	systemPrompts, compactions, err := buildPluginAgentCapabilities(ctx, host, promptProvider, promptModel, s.RootDir)
	if err != nil {
		closeErr := (&PluginGeneration{host: host, ownedRoots: ownedRoots}).close()
		return nil, errors.Join(err, closeErr)
	}
	pluginSkills := discoverPluginSkills(active)
	var discoveredSkills []skills.Skill
	if !s.UserBinding().IsUser() {
		discoveredSkills = discoverSkillsWithPlugins(s.RootDir, s.HomeDir, s.WuuHome, pluginSkills, cfg.Skills)
	}
	generationID := newPluginGenerationID(s.WuuHome)
	generation := &PluginGeneration{
		modelSelection:    p.modelSelection,
		id:                generationID,
		state:             kernel.state,
		settings:          cfg,
		requiredPlugins:   kernel.requiredPlugins,
		selectedPluginIDs: p.selectedPluginIDs,
		plugins:           append([]pluginpkg.Plugin(nil), discovered...),
		active:            append([]pluginpkg.Plugin(nil), active...),
		host:              host,
		hooks:             buildHookDispatcher(cfg, active, s.TitleClient, s.Model, nil),
		skills:            discoveredSkills,
		pluginSkills:      pluginSkills,
		mcpBinding:        mcpActivityBindingsFromPlugins(active, generationID),
		requestTransforms: buildPluginRequestTransforms(host, s.ProviderName, "", s.RootDir),
		systemPrompts:     systemPrompts,
		compactions:       compactions,
		ownedRoots:        append([]string(nil), ownedRoots...),
		engineGateways:    kernel.engineGateways,
		driverGateways:    kernel.driverGateways,
	}
	kernel.bindStateGeneration(generation, s)
	if kernel.selectedServices != nil {
		generation.credentials = kernel.selectedServices.credentials
		generation.configClient = kernel.selectedServices.config
		generation.chatGPTManager = kernel.selectedServices.chatGPT
		generation.artifactClient = kernel.selectedServices.artifacts
		generation.mcpOAuth = kernel.selectedServices.oauth
	}
	// Activation acquires the Session reference. A prepared candidate has no
	// live owners and must not keep an extra reference after publication.
	generation.bindActivityOwnership(s.ActivityRegistry)
	return generation, nil
}

// Tool listing can initialize provider-owned resources and belongs to activation.
func (s *Session) hydratePluginGeneration(generation *PluginGeneration) error {
	host := generation.host
	surface := activeSurface(nil)
	if selectsRuntimeModule(host, toolExecutionConsumer) {
		executor := newSelectedToolExecutor(host, s.candidateToolScope(generation, generation.requiredPlugins), selectedToolIdentity{cwd: s.RootDir})
		generation.tools = executor
		if generation.hooks != nil {
			generation.tools = hooks.NewHookedExecutor(executor, generation.hooks, "", s.RootDir)
		}
		var err error
		surface, err = executor.surface(context.Background())
		if err != nil {
			return err
		}
	}
	if selectsRuntimeModule(host, sessionExecutionConsumer) {
		var provider, model string
		if generation.modelSelection != nil {
			provider, model = generation.modelSelection.provider, generation.modelSelection.model
			if generation.models != nil && generation.models.err == nil && generation.models.roles.Main.APIModel != "" {
				model = generation.models.roles.Main.APIModel
			}
		} else {
			basis, err := s.CaptureAdvancedRuntime()
			if err != nil {
				return err
			}
			provider, model = basis.ProviderName, basis.Model
		}
		result, err := assembleSelectedPrompt(context.Background(), host, promptservice.Request{Role: "main", CWD: s.RootDir, Date: s.SessionDate, Provider: provider, Model: model, Surface: surface, Instructions: s.InstructionFiles, Skills: generation.skills}, generation.systemPrompts)
		if err != nil {
			return err
		}
		generation.systemPrompt, generation.promptSections = result.Content, result.Sections
	}
	return nil
}

// Failed preparation transfers snapshot cleanup to the same host drain barrier.
type failedPreparation struct {
	cause   error
	drained <-chan struct{}
}

func (e *failedPreparation) Error() string { return e.cause.Error() }
func (e *failedPreparation) Unwrap() error { return e.cause }
func releaseFailedSnapshots(err error, roots []string) {
	remove := func() {
		for _, root := range roots {
			_ = os.RemoveAll(root)
		}
	}
	var failed *failedPreparation
	if errors.As(err, &failed) {
		select {
		case <-failed.drained:
		default:
			go func() { <-failed.drained; remove() }()
			return
		}
	}
	remove()
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
// Active work retains same-ID implementations until its idle boundary. Local
// control and previews stop at publication, while removal or disable revokes all
// of that plugin's surfaces across retained generations.
func (s *Session) ActivatePluginGeneration(candidate *PluginGeneration, commit func() error) error {
	if s == nil {
		return errors.New("runtime is not initialized")
	}
	if candidate == nil || candidate.host == nil || candidate.hooks == nil {
		return errors.New("plugin candidate generation is not initialized")
	}
	if candidate.modelSelection != nil {
		// A selected provider can reenter model configuration from its own
		// activation callback. Do not wait on the transition that invoked it.
		if !s.pluginTransitionMu.TryLock() {
			closeErr := candidate.close()
			s.persistRevocationReport(candidate)
			return errors.Join(ErrStaleModelPublication, closeErr)
		}
	} else {
		s.pluginTransitionMu.Lock()
	}
	defer s.pluginTransitionMu.Unlock()
	s.pluginGenerationMu.Lock()
	if selection := candidate.modelSelection; selection != nil && selection.basisVersion != s.runtimePublicationVersion.Load() {
		s.pluginGenerationMu.Unlock()
		closeErr := candidate.close()
		s.persistRevocationReport(candidate)
		return errors.Join(ErrStaleModelPublication, closeErr)
	}
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
	// Local control cannot outlive provider replacement. Revoke leases before
	// the replacement activates, so a pinned foreground turn cannot keep sending
	// input or silently switch control surfaces after publication.
	old.revokeActivities("")
	// Ordinary foreground calls retain their generation until the idle boundary.
	old.host.StopBackground()
	// Activation may synchronously call host services. Keep old bindings
	// available until activation settles, without holding their lookup mutex.
	// Own the candidate before effectful callbacks can retain its state.
	candidate.retain()
	activationErr := activatePluginHost(context.Background(), candidate.host)
	modelErr := s.bindGenerationModels(candidate)
	activationErr = errors.Join(activationErr, modelErr)
	available := availablePluginPackages(candidate.active, candidate.host)
	if len(available) != len(candidate.active) {
		candidate.revokeMissingPlugins(candidate.availableModuleIDs(), s.TitleClient, s.Model)
		candidate.active = available
		candidate.pluginSkills = discoverPluginSkills(available)
		if !s.UserBinding().IsUser() {
			candidate.skills = discoverSkillsWithPlugins(s.RootDir, s.HomeDir, s.WuuHome, candidate.pluginSkills, candidate.settings.Skills)
		} else {
			candidate.skills = nil
		}
		candidate.mcpBinding = mcpActivityBindingsFromPlugins(available, candidate.id)
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
	activationErr = errors.Join(activationErr, s.hydratePluginGeneration(candidate))
	s.pluginGenerationMu.Lock()
	s.applyPluginGeneration(candidate)
	s.pluginGeneration = candidate
	// Conversations that already pinned the previous generation keep using it
	// until they rebuild. The Session reference is released here; remaining
	// thread runtimes keep the processes and tools alive.
	if s.retiredPluginGenerations == nil {
		s.retiredPluginGenerations = make(map[*PluginGeneration]struct{})
	}
	s.retiredPluginGenerations[old] = struct{}{}
	selectedIDs := make(map[string]bool)
	for _, id := range candidate.moduleIDs() {
		selectedIDs[id] = true
	}
	for _, id := range candidate.selectedPluginIDs {
		selectedIDs[id] = true
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
		// A failed same-ID replacement does not revoke the healthy executable
		// retained by accepted work. Removal/disable still revokes immediately,
		// and each old graph's own failures retain their dependency semantics.
		retainedIDs := generation.availableModuleIDs()
		for id := range retainedIDs {
			retainedIDs[id] = retainedIDs[id] && selectedIDs[id]
		}
		generation.revokeMissingPlugins(retainedIDs, s.TitleClient, s.Model)
		s.releasePluginGeneration(generation)
	}
	if activationErr != nil {
		return &pluginGenerationCommittedError{cause: activationErr}
	}
	return nil
}

// moduleIDs includes native lifecycle participants and declarative-only packages.
// Both belong to the generation even when they provide no runtime services.
func (g *PluginGeneration) moduleIDs() []string {
	ids := make(map[string]bool)
	for _, item := range g.active {
		ids[item.ID] = true
	}
	for _, status := range g.host.Statuses() {
		ids[status.ID] = true
	}
	out := make([]string, 0, len(ids))
	for id := range ids {
		out = append(out, id)
	}
	sort.Strings(out)
	return out
}

func (g *PluginGeneration) availableModuleIDs() map[string]bool {
	var failures map[string]error
	if g.host != nil {
		failures = g.host.DependencyFailures()
	}
	available := make(map[string]bool)
	for _, id := range g.moduleIDs() {
		available[id] = failures[id] == nil
	}
	for _, status := range g.host.Statuses() {
		if status.State == pluginhost.StateFailed || status.State == pluginhost.StateStopped {
			available[status.ID] = false
		}
	}
	return available
}

// revokeMissingPlugins stops removed or disabled implementations even when a
// conversation still pins this generation. Updates with the same ID retain
// their old implementation until outstanding work releases it.
func (g *PluginGeneration) revokeMissingPlugins(activeIDs map[string]bool, client providers.Client, model string) {
	g.revocationMu.Lock()
	defer g.revocationMu.Unlock()
	var dependencies map[string][]pluginhost.Dependency
	ordered := g.moduleIDs()
	if g.host != nil {
		dependencies = g.host.Dependencies()
		order, _ := g.host.DependencyOrder()
		rank := make(map[string]int, len(order))
		for index, id := range order {
			rank[id] = index
		}
		sort.SliceStable(ordered, func(i, j int) bool { return rank[ordered[i]] < rank[ordered[j]] })
	}
	g.revokedMu.Lock()
	// Same-ID replacements can change their requirements. A pinned generation
	// still needs its own providers; never apply the replacement's graph to it.
	retainedIDs := make(map[string]bool, len(ordered))
	for _, id := range ordered {
		retainedIDs[id] = activeIDs[id] && !g.revokedPlugins[id]
	}
	for changed := true; changed; {
		changed = false
		for _, id := range ordered {
			if !retainedIDs[id] {
				continue
			}
			for _, dependency := range dependencies[id] {
				retained, tracked := retainedIDs[dependency.Provider]
				if dependency.Required && tracked && !retained {
					retainedIDs[id] = false
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
	var retiring []string
	// The retained generation owns the selected service and package graph.
	// Consumers release their resources while their providers remain alive.
	for index := len(ordered) - 1; index >= 0; index-- {
		id := ordered[index]
		if activeIDs[id] && !g.revokedPlugins[id] {
			continue
		}
		if g.revokedPlugins[id] {
			continue
		}
		if g.revokedPlugins == nil {
			g.revokedPlugins = make(map[string]bool)
		}
		g.revokedPlugins[id] = true
		retiring = append(retiring, id)
	}
	// Publish the entire mask before any shutdown callback. Such callbacks may
	// read skills or inventory, so no mask lock may span an external effect.
	g.revokedMu.Unlock()
	if len(retiring) == 0 {
		return
	}
	dependencyFailures := make(map[string]error)
	failedStatuses := make(map[string]pluginhost.Status)
	if g.host != nil {
		dependencyFailures = g.host.DependencyFailures()
		for _, status := range g.host.Statuses() {
			if status.State == pluginhost.StateFailed {
				failedStatuses[status.ID] = status
			}
		}
	}
	if g.hooks != nil {
		g.hooks.Replace(buildHookDispatcher(g.settings, remaining, client, model, nil))
	}
	packages := make(map[string]pluginpkg.Plugin, len(g.active))
	for _, item := range g.active {
		packages[item.ID] = item
	}
	for _, id := range retiring {
		g.revokeActivities(id)
		if g.host != nil {
			ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
			cause := &pluginhost.UserQuestionError{Code: "plugin_disabled", Message: "module was removed or disabled"}
			var outcome pluginhost.ClientShutdownOutcome
			status, failed := failedStatuses[id]
			if failed {
				outcome, _ = g.host.RetireFailedPlugin(ctx, status, cause)
			} else {
				outcome, _ = g.host.RetirePlugin(ctx, id, cause)
			}
			cancel()
			if failure := dependencyFailures[id]; failure != nil && !failed {
				g.host.Add(pluginhost.Failed(id, failure))
			}
			if outcome.Err != nil {
				providers.DebugLogf("retire disabled module %q: %v", id, outcome.Err)
			}
		}
		if g.mcp != nil {
			for localName := range packages[id].MCPServers {
				name := PluginMCPServerName(id, localName)
				if err := g.mcp.Revoke(name); err != nil {
					providers.DebugLogf("revoke disabled plugin MCP server %q: %v", name, err)
				}
			}
		}
		if g.systemPrompts != nil {
			g.systemPrompts.RemoveByPlugin(id)
		}
		if g.compactions != nil {
			g.compactions.RemoveByPlugin(id)
		}
		if g.requestTransforms != nil {
			g.requestTransforms.RemoveByPlugin(id)
		}
	}

}

func (g *PluginGeneration) retain() {
	if g == nil {
		return
	}
	for refs := g.refs.Load(); refs >= 0; refs = g.refs.Load() {
		if g.refs.CompareAndSwap(refs, refs+1) {
			return
		}
	}
}

// Release drops one conversation or Session reference. It returns true when
// this call retired the generation. Revocation persistence belongs to the
// Session helper when a live Session is available.
func (g *PluginGeneration) Release() bool {
	if g == nil {
		return false
	}
	for refs := g.refs.Load(); ; refs = g.refs.Load() {
		if refs < 0 {
			return false
		}
		remaining := refs - 1
		if remaining <= 0 {
			remaining = -1
		}
		if g.refs.CompareAndSwap(refs, remaining) {
			if remaining > 0 {
				return false
			}
			break
		}
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

// RetainPluginHost captures the published host only when it registers the
// requested capability, keeping that generation alive while callbacks run.
// The caller must release the returned generation.
func (s *Session) RetainPluginHost(capabilityID string) (*pluginhost.Host, *PluginGeneration) {
	if s == nil {
		return nil, nil
	}
	s.pluginGenerationMu.Lock()
	defer s.pluginGenerationMu.Unlock()
	if s.PluginHost == nil || len(s.PluginHost.Capabilities(capabilityID)) == 0 {
		return nil, nil
	}
	if s.pluginGeneration == nil {
		s.pluginGeneration = s.capturePluginGeneration()
	}
	generation := s.pluginGeneration
	generation.retain()
	return generation.host, generation
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

func (s *Session) releasePluginGeneration(generation *PluginGeneration) error {
	if s == nil || generation == nil || !generation.Release() {
		return nil
	}
	s.pluginGenerationMu.Lock()
	delete(s.retiredPluginGenerations, generation)
	s.pluginGenerationMu.Unlock()
	s.persistRevocationReport(generation)
	return generation.closeErr
}

func (s *Session) capturePluginGeneration() *PluginGeneration {
	var manager *mcp.Manager
	if s.Toolkit != nil {
		manager = s.Toolkit.MCPManager()
	}
	hookSnapshot := hooks.NewDispatcher(nil)
	hookSnapshot.Replace(s.HookDispatcher)
	generationID := newPluginGenerationID(s.WuuHome)
	generation := &PluginGeneration{
		models:      captureGenerationModelBindings(s),
		id:          generationID,
		credentials: s.credentials, configClient: s.configClient, chatGPTManager: s.chatGPTManager, artifactClient: s.artifactClient, mcpOAuth: s.mcpOAuth,
		state:             &runtimeState{documents: s.Documents, sessions: s.Sessions, journal: s.InferenceJournalRuntime},
		settings:          config.Config{Extensions: s.ExtensionSettings},
		plugins:           append([]pluginpkg.Plugin(nil), s.Plugins...),
		active:            append([]pluginpkg.Plugin(nil), s.ActivePlugins...),
		host:              s.PluginHost,
		hooks:             hookSnapshot,
		skills:            append([]skills.Skill(nil), s.Skills...),
		pluginSkills:      s.pluginSkills,
		mcp:               manager,
		mcpBinding:        mcpActivityBindingsFromPlugins(s.ActivePlugins, generationID),
		requestTransforms: buildPluginRequestTransforms(s.PluginHost, s.ProviderName, "", s.RootDir),
		systemPrompts:     s.systemPrompts,
	}
	if s.StreamRunner != nil {
		generation.systemPrompt = s.StreamRunner.SystemPrompt
		generation.compactions = s.StreamRunner.CompactionRegistry
	}
	generation.bindActivityOwnership(s.ActivityRegistry)
	if s.Toolkit != nil {
		s.Toolkit.SetMCPActivityBindings(generation.mcpBinding)
	}
	generation.retain()
	return generation
}

func (s *Session) applyPluginGeneration(generation *PluginGeneration) {
	if generation == nil {
		return
	}
	s.runtimePublicationVersion.Add(1)
	s.advancedPropagationVersion.Add(1)
	s.Plugins = append([]pluginpkg.Plugin(nil), generation.plugins...)
	s.ActivePlugins = append([]pluginpkg.Plugin(nil), generation.active...)
	s.ExtensionSettings = generation.settings.Extensions
	s.PluginHost = generation.host
	s.applyGenerationModels(generation.models)
	if selection := generation.modelSelection; selection != nil && selection.permissions != nil {
		s.Permissions = *selection.permissions
	}
	if loading := generation.modelToolLoading; loading != nil {
		s.ToolLoadingPreference, s.ToolLoadingMode = loading.preference, loading.mode
		s.ToolSearchEnabled, s.NativeDeferredToolDiscovery = loading.search, loading.native
		s.DeferredToolCatalogPrompt = loading.catalog
	}
	s.credentials, s.configClient, s.chatGPTManager, s.artifactClient, s.mcpOAuth = generation.credentials, generation.configClient, generation.chatGPTManager, generation.artifactClient, generation.mcpOAuth
	if generation.state != nil {
		s.Documents, s.Sessions, s.InferenceJournalRuntime = generation.state.documents, generation.state.sessions, generation.state.journal
	} else {
		s.Documents, s.Sessions, s.InferenceJournalRuntime = nil, nil, nil
	}
	s.systemPrompts = generation.systemPrompts
	if s.StreamRunner != nil {
		if generation.tools != nil {
			s.StreamRunner.Tools = generation.tools
			s.Toolkit, s.CodeMode = generation.toolkit, generation.codeMode
			if generation.modelToolLoading != nil {
				s.StreamRunner.NativeDeferredToolDiscovery = generation.modelToolLoading.native
			}
		} else if generation.driverGateways != nil {
			s.StreamRunner.Tools = nil
			s.Toolkit, s.CodeMode = nil, nil
		} else {
			s.StreamRunner.Tools = replacePluginToolBindings(s.StreamRunner.Tools, generation.host, generation.hooks, "", s.RootDir)
		}
		bindPluginCompactionHooks(s.StreamRunner, generation.hooks, "", s.RootDir)
		s.StreamRunner.BeforeModelStep = pluginPreStepInjector(generation.host, s.ProviderName, s.Model, "", s.RootDir)
		s.StreamRunner.StepPolicy = pluginStepPolicy(generation.host, s.ProviderName, s.Model, "", s.RootDir)
		s.StreamRunner.BeforeRequest = pluginRequestInterceptorWithTransforms(generation.host, generation.requestTransforms, s.ProviderName, "", s.RootDir)
		s.StreamRunner.WrapModelClient = pluginModelClientWrapper(generation.host, "")
		s.StreamRunner.CompactionRegistry = generation.compactions
		if generation.driverGateways != nil {
			s.StreamRunner.SelectedPolicy = newSelectedLoopPolicyClient(generation.host)
			s.StreamRunner.SelectedToolBatch = newSelectedToolBatch(generation.host)
			s.StreamRunner.SelectedCompaction = newSelectedCompactionClient(generation.host)
		}
		s.StreamRunner.InferenceJournal = s.InferenceJournalForOwner("workspace-runtime")
		s.StreamRunner.BeginModelReceipts = generation.beginModelReceipts
		// Only a managed composition owns this binding. Embedded runtimes keep
		// the driver explicitly supplied with their runner.
		if generation.driverGateways != nil {
			s.StreamRunner.LoopDriver = resolveLoopDriver(s.DriverProfile, generation.host, generation.driverGateways)
		}
	}
	s.HookDispatcher = generation.hooks
	s.Skills = append([]skills.Skill(nil), generation.skills...)
	s.pluginSkills = generation.pluginSkills
	if s.Toolkit != nil {
		s.Toolkit.SetPluginManager(s.pluginManager(generation, runPluginManagementCommand))
		s.Toolkit.SetSkillsWithAvailability(s.Skills, generation.skillAvailable)
		s.bindLocalSkillRefresh(s.Toolkit, s.RootDir, generation)
		s.Toolkit.SetMCPActivityBindings(generation.mcpBinding)
		s.Toolkit.SetMCPManager(generation.mcp)
		configureToolkitSecurityExtensions(s.Toolkit, generation.host)
	}
	s.BaseSystemPrompt, s.BaseSystemPromptSections = generation.systemPrompt, generation.promptSections
	if s.StreamRunner != nil {
		s.StreamRunner.UpdateSystemPromptWithSections(generation.systemPrompt, agentPromptSections(generation.promptSections))
	}

}

// close retires every generation-owned resource in reverse ownership order
// and records the structured revocation report on the generation. The report
// attributes failures by plugin, resource, and phase; close still returns the
// joined error for callers that only need success or failure.
func (g *PluginGeneration) close() error {
	if g == nil {
		return nil
	}
	g.revocationMu.Lock()
	defer g.revocationMu.Unlock()
	if g.revocation != nil {
		g.finalizeShutdownLocked()
		return g.closeErr
	}
	g.refs.Store(-1)
	g.revokedMu.Lock()
	if g.revokedPlugins == nil {
		g.revokedPlugins = make(map[string]bool)
	}
	for _, item := range g.active {
		g.revokedPlugins[item.ID] = true
	}
	g.revokedMu.Unlock()
	report := &GenerationRevocationReport{GenerationID: g.id, RetiredAt: time.Now().UTC()}
	for _, owner := range g.revokeActivities("") {
		report.record(owner, "activity-control", RevocationPhaseCancelExecutions, nil)
	}
	var err error
	if g.mcp != nil && g.tools == nil {
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
		cancel()
	}
	g.requestTransforms = nil
	g.revocation, g.closeErr = report, err
	if !g.finalizeShutdownLocked() {
		host := g.host
		go func() {
			<-host.ShutdownDone()
			g.revocationMu.Lock()
			defer g.revocationMu.Unlock()
			g.finalizeShutdownLocked()
		}()
	}
	return g.closeErr
}

// The revocation lock serializes immediate, repeated and deferred finalization.
func (g *PluginGeneration) finalizeShutdownLocked() bool {
	if g.host != nil {
		select {
		case <-g.host.ShutdownDone():
		default:
			return false
		}
		if registry := g.host.ServiceRegistry(); registry != nil {
			registry.Close(context.Background())
			g.revocation.record("", "service-registry", RevocationPhaseRevokeServices, nil)
		}
		g.revokedMu.Lock()
		g.host = nil
		g.revokedMu.Unlock()
	}
	for index := len(g.ownedRoots) - 1; index >= 0; index-- {
		rootErr := os.RemoveAll(g.ownedRoots[index])
		g.revocation.recordDetail("", "package-snapshot", RevocationPhaseCleanup, g.ownedRoots[index], rootErr)
		g.closeErr = errors.Join(g.closeErr, rootErr)
	}
	g.ownedRoots = nil
	return true
}

// revocationReport returns the structured retirement record after close.
func (g *PluginGeneration) revocationReport() *GenerationRevocationReport {
	if g == nil {
		return nil
	}
	g.revocationMu.Lock()
	defer g.revocationMu.Unlock()
	return g.revocation
}

// persistRevocationReport retains a retired or rejected generation's
// revocation report so cleanup failures stay visible after the generation is
// gone. Persistence failure is only debug-logged: it must not block the
// lifecycle transition the report describes.
func (s *Session) persistRevocationReport(g *PluginGeneration) {
	if s == nil || g == nil {
		return
	}
	if err := appendGenerationRevocation(s.WuuHome, g.revocationReport()); err != nil {
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

// PluginExecutionSnapshot attributes one host-local execution to its generation.
// The (GenerationID, ID) pair is the diagnostic identity; ID remains unchanged
// for execution-scoped services and cancellation on the owning host.
type PluginExecutionSnapshot struct {
	pluginhost.ExecutionSnapshot
	GenerationID string `json:"generation_id"`
	Retained     bool   `json:"retained"`
}

// PluginExecutionSnapshots returns this Session's current and retained live
// executions, ordered by generation ID and host-local execution ID. It neither
// retains generations nor extends execution leases. Rows are point-in-time
// diagnostics and may finish while the caller consumes the result.
func (s *Session) PluginExecutionSnapshots() []PluginExecutionSnapshot {
	if s == nil {
		return nil
	}
	s.pluginGenerationMu.Lock()
	current := s.pluginGeneration
	generations := make([]*PluginGeneration, 0, len(s.retiredPluginGenerations)+1)
	if current != nil {
		generations = append(generations, current)
	}
	for generation := range s.retiredPluginGenerations {
		if generation != current {
			generations = append(generations, generation)
		}
	}
	s.pluginGenerationMu.Unlock()
	var snapshots []PluginExecutionSnapshot
	for _, generation := range generations {
		// A thread-model shadow can release its last reference without the
		// Session mutex. Serialize only the snapshot with host detachment;
		// never acquire a lifecycle reference or wait for plugin callbacks.
		generation.revokedMu.Lock()
		if generation.refs.Load() > 0 && generation.host != nil {
			for _, execution := range generation.host.ExecutionSnapshots() {
				execution.Detail = append([]byte(nil), execution.Detail...)
				snapshots = append(snapshots, PluginExecutionSnapshot{
					ExecutionSnapshot: execution,
					GenerationID:      generation.id,
					Retained:          generation != current,
				})
			}
		}
		generation.revokedMu.Unlock()
	}
	sort.Slice(snapshots, func(i, j int) bool {
		if snapshots[i].GenerationID != snapshots[j].GenerationID {
			return snapshots[i].GenerationID < snapshots[j].GenerationID
		}
		return snapshots[i].ID < snapshots[j].ID
	})
	return snapshots
}

// snapshotMutableExecutionPackages detaches executable and lazy declarative paths
// from the replaceable installed/development/project catalog. Callers serialize the copy with
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
		if item.Source != "dev" && item.Source != "user" && item.Source != "project" {
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
