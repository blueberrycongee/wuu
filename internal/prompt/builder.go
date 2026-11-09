// Package prompt implements a section-based system prompt builder.
//
// Static sections (base prompt, coordinator preamble, session environment)
// are placed first so the prompt prefix stays stable across turns, maximizing
// provider prompt-cache hit rates. Session-scoped discovered sections such as
// instruction files, skills, and workflows follow. Volatile repository state belongs in
// per-turn context injection or tools, not in this builder.
//
// Instruction files are truncated to MaxInstructionLines / MaxInstructionBytes to prevent
// prompt explosion from large project instruction files.
package prompt

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"strings"

	"github.com/blueberrycongee/wuu/internal/instructions"
	"github.com/blueberrycongee/wuu/internal/skills"
)

const (
	sectionInfoHashBytes = 16

	// MaxInstructionLines caps a single instruction file at 200 lines.
	MaxInstructionLines = 200
	// MaxInstructionBytes caps a single instruction file at 25 KB.
	MaxInstructionBytes = 25 * 1024
)

// Section is one logical piece of the system prompt.
type Section struct {
	Key     string // unique identifier for dedup / replacement
	Content string
	Static  bool // true = part of the fixed built-in prefix
}

// SectionInfo is metadata about one rendered prompt section. It intentionally
// excludes raw section text so it can be emitted in request telemetry.
type SectionInfo struct {
	Key    string
	Static bool
	Bytes  int
	Hash   string
}

// BuildResult is the rendered prompt plus metadata for each section in final
// provider-visible order.
type BuildResult struct {
	Content  string
	Sections []SectionInfo
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

// AddInstructions adds discovered instruction files with per-file truncation.
func (b *Builder) AddInstructions(files []instructions.File) {
	if len(files) == 0 {
		return
	}
	var sb strings.Builder
	sb.WriteString("# Workspace instructions\n\n")
	sb.WriteString("The following markdown files were discovered for this session. ")
	sb.WriteString("Instruction files may contain conventions, style guides, and constraints; follow them unless they conflict with higher-priority system, developer, or tool rules. ")
	sb.WriteString("When files overlap, prefer the more specific local or project instruction for that workspace. Legacy imported files may be stale, so verify time-sensitive details against the current workspace.\n\n")
	for _, f := range files {
		content := TruncateInstructions(f.Content, MaxInstructionLines, MaxInstructionBytes)
		fmt.Fprintf(&sb, "## %s _[%s · %s]_\n\n", f.Name, f.Source, f.Path)
		sb.WriteString(strings.TrimRight(content, "\n"))
		sb.WriteString("\n\n")
	}
	b.AddSection("instructions", strings.TrimRight(sb.String(), "\n"), false)
}

// MemdirSection renders the file-directory memory block: teaching text plus
// the notebook's MEMORY.md index snapshot (memory-redesign contract §4/§5).
// It is exposed as a plain function so thread-creation paths can re-render
// the section with a fresh index without rebuilding the whole prompt.
func MemdirSection(teaching, indexContent string) string {
	teaching = strings.TrimSpace(teaching)
	if teaching == "" {
		return ""
	}
	var sb strings.Builder
	sb.WriteString(teaching)
	sb.WriteString("\n\n## MEMORY.md\n\n")
	if index := strings.TrimSpace(indexContent); index != "" {
		sb.WriteString(index)
	} else {
		sb.WriteString("The MEMORY.md index is currently empty. Index lines will appear here as memories are saved.")
	}
	return sb.String()
}

// AddMemdir adds the file-directory memory section: teaching text (how to
// save, the four memory types, what not to save) plus a frozen snapshot of
// the notebook index. Mid-session writes change the files but not this
// prompt; the next session (or thread creation / compact) sees them.
func (b *Builder) AddMemdir(teaching, indexContent string) {
	b.AddSection("memdir", MemdirSection(teaching, indexContent), false)
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
	sb.WriteString("Skills provide specialized instructions and workflows for specific tasks.\n")
	sb.WriteString("Use the `load_skill` tool to load a skill when a task matches its description.\n")
	sb.WriteString("Users can also invoke skills directly by typing `/<skill-name>` (e.g. `/docs`). When that happens, treat the text after the command as the skill arguments and load the matching skill before acting.\n\n")
	sb.WriteString(skills.FormatAvailable(visible, true))
	b.AddSection("skills", strings.TrimRight(sb.String(), "\n"), false)
}

// Build returns the assembled system prompt. Static sections appear first
// (preserving insertion order), then dynamic sections.
func (b *Builder) Build() string {
	return b.BuildWithInfo().Content
}

// BuildWithInfo returns the assembled system prompt and section metadata.
func (b *Builder) BuildWithInfo() BuildResult {
	ordered := b.orderedSections()
	contents := make([]string, 0, len(ordered))
	infos := make([]SectionInfo, 0, len(ordered))
	for _, s := range ordered {
		contents = append(contents, s.Content)
		infos = append(infos, SectionInfo{
			Key:    s.Key,
			Static: s.Static,
			Bytes:  len([]byte(s.Content)),
			Hash:   shortSectionHash(s.Content),
		})
	}
	return BuildResult{
		Content:  strings.Join(contents, "\n\n"),
		Sections: infos,
	}
}

func (b *Builder) orderedSections() []Section {
	var statics, dynamics []Section
	for _, s := range b.sections {
		if s.Static {
			statics = append(statics, s)
		} else {
			dynamics = append(dynamics, s)
		}
	}
	return append(statics, dynamics...)
}

func shortSectionHash(value string) string {
	sum := sha256.Sum256([]byte(value))
	return hex.EncodeToString(sum[:sectionInfoHashBytes])
}

// TruncateInstructions caps content at maxLines and maxBytes, whichever
// limit is hit first. Appends a marker if truncation occurred.
func TruncateInstructions(content string, maxLines, maxBytes int) string {
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
