package session

import (
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	"github.com/blueberrycongee/wuu/internal/config"
)

// ProjectPresetSnapshot is resolved once at project creation. Each managed
// session carries the complete snapshot so recovery never depends on live
// settings or on a coordinator that may have been archived or deleted.
type ProjectPresetSnapshot struct {
	Mode string `json:"mode"`
	config.ProjectPresetConfig
}

func projectPresetJSON(preset *ProjectPresetSnapshot) string {
	if preset == nil {
		return ""
	}
	// The snapshot contains only strings and structs, so encoding cannot fail.
	encoded, _ := json.Marshal(preset)
	return string(encoded)
}

func (s Session) validateProjectPreset() error {
	if s.ProjectPreset == nil {
		return nil
	}
	preset := s.ProjectPreset
	if !config.ValidProjectPresetMode(preset.Mode) {
		return fmt.Errorf("invalid project preset mode %q", preset.Mode)
	}
	for role, selection := range map[string]config.ModelRoleConfig{"lead": preset.Lead, "side": preset.Side, "worker": preset.Worker} {
		if strings.TrimSpace(selection.Provider) == "" || strings.TrimSpace(selection.Model) == "" {
			return fmt.Errorf("project preset %s requires %s provider and model", preset.Mode, role)
		}
	}
	if s.EngineID != "" && s.EngineID != "wuu" {
		return errors.New("project presets require the Wuu engine")
	}
	selection := preset.Lead
	if s.Source == "project-session" {
		selection = preset.Worker
		if s.ProjectRole == "side" {
			selection = preset.Side
		}
	} else if s.Source != "project" {
		return errors.New("project preset requires a project session")
	}
	if s.Provider != selection.Provider || s.Model != selection.Model || s.Variant != selection.Variant || s.Effort != selection.Effort {
		return errors.New("project preset role models are locked at creation")
	}
	return nil
}
