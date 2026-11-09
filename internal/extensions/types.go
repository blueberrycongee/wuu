package extensions

import "strings"

type Kind string

const (
	KindSkill   Kind = "skill"
	KindCommand Kind = "command"
	KindMCP     Kind = "mcp"
	KindHook    Kind = "hook"
	KindPlugin  Kind = "plugin"
)

type TrustLevel string

const (
	TrustOfficialBundled TrustLevel = "official_bundled"
	TrustUserInstalled   TrustLevel = "user_installed"
	TrustProjectDeclared TrustLevel = "project_declared"
)

type Provenance struct {
	Kind     Kind   `json:"kind"`
	Source   string `json:"source"`
	Scope    string `json:"scope"`
	Path     string `json:"path,omitempty"`
	PluginID string `json:"plugin_id,omitempty"`
	Official bool   `json:"official,omitempty"`
}

func (p Provenance) TrustLevel() TrustLevel {
	if p.Official && strings.EqualFold(strings.TrimSpace(p.Source), "bundled") {
		return TrustOfficialBundled
	}
	if strings.EqualFold(strings.TrimSpace(p.Scope), "project") {
		return TrustProjectDeclared
	}
	return TrustUserInstalled
}
