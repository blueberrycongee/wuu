package externalengine

import (
	"encoding/json"
	"fmt"
	"strconv"
	"strings"

	"github.com/blueberrycongee/wuu/internal/enginecatalog"
)

// DiscoveredModel is one model an ACP agent advertised on session/new.
type DiscoveredModel struct {
	ID               string
	DisplayName      string
	DefaultEffort    string
	SupportedEfforts []string
	IsDefault        bool
	FastMode         bool
	DefaultSpeed     string
	Options          []enginecatalog.ModelOption
}

type acpConfigOption struct {
	ID       string            `json:"id"`
	Name     string            `json:"name"`
	Category string            `json:"category"`
	Type     string            `json:"type"`
	Current  string            `json:"currentValue"`
	Options  []acpConfigChoice `json:"options"`
}

// Some agents expose boolean configuration alongside ACP select options.
func (o *acpConfigOption) UnmarshalJSON(data []byte) error {
	type option acpConfigOption
	var wire struct {
		*option
		Current json.RawMessage `json:"currentValue"`
	}
	wire.option = (*option)(o)
	if err := json.Unmarshal(data, &wire); err != nil {
		return err
	}
	if len(wire.Current) == 0 {
		return nil
	}
	if o.Type == "boolean" {
		var value bool
		if err := json.Unmarshal(wire.Current, &value); err != nil {
			return err
		}
		o.Current = strconv.FormatBool(value)
		return nil
	}
	if err := json.Unmarshal(wire.Current, &o.Current); err != nil {
		return fmt.Errorf("configuration %q: %w", o.ID, err)
	}
	return nil
}

func (s acpSession) modelOptions() []enginecatalog.ModelOption {
	var options []enginecatalog.ModelOption
	speed, _, _ := s.speedOption()
	for _, option := range s.ConfigOptions {
		if option.ID == "" || speed != nil && option.ID == speed.ID {
			continue
		}
		if option.Category != "" && option.Category != "model_config" {
			continue
		}
		if option.Type != "select" && option.Type != "boolean" {
			continue
		}
		label := option.Name
		if label == "" {
			label = option.ID
		}
		item := enginecatalog.ModelOption{ID: option.ID, Label: label, Type: option.Type, DefaultValue: option.Current, Choices: []enginecatalog.ModelChoice{}}
		if option.Type == "boolean" {
			item.Choices = []enginecatalog.ModelChoice{{Value: "false", Label: "Off"}, {Value: "true", Label: "On"}}
		} else {
			for _, choice := range option.choices() {
				label := choice.Name
				if label == "" {
					label = choice.Value
				}
				item.Choices = append(item.Choices, enginecatalog.ModelChoice{Value: choice.Value, Label: label})
			}
		}
		options = append(options, item)
	}
	return options
}

func modelsFromACPSession(session acpSession) []DiscoveredModel {
	models := modelsFromConfigOptions(session.ConfigOptions)
	if session.modelConfigOption() == nil {
		models = modelsFromFirstClass(session)
	}
	for i := range models {
		if !models[i].IsDefault {
			continue
		}
		models[i].Options = session.modelOptions()
		efforts, current := thoughtLevelFromOptions(session.ConfigOptions)
		models[i] = attachEfforts(models[i:i+1], efforts, current)[0]
		option, on, _ := session.speedOption()
		if option != nil {
			models[i].FastMode, models[i].DefaultSpeed = true, "standard"
			if option.Current == on {
				models[i].DefaultSpeed = "fast"
			}
		}
	}
	return models
}

func thoughtLevelFromOptions(options []acpConfigOption) (efforts []string, current string) {
	for _, option := range options {
		if option.Type != "select" || option.Category != "thought_level" {
			continue
		}
		for _, choice := range option.choices() {
			if id := strings.TrimSpace(choice.Value); id != "" {
				efforts = append(efforts, id)
			}
		}
		return efforts, strings.TrimSpace(option.Current)
	}
	return nil, ""
}

func modelsFromConfigOptions(options []acpConfigOption) []DiscoveredModel {
	for _, option := range options {
		if option.Type != "select" || option.Category != "model" {
			continue
		}
		var models []DiscoveredModel
		current := strings.TrimSpace(option.Current)
		for _, choice := range option.choices() {
			id := strings.TrimSpace(choice.Value)
			if id == "" {
				continue
			}
			name := strings.TrimSpace(choice.Name)
			if name == "" {
				name = id
			}
			models = append(models, DiscoveredModel{ID: id, DisplayName: name, IsDefault: id == current})
		}
		return models
	}
	return nil
}

func modelsFromFirstClass(session acpSession) []DiscoveredModel {
	if session.Models == nil {
		return nil
	}
	current := strings.TrimSpace(session.Models.Current)
	var models []DiscoveredModel
	for _, model := range session.Models.Available {
		id := strings.TrimSpace(model.ID)
		if id == "" {
			continue
		}
		name := strings.TrimSpace(model.Name)
		if name == "" {
			name = id
		}
		models = append(models, DiscoveredModel{ID: id, DisplayName: name, IsDefault: id == current})
	}
	return models
}

func attachEfforts(models []DiscoveredModel, efforts []string, defaultEffort string) []DiscoveredModel {
	if len(models) == 0 {
		return models
	}
	if defaultEffort != "" {
		found := false
		for _, effort := range efforts {
			if effort == defaultEffort {
				found = true
				break
			}
		}
		if !found {
			defaultEffort = ""
		}
	}
	out := make([]DiscoveredModel, len(models))
	for i, model := range models {
		model.SupportedEfforts = append([]string(nil), efforts...)
		model.DefaultEffort = defaultEffort
		out[i] = model
	}
	return out
}

func (s acpSession) advertisedModelIDs() []string {
	var ids []string
	seen := map[string]bool{}
	add := func(id string) {
		id = strings.TrimSpace(id)
		if id == "" || seen[id] {
			return
		}
		seen[id] = true
		ids = append(ids, id)
	}
	if s.modelConfigOption() != nil {
		for _, model := range modelsFromConfigOptions(s.ConfigOptions) {
			add(model.ID)
		}
		return ids
	}
	if s.Models != nil {
		for _, model := range s.Models.Available {
			add(model.ID)
		}
	}
	return ids
}

func (s acpSession) hasAdvertisedModel(id string) bool {
	id = strings.TrimSpace(id)
	if id == "" {
		return false
	}
	for _, advertised := range s.advertisedModelIDs() {
		if advertised == id {
			return true
		}
	}
	return false
}

func (s acpSession) firstClassHasModel(id string) bool {
	if s.Models == nil {
		return false
	}
	id = strings.TrimSpace(id)
	for _, model := range s.Models.Available {
		if strings.TrimSpace(model.ID) == id {
			return true
		}
	}
	return false
}

func (s acpSession) firstClassCurrent() string {
	if s.Models == nil {
		return ""
	}
	return strings.TrimSpace(s.Models.Current)
}

func (s acpSession) modelConfigOption() *acpConfigOption {
	for i := range s.ConfigOptions {
		option := &s.ConfigOptions[i]
		if option.Type == "select" && option.Category == "model" {
			return option
		}
	}
	return nil
}

// HostPermissionMode is one Wuu access selection mapped onto an advertised
// ACP `category=mode` choice. Empty ID means the host still offers the
// selection (approval-bridge or auto-accept) without a native config option.
type HostPermissionMode struct {
	Mode  string
	ID    string
	Label string
}

// promptingModeChoices still send session/request_permission to the host.
// accept-edits is intentionally absent: it applies writes without asking.
var promptingModeChoices = []string{
	"agent",
	"default",
	"ask",
	"dontAsk",
	"dont_ask",
}

// readOnlyModeChoices are mutation-restricted native modes. `ask` is not
// included: several agents use it as their prompting default, which is
// Standard, not a distinct read-only policy.
var readOnlyModeChoices = []string{
	"read-only",
	"read_only",
	"readonly",
	"plan",
}

// unattendedModeChoices are the agent-specific ids for "never ask the user".
// Devin advertises `bypass`; the rest cover the ids other ACP agents publish
// for the same policy.
var unattendedModeChoices = []string{
	"bypassPermissions",
	"bypass_permissions",
	"bypass",
	"yolo",
	"agent-full-access",
	"danger-full-access",
	"full-access",
}

func permissionModesFromACPSession(session acpSession) []HostPermissionMode {
	option := session.modeOption()
	standard := pickMode(option, promptingModeChoices)
	readOnly := pickMode(option, readOnlyModeChoices)
	unconfined := pickMode(option, unattendedModeChoices)
	if readOnly.ID != "" && (readOnly.ID == standard.ID || readOnly.ID == unconfined.ID) {
		readOnly = pickedMode{}
	}
	modes := []HostPermissionMode{{
		Mode:  "standard",
		ID:    standard.ID,
		Label: standard.Label,
	}}
	if readOnly.ID != "" {
		modes = append(modes, HostPermissionMode{
			Mode:  "read_only",
			ID:    readOnly.ID,
			Label: readOnly.Label,
		})
	}
	return append(modes, HostPermissionMode{
		Mode:  "unconfined",
		ID:    unconfined.ID,
		Label: unconfined.Label,
	})
}

type pickedMode struct {
	ID, Label string
}

func pickMode(option *acpConfigOption, wanted []string) pickedMode {
	if option == nil {
		return pickedMode{}
	}
	for _, id := range wanted {
		if option.hasChoice(id) {
			return pickedMode{ID: id, Label: option.labelFor(id)}
		}
	}
	return pickedMode{}
}

func hostPermissionMode(session acpSession, hostMode string) (HostPermissionMode, bool) {
	hostMode = strings.TrimSpace(hostMode)
	for _, mode := range permissionModesFromACPSession(session) {
		if mode.Mode == hostMode {
			return mode, true
		}
	}
	return HostPermissionMode{}, false
}

func (s acpSession) modeOption() *acpConfigOption {
	for i := range s.ConfigOptions {
		option := &s.ConfigOptions[i]
		if option.Type == "select" && option.Category == "mode" {
			return option
		}
	}
	return nil
}

func (s acpSession) thoughtLevelOption() *acpConfigOption {
	for i := range s.ConfigOptions {
		option := &s.ConfigOptions[i]
		if option.Type == "select" && option.Category == "thought_level" {
			return option
		}
	}
	return nil
}

func (o *acpConfigOption) hasChoice(id string) bool {
	return o.labelFor(id) != ""
}

func (o *acpConfigOption) labelFor(id string) string {
	if o == nil {
		return ""
	}
	id = strings.TrimSpace(id)
	if id == "" {
		return ""
	}
	for _, choice := range o.choices() {
		if strings.TrimSpace(choice.Value) != id {
			continue
		}
		if name := strings.TrimSpace(choice.Name); name != "" {
			return name
		}
		return id
	}
	return ""
}

// ACP defines model_config placement, not a universal speed key. Only map
// advertised speed selectors; never send a guessed option ID or value.
func (s acpSession) speedOption() (*acpConfigOption, string, string) {
	for i := range s.ConfigOptions {
		option := &s.ConfigOptions[i]
		if option.Type != "select" {
			continue
		}
		switch strings.ReplaceAll(strings.ToLower(option.ID), "-", "_") {
		case "fast_mode", "speed", "service_tier":
		default:
			continue
		}
		for _, pair := range [][2]string{{"on", "off"}, {"fast", "standard"}, {"fast", "default"}, {"priority", "default"}} {
			if option.hasChoice(pair[0]) && option.hasChoice(pair[1]) {
				return option, pair[0], pair[1]
			}
		}
	}
	return nil, "", ""
}

// ACP select options can be flat or grouped; both carry the same values.
type acpConfigChoice struct {
	Value   string            `json:"value"`
	Name    string            `json:"name"`
	Options []acpConfigChoice `json:"options"`
}

func (o *acpConfigOption) choices() []acpConfigChoice {
	var choices []acpConfigChoice
	for _, choice := range o.Options {
		if len(choice.Options) > 0 {
			choices = append(choices, choice.Options...)
		} else {
			choices = append(choices, choice)
		}
	}
	return choices
}
