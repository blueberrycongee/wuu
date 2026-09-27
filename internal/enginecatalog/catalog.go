// Package enginecatalog holds process launch metadata, without loading engines
// or starting programs. Settings and runtime detection share this catalog.
package enginecatalog

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
)

type Entry struct {
	ID         string
	Name       string
	Binary     string
	Args       []string
	Protocol   string
	InstallURL string
}

// Entries returns a fresh catalog so callers cannot mutate shared arguments.
func Entries() []Entry {
	agyArgs := []string(nil)
	if runtime.GOOS == "linux" {
		agyArgs = []string{"--uid="}
	}
	return []Entry{
		{ID: "codex", Name: "Codex", Binary: "codex", Protocol: "codex"},
		{ID: "claude", Name: "Claude Code", Binary: "claude", Protocol: "claude"},
		{ID: "cursor", Name: "Cursor", Binary: "cursor-agent", Args: []string{"acp"}, Protocol: "acp", InstallURL: "https://cursor.com/docs/cli/acp"},
		{ID: "devin", Name: "Devin", Binary: "devin", Args: []string{"acp"}, Protocol: "acp", InstallURL: "https://docs.devin.ai/cli"},
		// --no-auto-update is top-level and skips the launch-time update
		// check. --no-leader is on `agent` so stdio does not attach to a
		// shared ~/.grok/leader.sock from the user's TUI.
		{ID: "grok", Name: "Grok", Binary: "grok", Args: []string{"--no-auto-update", "agent", "--no-leader", "stdio"}, Protocol: "acp", InstallURL: "https://x.ai/cli"},
		{ID: "hermes", Name: "Hermes", Binary: "hermes", Args: []string{"acp"}, Protocol: "acp", InstallURL: "https://hermes-agent.nousresearch.com/docs/user-guide/features/acp"},
		{ID: "pi", Name: "Pi", Binary: "pi-acp", Protocol: "acp", InstallURL: "https://github.com/svkozak/pi-acp"},
		{ID: "opencode", Name: "OpenCode", Binary: "opencode", Protocol: "opencode", InstallURL: "https://opencode.ai/docs"},
		{ID: "antigravity", Name: "Antigravity", Binary: "agy_acp_server", Args: agyArgs, Protocol: "acp", InstallURL: "https://antigravity.google/docs/ide/extensions"},
	}
}

func Lookup(id string) (Entry, bool) {
	for _, entry := range Entries() {
		if entry.ID == id {
			return entry, true
		}
	}
	return Entry{}, false
}

// Resolve honors an explicit setting, then the engine-specific environment
// override, then PATH and the standard install locations. An invalid
// override fails instead of selecting a different executable. It never
// executes a binary or installs a dependency.
func (e Entry) Resolve(override string) (string, error) {
	if override = strings.TrimSpace(override); override == "" {
		override = strings.TrimSpace(os.Getenv("WUU_" + strings.ToUpper(e.ID) + "_BINARY"))
	}
	if override != "" {
		return exec.LookPath(override)
	}
	if path, err := LookBinary(e.Binary); err == nil {
		return path, nil
	}
	for _, candidate := range extraLookupPaths(e) {
		path, err := exec.LookPath(candidate)
		if err != nil {
			continue
		}
		rememberDir(filepath.Dir(path))
		return path, nil
	}
	if e.ID == "antigravity" {
		if path, err := LookBinary("agy_acp_server.par"); err == nil {
			return path, nil
		}
	}
	return "", fmt.Errorf("%s executable %q not found; install it or configure its executable path (%s)", e.Name, e.Binary, e.InstallURL)
}

func extraLookupPaths(e Entry) []string {
	home, err := os.UserHomeDir()
	if err != nil {
		home = ""
	}
	joinHome := func(parts ...string) string {
		return filepath.Join(append([]string{home}, parts...)...)
	}
	var paths []string
	addHome := func(parts ...string) {
		if home == "" {
			return
		}
		paths = append(paths, joinHome(parts...))
	}
	addCommon := func(bin string) {
		paths = append(paths, filepath.Join("/opt/homebrew/bin", bin), filepath.Join("/usr/local/bin", bin))
	}
	switch e.ID {
	case "grok":
		addHome(".local", "bin", "grok")
		addHome(".grok", "bin", "grok")
		addHome(".npm-global", "bin", "grok")
		addCommon("grok")
	case "hermes":
		addHome(".local", "bin", "hermes")
		addHome(".hermes", "bin", "hermes")
		addCommon("hermes")
	case "devin":
		addHome(".local", "bin", "devin")
		addCommon("devin")
	case "pi":
		addHome(".local", "bin", "pi-acp")
		addHome(".npm-global", "bin", "pi-acp")
		addCommon("pi-acp")
	}
	return paths
}
