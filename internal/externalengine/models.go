package externalengine

import "strings"

// DiscoveredModel is one model an ACP agent advertised on session/new.
type DiscoveredModel struct {
	ID               string
	DisplayName      string
	DefaultEffort    string
	SupportedEfforts []string
	IsDefault        bool
}

type acpConfigOption struct {
	ID       string `json:"id"`
	Category string `json:"category"`
	Type     string `json:"type"`
	Current  string `json:"currentValue"`
	Options  []struct {
		Value string `json:"value"`
		Name  string `json:"name"`
	} `json:"options"`
}

func modelsFromACPSession(session acpSession) []DiscoveredModel {
	efforts, defaultEffort := thoughtLevelFromOptions(session.ConfigOptions)
	models := modelsFromConfigOptions(session.ConfigOptions)
	if len(models) == 0 {
		models = modelsFromFirstClass(session)
	}
	return attachEfforts(models, efforts, defaultEffort)
}

func thoughtLevelFromOptions(options []acpConfigOption) (efforts []string, current string) {
	for _, option := range options {
		if option.Type != "select" || option.Category != "thought_level" {
			continue
		}
		for _, choice := range option.Options {
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
		hasReal := false
		for _, choice := range option.Options {
			if id := strings.TrimSpace(choice.Value); id != "" && !strings.EqualFold(id, "default") {
				hasReal = true
				break
			}
		}
		var models []DiscoveredModel
		current := strings.TrimSpace(option.Current)
		for _, choice := range option.Options {
			id := strings.TrimSpace(choice.Value)
			if id == "" || hasReal && strings.EqualFold(id, "default") {
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
	for _, model := range modelsFromConfigOptions(s.ConfigOptions) {
		add(model.ID)
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
	if o == nil {
		return false
	}
	id = strings.TrimSpace(id)
	for _, choice := range o.Options {
		if strings.TrimSpace(choice.Value) == id {
			return true
		}
	}
	return false
}
