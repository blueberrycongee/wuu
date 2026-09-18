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
	// Failed and Cancelled describe review infrastructure, not a safety verdict.
	OutcomeFailed    = "failed"
	OutcomeCancelled = "cancelled"

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
	// UserMessages is populated by the host, never from tool arguments.
	UserMessages []string
	// ReferencedPaths contains literal path operands extracted by native tools.
	ReferencedPaths []string
}

// Decision is the host's final answer for one reviewed call.
type Decision struct {
	Outcome string
	Reason  string
}

// NeedsReview reports whether Approve for me should inspect this call.
// Workspace-local low/medium non-shell work stays on the ordinary allow/deny path.
func NeedsReview(req Request) bool {
	if !EnabledForMode(req.PermissionMode) {
		return false
	}
	// Read-only classification is not proof that a shell command cannot read
	// credentials (even a simple cat can). We do not have a full shell parser
	// here: keep all shell execution on the contextual review path instead of
	// hard-denying credential strings that may only be echo/source/code data.
	// Check the native tool name too, so missing Kind metadata cannot bypass it.
	if req.Tool.Kind == kindShell || req.Tool.Name == "bash" {
		return true
	}
	if req.Tool.ReadOnly && req.Tool.Risk != riskHigh {
		return false
	}
	if req.Tool.Risk == riskHigh || req.Tool.Destructive {
		return true
	}
	switch req.Tool.Kind {
	case kindGit, kindProcess, kindBrowser, kindMCP:
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

func looksLikePermissionEscalation(arguments string) bool {
	var payload map[string]any
	if json.Unmarshal([]byte(arguments), &payload) != nil {
		return false
	}
	for _, key := range []string{"permission_mode", "permission-mode"} {
		value, _ := payload[key].(string)
		if strings.EqualFold(strings.TrimSpace(value), "unconfined") {
			return true
		}
	}
	return false
}

func isWuuCredentialCall(req Request) bool {
	paths := append(argumentPaths(req.Arguments), req.ReferencedPaths...)
	for _, value := range paths {
		if !filepath.IsAbs(value) && req.CWD != "" {
			value = filepath.Join(req.CWD, value)
		}
		if isWuuCredentialPath(value) {
			return true
		}
		if resolved, err := filepath.EvalSymlinks(value); err == nil {
			value = resolved
		}
		if isWuuCredentialPath(value) {
			return true
		}
	}
	// NeedsReview routes shell calls to contextual review even when classified
	// read-only/low-risk. Do not substring-match programs or free-form content:
	// a credential basename in source is not evidence of file access.
	return false
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
	// Only patch headers name files. Added/deleted/context lines are content,
	// even when they contain credential paths or permission-mode examples.
	for _, key := range []string{"patchText", "patch_text", "patch"} {
		patch, _ := payload[key].(string)
		for _, line := range strings.Split(patch, "\n") {
			for _, prefix := range []string{"*** Add File: ", "*** Update File: ", "*** Delete File: ", "*** Move to: "} {
				if strings.HasPrefix(line, prefix) {
					if path := strings.TrimSpace(strings.TrimPrefix(line, prefix)); path != "" {
						paths = append(paths, path)
					}
				}
			}
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
	if filepath.Dir(filepath.Clean(absPath)) == filepath.Clean(home) {
		return true
	}
	if resolved, err := filepath.EvalSymlinks(home); err == nil {
		home = resolved
	}
	return filepath.Dir(filepath.Clean(absPath)) == filepath.Clean(home)
}
