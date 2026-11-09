package config

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"

	"github.com/blueberrycongee/wuu/internal/securefs"
	"github.com/blueberrycongee/wuu/internal/statepath"
)

// migrateLegacyGlobalStore performs a one-time, non-destructive copy of the
// user's pre-unification global config into the unified wuu home. It is
// idempotent and best-effort: if the new file already exists nothing happens,
// and any failure leaves the legacy file in place so callers can still fall
// back to the legacy read path. The legacy files are never deleted.
func migrateLegacyGlobalStore(home string) {
	if home == "" {
		return
	}
	if newPath, err := statepath.ConfigPath(home); err == nil {
		migrateGlobalFile(newPath, statepath.LegacyConfigPath(home), securefs.FileMode, "config.json")
	}
}

// migrateGlobalFile copies legacyPath to newPath when newPath is missing and
// legacyPath exists, preserving the requested permissions and printing a
// single migration notice to stderr. It is a no-op when the new file already
// exists, when the legacy file is absent, or when either path is empty.
func migrateGlobalFile(newPath, legacyPath string, perm os.FileMode, label string) {
	if newPath == "" || legacyPath == "" || newPath == legacyPath {
		return
	}
	if _, err := os.Stat(newPath); err == nil {
		return // new location already populated; never overwrite it
	} else if !errors.Is(err, os.ErrNotExist) {
		return // unexpected stat error; stay safe and skip migration
	}
	data, err := os.ReadFile(legacyPath)
	if err != nil {
		return // no legacy file to migrate
	}
	if err := os.MkdirAll(filepath.Dir(newPath), securefs.DirMode); err != nil {
		return
	}
	if err := os.WriteFile(newPath, data, perm); err != nil {
		return
	}
	// WriteFile applies perm subject to umask; re-assert it explicitly so a
	// migrated credential store keeps its 0600 mode regardless of umask.
	_ = os.Chmod(newPath, perm)
	fmt.Fprintf(os.Stderr, "wuu: migrated %s to %s (legacy copy kept at %s)\n", label, newPath, legacyPath)
}

// Temporary compatibility workaround: the removed bundled account-connectors
// plugin stored enable preferences under plugin-owned names. Carry an explicit
// enable over as an ordinary global server with the same endpoint. Remove this
// once releases that shipped account-connectors are no longer supported.
func migrateAccountConnectorServers(path string) {
	const prefix = "plugin.account-connectors."
	endpoints := map[string]string{
		"notion":    "https://mcp.notion.com/mcp",
		"linear":    "https://mcp.linear.app/mcp",
		"atlassian": "https://mcp.atlassian.com/v2/mcp",
		"sentry":    "https://mcp.sentry.dev/mcp",
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return
	}
	var root map[string]json.RawMessage
	if json.Unmarshal(data, &root) != nil {
		return
	}
	servers := map[string]json.RawMessage{}
	if root["mcp_servers"] != nil && json.Unmarshal(root["mcp_servers"], &servers) != nil {
		return
	}
	changed := dropAccountConnectorsModule(root)
	for name, raw := range servers {
		local, legacy := strings.CutPrefix(name, prefix)
		if !legacy {
			continue
		}
		delete(servers, name)
		changed = true
		var preference struct {
			Enabled       *bool           `json:"enabled"`
			ToolTimeoutMS json.RawMessage `json:"tool_timeout_ms,omitempty"`
		}
		endpoint, known := endpoints[local]
		if _, exists := servers[local]; exists || !known || json.Unmarshal(raw, &preference) != nil || preference.Enabled == nil || !*preference.Enabled {
			continue
		}
		definition, err := json.Marshal(struct {
			URL           string          `json:"url"`
			Transport     string          `json:"transport"`
			ToolTimeoutMS json.RawMessage `json:"tool_timeout_ms,omitempty"`
		}{endpoint, "http", preference.ToolTimeoutMS})
		if err != nil {
			return
		}
		servers[local] = definition
	}
	if !changed {
		return
	}
	if len(servers) > 0 || root["mcp_servers"] != nil {
		if root["mcp_servers"], err = json.Marshal(servers); err != nil {
			return
		}
	}
	next, err := json.MarshalIndent(root, "", "  ")
	if err != nil {
		return
	}
	_ = securefs.WriteFileAtomic(path, next)
}

// A saved composition choice for the removed package would otherwise select an
// unavailable module and stop the core from starting.
func dropAccountConnectorsModule(root map[string]json.RawMessage) bool {
	const id = "account-connectors"
	var composition map[string]json.RawMessage
	if root["composition"] == nil || json.Unmarshal(root["composition"], &composition) != nil {
		return false
	}
	changed := false
	var patches []map[string]json.RawMessage
	if composition["patches"] != nil && json.Unmarshal(composition["patches"], &patches) == nil {
		for _, patch := range patches {
			var modules map[string]json.RawMessage
			if patch["modules"] == nil || json.Unmarshal(patch["modules"], &modules) != nil {
				continue
			}
			if _, exists := modules[id]; !exists {
				continue
			}
			delete(modules, id)
			if raw, err := json.Marshal(modules); err == nil {
				patch["modules"], changed = raw, true
			}
		}
		if changed {
			if raw, err := json.Marshal(patches); err == nil {
				composition["patches"] = raw
			}
		}
	}
	var modules []string
	if composition["modules"] != nil && json.Unmarshal(composition["modules"], &modules) == nil {
		kept := slices.DeleteFunc(slices.Clone(modules), func(module string) bool { return module == id })
		if len(kept) != len(modules) {
			if raw, err := json.Marshal(kept); err == nil {
				composition["modules"], changed = raw, true
			}
		}
	}
	if !changed {
		return false
	}
	raw, err := json.Marshal(composition)
	if err != nil {
		return false
	}
	root["composition"] = raw
	return true
}
