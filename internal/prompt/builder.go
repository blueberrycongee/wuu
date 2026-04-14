// Package prompt implements a section-based system prompt builder.
//
// Static sections (base prompt, coordinator preamble) are placed first
// so the prompt prefix stays stable across turns — maximising provider
// prompt-cache hit rates. Dynamic sections (memory, skills, git
// context) follow.
//
// Memory files are truncated to MaxMemoryLines / MaxMemoryBytes to
// prevent prompt explosion from large AGENTS.md or CLAUDE.md files.
// Aligned with Claude Code's 200-line / 25 KB caps.
package prompt

import (
	"fmt"
	"strings"

	"github.com/blueberrycongee/wuu/internal/memory"
	"github.com/blueberrycongee/wuu/internal/skills"
)

const (
	// MaxMemoryLines caps a single memory file at 200 lines.
	MaxMemoryLines = 200
	// MaxMemoryBytes caps a single memory file at 25 KB.
	MaxMemoryBytes = 25 * 1024
)

// Section is one logical piece of the system prompt.
type Section struct {
	Key     string // unique identifier for dedup / replacement
	Content string
	Static  bool // true = part of the stable cache prefix
}

// Builder assembles the final system prompt from sections.
type Builder struct {
	sections []Section
}

// AddSection appends a named section. Duplicate keys overwrite.
func (b *Builder) AddSection(key, content string, static bool) {
	content = strings.TrimSpace(content)
	if content == "" {
		return
	}
	for i := range b.sections {
		if b.sections[i].Key == key {
			b.sections[i] = Section{Key: key, Content: content, Static: static}
			return
		}
	}
	b.sections = append(b.sections, Section{Key: key, Content: content, Static: static})
}

// AddMemory adds a "Memory" section from discovered memory files,
// applying per-file truncation.
func (b *Builder) AddMemory(files []memory.File) {
	if len(files) == 0 {
		return
	}
	var sb strings.Builder
	sb.WriteString("# Memory\n\n")
	sb.WriteString("The following memory files contain project- and user-defined conventions, ")
	sb.WriteString("style guides, and constraints. Treat them as binding instructions for this session.\n\n")
	for _, f := range files {
		content := TruncateMemory(f.Content, MaxMemoryLines, MaxMemoryBytes)
		fmt.Fprintf(&sb, "## %s _[%s · %s]_\n\n", f.Name, f.Source, f.Path)
		sb.WriteString(strings.TrimRight(content, "\n"))
		sb.WriteString("\n\n")
	}
	b.AddSection("memory", strings.TrimRight(sb.String(), "\n"), false)
}

// AddSkills adds a "Skills" section from discovered skills.
func (b *Builder) AddSkills(sks []skills.Skill) {
	visible := make([]skills.Skill, 0, len(sks))
	for _, s := range sks {
		if s.DisableModelInvoke {
			continue
		}
		visible = append(visible, s)
	}
	if len(visible) == 0 {
		return
	}

	var sb strings.Builder
	sb.WriteString("# Session-specific guidance\n\n")
	sb.WriteString("## Skills\n\n")
	sb.WriteString("The following skills are available in this session. Each skill is a reusable, ")
	sb.WriteString("project- or user-defined instruction set that encodes conventions, recipes, or workflows.\n\n")
	sb.WriteString("**How to use skills:**\n")
	sb.WriteString("1. Read the skill catalog below — match the user's intent against each skill's description and \"when to use\" guidance.\n")
	sb.WriteString("2. When a skill applies, call the `load_skill` tool with the skill's name to retrieve the full body. ")
	sb.WriteString("Pass any user-supplied arguments via the `arguments` parameter.\n")
	sb.WriteString("3. Follow the loaded skill's instructions exactly. If the skill body contains tool restrictions or step orderings, respect them.\n")
	sb.WriteString("4. Users can also invoke skills directly by typing `/<skill-name>` (e.g. `/commit`). When that happens, the skill body is injected as a user message — no need to call `load_skill` separately.\n\n")
	sb.WriteString("**Skill catalog:**\n\n")
	for _, s := range visible {
		desc := s.Description
		if desc == "" {
			desc = "(no description)"
		}
		fmt.Fprintf(&sb, "- **%s**: %s", s.Name, desc)
		if s.WhenToUse != "" {
			fmt.Fprintf(&sb, "\n  _When to use:_ %s", s.WhenToUse)
		}
		if s.ArgumentHint != "" {
			fmt.Fprintf(&sb, "\n  _Arguments:_ `%s`", s.ArgumentHint)
		}
		sb.WriteString("\n")
	}
	b.AddSection("skills", strings.TrimRight(sb.String(), "\n"), false)
}

// AddGitContext adds git status information as a dynamic section.
func (b *Builder) AddGitContext(gitInfo string) {
	if strings.TrimSpace(gitInfo) == "" {
		return
	}
	b.AddSection("git_context", "# Git Context\n\n"+gitInfo, false)
}

// Build returns the assembled system prompt. Static sections appear
// first (sorted by insertion order), then dynamic sections.
func (b *Builder) Build() string {
	var statics, dynamics []string
	for _, s := range b.sections {
		if s.Static {
			statics = append(statics, s.Content)
		} else {
			dynamics = append(dynamics, s.Content)
		}
	}
	all := append(statics, dynamics...)
	return strings.Join(all, "\n\n")
}

// TruncateMemory caps content at maxLines and maxBytes, whichever
// limit is hit first. Appends a marker if truncation occurred.
func TruncateMemory(content string, maxLines, maxBytes int) string {
	if len(content) <= maxBytes && countLines(content) <= maxLines {
		return content
	}

	lines := strings.SplitAfter(content, "\n")
	var b strings.Builder
	lineCount := 0
	for _, line := range lines {
		if lineCount >= maxLines || b.Len()+len(line) > maxBytes {
			omitted := len(lines) - lineCount
			fmt.Fprintf(&b, "\n[truncated — %d lines omitted]", omitted)
			return b.String()
		}
		b.WriteString(line)
		lineCount++
	}
	return b.String()
}

func countLines(s string) int {
	n := strings.Count(s, "\n")
	if len(s) > 0 && s[len(s)-1] != '\n' {
		n++
	}
	return n
}
