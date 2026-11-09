package extensions

import (
	"fmt"
	"strings"
)

// Closed permission catalog. Manifests that request unknown permissions fail
// validation/activation rather than being silently ignored.
const (
	PermProcessSpawn         = "process.spawn"
	PermSessionRead          = "session.read"
	PermSessionWrite         = "session.write"
	PermToolsDefine          = "tools.define"
	PermToolsIntercept       = "tools.intercept"
	PermShellEnv             = "shell.env"
	PermNetworkConnect       = "network.connect"
	PermCommandsExecute      = "commands.execute"
	PermFilesRead            = "files.read"
	PermFilesWrite           = "files.write"
	PermAccessibilityRead    = "accessibility.read"
	PermAccessibilityControl = "accessibility.control"
	PermScreenCapture        = "screen.capture"
	PermAppActivate          = "app.activate"
	PermInputSynthesize      = "input.synthesize"
)

var knownPermissions = map[string]struct{}{
	PermProcessSpawn:         {},
	PermSessionRead:          {},
	PermSessionWrite:         {},
	PermToolsDefine:          {},
	PermToolsIntercept:       {},
	PermShellEnv:             {},
	PermNetworkConnect:       {},
	PermCommandsExecute:      {},
	PermFilesRead:            {},
	PermFilesWrite:           {},
	PermAccessibilityRead:    {},
	PermAccessibilityControl: {},
	PermScreenCapture:        {},
	PermAppActivate:          {},
	PermInputSynthesize:      {},
}

var permissionAliases = map[string]string{
	"network":          PermNetworkConnect,
	"filesystem.read":  PermFilesRead,
	"filesystem.write": PermFilesWrite,
}

// NormalizePermissions canonicalizes legacy aliases, removes duplicates, and
// rejects names outside the closed permission catalog.
func NormalizePermissions(permissions []string) ([]string, error) {
	canonical := make([]string, 0, len(permissions))
	for _, permission := range permissions {
		permission = strings.TrimSpace(permission)
		if alias, ok := permissionAliases[permission]; ok {
			permission = alias
		}
		if permission == "" {
			continue
		}
		if !IsKnownPermission(permission) {
			return nil, fmt.Errorf("unknown permission %q", permission)
		}
		canonical = append(canonical, permission)
	}
	return normalizedStrings(canonical), nil
}

// IsKnownPermission reports whether name is a recognized permission in the
// closed catalog.
func IsKnownPermission(name string) bool {
	_, ok := knownPermissions[strings.TrimSpace(name)]
	return ok
}

// ValidatePermissions returns an error if any non-empty permission is not in
// the closed catalog.
func ValidatePermissions(permissions []string) error {
	_, err := NormalizePermissions(permissions)
	return err
}

// RequiredPermissionsForRuntime returns the permissions required to start the
// declared external runtime process.
func RequiredPermissionsForRuntime(spec *RuntimeSpec) []string {
	if spec == nil || strings.TrimSpace(spec.Protocol) == "" {
		return nil
	}
	return []string{PermProcessSpawn}
}

// RequiredPermissionsForMCPServer returns the permissions required to connect or
// spawn a declared MCP server.
func RequiredPermissionsForMCPServer(spec *MCPServerSpec) []string {
	if spec == nil {
		return nil
	}
	var out []string
	if strings.TrimSpace(spec.Command) != "" {
		out = append(out, PermProcessSpawn)
	}
	if strings.TrimSpace(spec.URL) != "" {
		out = append(out, PermNetworkConnect)
	}
	return out
}

// RequiredPermissionsForHook returns the permissions required to register a
// single hook entry. Command hooks spawn a process; prompt hooks observe and
// transform payloads.
func RequiredPermissionsForHook(entry *HookEntry) []string {
	if entry == nil {
		return nil
	}
	switch strings.ToLower(strings.TrimSpace(entry.Type)) {
	case "", "command":
		if strings.TrimSpace(entry.Command) != "" {
			return []string{PermProcessSpawn}
		}
	case "prompt":
		if strings.TrimSpace(entry.Prompt) != "" {
			return []string{PermSessionRead, PermSessionWrite}
		}
	}
	return nil
}

// RequiredPermissionsForCommand returns the permissions required to execute a
// command surface. prompt_template commands are host-rendered and require no
// runtime permission; runtime_action declares command execution permission
// before an approved desktop generation can activate it.
func RequiredPermissionsForCommand(spec *CommandSpec) []string {
	if spec == nil {
		return nil
	}
	switch strings.ToLower(strings.TrimSpace(string(spec.Kind))) {
	case string(CommandKindRuntimeAction):
		return []string{PermCommandsExecute}
	}
	return nil
}

// RequiredPermissionsForSkills returns no permissions: skill directories are
// host-read declarative data and do not grant code execution by themselves.
func RequiredPermissionsForSkills(skills []string) []string {
	return nil
}

// RequiredPermissionsForPackageSpec returns the union of all required
// permissions derived from a package spec.
func RequiredPermissionsForPackageSpec(spec PackageSpec) []string {
	var out []string
	out = append(out, RequiredPermissionsForRuntime(spec.Runtime)...)
	for _, server := range spec.MCPServers {
		out = append(out, RequiredPermissionsForMCPServer(&server)...)
	}
	for _, entries := range spec.Hooks {
		for _, entry := range entries {
			out = append(out, RequiredPermissionsForHook(&entry)...)
		}
	}
	for _, cmd := range spec.Commands {
		out = append(out, RequiredPermissionsForCommand(&cmd)...)
	}
	out = append(out, RequiredPermissionsForSkills(spec.Skills)...)
	return normalizedStrings(out)
}
