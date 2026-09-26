package statepath

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"os"
	"path/filepath"
	"strings"
)

const (
	envHomeVar = "WUU_HOME"

	// configFileName and authFileName are the canonical file names stored
	// directly inside the unified wuu home (~/.wuu, or WUU_HOME when set).
	configFileName = "config.json"
	authFileName   = "auth.json"

	// legacyGlobalRelative is the pre-unification global directory under the
	// real HOME that held config.json, auth.json, and user-level instruction
	// files before everything was consolidated into the wuu home. It is kept
	// for backward-compatible reads and one-time migration only. It is
	// intentionally NOT affected by WUU_HOME: it names the fixed location old
	// builds always wrote to.
	legacyGlobalRelative = ".config/wuu"
)

// Home returns the unified user-level wuu directory. It is the single root for
// both configuration (config.json, auth.json, user AGENTS.md) and runtime
// state (sessions, workspaces, plugin data, logs). WUU_HOME overrides it wholesale,
// mirroring Claude Code's CLAUDE_CONFIG_DIR: setting WUU_HOME relocates the
// entire directory, not just state.
func Home(homeDir string) (string, error) {
	if override := strings.TrimSpace(os.Getenv(envHomeVar)); override != "" {
		return filepath.Abs(override)
	}

	home := strings.TrimSpace(homeDir)
	if home == "" {
		var err error
		home, err = os.UserHomeDir()
		if err != nil {
			return "", err
		}
	}
	if strings.TrimSpace(home) == "" {
		return "", errors.New("home directory is unavailable")
	}
	return filepath.Join(home, ".wuu"), nil
}

// ConfigPath returns the canonical path to the user config file inside the
// unified wuu home (WUU_HOME/config.json when WUU_HOME is set, otherwise
// ~/.wuu/config.json).
func ConfigPath(homeDir string) (string, error) {
	home, err := Home(homeDir)
	if err != nil {
		return "", err
	}
	return filepath.Join(home, configFileName), nil
}

// AuthPath returns the canonical path to the user credential store inside the
// unified wuu home (WUU_HOME/auth.json when WUU_HOME is set, otherwise
// ~/.wuu/auth.json).
func AuthPath(homeDir string) (string, error) {
	home, err := Home(homeDir)
	if err != nil {
		return "", err
	}
	return filepath.Join(home, authFileName), nil
}

// UserInstructionsDir returns the canonical directory scanned for user-level
// instruction files (AGENTS.md, CLAUDE.md, ...). It is the unified wuu home
// and therefore honors WUU_HOME just like ConfigPath and AuthPath.
func UserInstructionsDir(homeDir string) (string, error) {
	return Home(homeDir)
}

// LegacyGlobalDir returns the pre-unification global directory
// ($HOME/.config/wuu) that held config.json, auth.json, and user-level
// instruction files. It ignores WUU_HOME because it names the fixed legacy
// location old builds always wrote to. It returns "" when homeDir is empty.
func LegacyGlobalDir(homeDir string) string {
	home := strings.TrimSpace(homeDir)
	if home == "" {
		return ""
	}
	return filepath.Join(home, legacyGlobalRelative)
}

// LegacyConfigPath returns the pre-unification config.json location
// ($HOME/.config/wuu/config.json), or "" when homeDir is empty.
func LegacyConfigPath(homeDir string) string {
	dir := LegacyGlobalDir(homeDir)
	if dir == "" {
		return ""
	}
	return filepath.Join(dir, configFileName)
}

// UserInstructionDirs returns the ordered directories scanned for user-level
// instruction files: the canonical unified home first, then the legacy
// $HOME/.config/wuu directory for backward compatibility. Missing pieces are
// skipped, so the result may have one entry (or none when homeDir is empty and
// the home cannot be resolved).
func UserInstructionDirs(homeDir string) []string {
	var dirs []string
	if canonical, err := UserInstructionsDir(homeDir); err == nil && strings.TrimSpace(canonical) != "" {
		dirs = append(dirs, canonical)
	}
	if legacy := LegacyGlobalDir(homeDir); legacy != "" {
		dirs = append(dirs, legacy)
	}
	return dirs
}

// LogDir returns the user-level directory for runtime logs.
func LogDir(wuuHome string) string {
	return filepath.Join(wuuHome, "log")
}

// SessionsDir returns the user-level directory for conversation sessions.
func SessionsDir(wuuHome string) string {
	return filepath.Join(wuuHome, "sessions")
}

// WorkspaceDirByID returns a stable user-level state directory for a workspace
// identified by an opaque, location-independent ID (the desktop's stable
// project id). Unlike WorkspaceDir it hashes the ID rather than the filesystem
// path, so the directory — and everything under it (memory, goals, scheduled
// tasks, session artifacts) — survives the workspace being moved or renamed on
// disk. The layout matches WorkspaceDir (<slug>-<hash16>) so every downstream
// RuntimeDir/WorktreeRoot/... helper is unaffected.
func WorkspaceDirByID(wuuHome, workspaceID string) (string, error) {
	if strings.TrimSpace(wuuHome) == "" {
		return "", errors.New("wuu home is required")
	}
	id := strings.TrimSpace(workspaceID)
	if id == "" {
		return "", errors.New("workspace id is required")
	}
	sum := sha256.Sum256([]byte(id))
	slug := sanitizeSlug(id)
	if slug == "" {
		slug = "workspace"
	}
	return filepath.Join(wuuHome, "workspaces", slug+"-"+hex.EncodeToString(sum[:])[:16]), nil
}

// WorkspaceDir returns a stable user-level state directory for one workspace,
// keyed by its filesystem path. Prefer WorkspaceDirByID for workspaces that
// carry a stable identity (registered projects); this path-keyed form remains
// for location-anchored roots that never move — the shared 对话 scratch dir,
// per-agent home dirs, and sub-agent worktree checkouts.
func WorkspaceDir(wuuHome, rootDir string) (string, error) {
	if strings.TrimSpace(wuuHome) == "" {
		return "", errors.New("wuu home is required")
	}
	if strings.TrimSpace(rootDir) == "" {
		return "", errors.New("workspace root is required")
	}
	abs, err := filepath.Abs(rootDir)
	if err != nil {
		return "", err
	}
	if ev, err := filepath.EvalSymlinks(abs); err == nil {
		abs = ev
	}

	sum := sha256.Sum256([]byte(abs))
	slug := sanitizeSlug(filepath.Base(abs))
	if slug == "" {
		slug = "workspace"
	}
	return filepath.Join(wuuHome, "workspaces", slug+"-"+hex.EncodeToString(sum[:])[:16]), nil
}

// AgentHomeDir returns the per-agent home directory used for direct-message
// threads with a named participant. The directory lives inside wuu's own state
// root so DM conversations are anchored to a stable location regardless of the
// project the user happens to have open. Callers are responsible for creating
// the directory with os.MkdirAll.
func AgentHomeDir(wuuHome, participantID string) string {
	id := strings.TrimSpace(participantID)
	if id == "" {
		id = "default"
	}
	return filepath.Join(wuuHome, "agents", id, "home")
}

// ProfileDir returns a stable user-level state directory for one agent profile.
func ProfileDir(wuuHome, agentName string) (string, error) {
	if strings.TrimSpace(wuuHome) == "" {
		return "", errors.New("wuu home is required")
	}
	name := strings.TrimSpace(agentName)
	if name == "" {
		name = "default"
	}
	sum := sha256.Sum256([]byte(name))
	slug := sanitizeSlug(name)
	if slug == "" {
		slug = "profile"
	}
	return filepath.Join(wuuHome, "profiles", slug+"-"+hex.EncodeToString(sum[:])[:16]), nil
}

// RuntimeDir returns the workspace-scoped process runtime directory.
func RuntimeDir(workspaceStateDir string) string {
	return filepath.Join(workspaceStateDir, "runtime")
}

// WorktreeRoot returns the workspace-scoped sub-agent worktree directory.
func WorktreeRoot(workspaceStateDir string) string {
	return filepath.Join(workspaceStateDir, "worktrees")
}

// SharedDir returns the workspace-scoped shared agent directory.
func SharedDir(workspaceStateDir string) string {
	return filepath.Join(workspaceStateDir, "shared")
}

// SessionArtifactDir returns workspace-scoped artifacts for one conversation.
func SessionArtifactDir(workspaceStateDir, sessionID string) string {
	return filepath.Join(workspaceStateDir, "sessions", sessionID)
}

// ThreadBrowserTabsPath returns the thread-scoped embedded-browser tab registry
// file. It lives under the session artifact directory, so it is reclaimed
// together with the thread on delete.
func ThreadBrowserTabsPath(workspaceStateDir, sessionID string) string {
	return filepath.Join(SessionArtifactDir(workspaceStateDir, sessionID), "browser_tabs.json")
}

func sanitizeSlug(input string) string {
	var b strings.Builder
	lastDash := false
	for _, r := range input {
		ok := r >= 'a' && r <= 'z' ||
			r >= 'A' && r <= 'Z' ||
			r >= '0' && r <= '9' ||
			r == '.' || r == '_' || r == '-'
		if ok {
			b.WriteRune(r)
			lastDash = false
			continue
		}
		if !lastDash {
			b.WriteByte('-')
			lastDash = true
		}
	}
	s := strings.Trim(b.String(), ".-")
	if len(s) > 48 {
		s = strings.Trim(s[:48], ".-")
	}
	return s
}
