package pluginprotocol

import (
	"encoding/json"

	"github.com/blueberrycongee/wuu/internal/enginecatalog"
)

// EngineDescription is provider-owned inventory metadata. BinaryOK describes
// readiness, including providers which do not require a local executable.
type EngineDescription struct {
	Enabled         *bool                      `json:"enabled,omitempty"`
	DisplayName     string                     `json:"display_name,omitempty"`
	Protocol        string                     `json:"protocol,omitempty"`
	InstallURL      string                     `json:"install_url,omitempty"`
	UpdateURL       string                     `json:"update_url,omitempty"`
	CLIVersion      string                     `json:"cli_version,omitempty"`
	BinaryPath      string                     `json:"binary_path,omitempty"`
	BinaryOK        bool                       `json:"binary_ok"`
	Error           string                     `json:"error,omitempty"`
	PermissionModes []EnginePermissionModeInfo `json:"permission_modes,omitempty"`
}

type EnginePermissionModeInfo struct {
	Mode  string `json:"mode"`
	ID    string `json:"id,omitempty"`
	Label string `json:"label,omitempty"`
}

type EngineModelInfo struct {
	Options          []enginecatalog.ModelOption `json:"options,omitempty"`
	ResolvedModel    string                      `json:"resolved_model,omitempty"`
	FastMode         bool                        `json:"fast_mode,omitempty"`
	DefaultSpeed     string                      `json:"default_speed,omitempty"`
	ID               string                      `json:"id"`
	DisplayName      string                      `json:"display_name,omitempty"`
	DefaultEffort    string                      `json:"default_effort,omitempty"`
	SupportedEfforts []string                    `json:"supported_efforts,omitempty"`
	IsDefault        bool                        `json:"is_default,omitempty"`
}

type EngineModelsRequest struct {
	CWD     string `json:"cwd,omitempty"`
	Refresh bool   `json:"refresh,omitempty"`
}

// Models errors accompany stale/partial results; transport failures remain errors.
type EngineModelsResult struct {
	Models          []EngineModelInfo          `json:"models"`
	PermissionModes []EnginePermissionModeInfo `json:"permission_modes,omitempty"`
	Status          string                     `json:"status"`
	Error           string                     `json:"error,omitempty"`
}

type EngineAuthRequest struct {
	MethodID string `json:"method_id,omitempty"`
	// Remote callers cannot invoke reserved local-only sign-in methods.
	Remote bool `json:"remote,omitempty"`
}
type EngineAuthMethod struct {
	ID          string `json:"id"`
	Name        string `json:"name"`
	Description string `json:"description,omitempty"`
	Type        string `json:"type,omitempty"`
}
type EngineAuthResult struct {
	Methods       []EngineAuthMethod `json:"methods"`
	Authenticated bool               `json:"authenticated"`
}
type EngineQuotaResult struct {
	Quota     json.RawMessage `json:"quota"`
	ErrorCode string          `json:"error_code,omitempty"`
}

// HistoryFork never permits reconstructing hidden native state by transcript replay.
// EngineHistoryMessage contains native identities, not a transcript replay.
type EngineHistoryMessage struct {
	Role           string `json:"role"`
	EngineTurnID   string `json:"engine_turn_id,omitempty"`
	ProviderItemID string `json:"provider_item_id,omitempty"`
	Steered        bool   `json:"steered,omitempty"`
}

type EngineHistoryForkRequest struct {
	Ref       string                 `json:"ref"`
	CWD       string                 `json:"cwd"`
	Retained  []EngineHistoryMessage `json:"retained"`
	Removed   []EngineHistoryMessage `json:"removed"`
	WholeTurn bool                   `json:"whole_turn"`
}
type EngineHistoryForkResult struct {
	Ref string `json:"ref"`
}
