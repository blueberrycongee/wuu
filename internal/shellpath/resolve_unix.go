//go:build !windows

package shellpath

// LoginBash is the shell for workspace commands: bash as a login shell,
// so user profile setup (version managers, PATH additions) applies.
func LoginBash() (Shell, error) {
	return Shell{Path: "bash", Args: []string{"-lc"}}, nil
}

// Sh is the shell for auxiliary commands (hooks, inline skill commands).
func Sh() (Shell, error) {
	return Shell{Path: "sh", Args: []string{"-c"}}, nil
}

// CommandEnv returns env unchanged: unix shells need no path setup here.
func CommandEnv(env []string) []string {
	return env
}
