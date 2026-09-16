package approvefor

import (
	"encoding/json"
	"path/filepath"
	"strings"

	"github.com/blueberrycongee/wuu/internal/config"
	"github.com/blueberrycongee/wuu/internal/statepath"
)

const (
	OutcomeAllow  = "allow"
	OutcomeDeny   = "deny"
	OutcomeUnsure = "unsure"

	AllowOnceLabel = "Allow once"
	DenyLabel      = "Deny"

	QuestionID = "approval.wuu_tool"

	riskHigh = "high"

	kindShell   = "shell"
	kindGit     = "git"
	kindProcess = "process"
	kindBrowser = "browser"
	kindMCP     = "mcp"
)

// Tool describes the call being reviewed without importing the tools package.
type Tool struct {
	Name        string
	Kind        string
	ReadOnly    bool
	Destructive bool
	Risk        string
	Reason      string
}

// Request is one native-engine tool call presented to the reviewer.
type Request struct {
	SessionID      string
	TurnID         string
	CallID         string
	PermissionMode string
	Tool           Tool
	Arguments      string
	CWD            string
}

// Decision is the host's final answer for one reviewed call.
type Decision struct {
	Outcome string
	Reason  string
}

// NeedsReview reports whether Approve for me should inspect this call.
// Workspace-local low/medium work stays on the ordinary allow/deny path.
func NeedsReview(req Request) bool {
	if !EnabledForMode(req.PermissionMode) {
		return false
	}
	if req.Tool.ReadOnly && req.Tool.Risk != riskHigh {
		return false
	}
	if req.Tool.Risk == riskHigh || req.Tool.Destructive {
		return true
	}
	switch req.Tool.Kind {
	case kindShell, kindGit, kindProcess, kindBrowser, kindMCP:
		return true
	}
	return false
}

// HardDenyReason returns a refusal that no reviewer, including the user, may override.
func HardDenyReason(req Request) string {
	if isWuuCredentialCall(req) {
		return "wuu credential files are never accessible to the agent in any permission mode"
	}
	if looksLikePermissionEscalation(req.Arguments) {
		return "approve for me cannot raise the session permission mode"
	}
	if req.Tool.Name == "write_file" || req.Tool.Name == "edit_file" || req.Tool.Name == "apply_patch" {
		if reason, ok := sensitivePathReasonFromArgs(req.Arguments); ok {
			return "sensitive path " + reason + " cannot be modified through Approve for me"
		}
	}
	return ""
}

func EnabledForMode(mode string) bool {
	return config.NormalizePermissionMode(mode) == config.PermissionModeStandard
}

func TruncateText(value string, limit int) string {
	value = strings.TrimSpace(value)
	if limit <= 0 || len(value) <= limit {
		return value
	}
	if limit <= 3 {
		return value[:limit]
	}
	return value[:limit-3] + "..."
}

func looksLikePermissionEscalation(arguments string) bool {
	lower := strings.ToLower(arguments)
	return strings.Contains(lower, "unconfined") &&
		(strings.Contains(lower, "permission_mode") || strings.Contains(lower, "permission-mode"))
}

func isWuuCredentialCall(req Request) bool {
	for _, value := range argumentPaths(req.Arguments) {
		if isWuuCredentialPath(value) {
			return true
		}
	}
	lower := strings.ToLower(req.Arguments)
	return strings.Contains(lower, "auth.json") || strings.Contains(lower, "credentials.json")
}

func sensitivePathReasonFromArgs(arguments string) (string, bool) {
	for _, value := range argumentPaths(arguments) {
		if reason, ok := sensitivePathReason(value); ok {
			return reason, true
		}
	}
	return "", false
}

func argumentPaths(arguments string) []string {
	var payload map[string]any
	if err := json.Unmarshal([]byte(strings.TrimSpace(arguments)), &payload); err != nil {
		return nil
	}
	var paths []string
	for _, key := range []string{"path", "file_path", "target"} {
		value, _ := payload[key].(string)
		if strings.TrimSpace(value) != "" {
			paths = append(paths, value)
		}
	}
	return paths
}

func sensitivePathReason(path string) (string, bool) {
	normalized := filepath.ToSlash(strings.TrimSpace(path))
	if normalized == "" {
		return "", false
	}
	lower := strings.ToLower(normalized)
	parts := strings.Split(lower, "/")
	for _, part := range parts {
		part = strings.Trim(part, `"'`)
		switch {
		case part == ".env" || strings.HasPrefix(part, ".env.") || strings.Contains(part, ".env"):
			return ".env file", true
		case part == "id_rsa" || part == "id_ed25519" || part == "id_ecdsa":
			return "SSH private key", true
		case strings.Contains(part, "private") && strings.Contains(part, "key"):
			return "private key path", true
		}
	}
	return "", false
}

func isWuuCredentialPath(absPath string) bool {
	if strings.TrimSpace(absPath) == "" {
		return false
	}
	base := filepath.Base(filepath.Clean(absPath))
	switch base {
	case "auth.json", "credentials.json", "remote.json", "phone.json":
	default:
		return false
	}
	home, err := statepath.Home("")
	if err != nil {
		return false
	}
	return filepath.Dir(filepath.Clean(absPath)) == filepath.Clean(home)
}
