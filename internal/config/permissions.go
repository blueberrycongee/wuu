package config

import "strings"

const (
	PermissionModeStandard   = "standard"
	PermissionModeReadOnly   = "read_only"
	PermissionModeUnconfined = "unconfined"
)

// ResolvedPermissions is now just the mode. There is no profile, approval
// policy, or reviewer in the authority model.
type ResolvedPermissions struct {
	Mode string `json:"mode,omitempty"`
}

func NormalizePermissionMode(mode string) string {
	switch strings.TrimSpace(mode) {
	case PermissionModeReadOnly:
		return PermissionModeReadOnly
	case PermissionModeUnconfined:
		return PermissionModeUnconfined
	case "", PermissionModeStandard:
		return PermissionModeStandard
	default:
		return PermissionModeStandard
	}
}

func ResolveAgentPermissions(agent AgentConfig) ResolvedPermissions {
	return ResolvedPermissions{Mode: NormalizePermissionMode(agent.PermissionMode)}
}
