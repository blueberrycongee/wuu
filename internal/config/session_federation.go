package config

import (
	"errors"
	"fmt"
	"strings"
)

// SessionFederationConfig is a host-owned maximum execution surface. It grants
// no access by itself: resource.authorization must allow every inbound action.
type SessionFederationConfig struct {
	Enabled         bool                `json:"enabled"`
	HostID          string              `json:"host_id"`
	StorageHost     string              `json:"storage_host,omitempty"`
	Models          map[string][]string `json:"models"`
	PermissionModes []string            `json:"permission_modes,omitempty"`
	ToolAllow       []string            `json:"tool_allow"`
	ToolDeny        []string            `json:"tool_deny,omitempty"`
}

func (c *SessionFederationConfig) Validate() error {
	if c == nil || !c.Enabled {
		return nil
	}
	if strings.TrimSpace(c.HostID) == "" || c.HostID == "local" || strings.TrimSpace(c.HostID) != c.HostID {
		return errors.New("session_federation.host_id must be a stable non-local host identifier")
	}
	for _, mode := range c.PermissionModes {
		if mode != PermissionModeReadOnly && mode != PermissionModeStandard && mode != PermissionModeUnconfined {
			return fmt.Errorf("invalid session_federation permission mode %q", mode)
		}
	}
	for provider, models := range c.Models {
		if strings.TrimSpace(provider) == "" {
			return errors.New("session_federation model provider is required")
		}
		for _, model := range models {
			if strings.TrimSpace(model) == "" {
				return errors.New("session_federation model is required")
			}
		}
	}
	for _, tool := range append(append([]string(nil), c.ToolAllow...), c.ToolDeny...) {
		if strings.TrimSpace(tool) == "" {
			return errors.New("session_federation tool names must be nonempty")
		}
	}
	return nil
}
