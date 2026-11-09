package shellpath

import (
	"regexp"
	"runtime"
	"strings"
)

// cmdNullRedirectRe matches cmd.exe-style redirection to the NUL device
// (2>nul, >nul, 1>>nul, 2> nul), keeping the redirection operator and the
// character that terminates the device name.
var cmdNullRedirectRe = regexp.MustCompile(`(?i)([0-9]?>>?[ \t]*)nul([^.\w]|$)`)

// NormalizeBashCommand rewrites cmd.exe-isms that Git Bash would
// misinterpret. Models emit "2>nul" from Windows muscle memory; under
// bash that creates a literal file named "nul" in the repository instead
// of discarding output. No-op outside Windows.
func NormalizeBashCommand(command string) string {
	if runtime.GOOS != "windows" {
		return command
	}
	return rewriteCmdNullRedirect(command)
}

// rewriteCmdNullRedirect rewrites only outside quotes, and not at all in
// commands carrying heredocs: quoted text and heredoc bodies are data,
// and mangling data is strictly worse than the failure being fixed (a
// stray file named "nul"). Unbalanced quotes leave the tail untouched.
func rewriteCmdNullRedirect(command string) string {
	if strings.Contains(command, "<<") {
		return command
	}
	rewrite := func(span string) string {
		return cmdNullRedirectRe.ReplaceAllString(span, "${1}/dev/null${2}")
	}
	var b strings.Builder
	spanStart := 0
	inSingle, inDouble := false, false
	for i := 0; i < len(command); i++ {
		switch c := command[i]; {
		case c == '\\' && !inSingle && i+1 < len(command):
			i++
		case c == '\'' && !inDouble:
			if inSingle {
				b.WriteString(command[spanStart : i+1])
			} else {
				b.WriteString(rewrite(command[spanStart:i]))
				b.WriteByte('\'')
			}
			inSingle = !inSingle
			spanStart = i + 1
		case c == '"' && !inSingle:
			if inDouble {
				b.WriteString(command[spanStart : i+1])
			} else {
				b.WriteString(rewrite(command[spanStart:i]))
				b.WriteByte('"')
			}
			inDouble = !inDouble
			spanStart = i + 1
		}
	}
	tail := command[spanStart:]
	if inSingle || inDouble {
		b.WriteString(tail)
	} else {
		b.WriteString(rewrite(tail))
	}
	return b.String()
}
