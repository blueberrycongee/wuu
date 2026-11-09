package enginecatalog

import (
	"bytes"
	"context"
	"io"
	"os/exec"
	"regexp"
	"strings"
	"time"
)

var cliVersionPattern = regexp.MustCompile(`^(?:codex-cli )?([0-9]+\.[0-9]+\.[0-9]+(?:[-+][0-9A-Za-z.-]+)?)(?: \(Claude Code\))?$`)

// ObserveVersion reports the installed version for CLIs with a known read-only
// version command. Unknown commands, failures and unexpected output return empty.
// It never invokes engine launch arguments, authentication, or an updater.
func ObserveVersion(ctx context.Context, engineID, binary string) string {
	switch engineID {
	case "codex", "claude":
	default:
		return ""
	}
	ctx, cancel := context.WithTimeout(ctx, 3*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, binary, "--version")
	// Bound both captured output and time spent waiting for inherited pipes.
	cmd.WaitDelay = time.Second
	var out versionOutput
	cmd.Stdout = &out
	cmd.Stderr = io.Discard
	if err := cmd.Run(); err != nil || out.overflow {
		return ""
	}
	match := cliVersionPattern.FindStringSubmatch(strings.TrimSpace(out.buffer.String()))
	if len(match) != 2 {
		return ""
	}
	return match[1]
}

type versionOutput struct {
	buffer   bytes.Buffer
	overflow bool
}

func (b *versionOutput) Write(p []byte) (int, error) {
	const limit = 4096
	n := len(p)
	if remaining := limit - b.buffer.Len(); n > remaining {
		b.overflow = true
		p = p[:remaining]
	}
	_, _ = b.buffer.Write(p)
	return n, nil
}
