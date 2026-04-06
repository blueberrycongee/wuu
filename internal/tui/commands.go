package tui

import (
	"fmt"
	"strings"
)

type slashCommand struct {
	Name string
	Args string
}

func parseSlashCommand(input string) (slashCommand, bool) {
	trimmed := strings.TrimSpace(input)
	if trimmed == "" || !strings.HasPrefix(trimmed, "/") {
		return slashCommand{}, false
	}
	fields := strings.Fields(trimmed[1:])
	if len(fields) == 0 {
		return slashCommand{}, false
	}

	name := strings.ToLower(strings.TrimSpace(fields[0]))
	args := ""
	if len(fields) > 1 {
		args = strings.TrimSpace(strings.Join(fields[1:], " "))
	}
	return slashCommand{Name: name, Args: args}, true
}

func (m *Model) handleSlash(input string) (string, bool) {
	cmd, ok := parseSlashCommand(input)
	if !ok {
		return "", false
	}

	switch cmd.Name {
	case "resume":
		if cmd.Args == "" {
			return "resume: session recovery is not configured yet. Pass a session id in future rounds.", true
		}
		return fmt.Sprintf("resume: requested session %q (implementation stub).", cmd.Args), true
	case "fork":
		return "fork: agent forking workflow will be wired in a later round.", true
	case "worktree":
		return fmt.Sprintf("worktree: current workspace %s", m.workspaceRoot), true
	case "skills":
		return "skills: available core capabilities include hooks, memory, slash-commands, and provider adapters.", true
	case "insight":
		return fmt.Sprintf("insight: transcript entries=%d pending=%v streaming=%v", m.entryCount(), m.pendingRequest, m.streaming), true
	default:
		return fmt.Sprintf("unknown command: /%s", cmd.Name), true
	}
}
