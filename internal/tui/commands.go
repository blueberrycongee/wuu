package tui

import (
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
	"time"
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
		if strings.TrimSpace(m.memoryPath) == "" {
			return "resume: memory file is disabled for this session.", true
		}
		entries, err := loadMemoryEntries(m.memoryPath)
		if err != nil {
			return fmt.Sprintf("resume: failed to read memory: %v", err), true
		}
		if len(entries) == 0 {
			return "resume: no saved entries found.", true
		}
		m.entries = entries
		m.refreshViewport(true)
		return fmt.Sprintf("resume: loaded %d entries from %s", len(entries), m.memoryPath), true
	case "fork":
		if strings.TrimSpace(m.memoryPath) == "" {
			return "fork: memory file is disabled for this session.", true
		}
		target := strings.TrimSuffix(m.memoryPath, filepath.Ext(m.memoryPath))
		target = fmt.Sprintf("%s.fork-%s.jsonl", target, time.Now().Format("20060102-150405"))
		if err := copyFile(m.memoryPath, target); err != nil {
			return fmt.Sprintf("fork: failed to create snapshot: %v", err), true
		}
		return fmt.Sprintf("fork: session snapshot created at %s", target), true
	case "worktree":
		branch := currentBranch(m.workspaceRoot)
		return fmt.Sprintf("worktree: workspace=%s branch=%s", m.workspaceRoot, branch), true
	case "skills":
		skills := discoverLocalSkills(filepath.Join(m.workspaceRoot, ".claude", "skills"))
		if len(skills) == 0 {
			return "skills: no local skills found under .claude/skills", true
		}
		return fmt.Sprintf("skills: %s", strings.Join(skills, ", ")), true
	case "insight":
		return fmt.Sprintf(
			"insight: transcript entries=%d pending=%v streaming=%v memory=%s",
			m.entryCount(),
			m.pendingRequest,
			m.streaming,
			m.memoryPath,
		), true
	default:
		return fmt.Sprintf("unknown command: /%s", cmd.Name), true
	}
}

func currentBranch(root string) string {
	cmd := exec.Command("git", "rev-parse", "--abbrev-ref", "HEAD")
	cmd.Dir = root
	out, err := cmd.Output()
	if err != nil {
		return "(unknown)"
	}
	branch := strings.TrimSpace(string(out))
	if branch == "" {
		return "(unknown)"
	}
	return branch
}

func discoverLocalSkills(baseDir string) []string {
	entries, err := os.ReadDir(baseDir)
	if err != nil {
		return nil
	}
	skills := make([]string, 0, len(entries))
	for _, entry := range entries {
		if !entry.IsDir() {
			continue
		}
		skillPath := filepath.Join(baseDir, entry.Name(), "SKILL.md")
		if _, statErr := os.Stat(skillPath); statErr == nil {
			skills = append(skills, entry.Name())
		}
	}
	sort.Strings(skills)
	return skills
}

func copyFile(src, dst string) error {
	input, err := os.Open(src)
	if err != nil {
		return fmt.Errorf("open source file: %w", err)
	}
	defer input.Close()

	if err := os.MkdirAll(filepath.Dir(dst), 0o755); err != nil {
		return fmt.Errorf("create destination directory: %w", err)
	}
	output, err := os.OpenFile(dst, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0o644)
	if err != nil {
		return fmt.Errorf("open destination file: %w", err)
	}
	defer output.Close()

	if _, err := io.Copy(output, input); err != nil {
		return fmt.Errorf("copy data: %w", err)
	}
	return nil
}
