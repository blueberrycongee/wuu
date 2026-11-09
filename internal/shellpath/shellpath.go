// Package shellpath resolves the POSIX shell that runs workspace commands
// (bash tool, managed background processes, command hooks, inline skill
// commands). The bash grammar is a cross-platform contract: command
// classification, redaction, and prompt guidance are all written against
// it, so Windows resolves a Git Bash interpreter instead of switching
// dialects. Unix resolution is static and preserves the historical
// interpreters exactly.
package shellpath

// Shell is a resolved command interpreter: the executable plus the
// argument prefix that precedes the command string.
type Shell struct {
	Path string
	Args []string
}

// CommandArgs returns the full argv (after the interpreter path) for
// running command through this shell.
func (s Shell) CommandArgs(command string) []string {
	args := make([]string, 0, len(s.Args)+1)
	args = append(args, s.Args...)
	return append(args, command)
}
