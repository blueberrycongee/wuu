package tools

import (
	"context"
	"fmt"
	"path/filepath"
	"strings"

	"github.com/blueberrycongee/wuu/internal/statepath"
)

// sourceCredentialExtensions are source-code suffixes. A filename such as
// credentials.go is authentication source, not a credential store. The
// credential/secret substring rule does not apply to these extensions.
// Credential stores keep their own suffixes (.json, .yaml, .pem, .env, …)
// and remain sensitive even when the basename also contains those words.
var sourceCredentialExtensions = map[string]struct{}{
	".c": {}, ".cc": {}, ".cpp": {}, ".cs": {}, ".go": {}, ".h": {},
	".hpp": {}, ".java": {}, ".js": {}, ".jsx": {}, ".kt": {}, ".m": {},
	".mjs": {}, ".mm": {}, ".php": {}, ".py": {}, ".rb": {}, ".rs": {},
	".scala": {}, ".svelte": {}, ".swift": {}, ".ts": {}, ".tsx": {},
	".vue": {}, ".cjs": {},
}

func isSourceCredentialPath(part string) bool {
	dot := strings.LastIndex(part, ".")
	if dot <= 0 || dot == len(part)-1 {
		return false
	}
	_, ok := sourceCredentialExtensions[part[dot:]]
	return ok
}

func sensitivePathReason(path string) (string, bool) {
	normalized := filepath.ToSlash(strings.TrimSpace(path))
	if normalized == "" {
		return "", false
	}
	lower := strings.ToLower(normalized)
	parts := strings.Split(lower, "/")
	for i, part := range parts {
		part = strings.Trim(part, `"'`)
		switch {
		case part == ".git" || part == ".hg" || part == ".svn":
			return "version-control metadata", true
		case part == ".env" || strings.HasPrefix(part, ".env.") || strings.Contains(part, ".env"):
			return ".env file", true
		case part == ".netrc":
			return ".netrc credentials", true
		case part == ".npmrc" || part == ".pypirc" || part == ".pgpass":
			return "credential configuration", true
		case (strings.Contains(part, "credential") || strings.Contains(part, "secret")) && (i != len(parts)-1 || !isSourceCredentialPath(part)):
			return "credential or secret path", true
		case part == "id_rsa" || part == "id_ed25519" || part == "id_ecdsa":
			return "SSH private key", true
		case strings.Contains(part, "private") && strings.Contains(part, "key"):
			return "private key path", true
		}
	}
	return "", false
}

func isSensitivePath(path string) bool {
	_, ok := sensitivePathReason(path)
	return ok
}

// wuuCredentialFileNames are the app's own credential files at the root of
// the wuu home directory. They are floor-protected in every permission
// mode, including unconfined: dedicated file tools may not read or write
// them. Other files in Wuu home use the ordinary file-scope boundary.
var wuuCredentialFileNames = map[string]struct{}{
	"auth.json":        {},
	"credentials.json": {},
	"remote.json":      {},
	"phone.json":       {},
}

// isWuuCredentialPath recognizes both the app's named credential files and
// symlink aliases to them, including when Wuu home itself is a symlink.
func isWuuCredentialPath(absPath string) bool {
	if strings.TrimSpace(absPath) == "" {
		return false
	}
	home, err := statepath.Home("")
	if err != nil {
		return false
	}
	matches := func(path string) bool {
		_, credential := wuuCredentialFileNames[filepath.Base(filepath.Clean(path))]
		return credential && filepath.Dir(filepath.Clean(path)) == filepath.Clean(home)
	}
	// Preserve the named-file guard even if the store is missing or points
	// outside Wuu home, then check actual targets to prevent alias bypasses.
	if matches(absPath) {
		return true
	}
	if resolved, err := filepath.EvalSymlinks(home); err == nil {
		home = resolved
	}
	if matches(absPath) {
		return true
	}
	if resolved, err := filepath.EvalSymlinks(absPath); err == nil {
		return matches(resolved)
	}
	return false
}

func wuuCredentialRefusal(toolName, action, absPath string) error {
	return fmt.Errorf("%s refuses to %s wuu credential file %q: it stores the app's own login credentials and is never accessible to the agent in any permission mode. Ask the user to manage it outside the session", toolName, action, absPath)
}

// redactSensitiveReadContent masks credential values in content read from a
// sensitive path while unconfined. Confined modes never reach this helper:
// the read itself is refused by rejectSensitiveReadPath instead.
func redactSensitiveReadContent(env *Env, absPath, content string) string {
	if content == "" || env == nil || !env.BypassToolHardProtections() {
		return content
	}
	if _, ok := sensitivePathReason(env.NormalizeDisplayPath(absPath)); !ok {
		return content
	}
	return redactToolOutput(content)
}

func rejectSensitiveReadPath(env *Env, toolName, absPath string) error {
	if isWuuCredentialPath(absPath) {
		return wuuCredentialRefusal(toolName, "read", absPath)
	}
	if env.BypassToolHardProtections() {
		return nil
	}
	displayPath := env.NormalizeDisplayPath(absPath)
	if reason, ok := sensitivePathReason(displayPath); ok {
		return fmt.Errorf("%s refuses to read sensitive path %q (%s). Chat approval does not lift this guard. Use a metadata-only command, or edit the file outside the session", toolName, displayPath, reason)
	}
	return nil
}

func rejectSensitiveToolPath(env *Env, toolName, action, absPath string) error {
	if isWuuCredentialPath(absPath) {
		return wuuCredentialRefusal(toolName, action, absPath)
	}
	// Sensitive-path writes stay blocked in every mode, including
	// unconfined: lifting the path boundary does not lift secret guards.
	displayPath := env.NormalizeDisplayPath(absPath)
	if reason, ok := sensitivePathReason(displayPath); ok {
		return fmt.Errorf("%s refuses to %s sensitive path %q (%s). This guard applies in every permission mode, including unconfined, and chat approval does not lift it. Edit the file outside the session", toolName, action, displayPath, reason)
	}
	return nil
}

// resolveReadTarget checks the actual file after worktree rebasing and symlink resolution.
func resolveReadTarget(ctx context.Context, env *Env, toolName, path string, managed bool) (string, error) {
	// Resolve aliases before the final sensitive-path check. An innocently
	// named symlink must not expose a credential file within a workspace root.
	resolved, err := filepath.EvalSymlinks(path)
	if err != nil {
		return "", err
	}
	if !managed {
		// Hosted worktrees can live under WUU_HOME. Recheck the actual target
		// against that execution root, not the infrastructure path above it.
		execRoot, err := env.ExecRootDir(ctx)
		if err != nil {
			return "", err
		}
		boundary := &Env{
			RootDir: execRoot, FileScopeRoots: append([]string{execRoot}, env.FileScopeRoots...),
			Unconfined: env.Unconfined, PermissionMode: env.PermissionMode, AllowMutations: env.AllowMutations,
		}
		if _, err := boundary.ResolvePath(resolved); err != nil {
			return "", err
		}
		if err := rejectSensitiveReadPath(boundary, toolName, resolved); err != nil {
			return "", err
		}
	}
	return resolved, nil
}
