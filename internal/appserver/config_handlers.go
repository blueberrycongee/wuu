package appserver

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/blueberrycongee/wuu/internal/agent"
	"github.com/blueberrycongee/wuu/internal/authstorage"
	"github.com/blueberrycongee/wuu/internal/config"
	"github.com/blueberrycongee/wuu/internal/extensions"
	"github.com/blueberrycongee/wuu/internal/grokbuildspec"
	"github.com/blueberrycongee/wuu/internal/hooks"
	"github.com/blueberrycongee/wuu/internal/modelbudget"
	"github.com/blueberrycongee/wuu/internal/modelcatalog"
	"github.com/blueberrycongee/wuu/internal/modelroles"
	"github.com/blueberrycongee/wuu/internal/modelvariant"
	pluginpkg "github.com/blueberrycongee/wuu/internal/plugin"
	"github.com/blueberrycongee/wuu/internal/pluginhost"
	"github.com/blueberrycongee/wuu/internal/providerfactory"
	"github.com/blueberrycongee/wuu/internal/providers"
	"github.com/blueberrycongee/wuu/internal/providers/codex"
	"github.com/blueberrycongee/wuu/internal/providers/grokbuild"
	"github.com/blueberrycongee/wuu/internal/providers/xaisub"
	"github.com/blueberrycongee/wuu/internal/runtime"
	"github.com/blueberrycongee/wuu/internal/session"
	"github.com/blueberrycongee/wuu/internal/skills"
	"github.com/blueberrycongee/wuu/internal/statepath"
	"github.com/blueberrycongee/wuu/internal/subagent"
	"github.com/blueberrycongee/wuu/internal/version"
)

func (s *Server) handleInitialize(req Request) error {
	var params InitializeParams
	if err := decodeParams(req.Params, &params); err != nil {
		return s.writeResponse(req.ID, nil, err)
	}
	if params.ProtocolVersion != "" && params.ProtocolVersion != ProtocolVersion {
		return s.writeResponse(req.ID, nil, fmt.Errorf("unsupported protocol version %q (server uses %q)", params.ProtocolVersion, ProtocolVersion))
	}
	s.setClientMethods(params.Capabilities.ReverseRPC.Methods)
	s.pinLegacyRuntimeSelections()
	core := version.Info()
	runtimeHost := s.rt.HostInfo()
	modelProfile, toolSurface := s.currentModelSurfaceSummaries()
	status := "ready"
	issues := make([]RuntimeIssue, 0, len(s.rt.ReadinessIssues))
	for _, issue := range s.rt.ReadinessIssues {
		status = "needs_setup"
		issues = append(issues, RuntimeIssue{Code: issue.Code, Provider: issue.Provider, Message: issue.Message})
	}
	return s.writeResponse(req.ID, InitializeResult{
		Status:          status,
		Issues:          issues,
		ProtocolVersion: ProtocolVersion,
		Core: CoreBuildInfo{
			Version: core.Version,
			Commit:  core.Commit,
			Date:    core.Date,
			Dirty:   core.Dirty,
		},
		Provider:    s.rt.ProviderName,
		Model:       s.rt.Model,
		Effort:      s.currentDisplayEffort(),
		Variant:     s.currentVariant(),
		MaxParallel: s.rt.MaxParallel(),
		RuntimeHost: RuntimeHostSummary{
			Kind:       string(runtimeHost.Kind),
			InstanceID: runtimeHost.InstanceID,
		},
		WorkspaceRoot:      s.rt.RootDir,
		Permissions:        s.currentPermissionSummary(),
		ExtensionTrust:     s.currentExtensionTrustSummary(),
		ExtensionInventory: s.currentExtensionInventory(),
		ModelProfile:       modelProfile,
		ToolSurface:        toolSurface,
		ModelRoles:         s.currentModelRoleSummaries(),
		ModelAliases:       s.currentModelAliasSummaries(),
		Providers:          s.providerSummaries(),
		AdvancedSettings:   s.currentAdvancedSettingsSummary(),
		GeneralSettings:    s.currentGeneralSettingsSummary(),
		Features:           FeatureFlags{Browser: s.supportsBrowserClient(), SafeMode: s.rt.SafeMode},
	}, nil)
}

func (s *Server) handleConfigRead(req Request) error {
	modelProfile, toolSurface := s.currentModelSurfaceSummaries()
	return s.writeResponse(req.ID, ConfigReadResult{
		Provider:           s.rt.ProviderName,
		Model:              s.rt.Model,
		Effort:             s.currentDisplayEffort(),
		Variant:            s.currentVariant(),
		MaxParallel:        s.rt.MaxParallel(),
		ConfigPath:         s.rt.ConfigPath,
		WorkspaceRoot:      s.rt.RootDir,
		SessionDir:         s.rt.SessionDir,
		Permissions:        s.currentPermissionSummary(),
		ExtensionTrust:     s.currentExtensionTrustSummary(),
		ExtensionInventory: s.currentExtensionInventory(),
		ModelProfile:       modelProfile,
		ToolSurface:        toolSurface,
		ModelRoles:         s.currentModelRoleSummaries(),
		ModelAliases:       s.currentModelAliasSummaries(),
		Providers:          s.providerSummaries(),
		AdvancedSettings:   s.currentAdvancedSettingsSummary(),
		GeneralSettings:    s.currentGeneralSettingsSummary(),
	}, nil)
}

func (s *Server) currentModelRoleSummaries() []ModelRoleSummary {
	if s == nil || s.rt == nil || s.rt.ModelRoles.Empty() {
		return nil
	}
	selections := s.rt.ModelRoles.List()
	out := make([]ModelRoleSummary, 0, len(selections))
	for _, selection := range selections {
		out = append(out, ModelRoleSummary{
			Role:         string(selection.Role),
			Provider:     selection.Provider,
			Model:        selection.Model,
			APIModel:     selection.APIModel,
			Effort:       selection.Effort,
			Variant:      selection.Variant,
			Inherited:    selection.Inherited,
			Capabilities: selection.Capabilities,
			Behavior:     selection.Behavior,
		})
	}
	return out
}

func (s *Server) currentModelAliasSummaries() map[string]ModelAliasSummary {
	if s == nil || s.rt == nil {
		return nil
	}
	cfg, _, err := s.rt.LoadEffectiveConfig()
	if err != nil {
		return nil
	}
	return modelAliasSummaries(cfg.Agent.ModelAliases)
}

func (s *Server) currentPermissionSummary() PermissionSummary {
	if s == nil || s.rt == nil {
		return PermissionSummary{}
	}
	permissions := s.rt.Permissions
	if strings.TrimSpace(permissions.Mode) == "" {
		permissions = config.ResolvedPermissions{Mode: config.PermissionModeStandard}
	}
	return PermissionSummary{
		Mode: strings.TrimSpace(permissions.Mode),
	}
}

func (s *Server) currentAdvancedSettingsSummary() AdvancedSettingsSummary {
	if s == nil || s.rt == nil {
		return AdvancedSettingsSummary{}
	}
	summary := AdvancedSettingsSummary{}
	if s.rt.StreamRunner != nil {
		summary.MaxSteps = s.rt.StreamRunner.MaxSteps
		summary.Temperature = s.rt.StreamRunner.Temperature
		summary.CompactThresholdPct = s.rt.StreamRunner.CompactThresholdPct
		summary.CompactKeepRecentTokens = s.rt.StreamRunner.CompactKeepRecentTokens
		summary.DisableAutoCompact = s.rt.StreamRunner.DisableAutoCompact
	}
	if cfg, _, err := s.rt.LoadEffectiveConfig(); err == nil {
		summary.MaxSteps = cfg.Agent.MaxSteps
		summary.MaxContextTokens = cfg.Agent.MaxContextTokens
		summary.Temperature = cfg.Agent.Temperature
		summary.CompactThresholdPct = cfg.Agent.CompactThresholdPct
		summary.CompactKeepRecentTokens = cfg.Agent.CompactKeepRecentTokens
		summary.DisableAutoCompact = cfg.Agent.DisableAutoCompact
		if provider, _, err := cfg.ResolveProvider(s.rt.ProviderName); err == nil {
			summary.ProviderContextWindow = provider.ContextWindow
		}
	}
	budget := s.rt.ModelBudget
	contextWindowTokens, contextWindowSource := budget.EffectiveContextWindow()
	summary.ContextWindowTokens = contextWindowTokens
	summary.ContextWindowSource = string(contextWindowSource)
	summary.InputLimitTokens = budget.InputLimitTokens
	summary.OutputReserveTokens = budget.OutputReserveTokens
	summary.CompactThresholdTokens = advancedCompactThresholdTokens(budget, summary.CompactThresholdPct)
	return summary
}

func (s *Server) currentGeneralSettingsSummary() GeneralSettingsSummary {
	if s == nil || s.rt == nil {
		return GeneralSettingsSummary{}
	}
	summary := GeneralSettingsSummary{
		MCPServerEnabled: map[string]bool{},
	}
	if cfg, _, err := s.rt.LoadEffectiveConfig(); err == nil {
		summary.GitAttributionEnabled = cfg.Agent.GitAttributionEnabledValue()
		activePluginServers := make(map[string]bool)
		for _, item := range s.rt.Plugins {
			for name := range item.MCPServers {
				activePluginServers[runtime.PluginMCPServerName(item.ID, name)] = true
			}
		}
		for name, server := range cfg.MCPServers {
			if runtime.IsPluginMCPServerName(name) && !activePluginServers[name] {
				continue
			}
			if server.Enabled == nil || *server.Enabled {
				summary.MCPServerEnabled[name] = true
			} else {
				summary.MCPServerEnabled[name] = false
			}
		}
	}
	return summary
}

func advancedCompactThresholdTokens(budget modelbudget.Budget, pct float64) int {
	if pct <= 0 || pct >= 1 {
		return budget.CompactThresholdTokens
	}
	baseWindow := budget.ContextWindowTokens
	if baseWindow <= 0 || (budget.InputLimitTokens > 0 && budget.InputLimitTokens < baseWindow) {
		baseWindow = budget.InputLimitTokens
	}
	if baseWindow <= 0 {
		return 0
	}
	threshold := int(float64(baseWindow) * pct)
	if budget.ContextWindowTokens > 0 {
		outputReserved := budget.ContextWindowTokens - budget.OutputReserveTokens
		if outputReserved > 0 && outputReserved < threshold {
			threshold = outputReserved
		}
	}
	return threshold
}

func (s *Server) currentModelSurfaceSummaries() (*ModelProfileSummary, *ToolSurfaceSummary) {
	if s == nil || s.rt == nil || s.rt.Toolkit == nil {
		return nil, nil
	}
	surface := s.rt.Toolkit.ActiveSurface()
	if surface.ProfileName == "" {
		return nil, nil
	}
	summary := surface.Summarize()
	profile := &ModelProfileSummary{
		ProfileName:   summary.ProfileName,
		Provider:      summary.Provider,
		Model:         summary.Model,
		EditPrimitive: summary.EditPrimitive,
		BashFirst:     summary.BashFirst,
	}
	return profile, &summary
}

func (s *Server) currentExtensionTrustSummary() ExtensionTrustSummary {
	main := ExtensionSessionTrustSummary{}
	if s != nil && s.rt != nil {
		mcpKnownTools := 0
		if s.rt.Toolkit != nil {
			if manager := s.rt.Toolkit.MCPManager(); manager != nil {
				mcpKnownTools = len(manager.AllTools())
			}
		}
		main.MCP = extensionSurfaceSummary(mcpKnownTools > 0, mcpKnownTools)
		main.Skills = extensionSurfaceSummary(len(s.rt.Skills) > 0, len(s.rt.Skills))
		main.Hooks = ExtensionSurfaceTrustSummary{Allowed: s.rt.HookDispatcher != nil, Active: hookDispatcherHasAny(s.rt.HookDispatcher)}
		main.Plugins = ExtensionSurfaceTrustSummary{Allowed: true, Active: len(s.rt.ActivePlugins) > 0, Count: len(s.rt.ActivePlugins)}
		main.ExternalTools = main.MCP
	}
	reviewer := ExtensionSessionTrustSummary{
		MCP:           ExtensionSurfaceTrustSummary{Allowed: false, Active: false},
		Hooks:         ExtensionSurfaceTrustSummary{Allowed: false, Active: false},
		Plugins:       ExtensionSurfaceTrustSummary{Allowed: false, Active: false},
		Skills:        ExtensionSurfaceTrustSummary{Allowed: false, Active: false},
		Workflows:     ExtensionSurfaceTrustSummary{Allowed: false, Active: false},
		ExternalTools: ExtensionSurfaceTrustSummary{Allowed: false, Active: false},
	}
	return ExtensionTrustSummary{
		MainSession:     main,
		ReviewerSession: reviewer,
	}
}

func (s *Server) currentExtensionInventory() []ExtensionInventoryRecord {
	if s == nil || s.rt == nil {
		return nil
	}
	cfg := s.currentExtensionConfig()
	grants := extensions.Settings{}
	if cfg.Extensions != nil {
		grants = *cfg.Extensions
	}
	if s.rt.ExtensionSettings != nil {
		grants = *s.rt.ExtensionSettings
	}
	pluginsByID := make(map[string]struct {
		scope    string
		official bool
	}, len(s.rt.Plugins))
	for _, item := range s.rt.Plugins {
		pluginsByID[item.ID] = struct {
			scope    string
			official bool
		}{scope: normalizedExtensionScope(item.Source, item.ManifestPath, s.rt.RootDir), official: item.Official}
	}
	activePluginSubjects := make(map[string]struct{}, len(s.rt.ActivePlugins))
	for _, item := range s.rt.ActivePlugins {
		activePluginSubjects[item.SubjectID] = struct{}{}
	}
	runtimeStatuses := make(map[string]pluginhost.Status)
	if s.rt.PluginHost != nil {
		for _, status := range s.rt.PluginHost.Statuses() {
			runtimeStatuses[status.ID] = status
		}
	}
	pendingUpdatesByID := map[string]pluginpkg.PendingUpdate{}
	if pendingUpdates, err := pluginpkg.ListPendingUpdates(s.rt.WuuHome); err == nil {
		for _, pending := range pendingUpdates {
			pendingUpdatesByID[pending.Package.ID] = pending
		}
	}
	packageActivationIssues := make(map[string][]pluginpkg.ActivationIssue)
	if plan, err := runtime.ResolvePluginActivationPlan(cfg, s.rt.Plugins); err == nil {
		packageActivationIssues = plan.Issues
	}

	records := make([]ExtensionInventoryRecord, 0, len(s.rt.Skills)+len(s.rt.Plugins))
	for _, skill := range s.rt.Skills {
		source := strings.TrimSpace(skill.Source)
		scope := normalizedExtensionScope(source, skill.Path, s.rt.RootDir)
		pluginID := strings.TrimPrefix(source, "plugin:")
		if pluginID == source {
			pluginID = ""
		} else if plugin, ok := pluginsByID[pluginID]; ok {
			scope = plugin.scope
		}
		records = append(records, ExtensionInventoryRecord{
			ID:   extensionSubjectID("skill", source, skill.Name),
			Name: skill.Name,
			Kind: extensions.KindSkill,
			Provenance: extensions.Provenance{
				Kind:     extensions.KindSkill,
				Source:   source,
				Scope:    scope,
				Path:     skill.Path,
				PluginID: pluginID,
				Official: strings.EqualFold(source, "bundled"),
			},
			State: ExtensionStateActive,
		})
	}
	for _, item := range s.rt.Plugins {
		scope := normalizedExtensionScope(item.Source, item.ManifestPath, s.rt.RootDir)
		pluginSource := pluginManifestSource(item.ManifestPath)
		if item.SubjectID == "" || item.Fingerprint == "" {
			if contract, err := item.PackageContract(); err == nil {
				item.SubjectID = contract.SubjectID
				item.Fingerprint = contract.Fingerprint
				item.EffectivePermissions = contract.Permissions
			}
		}
		if item.SubjectID == "" {
			item.SubjectID = extensions.SubjectID(scope, item.ID)
		}
		approval, state, grantScope, enabled := pluginPackageInventoryState(grants, item)
		runtimeState := ExtensionRuntimeInactive
		lastError := ""
		if _, ok := activePluginSubjects[item.SubjectID]; ok {
			runtimeState = ExtensionRuntimeActive
		}
		if status, ok := runtimeStatuses[item.ID]; ok {
			switch status.State {
			case pluginhost.StateStarting:
				runtimeState = ExtensionRuntimeStarting
			case pluginhost.StateActive:
				runtimeState = ExtensionRuntimeActive
			case pluginhost.StateFailed:
				runtimeState = ExtensionRuntimeFailed
			case pluginhost.StateStopped:
				runtimeState = ExtensionRuntimeStopped
			}
			lastError = status.Error
		}
		commands := make([]ExtensionCommandDescriptor, 0, len(item.Commands))
		for _, command := range item.Commands {
			template := command.Prompt
			if command.ResolvedPrompt != nil {
				template = command.ResolvedPrompt.Text
			}
			commands = append(commands, ExtensionCommandDescriptor{
				ID:          command.ID,
				Title:       command.Title,
				Description: command.Description,
				Kind:        ExtensionCommandKind(command.Kind),
				Template:    template,
				Contexts:    append([]string(nil), command.Contexts...),
				Aliases:     append([]string(nil), command.Aliases...),
				Keywords:    append([]string(nil), command.Keywords...),
			})
		}
		sort.Slice(commands, func(i, j int) bool { return commands[i].ID < commands[j].ID })
		themes := make([]ExtensionThemeDescriptor, 0, len(item.Themes))
		for _, theme := range item.Themes {
			themes = append(themes, ExtensionThemeDescriptor{
				ID:     theme.ID,
				Name:   theme.Name,
				Base:   theme.Base,
				Tokens: cloneStringMap(theme.Tokens),
				Syntax: cloneStringMap(theme.Syntax),
			})
		}
		sort.Slice(themes, func(i, j int) bool { return themes[i].ID < themes[j].ID })
		settingIDs := sortedMapKeys(item.Settings)
		settings := make([]ExtensionSettingDescriptor, 0, len(settingIDs))
		for _, id := range settingIDs {
			definition := item.Settings[id]
			settings = append(settings, ExtensionSettingDescriptor{
				ID:          id,
				Type:        ExtensionSettingType(definition.Type),
				Title:       definition.Title,
				Description: definition.Description,
				Default:     definition.Default,
				Enum:        append([]string(nil), definition.Enum...),
				Scope:       ExtensionSettingScope(definition.Scope),
				Apply:       ExtensionSettingApplyMode(definition.Apply),
			})
		}
		slots := make([]ExtensionSlotContributionDescriptor, 0, len(item.Slots))
		for _, contribution := range item.Slots {
			slots = append(slots, ExtensionSlotContributionDescriptor{
				ID: contribution.ID, Target: contribution.Target, Order: contribution.Order, Title: contribution.Title,
			})
		}
		surfaces := make([]ExtensionSurfaceContributionDescriptor, 0, len(item.Surfaces))
		for _, contribution := range item.Surfaces {
			surfaces = append(surfaces, ExtensionSurfaceContributionDescriptor{
				ID: contribution.ID, Target: contribution.Target, Mode: string(contribution.Mode), Order: contribution.Order, Title: contribution.Title,
			})
		}
		presenters := make([]ExtensionPresenterContributionDescriptor, 0, len(item.Presenters))
		for _, contribution := range item.Presenters {
			presenters = append(presenters, ExtensionPresenterContributionDescriptor{
				ID: contribution.ID, Target: contribution.Target, Mode: string(contribution.Mode), Priority: contribution.Priority, Title: contribution.Title,
			})
		}
		viewEntries := func(items []pluginpkg.ViewEntryContributionSpec) []ExtensionViewEntryDescriptor {
			entries := make([]ExtensionViewEntryDescriptor, 0, len(items))
			for _, contribution := range items {
				entries = append(entries, ExtensionViewEntryDescriptor{
					ID: contribution.ID, View: contribution.View, Title: contribution.Title,
					Description: contribution.Description, Icon: extensionIconDescriptor(contribution.Icon), Order: contribution.Order,
				})
			}
			return entries
		}
		navigation := viewEntries(item.Navigation)
		workspaceTools := viewEntries(item.WorkspaceTools)
		settingsPages := viewEntries(item.SettingsPages)
		packageRecord := ExtensionInventoryRecord{
			ID:          item.SubjectID,
			Name:        item.ID,
			Description: item.Description,
			Icon:        extensionIconDescriptor(item.Icon),
			Kind:        extensions.KindPlugin,
			Provenance: extensions.Provenance{
				Kind:     extensions.KindPlugin,
				Source:   pluginSource,
				Scope:    scope,
				Path:     item.ManifestPath,
				PluginID: item.ID,
				Official: item.Official,
			},
			State:                state,
			Executable:           item.Runtime != nil || len(item.MCPServers) > 0 || len(item.Hooks) > 0,
			Fingerprint:          item.Fingerprint,
			PackageSource:        item.Source,
			GrantScope:           grantScope,
			RequestedPermissions: cloneSortedStrings(item.EffectivePermissions),
			UnsupportedFields:    cloneSortedStrings(item.UnsupportedFields),
			ApprovalState:        approval,
			RuntimeState:         runtimeState,
			LastError:            lastError,
			Requires:             cloneSortedStrings(item.Requires),
			Breaks:               cloneSortedStrings(item.Breaks),
			Conflicts:            cloneSortedStrings(item.Conflicts),
			Enabled:              &enabled,
		}
		for _, issue := range packageActivationIssues[item.ID] {
			packageRecord.ActivationIssues = append(packageRecord.ActivationIssues, ExtensionPluginActivationIssue{
				Kind:            string(issue.Kind),
				RelatedPluginID: issue.RelatedPluginID,
			})
		}
		if item.Desktop != nil {
			packageRecord.Desktop = &ExtensionDesktopDescriptor{Entry: item.Desktop.Entry}
		}
		if len(commands) > 0 || len(themes) > 0 || len(settings) > 0 || len(slots) > 0 || len(surfaces) > 0 || len(presenters) > 0 || len(navigation) > 0 || len(workspaceTools) > 0 || len(settingsPages) > 0 {
			packageRecord.Contributions = &ExtensionContributions{
				Commands: commands, Themes: themes, Settings: settings,
				Slots: slots, Surfaces: surfaces, Presenters: presenters,
				Navigation: navigation, WorkspaceTools: workspaceTools, SettingsPages: settingsPages,
			}
		}
		if pending, ok := pendingUpdatesByID[item.ID]; ok && item.Source == "user" {
			packageRecord.PendingUpdate = &ExtensionPendingUpdate{
				Version:              pending.Package.Version,
				Fingerprint:          pending.Package.Fingerprint,
				ActiveFingerprint:    pending.ActiveFingerprint,
				RequestedPermissions: cloneSortedStrings(pending.Package.RequestedPermissions),
				EffectivePermissions: cloneSortedStrings(pending.Package.EffectivePermissions),
			}
		}
		records = append(records, packageRecord)

		mcpNames := sortedMapKeys(item.MCPServers)
		for _, name := range mcpNames {
			server := item.MCPServers[name]
			if override, ok := cfg.MCPServers[runtime.PluginMCPServerName(item.ID, name)]; ok && override.Enabled != nil {
				server.Enabled = override.Enabled
			}
			permissions := executablePermissions(item.RequestedPermissions, server.Command != "", server.URL != "", false)
			subjectID := extensionSubjectID("mcp", "plugin", item.ID, name)
			fingerprint, _ := extensions.Fingerprint(extensions.ExecutableSpec{
				Command:     server.Command,
				Args:        server.Args,
				URL:         server.URL,
				Env:         server.Env,
				Headers:     server.Headers,
				Permissions: permissions,
			})
			state, grantScope := executableExtensionState(grants, subjectID, fingerprint, server.Enabled, item.Official)
			records = append(records, ExtensionInventoryRecord{
				ID:                   subjectID,
				Name:                 name,
				Kind:                 extensions.KindMCP,
				Provenance:           extensions.Provenance{Kind: extensions.KindMCP, Source: "plugin:" + item.ID, Scope: scope, Path: item.ManifestPath, PluginID: item.ID, Official: item.Official},
				State:                state,
				Executable:           true,
				Fingerprint:          fingerprint,
				GrantScope:           grantScope,
				RequestedPermissions: permissions,
			})
		}
		appendHookInventory := func(event string, index int, entry config.HookEntry) {
			permissions := executablePermissions(item.RequestedPermissions, entry.Command != "", false, strings.EqualFold(strings.TrimSpace(entry.Type), "prompt"))
			subjectID := extensionSubjectID("hook", "plugin", item.ID, event, strconv.Itoa(index))
			fingerprint := hookFingerprint(event, index, entry, permissions)
			state, grantScope := executableExtensionState(grants, subjectID, fingerprint, nil, item.Official)
			records = append(records, ExtensionInventoryRecord{
				ID:                   subjectID,
				Name:                 event,
				Kind:                 extensions.KindHook,
				Provenance:           extensions.Provenance{Kind: extensions.KindHook, Source: "plugin:" + item.ID, Scope: scope, Path: item.ManifestPath, PluginID: item.ID, Official: item.Official},
				State:                state,
				Executable:           true,
				Fingerprint:          fingerprint,
				GrantScope:           grantScope,
				RequestedPermissions: permissions,
			})
		}
		for _, event := range sortedMapKeys(item.Hooks) {
			for index, entry := range item.Hooks[event] {
				appendHookInventory(event, index, entry)
			}
		}
	}

	for _, name := range sortedMapKeys(cfg.MCPServers) {
		if runtime.IsPluginMCPServerName(name) {
			continue
		}
		server := cfg.MCPServers[name]
		permissions := executablePermissions(nil, server.Command != "", server.URL != "", false)
		subjectID := extensionSubjectID("mcp", "config", name)
		fingerprint, _ := extensions.Fingerprint(extensions.ExecutableSpec{
			Command: server.Command, Args: server.Args, URL: server.URL, Env: server.Env, Headers: server.Headers, Permissions: permissions,
		})
		state, grantScope := executableExtensionState(grants, subjectID, fingerprint, server.Enabled, false)
		records = append(records, ExtensionInventoryRecord{
			ID: subjectID, Name: name, Kind: extensions.KindMCP,
			Provenance: extensions.Provenance{Kind: extensions.KindMCP, Source: "wuu_config", Scope: "project", Path: s.rt.ConfigPath},
			State:      state, Executable: true, Fingerprint: fingerprint, GrantScope: grantScope, RequestedPermissions: permissions,
		})
	}
	for _, event := range sortedMapKeys(cfg.Hooks) {
		for index, entry := range cfg.Hooks[event] {
			permissions := executablePermissions(nil, entry.Command != "", false, strings.EqualFold(strings.TrimSpace(entry.Type), "prompt"))
			subjectID := extensionSubjectID("hook", "config", event, strconv.Itoa(index))
			fingerprint := hookFingerprint(event, index, entry, permissions)
			state, grantScope := executableExtensionState(grants, subjectID, fingerprint, nil, false)
			records = append(records, ExtensionInventoryRecord{
				ID: subjectID, Name: event, Kind: extensions.KindHook,
				Provenance: extensions.Provenance{Kind: extensions.KindHook, Source: "wuu_config", Scope: "project", Path: s.rt.ConfigPath},
				State:      state, Executable: true, Fingerprint: fingerprint, GrantScope: grantScope, RequestedPermissions: permissions,
			})
		}
	}

	sort.Slice(records, func(i, j int) bool {
		if records[i].Kind != records[j].Kind {
			return records[i].Kind < records[j].Kind
		}
		if records[i].Name != records[j].Name {
			return records[i].Name < records[j].Name
		}
		return records[i].ID < records[j].ID
	})
	return records
}

func extensionIconDescriptor(icon *pluginpkg.IconSpec) *ExtensionIconDescriptor {
	if icon == nil {
		return nil
	}
	return &ExtensionIconDescriptor{
		Name: icon.Name, Path: icon.Path, Light: icon.Light, Dark: icon.Dark,
	}
}

func (s *Server) currentExtensionConfig() config.Config {
	if s == nil || s.rt == nil {
		return config.Config{}
	}
	if cfg, _, err := s.rt.LoadEffectiveConfig(); err == nil {
		return cfg
	}
	return config.Config{}
}

func pluginPackageInventoryState(settings extensions.Settings, item pluginpkg.Plugin) (ExtensionApprovalState, ExtensionState, extensions.GrantScope, bool) {
	enabled := settings.IsEnabled(item.SubjectID, item.EnabledByDefault())
	if item.AuthorizedDev {
		if !enabled {
			return ExtensionApprovalGranted, ExtensionStateRejected, "", false
		}
		return ExtensionApprovalGranted, ExtensionStateActive, "", true
	}
	if item.Official {
		if !enabled {
			return ExtensionApprovalOfficial, ExtensionStateRejected, "", false
		}
		return ExtensionApprovalOfficial, ExtensionStateActive, "", true
	}
	if settings.IsRejected(item.SubjectID, item.Fingerprint) {
		return ExtensionApprovalRejected, ExtensionStateRejected, "", enabled
	}
	if grant, ok := settings.FindGrant(item.SubjectID, item.Fingerprint); ok && inventoryPermissionSetContains(grant.Permissions, item.EffectivePermissions) {
		if !enabled {
			return ExtensionApprovalGranted, ExtensionStateRejected, grant.Scope, false
		}
		return ExtensionApprovalGranted, ExtensionStateGranted, grant.Scope, true
	}
	if settingsHasSubjectGrant(settings, item.SubjectID) {
		return ExtensionApprovalChanged, ExtensionStateChanged, "", enabled
	}
	return ExtensionApprovalPending, ExtensionStatePending, "", enabled
}

func settingsHasSubjectGrant(settings extensions.Settings, subjectID string) bool {
	for _, grant := range settings.Grants {
		if grant.SubjectID == subjectID {
			return true
		}
	}
	return false
}

func inventoryPermissionSetContains(granted, required []string) bool {
	set := make(map[string]struct{}, len(granted))
	for _, permission := range granted {
		set[strings.TrimSpace(permission)] = struct{}{}
	}
	for _, permission := range required {
		if _, ok := set[strings.TrimSpace(permission)]; !ok {
			return false
		}
	}
	return true
}

func executableExtensionState(grants extensions.Settings, subjectID, fingerprint string, enabled *bool, official bool) (ExtensionState, extensions.GrantScope) {
	if enabled != nil && !*enabled {
		return ExtensionStateRejected, ""
	}
	if official {
		return ExtensionStateGranted, ""
	}
	if grant, ok := grants.FindGrant(subjectID, fingerprint); ok {
		return ExtensionStateGranted, grant.Scope
	}
	if _, ok := grants.Grants[subjectID]; ok {
		return ExtensionStateChanged, ""
	}
	return ExtensionStatePending, ""
}

func hookFingerprint(event string, index int, entry config.HookEntry, permissions []string) string {
	fingerprint, _ := extensions.Fingerprint(extensions.ExecutableSpec{
		Command:     entry.Command,
		Args:        []string{event, strconv.Itoa(index), entry.Matcher, entry.Type, entry.Prompt, entry.Model, strconv.Itoa(entry.Timeout)},
		Permissions: permissions,
	})
	return fingerprint
}

func executablePermissions(declared []string, process, network, model bool) []string {
	permissions := append([]string(nil), declared...)
	if process {
		permissions = append(permissions, "process.spawn")
	}
	if network {
		permissions = append(permissions, "network.connect")
	}
	if model {
		permissions = append(permissions, "model.invoke")
	}
	return cloneSortedStrings(permissions)
}

func normalizedExtensionScope(source, path, projectRoot string) string {
	if strings.EqualFold(strings.TrimSpace(source), "project") {
		return "project"
	}
	if strings.EqualFold(strings.TrimSpace(source), "bundled") {
		return "bundled"
	}
	if path != "" && projectRoot != "" {
		if rel, err := filepath.Rel(projectRoot, path); err == nil && rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
			return "project"
		}
	}
	return "user"
}

func pluginManifestSource(path string) string {
	slashPath := filepath.ToSlash(path)
	switch {
	case strings.Contains(slashPath, "/.codex-plugin/"):
		return "codex"
	case strings.Contains(slashPath, "/.claude-plugin/"):
		return "claude"
	default:
		return "wuu"
	}
}

func extensionSubjectID(parts ...string) string {
	cleaned := make([]string, 0, len(parts))
	for _, part := range parts {
		part = strings.TrimSpace(part)
		if part != "" {
			cleaned = append(cleaned, part)
		}
	}
	return strings.Join(cleaned, ":")
}

func cloneSortedStrings(values []string) []string {
	seen := make(map[string]struct{}, len(values))
	out := make([]string, 0, len(values))
	for _, value := range values {
		value = strings.TrimSpace(value)
		if value == "" {
			continue
		}
		if _, ok := seen[value]; ok {
			continue
		}
		seen[value] = struct{}{}
		out = append(out, value)
	}
	sort.Strings(out)
	if len(out) == 0 {
		return nil
	}
	return out
}

func cloneStringMap(values map[string]string) map[string]string {
	if len(values) == 0 {
		return nil
	}
	out := make(map[string]string, len(values))
	for key, value := range values {
		out[key] = value
	}
	return out
}

func sortedMapKeys[V any](values map[string]V) []string {
	keys := make([]string, 0, len(values))
	for key := range values {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	return keys
}

func extensionSurfaceSummary(active bool, count int) ExtensionSurfaceTrustSummary {
	return ExtensionSurfaceTrustSummary{
		Allowed:      true,
		Active:       active,
		Count:        count,
		KnownTools:   count,
		VisibleTools: count,
	}
}

func hookDispatcherHasAny(dispatcher *hooks.Dispatcher) bool {
	if dispatcher == nil {
		return false
	}
	for _, event := range hooks.AllEvents() {
		if dispatcher.HasHooks(event) {
			return true
		}
	}
	return false
}

func (s *Server) handleExtensionPackageUpdate(req Request) error {
	var params ExtensionPackageUpdateParams
	if err := decodeParams(req.Params, &params); err != nil {
		return s.writeResponse(req.ID, nil, err)
	}
	if s.rt == nil {
		return s.writeResponse(req.ID, nil, errors.New("runtime is not initialized"))
	}
	releaseMutation, err := s.beginPluginGenerationMutation("change", pluginGenerationMutationActivation)
	if err != nil {
		return s.writeResponse(req.ID, nil, err)
	}
	defer releaseMutation()
	var selected *pluginpkg.Plugin
	for index := range s.rt.Plugins {
		if s.rt.Plugins[index].SubjectID == strings.TrimSpace(params.ID) {
			selected = &s.rt.Plugins[index]
			break
		}
	}
	if selected == nil {
		return s.writeResponse(req.ID, nil, fmt.Errorf("extension package %q was not found", params.ID))
	}
	if params.Action == ExtensionPackagePromoteUpdate || params.Action == ExtensionPackageRejectUpdate {
		return s.handlePendingPluginUpdate(req, params, *selected)
	}
	providedFingerprint := strings.TrimSpace(params.Fingerprint)
	if providedFingerprint != "" && providedFingerprint != selected.Fingerprint {
		return s.writeResponse(req.ID, nil, errors.New("extension package changed; refresh inventory before updating policy"))
	}
	if (params.Action == ExtensionPackageGrant || params.Action == ExtensionPackageReject) && providedFingerprint == "" {
		return s.writeResponse(req.ID, nil, errors.New("fingerprint is required for grant and reject actions"))
	}
	if selected.Official && (params.Action == ExtensionPackageGrant || params.Action == ExtensionPackageReject) {
		return s.writeResponse(req.ID, nil, errors.New("official bundled packages cannot be granted or rejected"))
	}

	userConfigPath, err := statepath.ConfigPath(s.rt.HomeDir)
	if err != nil {
		return s.writeResponse(req.ID, nil, fmt.Errorf("resolve user config: %w", err))
	}
	approvedAt := time.Now().UTC()
	preparedSettings := cloneExtensionSettings(s.rt.ExtensionSettings)
	if err := applyExtensionPackageAction(&preparedSettings, params.Action, *selected, approvedAt); err != nil {
		return s.writeResponse(req.ID, nil, err)
	}
	cfg := s.currentExtensionConfig()
	cfg.Extensions = &preparedSettings
	candidate, err := s.rt.PreflightExtensionPolicy(cfg)
	if err != nil {
		return s.writeResponse(req.ID, nil, err)
	}
	var persistedSettings extensions.Settings
	if err := s.rt.ActivatePluginGeneration(candidate, func() error {
		updated, updateErr := config.UpdateExtensionSettings(userConfigPath, func(settings *extensions.Settings) error {
			return applyExtensionPackageAction(settings, params.Action, *selected, approvedAt)
		})
		if updateErr != nil {
			return fmt.Errorf("persist extension policy: %w", updateErr)
		}
		persistedSettings = updated
		return nil
	}); err != nil {
		return s.writeResponse(req.ID, nil, err)
	}
	s.rt.SetExtensionSettings(&persistedSettings)
	s.schedulePluginTurnLifecycleReplay()
	s.resetThreadRuntimesForGeneralSettings("")
	return s.writeResponse(req.ID, ExtensionPackageUpdateResult{ExtensionInventory: s.currentExtensionInventory()}, nil)
}

func applyExtensionPackageAction(settings *extensions.Settings, action ExtensionPackageAction, selected pluginpkg.Plugin, approvedAt time.Time) error {
	if settings == nil {
		return errors.New("extension settings are required")
	}
	switch action {
	case ExtensionPackageGrant:
		if err := settings.RecordGrant(extensions.Grant{
			SubjectID:   selected.SubjectID,
			Fingerprint: selected.Fingerprint,
			Scope:       extensions.GrantScopeProject,
			Permissions: append([]string(nil), selected.EffectivePermissions...),
			ApprovedAt:  approvedAt,
		}); err != nil {
			return err
		}
		// Approve and enable are the same user gesture in the catalog. Record
		// the explicit enable here so default-disabled packages do not require a
		// second "enable" click after approval.
		settings.SetEnabled(selected.SubjectID, true)
		return nil
	case ExtensionPackageReject:
		return settings.RecordRejection(selected.SubjectID, selected.Fingerprint)
	case ExtensionPackageRevoke:
		settings.Revoke(selected.SubjectID)
	case ExtensionPackageEnable:
		settings.SetEnabled(selected.SubjectID, true)
	case ExtensionPackageDisable:
		settings.SetEnabled(selected.SubjectID, false)
	default:
		return fmt.Errorf("unsupported extension package action %q", action)
	}
	return nil
}

func (s *Server) handleExtensionCatalogRefresh(req Request) error {
	if s.rt == nil {
		return s.writeResponse(req.ID, nil, errors.New("runtime is not initialized"))
	}
	releaseMutation, err := s.beginPluginGenerationMutation("refresh", pluginGenerationMutationActivation)
	if err != nil {
		return s.writeResponse(req.ID, nil, err)
	}
	defer releaseMutation()
	if err := s.rt.RefreshExtensions(s.currentExtensionConfig()); err != nil {
		return s.writeResponse(req.ID, nil, err)
	}
	s.resetThreadRuntimesForGeneralSettings("")
	return s.writeResponse(req.ID, ExtensionCatalogRefreshResult{
		ExtensionInventory: s.currentExtensionInventory(),
		Skills:             skillSummaries(s.rt.Skills),
	}, nil)
}

func (s *Server) handleConfigAdvancedUpdate(req Request) error {
	var params ConfigAdvancedUpdateParams
	if err := decodeParams(req.Params, &params); err != nil {
		return s.writeResponse(req.ID, nil, err)
	}
	if s.hasRunningThread() {
		return s.writeResponse(req.ID, nil, errors.New("cannot change advanced settings while a turn is running"))
	}
	if s.rt == nil {
		return s.writeResponse(req.ID, nil, errors.New("runtime is not initialized"))
	}
	modelAliases := modelAliasConfigUpdate(params.ModelAliases)
	verificationModel := modelRoleConfigUpdate(params.VerificationModel)
	if modelAliases != nil || verificationModel != nil {
		candidate, _, err := s.rt.LoadEffectiveConfig()
		if err != nil {
			return s.writeResponse(req.ID, nil, err)
		}
		if modelAliases != nil {
			candidate.Agent.ModelAliases = make(map[string]config.ModelRoleConfig, len(modelAliases))
			for name, alias := range modelAliases {
				if alias != nil {
					candidate.Agent.ModelAliases[name] = *alias
				}
			}
		}
		if verificationModel != nil {
			candidate.Agent.ModelRoles.Verification = *verificationModel
		}
		if err := candidate.Validate(); err != nil {
			return s.writeResponse(req.ID, nil, err)
		}
	}
	if err := config.UpdateAdvancedRuntime(s.rt.ConfigPath, s.rt.ProviderName, config.AdvancedRuntimeUpdate{
		MaxSteps:                params.MaxSteps,
		MaxContextTokens:        params.MaxContextTokens,
		Temperature:             params.Temperature,
		CompactThresholdPct:     params.CompactThresholdPct,
		CompactKeepRecentTokens: params.CompactKeepRecentTokens,
		DisableAutoCompact:      params.DisableAutoCompact,
		ProviderContextWindow:   params.ProviderContextWindow,
		ModelAliases:            modelAliases,
		VerificationModel:       verificationModel,
	}); err != nil {
		return s.writeResponse(req.ID, nil, err)
	}

	cfg, _, err := s.rt.LoadEffectiveConfig()
	if err != nil {
		return s.writeResponse(req.ID, nil, err)
	}
	providerCfg, resolvedName, err := cfg.ResolveProvider(s.rt.ProviderName)
	if err != nil {
		return s.writeResponse(req.ID, nil, err)
	}
	providerCfg = s.withCachedCodexModels(resolvedName, providerCfg)
	ruleProviderName, ruleProviderCfg := modelcatalog.EnrichProvider(resolvedName, providerCfg, s.rt.Model)
	apiModel := modelcatalog.APIModel(ruleProviderCfg, s.rt.Model)
	roleSelections, err := modelroles.Resolve(cfg, modelroles.ResolveOptions{
		ProviderName:   resolvedName,
		ProviderConfig: providerCfg,
		Model:          s.rt.Model,
		Effort:         s.currentEffort(),
		Variant:        s.currentVariant(),
	})
	if err != nil {
		return s.writeResponse(req.ID, nil, err)
	}
	s.rt.ModelRoles = roleSelections
	modelBudget := runtime.ResolveModelBudget(s.rt.Model, ruleProviderCfg, cfg.Agent.MaxContextTokens)
	s.rt.ModelBudget = modelBudget
	workerBudget := runtime.ResolveModelBudget(roleSelections.Worker.Model, roleSelections.Worker.RuleProviderConfig, cfg.Agent.MaxContextTokens)
	s.rt.WorkerModelBudget = workerBudget
	if s.rt.StreamRunner != nil {
		s.rt.StreamRunner.MaxSteps = cfg.Agent.MaxSteps
		s.rt.StreamRunner.Temperature = cfg.Agent.Temperature
		s.rt.StreamRunner.CompactThresholdPct = cfg.Agent.CompactThresholdPct
		s.rt.StreamRunner.CompactKeepRecentTokens = cfg.Agent.CompactKeepRecentTokens
		s.rt.StreamRunner.DisableAutoCompact = cfg.Agent.DisableAutoCompact
		s.rt.StreamRunner.ContextWindowOverride = modelBudget.ContextWindowTokens
		s.rt.StreamRunner.MaxInputTokens = modelBudget.InputLimitTokens
		s.rt.StreamRunner.OutputReserveTokens = modelBudget.OutputReserveTokens
		s.rt.StreamRunner.CompactThresholdTokens = modelBudget.CompactThresholdTokens
	}
	s.updateRootAgentControlWorkerDefaults()
	s.updateIdleThreadAdvancedRuntime(cfg)
	if s.rt.Toolkit != nil {
		s.rt.Toolkit.ConfigureSurfaceForProviderModel(ruleProviderName, apiModel, true)
	}
	return s.writeResponse(req.ID, ConfigAdvancedUpdateResult{
		AdvancedSettings: s.currentAdvancedSettingsSummary(),
		ModelAliases:     modelAliasSummaries(cfg.Agent.ModelAliases),
		ModelRoles:       s.currentModelRoleSummaries(),
		Providers:        s.providerSummaries(),
	}, nil)
}

func modelAliasConfigUpdate(input *map[string]ModelAliasSummary) map[string]*config.ModelRoleConfig {
	if input == nil {
		return nil
	}
	out := make(map[string]*config.ModelRoleConfig, len(*input))
	for name, alias := range *input {
		selection := alias
		out[name] = &config.ModelRoleConfig{
			Provider: selection.Provider,
			Model:    selection.Model,
			Effort:   selection.Effort,
			Variant:  selection.Variant,
		}
	}
	return out
}

func modelRoleConfigUpdate(input *ModelAliasSummary) *config.ModelRoleConfig {
	if input == nil {
		return nil
	}
	return &config.ModelRoleConfig{
		Provider: strings.TrimSpace(input.Provider),
		Model:    strings.TrimSpace(input.Model),
		Effort:   strings.TrimSpace(input.Effort),
		Variant:  strings.TrimSpace(input.Variant),
	}
}

func modelAliasSummaries(input map[string]config.ModelRoleConfig) map[string]ModelAliasSummary {
	if len(input) == 0 {
		return nil
	}
	out := make(map[string]ModelAliasSummary, len(input))
	for name, alias := range input {
		out[strings.TrimSpace(name)] = ModelAliasSummary{
			Provider: alias.Provider,
			Model:    alias.Model,
			Effort:   alias.Effort,
			Variant:  alias.Variant,
		}
	}
	return out
}

func (s *Server) handleConfigGeneralUpdate(req Request) error {
	var params ConfigGeneralUpdateParams
	if err := decodeParams(req.Params, &params); err != nil {
		return s.writeResponse(req.ID, nil, err)
	}
	if s.hasRunningThread() {
		return s.writeResponse(req.ID, nil, errors.New("cannot change general settings while a turn is running"))
	}
	if s.rt == nil {
		return s.writeResponse(req.ID, nil, errors.New("runtime is not initialized"))
	}
	if err := config.UpdateGeneralSettings(s.rt.ConfigPath, config.GeneralSettingsUpdate{
		GitAttributionEnabled: params.GitAttributionEnabled,
		MCPEnabledToggles:     params.MCPEnabledToggles,
	}); err != nil {
		return s.writeResponse(req.ID, nil, err)
	}
	cfg, _, err := s.rt.LoadEffectiveConfig()
	if err != nil {
		return s.writeResponse(req.ID, nil, err)
	}
	systemPrompt := s.rt.ApplyGeneralConfig(cfg, os.Getenv("HOME"))
	s.resetThreadRuntimesForGeneralSettings(systemPrompt)
	return s.writeResponse(req.ID, ConfigGeneralUpdateResult{
		GeneralSettings: s.currentGeneralSettingsSummary(),
	}, nil)
}

func (s *Server) handleConfigModelUpdate(req Request) error {
	var params ConfigModelUpdateParams
	if err := decodeParams(req.Params, &params); err != nil {
		return s.writeResponse(req.ID, nil, err)
	}
	providerName := strings.TrimSpace(params.Provider)
	model := strings.TrimSpace(params.Model)
	threadID := strings.TrimSpace(params.ThreadID)
	// A conversation selection must never persist workspace defaults.
	if threadID != "" && params.BaseURL == nil && params.APIKey == nil && params.AuthToken == nil && params.Type == nil && !params.CreateProvider && params.RemoveModel == "" && params.ReuseCodexCredentials == nil {
		return s.handleThreadModelSelection(req, params)
	}
	if threadID != "" && (providerName != "" || model != "" || params.Variant != nil || params.Effort != nil || params.PermissionMode != nil) {
		return s.writeResponse(req.ID, nil, errors.New("save provider configuration separately from conversation selection"))
	}
	explicitSelection := providerName != "" || model != "" ||
		params.Effort != nil || params.Variant != nil || params.PermissionMode != nil
	if model == "" && (threadID == "" || params.CreateProvider) {
		return s.writeResponse(req.ID, nil, errors.New("model is required"))
	}
	if providerName == "" {
		providerName = s.rt.ProviderName
	}
	cfg, _, err := s.rt.LoadEffectiveConfig()
	if err != nil {
		return s.writeResponse(req.ID, nil, err)
	}
	var providerCfg config.ProviderConfig
	var resolvedName string
	// providerTypeValue is populated below when creatingProvider is true;
	// it is hoisted out of the if-block so the later CreateProviderRuntime
	// call can pass it as the new optional provider-type argument.
	providerTypeValue := ""
	var createBaseURL *string
	creatingProvider := params.CreateProvider
	autoDiscoveredGrokBuild := false
	if !creatingProvider {
		if _, discovered := localGrokBuildProvider(cfg, providerName, os.Getenv("HOME")); discovered {
			creatingProvider = true
			autoDiscoveredGrokBuild = true
		}
	}
	if creatingProvider {
		if providerName == "" {
			return s.writeResponse(req.ID, nil, errors.New("provider is required"))
		}
		if _, _, err := cfg.ResolveProvider(providerName); err == nil {
			return s.writeResponse(req.ID, nil, fmt.Errorf("provider %q already exists", providerName))
		}
		baseURL := strings.TrimSpace(stringValue(params.BaseURL))
		apiKey := strings.TrimSpace(stringValue(params.APIKey))
		authToken := strings.TrimSpace(stringValue(params.AuthToken))
		// Resolve the requested provider type. Empty / missing falls back to
		// "openai-compatible" (preserves the historical default). Unknown
		// values are rejected so the UI cannot accidentally create a
		// provider that the factory cannot service.
		providerTypeValue = "openai-compatible"
		if autoDiscoveredGrokBuild {
			providerTypeValue = "grok-build"
		} else if params.Type != nil {
			requested := strings.ToLower(strings.TrimSpace(*params.Type))
			switch requested {
			case "", "openai", "openai-compatible":
				if requested != "" {
					providerTypeValue = requested
				}
			case "anthropic", "claude", "anthropic-official":
				providerTypeValue = requested
			case "xai-subscription", "xai-oauth", "grok-subscription", "supergrok":
				providerTypeValue = requested
			case "grok-build", "xai-grok-build", "grok-cli":
				providerTypeValue = requested
			default:
				return s.writeResponse(req.ID, nil, fmt.Errorf("unsupported provider type %q", requested))
			}
		}
		if baseURL == "" && config.IsXAISubscriptionProvider(providerTypeValue) {
			baseURL = xaisub.DefaultBaseURL
		}
		if baseURL == "" && config.IsGrokBuildProvider(providerTypeValue) {
			baseURL = grokbuildspec.DefaultBaseURL
		}
		if baseURL == "" {
			return s.writeResponse(req.ID, nil, errors.New("base_url is required"))
		}
		if apiKey == "" && authToken == "" && !config.IsXAISubscriptionProvider(providerTypeValue) && !config.IsGrokBuildProvider(providerTypeValue) {
			return s.writeResponse(req.ID, nil, errors.New("api_key or auth_token is required"))
		}
		providerCfg = config.ProviderConfig{
			Type:      providerTypeValue,
			BaseURL:   baseURL,
			APIKey:    apiKey,
			AuthToken: authToken,
			Model:     model,
		}
		if config.IsXAISubscriptionProvider(providerTypeValue) {
			providerCfg.WireAPI = "responses"
			if strings.TrimSpace(providerCfg.Model) == "" {
				providerCfg.Model = xaisub.DefaultModel
				model = providerCfg.Model
			}
		}
		if config.IsGrokBuildProvider(providerTypeValue) {
			providerCfg = config.ApplyGrokBuildProviderDefaults(providerCfg)
			model = providerCfg.Model
		}
		createBaseURL = &baseURL
		resolvedName = providerName
	} else {
		providerCfg, resolvedName, err = cfg.ResolveProvider(providerName)
		if err != nil {
			return s.writeResponse(req.ID, nil, err)
		}
		providerCfg = s.withCachedCodexModels(resolvedName, providerCfg)
	}
	if model == "" {
		// Targeted request without an explicit model: the workspace side
		// keeps its current selection; the thread layers its own below.
		if resolvedName == s.rt.ProviderName {
			model = strings.TrimSpace(s.rt.Model)
		}
		if model == "" {
			model = strings.TrimSpace(providerCfg.Model)
		}
		if model == "" {
			return s.writeResponse(req.ID, nil, errors.New("model is required"))
		}
	}
	previousProviderCfg := providerCfg
	previousModel := strings.TrimSpace(providerCfg.Model)
	providerCfg.Model = model
	if removed := strings.TrimSpace(params.RemoveModel); removed != "" {
		if creatingProvider || threadID != "" || removed == model {
			return s.writeResponse(req.ID, nil, errors.New("model removal requires an existing provider and a different workspace model"))
		}
		providerCfg.Models = cloneProviderModelConfigs(providerCfg.Models)
		if providerCfg.Models == nil {
			providerCfg.Models = make(map[string]config.ProviderModelConfig)
		}
		entry := providerCfg.Models[removed]
		entry.Disabled = true
		providerCfg.Models[removed] = entry
	}
	connectionChanged := creatingProvider
	connectionLocked := isCodexProviderType(providerCfg.Type)
	if connectionLocked && (params.BaseURL != nil || strings.TrimSpace(stringValue(params.APIKey)) != "" || strings.TrimSpace(stringValue(params.AuthToken)) != "") {
		return s.writeResponse(req.ID, nil, errors.New("connection settings are managed by OpenAI OAuth for this provider"))
	}
	if params.ReuseCodexCredentials != nil {
		if !isCodexProviderType(providerCfg.Type) {
			return s.writeResponse(req.ID, nil, errors.New("reuse_codex_credentials is only valid for openai-codex providers"))
		}
		if providerCfg.ReuseCodexCredentials != *params.ReuseCodexCredentials {
			connectionChanged = true
		}
		providerCfg.ReuseCodexCredentials = *params.ReuseCodexCredentials
	}
	if params.BaseURL != nil {
		baseURL := strings.TrimSpace(*params.BaseURL)
		if baseURL == "" {
			return s.writeResponse(req.ID, nil, errors.New("base_url is required"))
		}
		if baseURL != strings.TrimSpace(providerCfg.BaseURL) {
			connectionChanged = true
		}
		providerCfg.BaseURL = baseURL
	}
	apiKeyForConfig := params.APIKey
	authTokenForConfig := params.AuthToken
	authKeyForStore := ""
	authTokenForStore := ""
	if params.APIKey != nil {
		apiKey := strings.TrimSpace(*params.APIKey)
		if apiKey != "" {
			connectionChanged = true
			authKeyForStore = apiKey
			providerCfg.APIKey = apiKey
			providerCfg.APIKeyEnv = ""
			providerCfg.AuthToken = ""
			providerCfg.AuthTokenEnv = ""
			empty := ""
			apiKeyForConfig = &empty
		} else {
			apiKeyForConfig = nil
		}
	}
	if params.AuthToken != nil {
		authToken := strings.TrimSpace(*params.AuthToken)
		if authToken != "" {
			connectionChanged = true
			authTokenForStore = authToken
			providerCfg.AuthToken = authToken
			providerCfg.AuthTokenEnv = ""
			providerCfg.APIKey = ""
			providerCfg.APIKeyEnv = ""
			empty := ""
			authTokenForConfig = &empty
			apiKeyForConfig = &empty
		} else {
			authTokenForConfig = nil
		}
	}
	variant := s.currentVariant()
	legacyEffort := s.currentEffort()
	selectionTouched := params.Variant != nil || params.Effort != nil
	if params.Variant != nil {
		variant = strings.TrimSpace(*params.Variant)
		if variant == "" && params.Effort == nil {
			legacyEffort = ""
		}
	}
	if params.Effort != nil {
		legacyEffort = strings.TrimSpace(*params.Effort)
		if params.Variant == nil {
			variant = legacyEffort
		}
	}
	ruleProviderName, ruleProviderCfg := modelcatalog.EnrichProvider(resolvedName, providerCfg, model)
	_, previousRuleProviderCfg := modelcatalog.EnrichProvider(resolvedName, previousProviderCfg, previousModel)
	providerClientChanged := providerClientConfigChanged(previousRuleProviderCfg, ruleProviderCfg)
	// Model choice takes precedence over a stale effort inherited by a caller.
	if resolvedName != s.rt.ProviderName || model != previousModel {
		if variant == "" {
			variant = legacyEffort
		}
		if variant != "" {
			if _, ok := modelvariant.OptionsForProvider(ruleProviderName, ruleProviderCfg, model, variant); !ok {
				variant, legacyEffort = "", ""
				selectionTouched = true
			}
		}
	}
	if params.Variant != nil && variant != "" {
		if _, ok := modelvariant.OptionsForProvider(ruleProviderName, ruleProviderCfg, model, variant); !ok {
			return s.writeResponse(req.ID, nil, fmt.Errorf("model %s does not support variant %s", model, variant))
		}
	}
	if params.Effort != nil && params.Variant == nil && variant != "" && len(modelvariant.SummariesForProvider(ruleProviderName, ruleProviderCfg, model)) > 0 {
		if _, ok := modelvariant.OptionsForProvider(ruleProviderName, ruleProviderCfg, model, variant); !ok {
			return s.writeResponse(req.ID, nil, fmt.Errorf("model %s does not support effort %s", model, variant))
		}
	}
	selection := modelvariant.ResolveForProvider(ruleProviderName, ruleProviderCfg, model, variant, legacyEffort)
	effort := selection.DisplayEffort
	effortForConfig, variantForConfig := selectionConfigPointers(selection, selectionTouched, s.currentVariant())
	if !explicitSelection {
		// Connection-only update: leave the persisted
		// workspace selection untouched.
		effortForConfig, variantForConfig = nil, nil
	}

	var client providers.StreamClient
	if resolvedName != s.rt.ProviderName || connectionChanged || providerClientChanged {
		client, err = providerfactory.BuildStreamClient(ruleProviderCfg, resolvedName)
		if err != nil {
			return s.writeResponse(req.ID, nil, err)
		}
	}
	if authKeyForStore != "" {
		store, storeErr := authstorage.ForHome(os.Getenv("HOME"))
		if storeErr != nil {
			return s.writeResponse(req.ID, nil, storeErr)
		}
		if err := store.Update(resolvedName, func(credentials *authstorage.Credentials) {
			credentials.Type = "api_key"
			credentials.APIKey = authKeyForStore
			credentials.AuthToken = ""
		}); err != nil {
			return s.writeResponse(req.ID, nil, err)
		}
	}
	if authTokenForStore != "" {
		store, storeErr := authstorage.ForHome(os.Getenv("HOME"))
		if storeErr != nil {
			return s.writeResponse(req.ID, nil, storeErr)
		}
		if err := store.Update(resolvedName, func(credentials *authstorage.Credentials) {
			credentials.Type = "auth_token"
			credentials.AuthToken = authTokenForStore
			credentials.APIKey = ""
		}); err != nil {
			return s.writeResponse(req.ID, nil, err)
		}
	}
	// Serialize the persisted write with the config watcher. Otherwise the
	// watcher can observe this interactive model/effort change and hot-apply it
	// again while (or just after) this handler is applying it itself.
	s.configRefreshMu.Lock()
	defer s.configRefreshMu.Unlock()
	if creatingProvider {
		baseURLForCreate := params.BaseURL
		if createBaseURL != nil {
			baseURLForCreate = createBaseURL
		}
		err = config.CreateProviderRuntime(s.rt.ConfigPath, resolvedName, &providerTypeValue, model, baseURLForCreate, apiKeyForConfig, authTokenForConfig, effortForConfig, variantForConfig, params.PermissionMode, params.ReuseCodexCredentials)
	} else {
		err = config.UpdateProviderRuntime(s.rt.ConfigPath, resolvedName, model, params.BaseURL, apiKeyForConfig, authTokenForConfig, effortForConfig, variantForConfig, params.PermissionMode, params.ReuseCodexCredentials, params.RemoveModel)
	}
	if err != nil {
		return s.writeResponse(req.ID, nil, err)
	}
	previousRuntimeProvider := s.rt.ProviderName
	roleSelections, err := modelroles.Resolve(cfg, modelroles.ResolveOptions{
		ProviderName:   resolvedName,
		ProviderConfig: providerCfg,
		Model:          model,
		Effort:         selection.LegacyEffort,
		Variant:        selection.Variant,
	})
	if err != nil {
		return s.writeResponse(req.ID, nil, err)
	}
	if params.PermissionMode != nil {
		permissions := config.ResolvedPermissions{Mode: config.NormalizePermissionMode(*params.PermissionMode)}
		s.rt.Permissions = permissions
		if s.rt.Toolkit != nil {
			runtime.ConfigureToolkitPermissions(s.rt.Toolkit, s.rt.Permissions)
		}
	}
	if err := s.applyModelSelectionToRuntime(cfg, resolvedName, model, ruleProviderName, ruleProviderCfg, selection, roleSelections, connectionChanged, providerClientChanged, previousRuntimeProvider, client, explicitSelection); err != nil {
		return s.writeResponse(req.ID, nil, err)
	}
	// The RPC response is the notification path for this in-process mutation.
	// Mark the just-applied file revision as observed so the periodic watcher
	// does not rebuild the runtime and notify the renderer again on its next
	// 500 ms tick.
	if fingerprint, fingerprintErr := s.effectiveConfigFingerprint(); fingerprintErr == nil {
		s.configFingerprint = fingerprint
	} else {
		providers.DebugLogf("record model update config fingerprint: %v", fingerprintErr)
	}

	modelProfile, toolSurface := s.currentModelSurfaceSummaries()
	return s.writeResponse(req.ID, ConfigModelUpdateResult{
		Provider:         resolvedName,
		Model:            model,
		Effort:           effort,
		Variant:          selection.Variant,
		MaxParallel:      s.rt.MaxParallel(),
		Permissions:      s.currentPermissionSummary(),
		ExtensionTrust:   s.currentExtensionTrustSummary(),
		ModelProfile:     modelProfile,
		ToolSurface:      toolSurface,
		ModelRoles:       s.currentModelRoleSummaries(),
		Providers:        s.providerSummaries(),
		AdvancedSettings: s.currentAdvancedSettingsSummary(),
	}, nil)
}

// applyModelSelectionToRuntime swaps the workspace runtime onto a resolved
// provider/model/variant selection in place. It is shared by the interactive
// config/model/update handler and the config-file watcher so an external edit
// to the effective config can be hot-applied without restarting the app-server.
func (s *Server) applyModelSelectionToRuntime(
	cfg config.Config,
	resolvedName string,
	model string,
	ruleProviderName string,
	ruleProviderCfg config.ProviderConfig,
	selection modelvariant.Selection,
	roleSelections modelroles.Set,
	connectionChanged bool,
	providerClientChanged bool,
	previousRuntimeProvider string,
	client providers.StreamClient,
	explicitSelection bool,
) error {
	if s == nil || s.rt == nil {
		return errors.New("runtime session is required")
	}
	if explicitSelection {
		s.rt.ProviderName = resolvedName
		s.rt.Model = model
	}
	if client != nil || len(s.rt.ReadinessIssues) == 0 {
		s.rt.ReadinessIssues = nil
	}
	s.rt.ModelRoles = roleSelections
	apiModel := modelcatalog.APIModel(ruleProviderCfg, model)
	if roleSelections.Title.Inherited {
		if client != nil {
			s.rt.TitleClient = client
		}
	} else {
		titleClient, err := providerfactory.BuildStreamClient(roleSelections.Title.RuleProviderConfig, roleSelections.Title.Provider)
		if err != nil {
			return fmt.Errorf("build title client: %w", err)
		}
		s.rt.TitleClient = titleClient
	}
	if s.rt.Toolkit != nil && (!roleSelections.Worker.Inherited || connectionChanged || providerClientChanged || resolvedName != previousRuntimeProvider) {
		workerClient, err := providerfactory.BuildStreamClient(roleSelections.Worker.RuleProviderConfig, roleSelections.Worker.Provider)
		if err != nil {
			return fmt.Errorf("build worker client: %w", err)
		}
		s.rt.WorkerClient = workerClient
	}
	if explicitSelection && s.rt.Toolkit != nil {
		s.rt.Toolkit.ConfigureSurfaceForProviderModel(ruleProviderName, apiModel, true)
	}
	if explicitSelection {
		if err := s.rt.ReconfigureToolLoading(cfg.Agent, ruleProviderCfg, apiModel, selection.ProviderOptions); err != nil {
			return fmt.Errorf("reconfigure tool loading: %w", err)
		}
	}
	if s.rt.StreamRunner != nil && client != nil {
		s.rt.StreamRunner.UpdateProviderConnection(client, agent.BuildProviderObservationKey(resolvedName, ruleProviderCfg.BaseURL, ruleProviderCfg.Type, ruleProviderCfg.API, ruleProviderCfg.WireAPI, ruleProviderCfg.StreamTransport, agent.ProviderUsageNormalizationKey(ruleProviderCfg.CacheCreationInputTokensOmitted, ruleProviderCfg.InputTokensIncludeCacheRead)))
	}
	if explicitSelection {
		s.rt.RefreshSystemPrompt(resolvedName, apiModel)
		modelBudget := runtime.ResolveModelBudget(model, ruleProviderCfg, cfg.Agent.MaxContextTokens)
		s.rt.ModelBudget = modelBudget
		s.rt.WorkerModelBudget = runtime.ResolveModelBudget(roleSelections.Worker.Model, roleSelections.Worker.RuleProviderConfig, cfg.Agent.MaxContextTokens)
		if s.rt.StreamRunner != nil {
			s.rt.StreamRunner.ProviderName = resolvedName
			s.rt.StreamRunner.Model = model
			s.rt.StreamRunner.APIModel = apiModel
			s.rt.StreamRunner.MediaInput = providers.MediaInputPolicy{
				Image:      roleSelections.Main.Capabilities.ImageInput,
				File:       roleSelections.Main.Capabilities.FileInput,
				ImageKnown: roleSelections.Main.Capabilities.ImageInputKnown,
				FileKnown:  roleSelections.Main.Capabilities.FileInputKnown,
			}
			s.rt.StreamRunner.Effort = selection.LegacyEffort
			s.rt.StreamRunner.Variant = selection.Variant
			s.rt.StreamRunner.ProviderOptions = modelvariant.CloneOptions(selection.ProviderOptions)
			s.rt.StreamRunner.ContextWindowOverride = modelBudget.ContextWindowTokens
			s.rt.StreamRunner.MaxInputTokens = modelBudget.InputLimitTokens
			s.rt.StreamRunner.OutputReserveTokens = modelBudget.OutputReserveTokens
			s.rt.StreamRunner.CompactThresholdTokens = modelBudget.CompactThresholdTokens
		}
	}
	s.updateRootAgentControlWorkerDefaults()
	if connectionChanged {
		// Connection credentials/endpoints are workspace configuration, so every
		// cached runtime must rebuild before its next turn. Running turns keep
		// their admitted snapshot until they settle.
		s.resetThreadRuntimesForGeneralSettings("")
	}
	return nil
}

func providerClientConfigChanged(previous, next config.ProviderConfig) bool {
	return strings.TrimSpace(previous.Type) != strings.TrimSpace(next.Type) ||
		strings.TrimSpace(previous.BaseURL) != strings.TrimSpace(next.BaseURL) ||
		strings.TrimSpace(previous.API) != strings.TrimSpace(next.API) ||
		strings.TrimSpace(previous.NPM) != strings.TrimSpace(next.NPM) ||
		strings.TrimSpace(previous.WireAPI) != strings.TrimSpace(next.WireAPI) ||
		strings.TrimSpace(previous.APIKey) != strings.TrimSpace(next.APIKey) ||
		strings.TrimSpace(previous.APIKeyEnv) != strings.TrimSpace(next.APIKeyEnv) ||
		strings.TrimSpace(previous.AuthToken) != strings.TrimSpace(next.AuthToken) ||
		strings.TrimSpace(previous.AuthTokenEnv) != strings.TrimSpace(next.AuthTokenEnv) ||
		previous.ReuseCodexCredentials != next.ReuseCodexCredentials ||
		previous.ReuseGrokCredentials != next.ReuseGrokCredentials ||
		previous.NativeCompactionEnabled() != next.NativeCompactionEnabled() ||
		previous.StreamConnectTimeoutMS != next.StreamConnectTimeoutMS ||
		previous.StreamHeaderTimeoutMS != next.StreamHeaderTimeoutMS ||
		previous.StreamIdleTimeoutMS != next.StreamIdleTimeoutMS ||
		strings.TrimSpace(previous.StreamTransport) != strings.TrimSpace(next.StreamTransport) ||
		previous.CacheCreationInputTokensOmitted != next.CacheCreationInputTokensOmitted ||
		previous.InputTokensIncludeCacheRead != next.InputTokensIncludeCacheRead ||
		!reflect.DeepEqual(previous.Headers, next.Headers)
}

func (s *Server) currentConfigModelUpdateResult() ConfigModelUpdateResult {
	modelProfile, toolSurface := s.currentModelSurfaceSummaries()
	return ConfigModelUpdateResult{
		Provider:         s.rt.ProviderName,
		Model:            s.rt.Model,
		Effort:           s.currentDisplayEffort(),
		Variant:          s.currentVariant(),
		MaxParallel:      s.rt.MaxParallel(),
		Permissions:      s.currentPermissionSummary(),
		ExtensionTrust:   s.currentExtensionTrustSummary(),
		ModelProfile:     modelProfile,
		ToolSurface:      toolSurface,
		ModelRoles:       s.currentModelRoleSummaries(),
		Providers:        s.providerSummaries(),
		AdvancedSettings: s.currentAdvancedSettingsSummary(),
	}
}

func (s *Server) handleConfigCodexModels(ctx context.Context, req Request) error {
	var params ConfigCodexModelsParams
	if err := decodeParams(req.Params, &params); err != nil {
		return s.writeResponse(req.ID, nil, err)
	}
	providerName := strings.TrimSpace(params.Provider)
	if providerName == "" {
		providerName = s.rt.ProviderName
	}
	cfg, _, err := s.rt.LoadEffectiveConfig()
	if err != nil {
		return s.writeResponse(req.ID, nil, err)
	}
	providerCfg, resolvedName, err := cfg.ResolveProvider(providerName)
	if err != nil {
		return s.writeResponse(req.ID, nil, err)
	}
	if !isCodexProviderType(providerCfg.Type) {
		return s.writeResponse(req.ID, nil, fmt.Errorf("provider %s uses type %s; Codex models require openai-codex", resolvedName, providerCfg.Type))
	}
	client, err := codex.New(codex.ClientConfig{
		BaseURL:               providerCfg.BaseURL,
		APIKey:                explicitProviderAPIKey(providerCfg),
		Headers:               providerCfg.Headers,
		ReuseCodexCredentials: providerCfg.ReuseCodexCredentials,
		NativeCompaction:      providerCfg.NativeCompaction,
	})
	if err != nil {
		return s.writeResponse(req.ID, nil, err)
	}
	models, err := client.Models(ctx)
	if err != nil {
		return s.writeResponse(req.ID, nil, err)
	}
	s.cacheCodexModels(resolvedName, models)
	out := codexModelSummaries(models)
	providerCfg = s.withCachedCodexModels(resolvedName, providerCfg)
	ruleProviderName, ruleProviderCfg := modelcatalog.EnrichProvider(resolvedName, providerCfg, providerCfg.Model)
	selection := modelvariant.ResolveForProvider(ruleProviderName, ruleProviderCfg, providerCfg.Model, cfg.Agent.Variant, cfg.Agent.Effort)
	effort := selection.DisplayEffort
	variant := selection.Variant
	if resolvedName == s.rt.ProviderName {
		effort = s.currentDisplayEffort()
		variant = s.currentVariant()
	}
	return s.writeResponse(req.ID, ConfigCodexModelsResult{
		Providers: s.providerSummaries(),
		Provider:  resolvedName,
		Model:     providerCfg.Model,
		Effort:    effort,
		Variant:   variant,
		Models:    out,
	}, nil)
}

func (s *Server) handleConfigProviderRemove(req Request) error {
	var params ConfigProviderRemoveParams
	if err := decodeParams(req.Params, &params); err != nil {
		return s.writeResponse(req.ID, nil, err)
	}
	providerName := strings.TrimSpace(params.Provider)
	if providerName == "" {
		return s.writeResponse(req.ID, nil, errors.New("provider is required"))
	}
	cfg, _, err := s.rt.LoadEffectiveConfig()
	if err != nil {
		return s.writeResponse(req.ID, nil, err)
	}
	existing, resolvedName, lookupErr := cfg.ResolveProvider(providerName)
	if lookupErr != nil {
		return s.writeResponse(req.ID, nil, lookupErr)
	}
	if isCodexProviderType(existing.Type) {
		return s.writeResponse(req.ID, nil, fmt.Errorf("provider %q is managed by OpenAI OAuth and cannot be removed", providerName))
	}
	if threadID, inUse := s.runningTurnUsingProvider(resolvedName); inUse {
		return s.writeResponse(req.ID, nil, fmt.Errorf("cannot remove provider %q while it is used by a running turn in thread %q", resolvedName, threadID))
	}
	// Refuse to remove the last remaining provider when no fallback is
	// supplied — config.RemoveProvider would surface a similar error but
	// doing the check here gives the renderer a clearer 4xx-style error.
	if len(cfg.Providers) <= 1 && strings.TrimSpace(params.FallbackProvider) == "" {
		return s.writeResponse(req.ID, nil, errors.New("cannot remove the last configured provider"))
	}
	removedWasDefault := cfg.DefaultProvider == providerName
	authStore, authErr := authstorage.ForHome(os.Getenv("HOME"))
	if authErr != nil {
		return s.writeResponse(req.ID, nil, fmt.Errorf("prepare credential cleanup: %w", authErr))
	}
	if authErr := authStore.DeleteProvider(resolvedName); authErr != nil {
		return s.writeResponse(req.ID, nil, fmt.Errorf("remove provider credential: %w", authErr))
	}
	if config.IsXAISubscriptionProvider(existing.Type) {
		if err := xaisub.DeleteTokens(os.Getenv("HOME")); err != nil {
			return s.writeResponse(req.ID, nil, fmt.Errorf("remove SuperGrok credential: %w", err))
		}
	}
	newDefault, removeErr := config.RemoveProvider(s.rt.ConfigPath, providerName, params.FallbackProvider, params.FallbackModel)
	if removeErr != nil {
		return s.writeResponse(req.ID, nil, fmt.Errorf("credential removed but provider config update failed; re-enter the credential before retrying: %w", removeErr))
	}
	if !removedWasDefault {
		// Removed an inactive provider; nothing in the runtime needs
		// reconfiguring because the active selection is unchanged.
		return s.writeResponse(req.ID, ConfigProviderRemoveResult{
			Provider:         s.rt.ProviderName,
			Model:            s.rt.Model,
			Variant:          s.currentVariant(),
			Permissions:      s.currentPermissionSummary(),
			ExtensionTrust:   s.currentExtensionTrustSummary(),
			ModelProfile:     nil,
			ToolSurface:      nil,
			ModelRoles:       s.currentModelRoleSummaries(),
			Providers:        s.providerSummaries(),
			AdvancedSettings: s.currentAdvancedSettingsSummary(),
		}, nil)
	}
	// Default provider changed — reload the config and rebuild the
	// runtime for the new default. Falling back to the removed
	// provider's model when the caller did not supply fallbackModel
	// keeps the renderer from showing an empty model after the swap.
	fallbackName := strings.TrimSpace(newDefault)
	if fallbackName == "" {
		return s.writeResponse(req.ID, nil, errors.New("provider removal left no default_provider configured"))
	}
	fallbackModel := strings.TrimSpace(params.FallbackModel)
	if fallbackModel == "" {
		if fb, ok := cfg.Providers[fallbackName]; ok {
			fallbackModel = fb.Model
		}
	}
	synthetic := req
	synthetic.Method = MethodConfigModelUpdate
	synthetic.Params, err = json.Marshal(ConfigModelUpdateParams{
		Provider: fallbackName,
		Model:    fallbackModel,
	})
	if err != nil {
		return s.writeResponse(req.ID, nil, fmt.Errorf("marshal fallback params: %w", err))
	}
	return s.handleConfigModelUpdate(synthetic)
}

func (s *Server) runningTurnUsingProvider(providerName string) (string, bool) {
	providerName = strings.TrimSpace(providerName)
	if providerName == "" {
		return "", false
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, th := range s.threads {
		th.mu.Lock()
		running := th.running
		runningProvider := strings.TrimSpace(th.runningProviderName)
		if running && runningProvider == "" {
			runningProvider = strings.TrimSpace(th.ModelProvider)
		}
		threadID := th.ID
		th.mu.Unlock()
		if running && runningProvider == providerName {
			return threadID, true
		}
	}
	return "", false
}

func (s *Server) handleSkillList(req Request) error {
	return s.writeResponse(req.ID, SkillListResult{Skills: skillSummaries(s.rt.Skills)}, nil)
}

func skillSummaries(items []skills.Skill) []SkillSummary {
	out := make([]SkillSummary, 0, len(items))
	for _, item := range items {
		out = append(out, SkillSummary{
			Name:                  item.Name,
			Description:           item.Description,
			WhenToUse:             item.WhenToUse,
			TriggerCondition:      item.TriggerCondition,
			Source:                item.Source,
			Path:                  item.Path,
			ArgumentHint:          item.ArgumentHint,
			Model:                 item.Model,
			Context:               item.Context,
			Agent:                 item.Agent,
			AllowedTools:          append([]string(nil), item.AllowedTools...),
			RequiredContext:       append([]string(nil), item.RequiredContext...),
			Examples:              append([]string(nil), item.Examples...),
			VerificationChecklist: append([]string(nil), item.VerificationChecklist...),
			ProgressiveDisclosure: item.ProgressiveDisclosure,
			UserInvocable:         item.UserInvocable,
			DisableModelInvoke:    item.DisableModelInvoke,
			Paths:                 append([]string(nil), item.Paths...),
			Effort:                item.Effort,
			Version:               item.Version,
		})
	}
	return out
}

func (s *Server) beginThreadRuntimeSelectionMutation(threadID string) (*threadState, func(), error) {
	threadID = strings.TrimSpace(threadID)
	if threadID == "" {
		return nil, func() {}, nil
	}
	th, err := s.ensureThreadLoaded(threadID)
	if err != nil {
		return nil, func() {}, err
	}
	th.mu.Lock()
	busy := th.running || th.admissionReserved || th.runtimeSelectionMutation ||
		(th.execRuntime != nil && threadRuntimeHasOutstandingWork(th.ID, th.execRuntime))
	if busy {
		th.mu.Unlock()
		return nil, func() {}, fmt.Errorf("cannot change model or permission mode while thread %q is running", threadID)
	}
	th.runtimeSelectionMutation = true
	persist := th.PersistHistory
	th.mu.Unlock()

	var mutationLease *session.ThreadExecutionLease
	if persist {
		mutationLease, err = s.tryAcquireThreadMutationLease(threadID)
		if err != nil {
			th.mu.Lock()
			th.runtimeSelectionMutation = false
			th.mu.Unlock()
			if errors.Is(err, errThreadExecutionBusy) {
				return nil, func() {}, fmt.Errorf("cannot change model or permission mode while thread %q is running", threadID)
			}
			return nil, func() {}, err
		}
	}
	release := func() {
		releaseThreadMutationLease(threadID, mutationLease)
		th.mu.Lock()
		th.runtimeSelectionMutation = false
		th.mu.Unlock()
	}
	return th, release, nil
}

func (s *Server) handleThreadModelSelection(req Request, params ConfigModelUpdateParams) error {
	th, release, err := s.beginThreadRuntimeSelectionMutation(params.ThreadID)
	if err != nil {
		return s.writeResponse(req.ID, nil, err)
	}
	defer release()
	th.mu.Lock()
	if th.NamedAgentID != "" {
		th.mu.Unlock()
		return s.writeResponse(req.ID, nil, errors.New("collaboration model selection is pinned; create another session with the desired model"))
	}

	provider, model := th.ModelProvider, th.Model
	variant, effort, permission := th.ModelVariant, th.ModelEffort, th.PermissionMode
	th.mu.Unlock()
	if provider == "" {
		provider = s.rt.ProviderName
	}
	if model == "" {
		model = s.rt.Model
	}
	previousProvider, previousModel := provider, model
	if strings.TrimSpace(params.Provider) != "" {
		provider = strings.TrimSpace(params.Provider)
	}
	if strings.TrimSpace(params.Model) != "" {
		model = strings.TrimSpace(params.Model)
	}
	cfg, _, err := s.rt.LoadEffectiveConfig()
	if err != nil {
		return s.writeResponse(req.ID, nil, err)
	}
	providerCfg, resolvedName, err := cfg.ResolveProvider(provider)
	if err != nil {
		return s.writeResponse(req.ID, nil, err)
	}
	providerCfg = s.withCachedCodexModels(resolvedName, providerCfg)
	if providerCfg.Models[model].Disabled {
		return s.writeResponse(req.ID, nil, fmt.Errorf("model %s is disabled", model))
	}
	ruleName, ruleCfg := modelcatalog.EnrichProvider(resolvedName, providerCfg, model)
	if params.Variant != nil {
		variant = strings.TrimSpace(*params.Variant)
		effort = ""
	}
	if params.Effort != nil {
		effort = strings.TrimSpace(*params.Effort)
		if params.Variant == nil {
			variant = effort
		}
	}
	if (provider != previousProvider || model != previousModel) && variant == "" {
		variant = effort
	}
	if variant != "" {
		if _, ok := modelvariant.OptionsForProvider(ruleName, ruleCfg, model, variant); !ok {
			if provider == previousProvider && model == previousModel && (params.Variant != nil || params.Effort != nil) {
				return s.writeResponse(req.ID, nil, fmt.Errorf("model %s does not support variant %s", model, variant))
			}
			variant, effort = "", ""
		}
	}
	selection := modelvariant.ResolveForProvider(ruleName, ruleCfg, model, variant, effort)
	if permission == "" {
		permission = config.NormalizePermissionMode(s.rt.Permissions.Mode)
	}
	if params.PermissionMode != nil {
		permission = config.NormalizePermissionMode(*params.PermissionMode)
	}
	if err := s.updateThreadRuntimeForModelUpdate(th, resolvedName, model, selection.Variant, selection.LegacyEffort, permission); err != nil {
		return s.writeResponse(req.ID, nil, err)
	}
	modelProfile, toolSurface := s.currentModelSurfaceSummaries()
	return s.writeResponse(req.ID, ConfigModelUpdateResult{
		Provider: s.rt.ProviderName, Model: s.rt.Model,
		Effort: s.currentDisplayEffort(), Variant: s.currentVariant(),
		MaxParallel: s.rt.MaxParallel(), Permissions: s.currentPermissionSummary(),
		ExtensionTrust: s.currentExtensionTrustSummary(), ModelProfile: modelProfile,
		ToolSurface: toolSurface, ModelRoles: s.currentModelRoleSummaries(),
		Providers: s.providerSummaries(), AdvancedSettings: s.currentAdvancedSettingsSummary(),
	}, nil)
}

func (s *Server) updateThreadRuntimeForModelUpdate(th *threadState, providerName, model, variant, effort, permissionMode string) error {
	if th == nil {
		return nil
	}
	selection := session.RuntimeSelection{
		Provider:       providerName,
		Model:          model,
		Variant:        variant,
		Effort:         effort,
		PermissionMode: permissionMode,
	}
	if th.PersistHistory {
		if _, err := session.SetRuntimeSelection(s.rt.SessionDir, th.ID, selection); err != nil {
			return err
		}
	}
	var detached detachedThreadRuntime
	th.mu.Lock()
	modelSelectionChanged := th.ModelProvider != strings.TrimSpace(selection.Provider) ||
		th.Model != strings.TrimSpace(selection.Model) ||
		th.ModelVariant != strings.TrimSpace(selection.Variant) ||
		th.ModelEffort != strings.TrimSpace(selection.Effort)
	applyThreadRuntimeSelection(th, selection)
	if modelSelectionChanged && th.execRuntime != nil {
		detached = detachThreadRuntimeLocked(th)
	}
	thread := th.snapshotLocked()
	th.mu.Unlock()
	if detached.runtime != nil || detached.subscription != nil {
		releaseDetachedThreadRuntime(detached)
	}
	return s.notifyThreadUpdated(thread)
}

func (s *Server) resetThreadRuntimesForGeneralSettings(systemPrompt string) {
	if s == nil || s.rt == nil {
		return
	}
	var releases []detachedThreadRuntime
	s.mu.Lock()
	for _, th := range s.threads {
		th.mu.Lock()
		if th.NamedAgentID != "" {
			th.mu.Unlock()
			continue
		}
		if strings.TrimSpace(systemPrompt) != "" {
			th.History = replaceBaseSystemPrompt(th.History, systemPrompt)
			if th.PersistHistory {
				if err := rewriteChatHistory(s.rt.SessionDir, th.ID, th.History); err != nil {
					providers.DebugLogf("rewrite thread %q system prompt after general settings update: %v", th.ID, err)
				}
			}
		}
		if th.execRuntime == nil {
			th.pendingRuntimeReset = false
			th.mu.Unlock()
			continue
		}
		// A running root turn may still spawn workers, and already-started
		// workers need this runtime's terminal finalizer and notification
		// forwarding. Keep the runtime installed until no live work depends on
		// it; the next admission consumes the reset before rebuilding.
		if th.running || threadRuntimeHasOutstandingWork(th.ID, th.execRuntime) {
			th.pendingRuntimeReset = true
		} else {
			releases = append(releases, detachThreadRuntimeLocked(th))
		}
		th.mu.Unlock()
	}
	s.mu.Unlock()
	for _, detached := range releases {
		releaseDetachedThreadRuntime(detached)
	}
}

// updateIdleThreadAdvancedRuntime propagates an advanced-settings change to
// idle cached runtimes without rebuilding them (which would churn their
// prompt-cache prefixes). Advanced settings split into two kinds, and the split
// is the whole point of this function (issue #81):
//
//   - Behavior knobs (MaxSteps, Temperature, compact percentage/keep-recent,
//     auto-compact toggle) are workspace-uniform and copied verbatim.
//   - Budgets and worker defaults are DERIVED from a conversation's own
//     selection, so they are recomputed from each thread's pin — never copied
//     from the workspace runtime. Copying s.rt's here would, for a thread
//     pinned to a smaller-window model, swap in the workspace model's window
//     (miscomputing compaction) and repoint the thread's worker at the
//     workspace worker provider.
func (s *Server) updateIdleThreadAdvancedRuntime(cfg config.Config) {
	if s == nil || s.rt == nil || s.rt.StreamRunner == nil {
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, th := range s.threads {
		th.mu.Lock()
		if th.NamedAgentID != "" {
			th.mu.Unlock()
			continue
		}
		if th.running || th.execRuntime == nil {
			th.mu.Unlock()
			continue
		}
		derivation, err := s.rt.DeriveThreadModel(cfg, th.execRuntime.Selection)
		if err != nil {
			// The thread pins a model that no longer resolves (e.g. a removed
			// provider). Leave its existing derived budgets/worker in place;
			// admission self-heals the dead pin on the next turn.
			providers.DebugLogf("advanced update: re-derive thread %q model: %v", th.ID, err)
			th.mu.Unlock()
			continue
		}
		if th.execRuntime.StreamRunner != nil {
			runner := th.execRuntime.StreamRunner
			// Workspace behavior knobs: uniform, copied verbatim.
			runner.MaxSteps = s.rt.StreamRunner.MaxSteps
			runner.Temperature = s.rt.StreamRunner.Temperature
			runner.CompactThresholdPct = s.rt.StreamRunner.CompactThresholdPct
			runner.CompactKeepRecentTokens = s.rt.StreamRunner.CompactKeepRecentTokens
			runner.DisableAutoCompact = s.rt.StreamRunner.DisableAutoCompact
			// Derived budget: recomputed from this thread's own pin.
			runner.ContextWindowOverride = derivation.ModelBudget.ContextWindowTokens
			runner.MaxInputTokens = derivation.ModelBudget.InputLimitTokens
			runner.OutputReserveTokens = derivation.ModelBudget.OutputReserveTokens
			runner.CompactThresholdTokens = derivation.ModelBudget.CompactThresholdTokens
		}
		th.execRuntime.ModelBudget = derivation.ModelBudget
		th.execRuntime.WorkerModelBudget = derivation.WorkerModelBudget
		if th.execRuntime.AgentControl != nil {
			th.execRuntime.AgentControl.UpdateWorkerDefaults(
				derivation.WorkerClient,
				derivation.WorkerAPIModel,
				s.workerManagerOptionsForDerivation(derivation),
			)
		}
		th.mu.Unlock()
	}
}

// workerManagerOptionsForDerivation builds worker manager options from a
// thread's own derived worker role, layering the workspace behavior knobs
// (temperature, compaction cadence) that apply uniformly. It is the per-thread
// analogue of currentWorkerManagerOptions, which serves the workspace runtime.
func (s *Server) workerManagerOptionsForDerivation(derivation runtime.ThreadModelDerivation) subagent.ManagerOptions {
	temperature := 0.0
	compactPct := 0.0
	keepRecent := 0
	disableCompact := false
	if s.rt != nil && s.rt.StreamRunner != nil {
		temperature = s.rt.StreamRunner.Temperature
		compactPct = s.rt.StreamRunner.CompactThresholdPct
		keepRecent = s.rt.StreamRunner.CompactKeepRecentTokens
		disableCompact = s.rt.StreamRunner.DisableAutoCompact
	}
	return subagent.ManagerOptions{
		DefaultProviderName:     derivation.WorkerProvider,
		DefaultEffort:           derivation.WorkerEffort,
		DefaultProviderOptions:  derivation.WorkerOptions,
		ContextWindowOverride:   derivation.WorkerModelBudget.ContextWindowTokens,
		MaxInputTokens:          derivation.WorkerModelBudget.InputLimitTokens,
		OutputReserveTokens:     derivation.WorkerModelBudget.OutputReserveTokens,
		CompactThresholdTokens:  derivation.WorkerModelBudget.CompactThresholdTokens,
		Temperature:             temperature,
		CompactThresholdPct:     compactPct,
		CompactKeepRecentTokens: keepRecent,
		DisableAutoCompact:      disableCompact,
	}
}

func (s *Server) updateRootAgentControlWorkerDefaults() {
	if s == nil || s.rt == nil || s.rt.AgentControl == nil {
		return
	}
	s.rt.AgentControl.UpdateWorkerDefaults(
		s.rt.WorkerClient,
		s.rt.ModelRoles.Worker.APIModel,
		s.currentWorkerManagerOptions(),
	)
}

func (s *Server) currentWorkerManagerOptions() subagent.ManagerOptions {
	if s == nil || s.rt == nil {
		return subagent.ManagerOptions{}
	}
	compactPct := 0.0
	keepRecent := 0
	disableCompact := false
	temperature := 0.0
	if s.rt.StreamRunner != nil {
		compactPct = s.rt.StreamRunner.CompactThresholdPct
		keepRecent = s.rt.StreamRunner.CompactKeepRecentTokens
		disableCompact = s.rt.StreamRunner.DisableAutoCompact
		temperature = s.rt.StreamRunner.Temperature
	}
	return subagent.ManagerOptions{
		DefaultProviderName:     workerProviderName(s.rt),
		DefaultEffort:           s.rt.ModelRoles.Worker.LegacyEffort,
		DefaultProviderOptions:  s.rt.ModelRoles.Worker.ProviderOptions,
		ContextWindowOverride:   s.rt.WorkerModelBudget.ContextWindowTokens,
		MaxInputTokens:          s.rt.WorkerModelBudget.InputLimitTokens,
		OutputReserveTokens:     s.rt.WorkerModelBudget.OutputReserveTokens,
		CompactThresholdTokens:  s.rt.WorkerModelBudget.CompactThresholdTokens,
		Temperature:             temperature,
		CompactThresholdPct:     compactPct,
		CompactKeepRecentTokens: keepRecent,
		DisableAutoCompact:      disableCompact,
	}
}

func (s *Server) currentEffort() string {
	if s == nil || s.rt == nil || s.rt.StreamRunner == nil {
		return ""
	}
	return strings.TrimSpace(s.rt.StreamRunner.Effort)
}

func (s *Server) currentVariant() string {
	if s == nil || s.rt == nil || s.rt.StreamRunner == nil {
		return ""
	}
	return strings.TrimSpace(s.rt.StreamRunner.Variant)
}

func (s *Server) currentDisplayEffort() string {
	if variant := s.currentVariant(); variant != "" {
		return variant
	}
	return s.currentEffort()
}

func (s *Server) currentSessionRuntimeSelection() session.RuntimeSelection {
	selection := session.RuntimeSelection{}
	if s != nil && s.rt != nil {
		selection.Provider = s.rt.ProviderName
		selection.Model = s.rt.Model
		selection.Variant = s.currentVariant()
		selection.Effort = s.currentEffort()
		selection.PermissionMode = config.NormalizePermissionMode(s.rt.Permissions.Mode)
	}
	return selection
}

// pinLegacyRuntimeSelections backfills this workspace's persisted sessions
// that predate per-session runtime selection. Defaults come from the durable
// config rather than live runtime state so transient process overrides (e.g.
// a one-shot CLI permission mode) are never pinned. Pinning is best-effort:
// a bad row is logged and skipped instead of blocking initialization.
func (s *Server) pinLegacyRuntimeSelections() {
	if s == nil || s.rt == nil {
		return
	}
	cfg, _, err := s.rt.LoadEffectiveConfig()
	if err != nil {
		providers.DebugLogf("load config to pin legacy runtime selections: %v", err)
		return
	}
	providerCfg, providerName, err := cfg.ResolveProvider("")
	if err != nil {
		providers.DebugLogf("resolve default provider to pin legacy runtime selections: %v", err)
		return
	}
	defaults := session.RuntimeSelection{
		Provider:       strings.TrimSpace(providerName),
		Model:          strings.TrimSpace(providerCfg.Model),
		Variant:        strings.TrimSpace(cfg.Agent.Variant),
		Effort:         strings.TrimSpace(cfg.Agent.Effort),
		PermissionMode: config.NormalizePermissionMode(cfg.Agent.PermissionMode),
	}
	if defaults.Provider == "" || defaults.Model == "" {
		return
	}
	// Strict workspace scoping: DM and group sessions of other workspaces
	// must keep their own defaults, so the DM-inclusive listing is wrong here.
	sessions, err := session.ListForCWD(s.rt.SessionDir, s.rt.RootDir, s.rt.WorkspaceID, 0)
	if err != nil {
		providers.DebugLogf("list sessions to pin legacy runtime selections: %v", err)
		return
	}
	for _, sess := range sessions {
		selection := runtimeSelectionFromSession(sess)
		legacySelection := selection.PermissionMode == ""
		changed := false
		if selection.Provider == "" || selection.Model == "" {
			selection.Provider = defaults.Provider
			selection.Model = defaults.Model
			changed = true
		}
		if legacySelection && selection.Variant == "" && selection.Effort == "" {
			selection.Variant = defaults.Variant
			selection.Effort = defaults.Effort
			changed = true
		}
		if selection.PermissionMode == "" {
			selection.PermissionMode = defaults.PermissionMode
			changed = true
		}
		if changed {
			if _, err := session.SetRuntimeSelection(s.rt.SessionDir, sess.ID, selection); err != nil {
				providers.DebugLogf("pin runtime selection for session %q: %v", sess.ID, err)
			}
		}
	}
}

func selectionConfigPointers(selection modelvariant.Selection, touched bool, previousVariant string) (*string, *string) {
	if !touched && (strings.TrimSpace(previousVariant) == "" || selection.Variant != "") {
		return nil, nil
	}
	if selection.Variant != "" {
		empty := ""
		variant := selection.Variant
		return &empty, &variant
	}
	if selection.LegacyEffort != "" {
		effort := selection.LegacyEffort
		empty := ""
		return &effort, &empty
	}
	emptyEffort := ""
	emptyVariant := ""
	return &emptyEffort, &emptyVariant
}

func (s *Server) providerSummaries() []ProviderSummary {
	cfg, _, err := s.rt.LoadEffectiveConfig()
	if err != nil {
		return nil
	}
	home := os.Getenv("HOME")
	autoDiscovered := false
	if provider, ok := localGrokBuildProvider(cfg, "grok-build", home); ok {
		providers := make(map[string]config.ProviderConfig, len(cfg.Providers)+1)
		for name, configured := range cfg.Providers {
			providers[name] = configured
		}
		providers["grok-build"] = provider
		cfg.Providers = providers
		autoDiscovered = true
	}
	// Discovery enriches the same inventory used by settings and selection;
	// it must not mutate the persisted provider configuration.
	enriched := make(map[string]config.ProviderConfig, len(cfg.Providers))
	for name, provider := range cfg.Providers {
		enriched[name] = s.withCachedCodexModels(name, provider)
	}
	cfg.Providers = enriched
	summaries := providerSummariesFromConfig(cfg, home)
	if autoDiscovered {
		for index := range summaries {
			if summaries[index].Name == "grok-build" {
				summaries[index].AutoDiscovered = true
				break
			}
		}
	}
	return summaries
}

func localGrokBuildProvider(cfg config.Config, providerName, home string) (config.ProviderConfig, bool) {
	if strings.TrimSpace(providerName) != "grok-build" {
		return config.ProviderConfig{}, false
	}
	if _, exists := cfg.Providers[providerName]; exists {
		return config.ProviderConfig{}, false
	}
	for _, provider := range cfg.Providers {
		if config.IsGrokBuildProvider(provider.Type) {
			return config.ProviderConfig{}, false
		}
	}
	if _, err := grokbuild.LocalOAuthStatus(home); err != nil {
		return config.ProviderConfig{}, false
	}
	return config.ApplyGrokBuildProviderDefaults(config.ProviderConfig{Type: "grok-build"}), true
}

func providerSummariesFromConfig(cfg config.Config, home string) []ProviderSummary {
	names := make([]string, 0, len(cfg.Providers))
	for name := range cfg.Providers {
		names = append(names, name)
	}
	sort.Strings(names)
	out := make([]ProviderSummary, 0, len(names))
	for _, name := range names {
		provider := cfg.Providers[name]
		summary := ProviderSummary{
			Name:             name,
			Type:             provider.Type,
			Model:            provider.Model,
			BaseURL:          provider.BaseURL,
			APIKeyConfigured: providerHasAuth(name, provider, home),
			ConnectionLocked: isCodexProviderType(provider.Type) || config.IsXAISubscriptionProvider(provider.Type) || config.IsGrokBuildProvider(provider.Type),
			Models:           providerModelSummaries(name, provider),
		}
		if isCodexProviderType(provider.Type) {
			summary.ReuseCodexCredentials = provider.ReuseCodexCredentials
			if source, err := codex.LocalOAuthStatus(home); err == nil {
				summary.CodexCredentialSource = source
			}
		}
		out = append(out, summary)
	}
	return out
}

func providerHasAuth(name string, provider config.ProviderConfig, home string) bool {
	if isCodexProviderType(provider.Type) {
		if strings.TrimSpace(provider.APIKey) != "" || configuredEnvValue(provider.APIKeyEnv) != "" {
			return true
		}
		source, err := codex.LocalOAuthStatus(home)
		return err == nil && (source == "wuu-auth-store" || provider.ReuseCodexCredentials)
	}
	if config.IsXAISubscriptionProvider(provider.Type) {
		_, err := xaisub.LocalOAuthStatus(home)
		return err == nil
	}
	if config.IsGrokBuildProvider(provider.Type) {
		if strings.TrimSpace(provider.APIKey) != "" || configuredEnvValue(provider.APIKeyEnv) != "" {
			return true
		}
		if !provider.ReuseGrokCredentials {
			return false
		}
		_, err := grokbuild.LocalOAuthStatus(home)
		return err == nil
	}

	if _, err := providerfactory.ResolveAPIKeyWithHome(provider, name, home); err == nil {
		return true
	}
	if !providerUsesAnthropicAuth(provider) {
		return false
	}
	if strings.TrimSpace(provider.AuthToken) != "" {
		return true
	}
	authTokenEnv := strings.TrimSpace(provider.AuthTokenEnv)
	if authTokenEnv == "" {
		authTokenEnv = "ANTHROPIC_AUTH_TOKEN"
	}
	if configuredEnvValue(authTokenEnv) != "" {
		return true
	}
	store, err := authstorage.ForHome(home)
	if err != nil {
		return false
	}
	credentials, err := store.Get(name)
	return err == nil && strings.TrimSpace(credentials.AuthToken) != ""
}

func configuredEnvValue(name string) string {
	name = strings.TrimSpace(name)
	if name == "" {
		return ""
	}
	return strings.TrimSpace(os.Getenv(name))
}

func providerUsesAnthropicAuth(provider config.ProviderConfig) bool {
	providerType := strings.ToLower(strings.TrimSpace(provider.Type))
	providerType = strings.ReplaceAll(providerType, "_", "-")
	if providerType == "anthropic" || providerType == "claude" || providerType == "anthropic-official" {
		return true
	}
	npm := strings.ToLower(strings.TrimSpace(provider.NPM))
	npm = strings.TrimPrefix(npm, "npm:")
	return npm == "@ai-sdk/anthropic"
}

func providerModelSummaries(providerName string, provider config.ProviderConfig) []ProviderModelSummary {
	if config.IsGrokBuildProvider(provider.Type) {
		provider = config.ApplyGrokBuildProviderDefaults(provider)
	}
	ruleProviderName := providerName
	ruleProvider := provider
	var catalogProvider modelcatalog.Provider
	hasCatalog := false
	if matched, ok := modelcatalog.MatchProvider(providerName, provider); ok {
		catalogProvider = matched
		hasCatalog = true
		ruleProviderName = matched.ID
		ruleProvider = modelcatalog.MergeProvider(provider, matched)
	} else if isCodexProviderType(provider.Type) {
		if matched, ok := modelcatalog.CodexSubscriptionCatalogProvider(codexCatalogModelIDs(provider)...); ok {
			catalogProvider = matched
			hasCatalog = true
			ruleProvider = modelcatalog.MergeProvider(provider, matched)
		}
	}
	current := strings.TrimSpace(provider.Model)
	selectedRuleProviderName := ruleProviderName
	selectedRuleProvider := ruleProvider
	if current != "" {
		selectedRuleProviderName, selectedRuleProvider = modelcatalog.EnrichProvider(providerName, provider, current)
	}

	models := make(map[string]ProviderModelSummary, len(ruleProvider.Models)+1)
	disabled := map[string]bool{}
	for id, model := range provider.Models {
		id = strings.TrimSpace(id)
		if id == "" {
			continue
		}
		if model.Disabled {
			disabled[id] = true
			continue
		}
		modelRuleProviderName := ruleProviderName
		modelRuleProvider := ruleProvider
		if id == current {
			modelRuleProviderName = selectedRuleProviderName
			modelRuleProvider = selectedRuleProvider
		}
		model = modelRuleProvider.Models[id]
		capabilities, behavior := modelroles.BuildFacts(modelRuleProviderName, modelRuleProvider, id)
		models[id] = ProviderModelSummary{
			ID:               id,
			DisplayName:      strings.TrimSpace(model.Name),
			DefaultEffort:    strings.TrimSpace(model.DefaultEffort),
			DefaultVariant:   strings.TrimSpace(model.DefaultVariant),
			SupportedEfforts: modelvariant.SupportedEffortsForProvider(modelRuleProviderName, modelRuleProvider, id, model),
			Variants:         providerVariantSummaries(modelvariant.SummariesForProvider(modelRuleProviderName, modelRuleProvider, id)),
			Capabilities:     capabilities,
			Behavior:         behavior,
			Source:           "config",
		}
	}
	if hasCatalog {
		for _, catalogModel := range catalogProvider.Models {
			id := strings.TrimSpace(catalogModel.ID)
			if id == "" || disabled[id] {
				continue
			}
			if _, ok := models[id]; ok {
				continue
			}
			model := ruleProvider.Models[id]
			capabilities, behavior := modelroles.BuildFacts(ruleProviderName, ruleProvider, id)
			models[id] = ProviderModelSummary{
				ID:               id,
				DisplayName:      strings.TrimSpace(model.Name),
				DefaultEffort:    strings.TrimSpace(model.DefaultEffort),
				DefaultVariant:   strings.TrimSpace(model.DefaultVariant),
				SupportedEfforts: modelvariant.SupportedEffortsForProvider(ruleProviderName, ruleProvider, id, model),
				Variants:         providerVariantSummaries(modelvariant.SummariesForProvider(ruleProviderName, ruleProvider, id)),
				Capabilities:     capabilities,
				Behavior:         behavior,
				Source:           "models.dev",
			}
		}
	}
	if current != "" {
		if _, ok := models[current]; !ok {
			model := selectedRuleProvider.Models[current]
			capabilities, behavior := modelroles.BuildFacts(selectedRuleProviderName, selectedRuleProvider, current)
			models[current] = ProviderModelSummary{
				ID:               current,
				DisplayName:      strings.TrimSpace(model.Name),
				DefaultEffort:    strings.TrimSpace(model.DefaultEffort),
				DefaultVariant:   strings.TrimSpace(model.DefaultVariant),
				SupportedEfforts: modelvariant.SupportedEffortsForProvider(selectedRuleProviderName, selectedRuleProvider, current, model),
				Variants:         providerVariantSummaries(modelvariant.SummariesForProvider(selectedRuleProviderName, selectedRuleProvider, current)),
				Capabilities:     capabilities,
				Behavior:         behavior,
				Source:           "selected",
			}
		}
	}
	out := make([]ProviderModelSummary, 0, len(models))
	for _, model := range models {
		modelRuleProviderName := ruleProviderName
		modelRuleProvider := ruleProvider
		if model.ID == current {
			modelRuleProviderName = selectedRuleProviderName
			modelRuleProvider = selectedRuleProvider
		}
		if len(model.Variants) == 0 {
			model.Variants = providerVariantSummaries(modelvariant.SummariesForProvider(modelRuleProviderName, modelRuleProvider, model.ID))
		}
		if len(model.SupportedEfforts) == 0 {
			model.SupportedEfforts = modelvariant.EffortIDs(modelVariantSummaries(model.Variants))
		}
		if model.DefaultVariant == "" && model.DefaultEffort != "" {
			model.DefaultVariant = model.DefaultEffort
		}
		if model.DefaultVariant == "" {
			model.DefaultVariant = modelvariant.DefaultVariantForProvider(modelRuleProviderName, modelRuleProvider, model.ID)
		}
		out = append(out, model)
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].ID == current {
			return true
		}
		if out[j].ID == current {
			return false
		}
		return strings.ToLower(out[i].ID) < strings.ToLower(out[j].ID)
	})
	return out
}

func providerVariantSummaries(variants []modelvariant.Variant) []ProviderModelVariantSummary {
	if len(variants) == 0 {
		return nil
	}
	out := make([]ProviderModelVariantSummary, 0, len(variants))
	for _, variant := range variants {
		out = append(out, ProviderModelVariantSummary{
			ID:      variant.ID,
			Options: modelvariant.CloneOptions(variant.Options),
		})
	}
	return out
}

func modelVariantSummaries(variants []ProviderModelVariantSummary) []modelvariant.Variant {
	if len(variants) == 0 {
		return nil
	}
	out := make([]modelvariant.Variant, 0, len(variants))
	for _, variant := range variants {
		out = append(out, modelvariant.Variant{
			ID:      variant.ID,
			Options: modelvariant.CloneOptions(variant.Options),
		})
	}
	return out
}

func isCodexProviderType(providerType string) bool {
	s := strings.ToLower(strings.TrimSpace(providerType))
	s = strings.ReplaceAll(s, "_", "-")
	return s == "openai-codex" || s == "codex-subscription" || s == "chatgpt-codex"
}

func (s *Server) cacheCodexModels(providerName string, models []codex.ModelInfo) {
	providerName = strings.TrimSpace(providerName)
	if s == nil || providerName == "" {
		return
	}
	configs := codexLiveModelConfigs(models)
	s.codexModelsMu.Lock()
	defer s.codexModelsMu.Unlock()
	if s.codexModelCache == nil {
		s.codexModelCache = make(map[string]map[string]config.ProviderModelConfig)
	}
	s.codexModelCache[providerName] = configs
}

func (s *Server) withCachedCodexModels(providerName string, provider config.ProviderConfig) config.ProviderConfig {
	if s == nil || !isCodexProviderType(provider.Type) {
		return provider
	}
	providerName = strings.TrimSpace(providerName)
	if providerName == "" {
		return provider
	}
	s.codexModelsMu.Lock()
	cached := cloneProviderModelConfigs(s.codexModelCache[providerName])
	s.codexModelsMu.Unlock()
	if len(cached) == 0 {
		return provider
	}
	out := provider
	out.Models = cloneProviderModelConfigs(provider.Models)
	if out.Models == nil {
		out.Models = make(map[string]config.ProviderModelConfig, len(cached))
	}
	for id, live := range cached {
		merged := modelcatalog.MergeModelConfig(live, out.Models[id])
		// User removals survive live discovery, just as they survive catalog refreshes.
		merged.Disabled = out.Models[id].Disabled
		// Live Codex model discovery is authoritative for adjustable reasoning
		// levels. Do not retain catalog-only variants that the official model
		// response omitted.
		if len(live.SupportedEfforts) > 0 {
			merged.SupportedEfforts = append([]string(nil), live.SupportedEfforts...)
			merged.Variants = codexReasoningVariants(live.SupportedEfforts)
		}
		out.Models[id] = merged
	}
	return out
}

func codexLiveModelConfigs(models []codex.ModelInfo) map[string]config.ProviderModelConfig {
	out := make(map[string]config.ProviderModelConfig, len(models))
	for _, model := range models {
		id := strings.TrimSpace(model.Slug)
		if id == "" {
			continue
		}
		out[id] = codexLiveModelConfig(model)
		for _, alias := range modelcatalog.CodexSubscriptionModelAliases(id) {
			aliasID := strings.TrimSpace(alias.ID)
			if aliasID == "" {
				continue
			}
			cfg := modelcatalog.MergeModelConfig(modelcatalog.ModelConfig(alias), out[id])
			applyCodexSubscriptionLimit(aliasID, &cfg)
			out[aliasID] = cfg
		}
	}
	return out
}

func codexLiveModelConfig(model codex.ModelInfo) config.ProviderModelConfig {
	id := strings.TrimSpace(model.Slug)
	cfg := codexCatalogFallbackModelConfig(id)
	cfg.ID = id
	if name := strings.TrimSpace(model.DisplayName); name != "" {
		cfg.Name = name
	}
	efforts := normalizedCodexEfforts(model.SupportedReasoning)
	if len(efforts) > 0 || strings.TrimSpace(model.DefaultReasoningLevel) != "" {
		reasoning := true
		cfg.Reasoning = &reasoning
	}
	if len(efforts) > 0 {
		cfg.SupportedEfforts = efforts
		cfg.Variants = codexReasoningVariants(efforts)
	}
	if effort := strings.TrimSpace(model.DefaultReasoningLevel); effort != "" {
		cfg.DefaultEffort = effort
		cfg.DefaultVariant = effort
	}
	applyCodexSubscriptionLimit(id, &cfg)
	return cfg
}

func codexCatalogModelIDs(provider config.ProviderConfig) []string {
	seen := map[string]bool{}
	out := make([]string, 0, len(provider.Models)+1)
	add := func(id string) {
		id = strings.TrimSpace(id)
		if id == "" || seen[id] {
			return
		}
		seen[id] = true
		out = append(out, id)
	}
	add(provider.Model)
	for id, model := range provider.Models {
		add(id)
		add(model.ID)
	}
	return out
}

func codexModelSummaries(models []codex.ModelInfo) []CodexModelSummary {
	out := make([]CodexModelSummary, 0, len(models))
	seen := map[string]bool{}
	for _, model := range models {
		id := strings.TrimSpace(model.Slug)
		if id == "" || seen[id] {
			continue
		}
		seen[id] = true
		out = append(out, codexModelSummaryFromLive(model))
		for _, alias := range modelcatalog.CodexSubscriptionModelAliases(id) {
			aliasID := strings.TrimSpace(alias.ID)
			if aliasID == "" || seen[aliasID] {
				continue
			}
			seen[aliasID] = true
			out = append(out, codexModelSummaryFromCatalogAlias(alias, model))
		}
	}
	return out
}

func codexModelSummaryFromLive(model codex.ModelInfo) CodexModelSummary {
	return CodexModelSummary{
		Slug:                  model.Slug,
		DisplayName:           model.DisplayName,
		DefaultReasoningLevel: model.DefaultReasoningLevel,
		SupportedReasoning:    normalizedCodexEfforts(model.SupportedReasoning),
		SupportedInAPI:        model.SupportedInAPI,
	}
}

func codexModelSummaryFromCatalogAlias(alias modelcatalog.Model, base codex.ModelInfo) CodexModelSummary {
	efforts := normalizedCodexEfforts(base.SupportedReasoning)
	if len(efforts) == 0 {
		efforts = codexReasoningEffortsFromCatalog(alias)
	}
	return CodexModelSummary{
		Slug:                  strings.TrimSpace(alias.ID),
		DisplayName:           strings.TrimSpace(alias.Name),
		DefaultReasoningLevel: base.DefaultReasoningLevel,
		SupportedReasoning:    efforts,
		SupportedInAPI:        base.SupportedInAPI,
	}
}

func codexReasoningEffortsFromCatalog(model modelcatalog.Model) []string {
	for _, option := range model.ReasoningOptions {
		if kind, _ := option["type"].(string); strings.TrimSpace(kind) != "effort" {
			continue
		}
		switch values := option["values"].(type) {
		case []any:
			out := make([]string, 0, len(values))
			for _, value := range values {
				if effort, ok := value.(string); ok {
					out = append(out, effort)
				}
			}
			return normalizedCodexEfforts(out)
		case []string:
			return normalizedCodexEfforts(values)
		}
	}
	return nil
}

func codexCatalogFallbackModelConfig(modelID string) config.ProviderModelConfig {
	provider, ok := modelcatalog.MatchProvider("openai", config.ProviderConfig{Type: "openai"})
	if !ok {
		return config.ProviderModelConfig{ID: modelID}
	}
	for _, model := range provider.Models {
		if strings.TrimSpace(model.ID) == modelID {
			return modelcatalog.ModelConfig(model)
		}
	}
	return config.ProviderModelConfig{ID: modelID}
}

func applyCodexSubscriptionLimit(modelID string, cfg *config.ProviderModelConfig) {
	if cfg == nil {
		return
	}
	inputCap := modelbudget.CodexSubscriptionInputCap(modelID, "openai-codex")
	if inputCap <= 0 {
		return
	}
	contextCap := modelbudget.CodexSubscriptionContextWindow(modelID, "openai-codex")
	contextWindow := 0
	if cfg.ContextWindow > 0 {
		contextWindow = cfg.ContextWindow
	}
	inputLimit := inputCap
	outputLimit := 128_000
	if cfg.Limit != nil {
		if cfg.Limit.Context > 0 && (contextWindow <= 0 || cfg.Limit.Context < contextWindow) {
			contextWindow = cfg.Limit.Context
		}
		if cfg.Limit.Input > 0 && cfg.Limit.Input < inputLimit {
			inputLimit = cfg.Limit.Input
		}
		if cfg.Limit.Output > 0 && cfg.Limit.Output < outputLimit {
			outputLimit = cfg.Limit.Output
		}
	}
	if contextWindow <= 0 {
		contextWindow = contextCap
	}
	if contextCap > 0 && (contextWindow <= 0 || contextWindow > contextCap) {
		contextWindow = contextCap
	}
	if contextWindow <= 0 {
		return
	}
	cfg.ContextWindow = contextWindow
	cfg.Limit = &config.ProviderModelLimitConfig{
		Context: contextWindow,
		Input:   inputLimit,
		Output:  outputLimit,
	}
}

func normalizedCodexEfforts(values []string) []string {
	seen := map[string]bool{}
	out := make([]string, 0, len(values))
	for _, value := range values {
		value = strings.TrimSpace(value)
		// The catalog can expose non-standard effort names that the provider API
		// rejects when sent verbatim. Do not advertise those as request variants.
		if value == "" || strings.EqualFold(value, "ultra") || seen[value] {
			continue
		}
		seen[value] = true
		out = append(out, value)
	}
	return out
}

func codexReasoningVariants(efforts []string) map[string]map[string]any {
	if len(efforts) == 0 {
		return nil
	}
	out := make(map[string]map[string]any, len(efforts))
	for _, effort := range efforts {
		effort = strings.TrimSpace(effort)
		if effort == "" {
			continue
		}
		out[effort] = map[string]any{
			"reasoningEffort":  effort,
			"reasoningSummary": "auto",
			"include":          []any{"reasoning.encrypted_content"},
		}
	}
	if len(out) == 0 {
		return nil
	}
	return out
}

func cloneProviderModelConfigs(input map[string]config.ProviderModelConfig) map[string]config.ProviderModelConfig {
	if len(input) == 0 {
		return nil
	}
	out := make(map[string]config.ProviderModelConfig, len(input))
	for key, value := range input {
		out[key] = value
	}
	return out
}

func stringValue(value *string) string {
	if value == nil {
		return ""
	}
	return *value
}

func explicitProviderAPIKey(provider config.ProviderConfig) string {
	if key := strings.TrimSpace(provider.APIKey); key != "" {
		return key
	}
	if envKey := strings.TrimSpace(provider.APIKeyEnv); envKey != "" {
		return strings.TrimSpace(os.Getenv(envKey))
	}
	return ""
}
