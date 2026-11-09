package plugin

import (
	"fmt"
	"os/exec"
	"runtime"
	"strings"

	"github.com/blueberrycongee/wuu/internal/pluginhost"
	"github.com/blueberrycongee/wuu/internal/version"
)

// CompatibilityOptions describes the host facts used before a plugin may be
// approved or activated. Tests and future shells can provide deterministic
// values without changing process-global state.
type CompatibilityOptions struct {
	GOOS       string
	WuuVersion string
	LookPath   func(string) (string, error)
	LookupEnv  func(string) (string, bool)
}

func defaultCompatibilityOptions() CompatibilityOptions {
	return CompatibilityOptions{
		GOOS:       runtime.GOOS,
		WuuVersion: version.Info().Version,
		LookPath:   exec.LookPath,
	}
}

// ValidateHostCompatibility rejects a package that cannot execute on the
// current host. It deliberately runs after manifest parsing so inspection,
// installation, project discovery, and development activation share one gate.
func ValidateHostCompatibility(item Plugin, options CompatibilityOptions) error {
	if strings.TrimSpace(options.GOOS) == "" {
		options.GOOS = runtime.GOOS
	}
	if strings.TrimSpace(options.WuuVersion) == "" {
		options.WuuVersion = version.Info().Version
	}
	if options.LookPath == nil {
		options.LookPath = exec.LookPath
	}
	if !supportsPlatform(item.Platforms, options.GOOS) {
		return fmt.Errorf("plugin %q does not support platform %s", item.ID, options.GOOS)
	}
	if err := CheckMinimumWuuVersion(item.MinimumWuuVersion, options.WuuVersion); err != nil {
		return fmt.Errorf("plugin %q: %w", item.ID, err)
	}
	if item.Runtime != nil {
		command, _ := pluginhost.ResolveRuntimeCommand(item.Runtime.Command, options.LookupEnv)
		if _, err := options.LookPath(command); err != nil {
			if item.Runtime.Command == "node" || item.Runtime.Command == "node.exe" {
				return fmt.Errorf("plugin %q requires Node.js 22.7+ or the Wuu desktop runtime (runtime command %q is not executable): %w", item.ID, command, err)
			}
			return fmt.Errorf("plugin %q runtime command %q is not executable: %w", item.ID, item.Runtime.Command, err)
		}
	}
	return nil
}

func filterCompatiblePlugins(items []Plugin, options CompatibilityOptions) []Plugin {
	out := make([]Plugin, 0, len(items))
	for _, item := range items {
		if ValidateHostCompatibility(item, options) == nil {
			out = append(out, item)
		}
	}
	return out
}
