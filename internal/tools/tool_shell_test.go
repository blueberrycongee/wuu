package tools

import (
	"strings"
	"testing"
)

func TestShellCommandEnvDropsLauncherGOROOT(t *testing.T) {
	env := shellCommandEnv([]string{
		"PATH=/usr/bin",
		"GOROOT=/tmp/launcher-toolchain",
		"HOME=/home/test",
	})

	values := make(map[string]string, len(env))
	for _, entry := range env {
		key, value, ok := strings.Cut(entry, "=")
		if ok {
			values[key] = value
		}
	}
	if _, ok := values["GOROOT"]; ok {
		t.Fatalf("shell environment leaked launcher GOROOT: %q", values["GOROOT"])
	}
	if values["PATH"] != "/usr/bin" || values["HOME"] != "/home/test" {
		t.Fatalf("shell environment lost inherited user values: %+v", values)
	}
	if values["GIT_TERMINAL_PROMPT"] != "0" || values["PAGER"] != "cat" {
		t.Fatalf("shell environment lost non-interactive overrides: %+v", values)
	}
}
