package runtime

import (
	"context"
	"errors"
	"fmt"
	"github.com/blueberrycongee/wuu/internal/config"
	"github.com/blueberrycongee/wuu/internal/executionenv"
	"github.com/blueberrycongee/wuu/internal/mcp"
	pluginpkg "github.com/blueberrycongee/wuu/internal/plugin"
	"github.com/blueberrycongee/wuu/internal/tools"
	"github.com/blueberrycongee/wuu/internal/userspace"
	"github.com/blueberrycongee/wuu/internal/workspaces"
	"maps"
	"path/filepath"
)

func (s *Session) UserBinding() userspace.Binding {
	return userspace.Binding{UserSpaceID: s.UserSpaceID, ProductDomain: s.ProductDomain}
}

// This one-way tool restriction is defense in depth, not proof of two-way host
// isolation. Legacy Coding tools keep their existing permissions. The default
// user-host contract isolates memory APIs, not same-identity OS/file access.
func memoryProtectedPaths(home string, binding userspace.Binding) []string {
	if !binding.IsUser() {
		return nil
	}
	return []string{binding.State(home), filepath.Join(home, "memory"), filepath.Join(home, "working-notes"), filepath.Join(home, "sessions"), filepath.Join(home, "workspaces"), filepath.Join(home, "log"), filepath.Join(home, "plugin-storage"), filepath.Join(home, "agents"), filepath.Join(home, "scratch")}
}

func runtimeFileScopeRoots(root, home string, binding userspace.Binding, extras ...string) []string {
	if binding.IsUser() {
		return []string{root}
	}
	return workspaces.BoundaryRoots(root, home, extras...)
}

// UserHostIsolationMode selects the assurance required at activation. The zero
// value uses logical memory-domain isolation for explicitly bound user hosts.
// No mode changes legacy Coding execution permissions.
type UserHostIsolationMode string

const (
	UserHostIsolationLogicalMemory UserHostIsolationMode = "logical_memory"
	UserHostIsolationVerifiedHost  UserHostIsolationMode = "verified_host"
)

func resolveUserHostIsolation(ctx context.Context, binding userspace.Binding, home string, mode UserHostIsolationMode, verifier UserHostIsolationVerifier) (UserHostIsolationMode, error) {
	if !binding.IsUser() {
		if mode != "" {
			return "", fmt.Errorf("user host isolation mode requires an explicit user binding")
		}
		return "", nil
	}
	switch mode {
	case "", UserHostIsolationLogicalMemory:
		return UserHostIsolationLogicalMemory, nil
	case UserHostIsolationVerifiedHost:
		if err := VerifyUserHostIsolation(ctx, binding, home, verifier); err != nil {
			return "", err
		}
		return UserHostIsolationVerifiedHost, nil
	default:
		return "", fmt.Errorf("unsupported user host isolation mode %q", mode)
	}
}

const UserHostIsolationRequiredCode = "user_host_isolation_required"

// UserHostIsolationVerifier is supplied only by a trusted embedding host. It
// must verify that other product hosts cannot read this user's data, including
// through unrestricted subprocesses, browser bridges, extensions and old
// sessions. A configuration flag or caller assertion is not sufficient.
// Only the explicit verified_host mode requires this stronger boundary. Logical
// memory isolation does not attest to subprocess or filesystem confidentiality.
type UserHostIsolationVerifier interface {
	VerifyUserHostIsolation(context.Context, userspace.Binding, string) error
}

func VerifyUserHostIsolation(ctx context.Context, binding userspace.Binding, home string, verifier UserHostIsolationVerifier) error {
	if !binding.IsUser() {
		return nil
	}
	if verifier == nil {
		return fmt.Errorf(UserHostIsolationRequiredCode + ": verified_host mode requires verified host isolation; unrestricted Coding processes sharing this OS identity can read Personal data. Configure a verified separate host identity before activation")
	}
	if err := verifier.VerifyUserHostIsolation(ctx, binding, home); err != nil {
		return fmt.Errorf(UserHostIsolationRequiredCode+": verified host isolation unavailable: %w", err)
	}
	return nil
}

func userHostConfig(cfg config.Config, binding userspace.Binding, shared bool) config.Config {
	if !binding.IsUser() {
		return cfg
	}
	cfg.Hooks = nil

	if !shared {
		cfg.MCPServers = nil
	} else {
		preferences := make(map[string]config.MCPServerConfig)
		for name, server := range cfg.MCPServers {
			if IsPluginMCPServerName(name) {
				preferences[name] = config.MCPServerConfig{Enabled: server.Enabled, ToolTimeoutMS: server.ToolTimeoutMS}
			}
		}
		cfg.MCPServers = preferences
	}
	cfg.MCPJson = nil
	cfg.ExecutionEnvironments = executionenv.Config{}
	return cfg
}

// ProductDataRoot owns histories, summaries, records, artifacts and caches.
// WuuHome remains the shared settings/plugin installation root only.
func (s *Session) ProductDataRoot() string {
	if s.DataRoot != "" {
		return s.DataRoot
	}
	return s.WuuHome
}

func userHostWorkspaceStateDir(home, root string, binding userspace.Binding) (string, error) {
	return resolveWorkspaceStateDir(binding.State(home), "", root)
}

// Discovery and file access share the owning runtime's user-space boundary.
func configureToolkitWorkspaceScope(kit *tools.Toolkit, home string, binding userspace.Binding, extras ...string) {
	kit.SetFileScopeRoots(runtimeFileScopeRoots(kit.RootDir(), home, binding, extras...))
	var catalog []workspaces.Workspace
	if !binding.IsUser() {
		catalog, _ = workspaces.Available(home)
	}
	kit.SetRegisteredWorkspaces(catalog)
}

// sharedConnectorInputs limits the shared user-host surface to installed global
// connector declarations and their saved preferences. Project-owned packages and
// arbitrary MCP server definitions never cross the user-host boundary.
func sharedConnectorInputs(cfg config.Config, plugins []pluginpkg.Plugin) (config.Config, []pluginpkg.Plugin) {
	cfg.MCPServers = maps.Clone(cfg.MCPServers)
	allowed := make(map[string]bool)
	filtered := make([]pluginpkg.Plugin, 0, len(plugins))
	for _, item := range plugins {
		if item.Source != "user" && item.Source != "bundled" {
			continue
		}
		servers := make(map[string]config.MCPServerConfig)
		for name, definition := range item.MCPServers {
			if definition.Connector == nil {
				continue
			}
			servers[name] = definition
			allowed[PluginMCPServerName(item.ID, name)] = true
		}
		if len(servers) == 0 {
			continue
		}
		item.MCPServers = servers
		filtered = append(filtered, item)
	}
	for name := range cfg.MCPServers {
		if !allowed[name] {
			delete(cfg.MCPServers, name)
		}
	}
	return cfg, filtered
}

func validateSharedConnectorPreference(cfg config.Config, item pluginpkg.Plugin, name string) error {
	for localName, definition := range item.MCPServers {
		if PluginMCPServerName(item.ID, localName) != name {
			continue
		}
		if definition.Connector == nil || (item.Source != "user" && item.Source != "bundled") {
			break
		}
		if cfg.Extensions != nil {
			if !cfg.Extensions.IsEnabled(item.SubjectID, PluginEnabledByDefault(cfg, item)) || !pluginManagementTrusted(*cfg.Extensions, item) {
				break
			}
		} else if !item.Official || !PluginEnabledByDefault(cfg, item) {
			break
		}
		if override, ok := cfg.MCPServers[name]; ok && override.Enabled != nil {
			definition.Enabled = override.Enabled
		}
		if definition.ConnectorEnabled() {
			return nil
		}
		break
	}
	return errors.New("shared MCP connector is disabled or no longer authorized")
}

// Shared connector calls recheck the selected configuration and the active
// package identity immediately before dispatch; an old tool cannot acquire a
// replacement package's authority.
func (s *Session) bindSharedMCPAdmission(manager *mcp.Manager, plugins []pluginpkg.Plugin) {
	if manager == nil {
		return
	}
	owners := make(map[string]pluginpkg.Plugin)
	for _, item := range plugins {
		for name := range item.MCPServers {
			owners[PluginMCPServerName(item.ID, name)] = item
		}
	}
	manager.SetCallAdmission(func(ctx context.Context, name string) error {
		item, ok := owners[name]
		if !ok {
			if !s.UserBinding().IsUser() {
				return nil
			}
			return errors.New("shared MCP connector is unavailable")
		}
		active := false
		for _, current := range s.ExtensionSnapshot().ActivePlugins {
			if current.ID == item.ID && current.Source == item.Source && current.Fingerprint == item.Fingerprint && current.SubjectID == item.SubjectID {
				active = true
				break
			}
		}
		if !active {
			return errors.New("shared MCP connector owner changed")
		}
		services, release, err := s.RetainStateServices()
		if err != nil {
			return err
		}
		defer release()
		view, err := services.Config.Read(ctx)
		if err != nil {
			return err
		}
		return validateSharedConnectorPreference(view.Config, item, name)
	})
}
