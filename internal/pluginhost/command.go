package pluginhost

import (
	"os"
	"os/exec"
	"strings"
)

// ResolveRuntimeCommand makes the desktop's Node runtime available to every
// Node plugin, including local development generations and community packages.
// Explicit executable paths are left alone. The boolean indicates that the
// selected executable needs Electron's run-as-node environment when launched.
func ResolveRuntimeCommand(command string, lookupEnv func(string) (string, bool)) (string, bool) {
	command = strings.TrimSpace(command)
	if command != "node" && command != "node.exe" {
		return command, false
	}
	if lookupEnv == nil {
		lookupEnv = os.LookupEnv
	}
	if executable, ok := lookupEnv("WUU_NODE_EXECUTABLE"); ok && strings.TrimSpace(executable) != "" {
		return strings.TrimSpace(executable), true
	}
	return command, false
}

// RuntimeCommand creates a runtime process with the same minimal environment
// for normal activation and single-file development validation.
func RuntimeCommand(command string, args []string, explicit map[string]string) *exec.Cmd {
	command, desktopNode := ResolveRuntimeCommand(command, nil)
	cmd := exec.Command(command, args...)
	if desktopNode {
		env := make(map[string]string, len(explicit)+1)
		for key, value := range explicit {
			env[key] = value
		}
		env["ELECTRON_RUN_AS_NODE"] = "1"
		explicit = env
	}
	cmd.Env = buildEnv(explicit)
	return cmd
}
