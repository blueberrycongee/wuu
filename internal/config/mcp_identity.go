package config

import (
	"encoding/json"
	"errors"
	"strings"
	"unicode"
)

// MCPIdentityProbe is a fixed read-only tool call declared by a trusted package.
// Mappings are RFC 6901 JSON Pointers, not executable expressions.
type MCPIdentityProbe struct {
	Tool      string                       `json:"tool"`
	Arguments json.RawMessage              `json:"arguments"`
	Result    string                       `json:"result"`
	Account   MCPIdentityAccountMapping    `json:"account"`
	Workspace *MCPIdentityWorkspaceMapping `json:"workspace,omitempty"`
}

type MCPIdentityAccountMapping struct {
	ID    string `json:"id"`
	Label string `json:"label,omitempty"`
	Email string `json:"email,omitempty"`
	Kind  string `json:"kind,omitempty"`
}

type MCPIdentityWorkspaceMapping struct {
	ID    string `json:"id"`
	Label string `json:"label,omitempty"`
}

func (p *MCPIdentityProbe) Validate() error {
	if p == nil {
		return nil
	}
	if p.Tool == "" || len(p.Tool) > 256 || strings.ContainsFunc(p.Tool, func(r rune) bool { return unicode.IsSpace(r) || unicode.IsControl(r) }) {
		return errors.New("identity_probe.tool must be a bounded tool name")
	}
	if p.Result != "structured_content" && p.Result != "single_text_json" {
		return errors.New("identity_probe.result must be structured_content or single_text_json")
	}
	if len(p.Arguments) > 8192 {
		return errors.New("identity_probe.arguments exceeds 8192 bytes")
	}
	var args map[string]any
	if err := json.Unmarshal(p.Arguments, &args); err != nil || args == nil {
		return errors.New("identity_probe.arguments must be a JSON object")
	}
	if !safeIdentityArguments(args, 0) {
		return errors.New("identity_probe.arguments contains unsafe keys or excessive nesting")
	}
	pointers := []string{p.Account.ID, p.Account.Label, p.Account.Email, p.Account.Kind}
	if p.Account.ID == "" {
		return errors.New("identity_probe.account.id is required")
	}
	if p.Workspace != nil {
		if p.Workspace.ID == "" {
			return errors.New("identity_probe.workspace.id is required")
		}
		pointers = append(pointers, p.Workspace.ID, p.Workspace.Label)
	}
	for _, pointer := range pointers {
		if pointer == "" {
			continue
		}
		if len(pointer) > 512 || pointer[0] != '/' || strings.Count(pointer, "/") > 32 || strings.ContainsFunc(pointer, unicode.IsControl) {
			return errors.New("identity_probe mappings must be bounded RFC 6901 JSON Pointers")
		}
		for _, part := range strings.Split(pointer[1:], "/") {
			for i := 0; i < len(part); i++ {
				if part[i] == '~' {
					if i+1 >= len(part) || (part[i+1] != '0' && part[i+1] != '1') {
						return errors.New("identity_probe mapping contains an invalid JSON Pointer escape")
					}
					i++
				}
			}
			key := strings.ReplaceAll(strings.ReplaceAll(part, "~1", "/"), "~0", "~")
			if unsafeIdentityKey(key) {
				return errors.New("identity_probe mapping contains an unsafe object key")
			}
		}
	}
	return nil
}

func unsafeIdentityKey(key string) bool {
	return key == "__proto__" || key == "prototype" || key == "constructor"
}

func safeIdentityArguments(value any, depth int) bool {
	if depth > 16 {
		return false
	}
	switch v := value.(type) {
	case map[string]any:
		for key, item := range v {
			if unsafeIdentityKey(key) || !safeIdentityArguments(item, depth+1) {
				return false
			}
		}
	case []any:
		for _, item := range v {
			if !safeIdentityArguments(item, depth+1) {
				return false
			}
		}
	}
	return true
}
