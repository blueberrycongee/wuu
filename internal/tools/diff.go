package tools

import (
	"strings"

	"github.com/sergi/go-diff/diffmatchpatch"
)

// DiffLine represents a single line in a unified diff.
type DiffLine struct {
	Op      string `json:"op"`      // "equal", "insert", "delete"
	Content string `json:"content"` // line content (no trailing newline)
}

// DiffHunk represents a contiguous block of changes with surrounding context.
type DiffHunk struct {
	OldStart int        `json:"old_start"`
	NewStart int        `json:"new_start"`
	Lines    []DiffLine `json:"lines"`
}

// DiffResult is the structured diff included in tool results.
type DiffResult struct {
	Hunks     []DiffHunk `json:"hunks,omitempty"`
	NewFile   bool       `json:"new_file,omitempty"`
	Lines     int        `json:"lines,omitempty"` // total lines for new files
	OldLines  int        `json:"old_lines,omitempty"`
	NewLines  int        `json:"new_lines,omitempty"`
	Truncated bool       `json:"truncated,omitempty"`
	Summary   string     `json:"summary,omitempty"`
}

// computeDiff generates unified diff hunks between old and new text.
// contextLines controls how many unchanged lines surround each change.
func computeDiff(oldText, newText string, contextLines int) DiffResult {
	dmp := diffmatchpatch.New()
	a, b, lines := dmp.DiffLinesToChars(oldText, newText)
	diffs := dmp.DiffMain(a, b, false)
	diffs = dmp.DiffCharsToLines(diffs, lines)
	diffs = dmp.DiffCleanupSemantic(diffs)

	// Convert diffs to line-level operations.
	var allLines []DiffLine
	for _, d := range diffs {
		text := d.Text
		// Split into individual lines, preserving content.
		splitLines := strings.Split(text, "\n")
		// Last element after split on trailing \n is empty — skip it.
		if len(splitLines) > 0 && splitLines[len(splitLines)-1] == "" {
			splitLines = splitLines[:len(splitLines)-1]
		}
		var op string
		switch d.Type {
		case diffmatchpatch.DiffEqual:
			op = "equal"
		case diffmatchpatch.DiffInsert:
			op = "insert"
		case diffmatchpatch.DiffDelete:
			op = "delete"
		}
		for _, l := range splitLines {
			allLines = append(allLines, DiffLine{Op: op, Content: l})
		}
	}

	// Group into hunks with context.
	return groupIntoHunks(allLines, contextLines)
}

// groupIntoHunks splits a flat list of diff lines into hunks,
// keeping contextLines of unchanged lines around each change.
func groupIntoHunks(lines []DiffLine, contextLines int) DiffResult {
	if len(lines) == 0 {
		return DiffResult{}
	}

	// Find ranges of changed lines.
	type changeRange struct{ start, end int }
	var changes []changeRange
	for i, l := range lines {
		if l.Op != "equal" {
			if len(changes) == 0 || i > changes[len(changes)-1].end+1 {
				changes = append(changes, changeRange{i, i})
			} else {
				changes[len(changes)-1].end = i
			}
		}
	}

	if len(changes) == 0 {
		return DiffResult{} // no changes
	}

	// Merge nearby changes (within 2*contextLines gap).
	var merged []changeRange
	for _, c := range changes {
		if len(merged) > 0 && c.start-merged[len(merged)-1].end <= 2*contextLines {
			merged[len(merged)-1].end = c.end
		} else {
			merged = append(merged, c)
		}
	}

	// Build hunks.
	var hunks []DiffHunk
	for _, m := range merged {
		start := m.start - contextLines
		if start < 0 {
			start = 0
		}
		end := m.end + contextLines
		if end >= len(lines) {
			end = len(lines) - 1
		}

		// Compute old/new start line numbers.
		oldLine := 1
		newLine := 1
		for i := 0; i < start; i++ {
			switch lines[i].Op {
			case "equal":
				oldLine++
				newLine++
			case "delete":
				oldLine++
			case "insert":
				newLine++
			}
		}

		hunk := DiffHunk{
			OldStart: oldLine,
			NewStart: newLine,
			Lines:    make([]DiffLine, 0, end-start+1),
		}
		for i := start; i <= end; i++ {
			hunk.Lines = append(hunk.Lines, lines[i])
		}
		hunks = append(hunks, hunk)
	}

	return DiffResult{Hunks: hunks}
}
