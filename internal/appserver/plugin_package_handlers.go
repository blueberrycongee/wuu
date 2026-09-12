package appserver

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/blueberrycongee/wuu/internal/config"
	"github.com/blueberrycongee/wuu/internal/extensions"
	pluginpkg "github.com/blueberrycongee/wuu/internal/plugin"
	"github.com/blueberrycongee/wuu/internal/providers"
	"github.com/blueberrycongee/wuu/internal/session"
	"github.com/blueberrycongee/wuu/internal/statepath"
)

func (s *Server) handlePluginDesktopModuleRead(req Request) error {
	var params PluginDesktopModuleReadParams
	if err := decodeParams(req.Params, &params); err != nil {
		return s.writeResponse(req.ID, nil, err)
	}
	params.ID = strings.TrimSpace(params.ID)
	params.Fingerprint = strings.TrimSpace(params.Fingerprint)
	if params.ID == "" || params.Fingerprint == "" {
		return s.writeResponse(req.ID, nil, errors.New("plugin id and fingerprint are required"))
	}

	var selected *pluginpkg.Plugin
	for index := range s.rt.Plugins {
		if s.rt.Plugins[index].SubjectID == params.ID {
			selected = &s.rt.Plugins[index]
			break
		}
	}
	if selected == nil {
		return s.writeResponse(req.ID, nil, fmt.Errorf("plugin %q is not available in this workspace", params.ID))
	}
	fresh, err := pluginpkg.LoadManifestWithOptions(selected.ManifestPath, pluginpkg.LoadOptions{
		Source:      selected.Source,
		Official:    selected.Official,
		WorkspaceID: selected.WorkspaceID,
	})
	if err != nil {
		return s.writeResponse(req.ID, nil, fmt.Errorf("reload desktop plugin %q: %w", params.ID, err))
	}
	if fresh.SubjectID != params.ID || fresh.Fingerprint != params.Fingerprint {
		return s.writeResponse(req.ID, nil, errors.New("desktop plugin changed; refresh inventory before loading it"))
	}
	// Development authorization is established by the receipt-backed discovery
	// path, not by plugin.json. Preserve that verified state across the required
	// fresh manifest read so inventory and desktop module loading apply the same
	// trust decision.
	fresh.AuthorizedDev = selected.AuthorizedDev

	settings := extensions.Settings{}
	if s.rt.ExtensionSettings != nil {
		settings = *s.rt.ExtensionSettings
	} else if cfg := s.currentExtensionConfig(); cfg.Extensions != nil {
		settings = *cfg.Extensions
	}
	approval, state, _, enabled := pluginPackageInventoryState(settings, fresh)
	approved := approval == ExtensionApprovalGranted || approval == ExtensionApprovalOfficial
	active := state == ExtensionStateGranted || state == ExtensionStateActive
	if !enabled || !approved || !active {
		return s.writeResponse(req.ID, nil, errors.New("desktop plugin is not approved and enabled"))
	}
	if fresh.Desktop == nil {
		return s.writeResponse(req.ID, nil, errors.New("plugin does not declare a desktop entry"))
	}
	source, err := os.ReadFile(filepath.Join(fresh.Root, filepath.FromSlash(fresh.Desktop.Entry)))
	if err != nil {
		return s.writeResponse(req.ID, nil, fmt.Errorf("read desktop plugin entry: %w", err))
	}
	digest := sha256.Sum256(source)
	return s.writeResponse(req.ID, PluginDesktopModuleReadResult{
		ID:          fresh.SubjectID,
		Fingerprint: fresh.Fingerprint,
		Entry:       fresh.Desktop.Entry,
		MediaType:   "text/javascript",
		Digest:      hex.EncodeToString(digest[:]),
		Source:      string(source),
	}, nil)
}

func (s *Server) handlePluginIconRead(req Request) error {
	var params PluginIconReadParams
	if err := decodeParams(req.Params, &params); err != nil {
		return s.writeResponse(req.ID, nil, err)
	}
	params.ID = strings.TrimSpace(params.ID)
	params.Fingerprint = strings.TrimSpace(params.Fingerprint)
	params.Path = filepath.ToSlash(strings.TrimSpace(params.Path))
	if params.ID == "" || params.Fingerprint == "" || params.Path == "" {
		return s.writeResponse(req.ID, nil, errors.New("plugin id, fingerprint, and icon path are required"))
	}

	var selected *pluginpkg.Plugin
	for index := range s.rt.Plugins {
		if s.rt.Plugins[index].SubjectID == params.ID {
			selected = &s.rt.Plugins[index]
			break
		}
	}
	if selected == nil {
		return s.writeResponse(req.ID, nil, fmt.Errorf("plugin %q is not available in this workspace", params.ID))
	}
	fresh, err := pluginpkg.LoadManifestWithOptions(selected.ManifestPath, pluginpkg.LoadOptions{
		Source: selected.Source, Official: selected.Official, WorkspaceID: selected.WorkspaceID,
	})
	if err != nil {
		return s.writeResponse(req.ID, nil, fmt.Errorf("reload plugin icon %q: %w", params.ID, err))
	}
	if fresh.SubjectID != params.ID || fresh.Fingerprint != params.Fingerprint {
		return s.writeResponse(req.ID, nil, errors.New("plugin changed; refresh inventory before loading its icon"))
	}
	declared := false
	check := func(icon *pluginpkg.IconSpec) {
		if icon == nil {
			return
		}
		for _, path := range icon.AssetPaths() {
			if path == params.Path {
				declared = true
			}
		}
	}
	check(fresh.Icon)
	for _, entries := range [][]pluginpkg.ViewEntryContributionSpec{fresh.Navigation, fresh.WorkspaceTools, fresh.SettingsPages} {
		for _, entry := range entries {
			check(entry.Icon)
		}
	}
	if !declared {
		return s.writeResponse(req.ID, nil, errors.New("icon path is not declared by this plugin generation"))
	}
	data, err := os.ReadFile(filepath.Join(fresh.Root, filepath.FromSlash(params.Path)))
	if err != nil {
		return s.writeResponse(req.ID, nil, fmt.Errorf("read plugin icon: %w", err))
	}
	mediaType := ""
	switch strings.ToLower(filepath.Ext(params.Path)) {
	case ".svg":
		mediaType = "image/svg+xml"
	case ".png":
		mediaType = "image/png"
	case ".webp":
		mediaType = "image/webp"
	}
	if mediaType == "" {
		return s.writeResponse(req.ID, nil, errors.New("unsupported plugin icon format"))
	}
	digest := sha256.Sum256(data)
	return s.writeResponse(req.ID, PluginIconReadResult{
		ID: fresh.SubjectID, Fingerprint: fresh.Fingerprint, Path: params.Path,
		MediaType: mediaType, Digest: hex.EncodeToString(digest[:]), Data: data,
	}, nil)
}

func (s *Server) handlePluginPackageInspect(req Request) error {
	var params PluginPackageInspectParams
	if err := decodeParams(req.Params, &params); err != nil {
		return s.writeResponse(req.ID, nil, err)
	}
	inspected, err := pluginpkg.InspectPackage(params.Path)
	if err != nil {
		return s.writeResponse(req.ID, nil, fmt.Errorf("inspect plugin package: %w", err))
	}
	return s.writeResponse(req.ID, PluginPackageInspectResult{
		Package: pluginPackageMetadata(inspected),
	}, nil)
}

func (s *Server) handlePluginPackageInstall(req Request) error {
	var params PluginPackageInstallParams
	if err := decodeParams(req.Params, &params); err != nil {
		return s.writeResponse(req.ID, nil, err)
	}
	releaseMutation, err := s.beginPluginGenerationMutation("install", pluginGenerationMutationCatalog)
	if err != nil {
		return s.writeResponse(req.ID, nil, err)
	}
	defer releaseMutation()

	inspected, err := pluginpkg.InspectPackage(params.Path)
	if err != nil {
		return s.writeResponse(req.ID, nil, fmt.Errorf("install plugin package: %w", err))
	}
	installedPath := filepath.Join(s.rt.WuuHome, "plugins", inspected.ID)
	if _, statErr := os.Lstat(installedPath); statErr == nil {
		pending, err := pluginpkg.StagePackageUpdate(s.rt.WuuHome, params.Path)
		if err != nil {
			return s.writeResponse(req.ID, nil, fmt.Errorf("stage plugin package update: %w", err))
		}
		return s.writeResponse(req.ID, PluginPackageInstallResult{
			Package:            pluginPackageMetadata(pending.Package),
			Pending:            true,
			ActiveFingerprint:  pending.ActiveFingerprint,
			ExtensionInventory: s.currentExtensionInventory(),
			Skills:             skillSummaries(s.rt.Skills),
		}, nil)
	} else if !os.IsNotExist(statErr) {
		return s.writeResponse(req.ID, nil, fmt.Errorf("inspect installed plugin package %q: %w", inspected.ID, statErr))
	}

	installed, err := pluginpkg.InstallPackage(s.rt.WuuHome, params.Path)
	if err != nil {
		return s.writeResponse(req.ID, nil, fmt.Errorf("install plugin package: %w", err))
	}
	inventory, skills, err := s.refreshPluginCatalog()
	if err != nil {
		return s.writeResponse(req.ID, nil, fmt.Errorf("plugin %q was installed, but extension refresh failed: %w", installed.Package.ID, err))
	}
	return s.writeResponse(req.ID, PluginPackageInstallResult{
		Package:            pluginPackageMetadata(installed.Package),
		Replaced:           installed.Replaced,
		Pending:            false,
		ExtensionInventory: inventory,
		Skills:             skills,
	}, nil)
}

func (s *Server) handlePluginPackageRemove(req Request) error {
	var params PluginPackageRemoveParams
	if err := decodeParams(req.Params, &params); err != nil {
		return s.writeResponse(req.ID, nil, err)
	}
	releaseMutation, err := s.beginPluginGenerationMutation("remove", pluginGenerationMutationActivation)
	if err != nil {
		return s.writeResponse(req.ID, nil, err)
	}
	defer releaseMutation()

	params.ID = strings.TrimSpace(params.ID)
	if err := pluginpkg.ValidateInstallID(params.ID); err != nil {
		return s.writeResponse(req.ID, nil, fmt.Errorf("remove plugin package: %w", err))
	}
	var selected *pluginpkg.Plugin
	for index := range s.rt.Plugins {
		item := &s.rt.Plugins[index]
		if item.Source == "user" && item.ID == params.ID {
			selected = item
			break
		}
	}
	if selected == nil {
		return s.writeResponse(req.ID, nil, fmt.Errorf("installed user plugin %q was not found", params.ID))
	}
	pending, pendingErr := pluginpkg.ReadPendingUpdate(s.rt.WuuHome, params.ID)
	if pendingErr != nil && !errors.Is(pendingErr, pluginpkg.ErrPendingUpdateNotFound) {
		return s.writeResponse(req.ID, nil, fmt.Errorf("remove plugin package: inspect pending update: %w", pendingErr))
	}
	configPath, err := statepath.ConfigPath(s.rt.HomeDir)
	if err != nil {
		return s.writeResponse(req.ID, nil, fmt.Errorf("resolve plugin policy path: %w", err))
	}
	preparedSettings := cloneExtensionSettings(s.rt.ExtensionSettings)
	preparedSettings.Revoke(selected.SubjectID)
	cfg := s.currentExtensionConfig()
	cfg.Extensions = &preparedSettings
	candidate, err := s.rt.PreflightPluginRemoval(cfg, params.ID)
	if err != nil {
		return s.writeResponse(req.ID, nil, fmt.Errorf("prepare plugin removal: %w", err))
	}
	var (
		packageRemoval *pluginpkg.UninstallTransaction
		pendingRemoval *pluginpkg.PendingUpdateRemoval
		removed        pluginpkg.UninstallResult
		persisted      extensions.Settings
	)
	rollbackRemoval := func(cause error) error {
		var rollbackErr error
		if pendingRemoval != nil {
			rollbackErr = errors.Join(rollbackErr, pendingRemoval.Rollback())
		}
		if packageRemoval != nil {
			rollbackErr = errors.Join(rollbackErr, packageRemoval.Rollback())
		}
		if rollbackErr != nil {
			return fmt.Errorf("%w (rollback plugin removal: %v)", cause, rollbackErr)
		}
		return cause
	}
	if err := s.rt.ActivatePluginGeneration(candidate, func() error {
		var prepareErr error
		packageRemoval, removed, prepareErr = pluginpkg.PrepareUninstallPackage(s.rt.WuuHome, params.ID)
		if prepareErr != nil {
			return fmt.Errorf("remove plugin package: %w", prepareErr)
		}
		if packageRemoval == nil || !removed.Removed {
			return errors.New("installed plugin disappeared during removal")
		}
		if pendingErr == nil {
			pendingRemoval, prepareErr = pluginpkg.PreparePendingUpdateRemoval(s.rt.WuuHome, params.ID, pending.Package.Fingerprint)
			if prepareErr != nil {
				return rollbackRemoval(fmt.Errorf("remove pending plugin update: %w", prepareErr))
			}
		}
		persisted, prepareErr = config.UpdateExtensionSettings(configPath, func(settings *extensions.Settings) error {
			settings.Revoke(selected.SubjectID)
			return nil
		})
		if prepareErr != nil {
			return rollbackRemoval(fmt.Errorf("clear plugin policy: %w", prepareErr))
		}
		return nil
	}); err != nil {
		return s.writeResponse(req.ID, nil, err)
	}
	s.rt.SetExtensionSettings(&persisted)
	if pendingRemoval != nil {
		if err := pendingRemoval.Commit(); err != nil {
			providers.DebugLogf("finalize pending plugin removal: %v", err)
		}
	}
	if err := packageRemoval.Commit(); err != nil {
		providers.DebugLogf("finalize plugin removal: %v", err)
	} else if err := session.DeletePluginTurnLifecycleOutboxForPlugin(s.rt.SessionDir, removed.ID); err != nil {
		providers.DebugLogf("delete plugin lifecycle outbox for removed plugin %q: %v", removed.ID, err)
	}
	s.schedulePluginTurnLifecycleReplay()
	s.resetThreadRuntimesForGeneralSettings("")
	return s.writeResponse(req.ID, PluginPackageRemoveResult{
		ID:                 removed.ID,
		Removed:            removed.Removed,
		ExtensionInventory: s.currentExtensionInventory(),
		Skills:             skillSummaries(s.rt.Skills),
	}, nil)
}

func (s *Server) handlePendingPluginUpdate(req Request, params ExtensionPackageUpdateParams, selected pluginpkg.Plugin) error {
	if selected.Source != "user" {
		return s.writeResponse(req.ID, nil, errors.New("pending updates can only be managed for installed user plugins"))
	}
	pending, err := pluginpkg.ReadPendingUpdate(s.rt.WuuHome, selected.ID)
	if err != nil {
		return s.writeResponse(req.ID, nil, fmt.Errorf("read pending plugin update: %w", err))
	}
	if strings.TrimSpace(params.Fingerprint) == "" || params.Fingerprint != pending.Package.Fingerprint {
		return s.writeResponse(req.ID, nil, errors.New("pending plugin update changed; refresh inventory before updating it"))
	}
	if params.Action == ExtensionPackageRejectUpdate {
		if err := pluginpkg.RejectPendingUpdate(s.rt.WuuHome, selected.ID, params.Fingerprint); err != nil {
			return s.writeResponse(req.ID, nil, fmt.Errorf("reject pending plugin update: %w", err))
		}
		return s.writeResponse(req.ID, ExtensionPackageUpdateResult{ExtensionInventory: s.currentExtensionInventory()}, nil)
	}
	configPath, err := statepath.ConfigPath(s.rt.HomeDir)
	if err != nil {
		return s.writeResponse(req.ID, nil, fmt.Errorf("resolve plugin update policy path: %w", err))
	}
	previousSettings := cloneExtensionSettings(s.rt.ExtensionSettings)
	preparedSettings := cloneExtensionSettings(s.rt.ExtensionSettings)
	scope := extensions.GrantScopeUser
	if prior, ok := preparedSettings.Grants[selected.SubjectID]; ok && prior.Scope != "" {
		scope = prior.Scope
	}
	grant := extensions.Grant{
		SubjectID:   selected.SubjectID,
		Fingerprint: pending.Package.Fingerprint,
		Scope:       scope,
		Permissions: append([]string(nil), pending.Package.EffectivePermissions...),
		ApprovedAt:  time.Now().UTC(),
	}
	if err := preparedSettings.RecordGrant(grant); err != nil {
		return s.writeResponse(req.ID, nil, fmt.Errorf("prepare plugin update approval: %w", err))
	}
	restorePreviousApproval := func() error {
		_, rollbackErr := config.UpdateExtensionSettings(configPath, func(settings *extensions.Settings) error {
			*settings = cloneExtensionSettings(&previousSettings)
			return nil
		})
		return rollbackErr
	}
	cfg := s.currentExtensionConfig()
	cfg.Extensions = &preparedSettings
	candidate, err := s.rt.PreflightPluginUpdate(cfg, selected.ID, params.Fingerprint, filepath.Join(pending.Path, "package"), pending.Package.ManifestPath)
	if err != nil {
		return s.writeResponse(req.ID, nil, fmt.Errorf("activate pending plugin update: %w", err))
	}
	var settings extensions.Settings
	if err := s.rt.ActivatePluginGeneration(candidate, func() error {
		updated, persistErr := config.UpdateExtensionSettings(configPath, func(settings *extensions.Settings) error {
			return settings.RecordGrant(grant)
		})
		if persistErr != nil {
			return fmt.Errorf("persist plugin update approval: %w", persistErr)
		}
		settings = updated
		_, promoteErr := pluginpkg.PromotePendingUpdate(s.rt.WuuHome, selected.ID, params.Fingerprint)
		if promoteErr != nil {
			if rollbackErr := restorePreviousApproval(); rollbackErr != nil {
				return fmt.Errorf("promote pending plugin update: %w (restore previous plugin approval: %v)", promoteErr, rollbackErr)
			}
			return fmt.Errorf("promote pending plugin update: %w", promoteErr)
		}
		return nil
	}); err != nil {
		return s.writeResponse(req.ID, nil, err)
	}
	s.rt.SetExtensionSettings(&settings)
	s.schedulePluginTurnLifecycleReplay()
	s.resetThreadRuntimesForGeneralSettings("")
	return s.writeResponse(req.ID, ExtensionPackageUpdateResult{ExtensionInventory: s.currentExtensionInventory()}, nil)
}

func cloneExtensionSettings(current *extensions.Settings) extensions.Settings {
	clone := extensions.Settings{
		Grants:   map[string]extensions.Grant{},
		Enabled:  map[string]bool{},
		Disabled: map[string]bool{},
		Rejected: map[string]extensions.PolicyDecision{},
	}
	if current == nil {
		return clone
	}
	for key, grant := range current.Grants {
		grant.Permissions = append([]string(nil), grant.Permissions...)
		clone.Grants[key] = grant
	}
	for key, disabled := range current.Disabled {
		clone.Disabled[key] = disabled
	}
	for key, enabled := range current.Enabled {
		clone.Enabled[key] = enabled
	}
	for key, decision := range current.Rejected {
		clone.Rejected[key] = decision
	}
	return clone
}

// pluginGenerationMutationKind distinguishes catalog-only changes (install,
// stage, validate) from changes that swap the live generation (grant,
// enable, disable, remove). Catalog changes never touch active bindings, so
// they must not be rejected while threads are busy.
type pluginGenerationMutationKind int

const (
	pluginGenerationMutationCatalog pluginGenerationMutationKind = iota
	pluginGenerationMutationActivation
)

func (s *Server) beginPluginGenerationMutation(action string, kind pluginGenerationMutationKind) (func(), error) {
	if s == nil || s.rt == nil {
		return func() {}, errors.New("runtime is not initialized")
	}
	if strings.TrimSpace(s.rt.WuuHome) == "" {
		return func() {}, errors.New("runtime Wuu home is not configured")
	}
	if !s.pluginGenerationMutation.CompareAndSwap(false, true) {
		return func() {}, fmt.Errorf("cannot %s plugin packages while another plugin change is running", action)
	}
	releaseAdmission := func() { s.pluginGenerationMutation.Store(false) }

	if kind == pluginGenerationMutationActivation {
		s.mu.Lock()
		threads := make([]*threadState, 0, len(s.threads))
		for _, th := range s.threads {
			threads = append(threads, th)
		}
		s.mu.Unlock()
		for _, th := range threads {
			if th == nil {
				continue
			}
			th.mu.Lock()
			// Collaboration sessions own an independent execution environment and
			// hold no references to the interactive extension generation.
			busy := th.NamedAgentID == "" && (th.running || th.executionLease != nil || th.admissionReserved || th.runtimeSelectionMutation ||
				(th.execRuntime != nil && threadRuntimeHasOutstandingWork(th.ID, th.execRuntime)))
			th.mu.Unlock()
			if busy {
				releaseAdmission()
				return func() {}, fmt.Errorf("cannot %s plugin packages while a turn is running or background work remains on thread %q", action, th.ID)
			}
		}
	}

	// New turn admission is closed before taking this mutex, so any admission
	// already holding a thread lock can finish its refresh first. Mutations then
	// share the same serialization boundary as the watcher for their complete
	// disk and runtime transaction.
	s.pluginGenerationRefreshMu.Lock()
	releaseLocal := func() {
		s.pluginGenerationRefreshMu.Unlock()
		releaseAdmission()
	}

	// Catalog-only mutations use a lease that is compatible with in-flight
	// executions: installing a package must not block turns. The epoch is
	// advanced on release, after the caller's disk work, so peers only
	// revalidate complete state.
	if kind == pluginGenerationMutationCatalog {
		catalogLease, acquired, err := session.TryAcquirePluginCatalogMutationLease(s.rt.WuuHome)
		if err != nil {
			releaseLocal()
			return func() {}, fmt.Errorf("acquire plugin catalog mutation lease: %w", err)
		}
		if !acquired {
			releaseLocal()
			return func() {}, fmt.Errorf("cannot %s plugin packages while another app-server is changing the plugin catalog", action)
		}
		return func() {
			epoch, advanceErr := catalogLease.Advance()
			if advanceErr != nil {
				providers.DebugLogf("advance plugin catalog generation: %v", advanceErr)
			} else {
				s.pluginGenerationEpoch.Store(epoch)
			}
			if err := catalogLease.Release(); err != nil {
				providers.DebugLogf("release plugin catalog mutation lease: %v", err)
			}
			releaseLocal()
		}, nil
	}

	lease, acquired, err := session.TryAcquirePluginGenerationMutationLease(s.rt.WuuHome)
	if err != nil {
		releaseLocal()
		return func() {}, fmt.Errorf("acquire plugin generation mutation lease: %w", err)
	}
	if !acquired {
		releaseLocal()
		return func() {}, fmt.Errorf("cannot %s plugin packages while another app-server is running a turn or background work", action)
	}
	epoch, err := lease.Advance()
	if err != nil {
		_ = lease.Release()
		releaseLocal()
		return func() {}, fmt.Errorf("advance plugin generation: %w", err)
	}
	return func() {
		if err := lease.Release(); err != nil {
			providers.DebugLogf("release plugin generation mutation lease: %v", err)
		}
		// This server performed the complete serialized transaction and already
		// has its final runtime state, including rollback on failure. Peers still
		// observe the advanced epoch and refresh independently.
		s.pluginGenerationEpoch.Store(epoch)
		releaseLocal()
	}, nil
}

func (s *Server) refreshPluginPackages() ([]ExtensionInventoryRecord, []SkillSummary, error) {
	if err := s.refreshExtensions(s.currentExtensionConfig()); err != nil {
		return nil, nil, err
	}
	s.schedulePluginTurnLifecycleReplay()
	s.resetThreadRuntimesForGeneralSettings("")
	return s.currentExtensionInventory(), skillSummaries(s.rt.Skills), nil
}

// refreshPluginCatalog updates the installed-package inventory without
// touching the active plugin generation, thread runtimes, or any in-flight
// turn. Installation and staging are catalog-only until the user grants or
// enables a package.
func (s *Server) refreshPluginCatalog() ([]ExtensionInventoryRecord, []SkillSummary, error) {
	if err := s.rt.RefreshPluginCatalog(); err != nil {
		return nil, nil, err
	}
	return s.currentExtensionInventory(), skillSummaries(s.rt.Skills), nil
}

func pluginPackageMetadata(item pluginpkg.PackageInspection) PluginPackageMetadata {
	return PluginPackageMetadata{
		ID:                   item.ID,
		Name:                 item.Name,
		Version:              item.Version,
		Description:          item.Description,
		SourceKind:           PluginPackageSourceKind(item.SourceKind),
		ArchiveRoot:          item.ArchiveRoot,
		ManifestPath:         item.ManifestPath,
		FileCount:            item.FileCount,
		UnpackedSize:         item.UnpackedSize,
		Fingerprint:          item.Fingerprint,
		RequestedPermissions: append([]string(nil), item.RequestedPermissions...),
		EffectivePermissions: append([]string(nil), item.EffectivePermissions...),
		UnsupportedFields:    append([]string(nil), item.UnsupportedFields...),
	}
}
