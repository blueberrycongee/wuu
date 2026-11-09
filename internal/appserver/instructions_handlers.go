package appserver

import (
	"errors"
	"strings"
)

// MethodInstructionsList exposes the instruction files (AGENTS.md, CLAUDE.md,
// ...) loaded into the base system prompt at session start so users can see
// which project rules are actually in effect.
//
// The method is intentionally read-only and lives in its own handler file
// (like thread_handlers.go) rather than touching model.go, so it does not
// collide with unrelated in-flight changes there.
const MethodInstructionsList = "instructions/list"

// InstructionFile describes one loaded instruction file for the desktop UI.
type InstructionFile struct {
	// Path is the absolute on-disk path of the file.
	Path string `json:"path"`
	// Name is the base filename (AGENTS.md, CLAUDE.md, ...).
	Name string `json:"name"`
	// Source is the raw instructions.File.Source ("user", "project", "local",
	// "claude_auto", ...). Kept for callers that want the fine-grained
	// origin; Scope is the two-level collapse most surfaces render.
	Source string `json:"source"`
	// Scope collapses Source into the two levels the desktop shows:
	// "global" (user-level) or "project".
	Scope string `json:"scope"`
	// Bytes is the size of Content, so the UI can label large files
	// without measuring the string itself.
	Bytes int `json:"bytes"`
	// Content is the raw file contents, returned inline so the UI can
	// expand a preview without a second round-trip. Instruction files are
	// small and only fetched on demand.
	Content string `json:"content"`
}

// InstructionsListResult is the payload returned by MethodInstructionsList.
type InstructionsListResult struct {
	Files []InstructionFile `json:"files"`
}

// handleInstructionsList returns the instruction files discovered for the
// active runtime session. The list is the same set instructions.Discover fed
// into the base system prompt; when no files are found the list is empty.
func (s *Server) handleInstructionsList(req Request) error {
	if s == nil || s.rt == nil {
		return s.writeResponse(req.ID, nil, errors.New("runtime session is required"))
	}
	files := make([]InstructionFile, 0, len(s.rt.InstructionFiles))
	for _, f := range s.rt.InstructionFiles {
		files = append(files, InstructionFile{
			Path:    f.Path,
			Name:    f.Name,
			Source:  f.Source,
			Scope:   instructionFileScope(f.Source),
			Bytes:   len(f.Content),
			Content: f.Content,
		})
	}
	return s.writeResponse(req.ID, InstructionsListResult{Files: files}, nil)
}

// instructionFileScope collapses instructions.File.Source into the two-level model
// the desktop shows. User-level files apply everywhere and read as "global";
// everything discovered in the project hierarchy (project / local /
// claude_auto) reads as "project".
func instructionFileScope(source string) string {
	switch strings.ToLower(strings.TrimSpace(source)) {
	case "user":
		return "global"
	default:
		return "project"
	}
}
