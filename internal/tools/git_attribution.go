package tools

import (
	"os"
	"sync"

	"github.com/blueberrycongee/wuu/internal/gitattribution"
)

type gitAttributionShellState struct {
	sync.Mutex
	key    string
	prefix string
}

func (e *Env) gitAttributionEnabled() bool {
	return e == nil || !e.GitAttributionDisabled
}

func (e *Env) gitAttributionShellPrefix() (string, error) {
	if e == nil {
		return buildGitAttributionShellPrefix("", "")
	}

	stateDir := e.SessionDir
	if len(e.ProtectedPaths) > 0 {
		var err error
		stateDir, err = e.ensureProcessSandboxTempDir()
		if err != nil {
			return "", err
		}
	}
	e.gitAttributionShell.Lock()
	defer e.gitAttributionShell.Unlock()
	key := e.GitWrapperExecutable + "\x00" + stateDir
	if e.gitAttributionShell.key == key && e.gitAttributionShell.prefix != "" {
		return e.gitAttributionShell.prefix, nil
	}
	prefix, err := buildGitAttributionShellPrefix(e.GitWrapperExecutable, stateDir)
	if err != nil {
		return "", err
	}
	e.gitAttributionShell.key = key
	e.gitAttributionShell.prefix = prefix
	return prefix, nil
}

func buildGitAttributionShellPrefix(executable, stateDir string) (string, error) {
	if executable == "" {
		resolved, err := os.Executable()
		if err != nil {
			return "", err
		}
		executable = resolved
	}
	binDir, err := gitattribution.EnsureWrapper(executable, stateDir)
	if err != nil {
		return "", err
	}
	return gitattribution.ShellPrefix(binDir), nil
}
