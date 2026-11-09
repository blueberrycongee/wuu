//go:build windows

package shellpath

import (
	"os"
	"os/exec"
	"sync"
)

// resolvedGitBash caches resolution for the process lifetime: the chain
// stats well-known paths and walks PATH, and every shell-out repeats it
// otherwise.
var resolvedGitBash = sync.OnceValues(func() (string, error) {
	return findGitBash(os.Getenv, fileExists, exec.LookPath)
})

func fileExists(path string) bool {
	info, err := os.Stat(path)
	return err == nil && !info.IsDir()
}

func dirExists(path string) bool {
	info, err := os.Stat(path)
	return err == nil && info.IsDir()
}

// LoginBash is the shell for workspace commands. Windows runs Git Bash
// with -c rather than -lc: a login shell would source the MSYS profiles
// on every command (slow, nondeterministic PATH), so CommandEnv injects
// the Git tool directories explicitly instead.
func LoginBash() (Shell, error) {
	path, err := resolvedGitBash()
	if err != nil {
		return Shell{}, err
	}
	return Shell{Path: path, Args: []string{"-c"}}, nil
}

// Sh is the shell for auxiliary commands (hooks, inline skill commands).
// Windows has no /bin/sh; Git Bash serves both roles.
func Sh() (Shell, error) {
	return LoginBash()
}

// CommandEnv layers Git Bash runtime requirements onto env. When Git Bash
// is unresolvable env passes through unchanged; the spawn itself will
// already have failed with the install hint.
func CommandEnv(env []string) []string {
	path, err := resolvedGitBash()
	if err != nil {
		return env
	}
	return gitBashCommandEnv(env, path, dirExists)
}
