package plugin

import (
	"errors"
	"fmt"
	"os/exec"
	"runtime"
	"strconv"
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
	if minimum := strings.TrimSpace(item.MinimumWuuVersion); minimum != "" {
		minimumVersion, err := parseCompatibilityVersion(minimum)
		if err != nil {
			return fmt.Errorf("plugin %q minimum Wuu version: %w", item.ID, err)
		}
		currentVersion, err := parseCompatibilityVersion(options.WuuVersion)
		if err != nil {
			return fmt.Errorf("resolve current Wuu version %q: %w", options.WuuVersion, err)
		}
		if compareCompatibilityVersion(currentVersion, minimumVersion) < 0 {
			return fmt.Errorf("plugin %q requires Wuu %s or newer (current: %s)", item.ID, minimum, options.WuuVersion)
		}
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

type compatibilityVersion [3]uint64

func parseCompatibilityVersion(raw string) (compatibilityVersion, error) {
	value := strings.TrimSpace(strings.TrimPrefix(strings.TrimSpace(raw), "v"))
	if value == "" {
		return compatibilityVersion{}, errors.New("version is empty")
	}
	if index := strings.IndexAny(value, "-+"); index >= 0 {
		value = value[:index]
	}
	parts := strings.Split(value, ".")
	if len(parts) != 3 {
		return compatibilityVersion{}, fmt.Errorf("version %q must use major.minor.patch", raw)
	}
	var parsed compatibilityVersion
	for index, part := range parts {
		if part == "" || (len(part) > 1 && part[0] == '0') {
			return compatibilityVersion{}, fmt.Errorf("version %q is not semantic versioning", raw)
		}
		number, err := strconv.ParseUint(part, 10, 64)
		if err != nil {
			return compatibilityVersion{}, fmt.Errorf("version %q is not semantic versioning", raw)
		}
		parsed[index] = number
	}
	return parsed, nil
}

func compareCompatibilityVersion(left, right compatibilityVersion) int {
	for index := range left {
		if left[index] < right[index] {
			return -1
		}
		if left[index] > right[index] {
			return 1
		}
	}
	return 0
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
