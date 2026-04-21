package tui

import (
	"strings"
	"unicode"
)

// highlightSeg is a visual-column range for search match highlighting.
type highlightSeg struct {
	start     int
	end       int
	isCurrent bool
}

// searchState tracks an in-viewport search session.
// Search is a screen overlay (not component state) — matches are found
// in the rendered content string and highlighted via SGR overlay.
type searchState struct {
	Active      bool
	Query       string
	Matches     []searchMatch // all matches in current rendered content
	CurrentIdx  int           // which match is currently navigated to
	CaseSensitive bool
}

type searchMatch struct {
	StartLine int // 0-indexed line in rendered content
	StartCol  int // 0-indexed visual column on that line
	EndLine   int
	EndCol    int
	Length    int // rune length of match
}

func (s *searchState) clear() {
	s.Active = false
	s.Query = ""
	s.Matches = nil
	s.CurrentIdx = 0
}

func (s *searchState) hasMatches() bool {
	return s.Active && len(s.Matches) > 0
}

func (s *searchState) currentMatch() *searchMatch {
	if !s.hasMatches() {
		return nil
	}
	if s.CurrentIdx < 0 {
		s.CurrentIdx = 0
	}
	if s.CurrentIdx >= len(s.Matches) {
		s.CurrentIdx = len(s.Matches) - 1
	}
	return &s.Matches[s.CurrentIdx]
}

func (s *searchState) next() {
	if len(s.Matches) == 0 {
		return
	}
	s.CurrentIdx = (s.CurrentIdx + 1) % len(s.Matches)
}

func (s *searchState) prev() {
	if len(s.Matches) == 0 {
		return
	}
	s.CurrentIdx = (s.CurrentIdx - 1 + len(s.Matches)) % len(s.Matches)
}

// searchInContent finds all occurrences of query in rendered content.
// Returns matches in content-absolute coordinates (line, col).
func searchInContent(content, query string, caseSensitive bool) []searchMatch {
	if query == "" {
		return nil
	}
	lines := strings.Split(content, "\n")
	var matches []searchMatch

	searchQuery := query
	if !caseSensitive {
		searchQuery = strings.ToLower(query)
	}

	for lineIdx, line := range lines {
		searchLine := line
		if !caseSensitive {
			searchLine = strings.ToLower(line)
		}

		// Find all occurrences on this line.
		start := 0
		for {
			idx := strings.Index(searchLine[start:], searchQuery)
			if idx < 0 {
				break
			}
			absStart := start + idx
			absEnd := absStart + len(searchQuery)

			// Compute visual column (accounting for ANSI codes).
			visualStart := visualWidth(line[:absStart])
			visualEnd := visualWidth(line[:absEnd])

			matches = append(matches, searchMatch{
				StartLine: lineIdx,
				StartCol:  visualStart,
				EndLine:   lineIdx,
				EndCol:    visualEnd,
				Length:    len([]rune(query)),
			})
			start = absEnd
			if start >= len(searchLine) {
				break
			}
		}
	}
	return matches
}

// visualWidth returns the visual display width of a string that may
// contain ANSI escape sequences. Strips ANSI first, then counts runes.
func visualWidth(s string) int {
	// Simple approach: strip ANSI and count runes.
	// For exact visual width we should use lipgloss.Width, but that
	// processes ANSI — we need to strip first for raw text width.
	clean := stripANSIFromString(s)
	w := 0
	for _, r := range clean {
		if r == '\t' {
			w += 4 - (w % 4)
		} else if unicode.IsPrint(r) {
			w++
		}
	}
	return w
}

// stripANSIFromString removes ANSI escape sequences from a string.
func stripANSIFromString(s string) string {
	var b strings.Builder
	inESC := false
	for i := 0; i < len(s); i++ {
		c := s[i]
		if inESC {
			if c >= 0x40 && c <= 0x7E {
				inESC = false
			}
			continue
		}
		if c == '\x1b' {
			inESC = true
			continue
		}
		b.WriteByte(c)
	}
	return b.String()
}

// overlaySearchHighlight applies search match highlights onto the visible
// viewport output. Similar to overlaySelection, this works on the visible
// window using viewport YOffset for coordinate translation.
func overlaySearchHighlight(output string, state *searchState, yOffset, vpWidth int) string {
	if !state.hasMatches() {
		return output
	}

	lines := strings.Split(output, "\n")
	var out strings.Builder

	for row, line := range lines {
		contentRow := row + yOffset
		// Find matches that span this visible row.
		var rowMatches []searchMatch
		for _, m := range state.Matches {
			if m.StartLine <= contentRow && m.EndLine >= contentRow {
				rowMatches = append(rowMatches, m)
			}
		}
		if len(rowMatches) == 0 {
			out.WriteString(line)
			if row < len(lines)-1 {
				out.WriteString("\n")
			}
			continue
		}

		// Build highlight segments for this line.
		// We need to overlay highlight on the raw text while preserving
		// existing ANSI codes. This is done by finding the visual column
		// ranges and wrapping those segments.
		line = overlayHighlightsOnLine(line, rowMatches, contentRow, state.CurrentIdx, vpWidth)
		out.WriteString(line)
		if row < len(lines)-1 {
			out.WriteString("\n")
		}
	}

	return out.String()
}

// overlayHighlightsOnLine applies highlight styles to matched segments on
// a single line, preserving existing ANSI codes outside matched regions.
func overlayHighlightsOnLine(line string, matches []searchMatch, contentRow, currentIdx, vpWidth int) string {
	var segs []highlightSeg
	for i, m := range matches {
		if m.StartLine > contentRow || m.EndLine < contentRow {
			continue
		}
		var s, e int
		if m.StartLine == contentRow {
			s = m.StartCol
		} else {
			s = 0
		}
		if m.EndLine == contentRow {
			e = m.EndCol
		} else {
			e = vpWidth
		}
		if s < e {
			segs = append(segs, highlightSeg{start: s, end: e, isCurrent: i == currentIdx})
		}
	}
	if len(segs) == 0 {
		return line
	}

	// Merge overlapping segments.
	segs = mergeSegs(segs)

	// Walk the line character by character (preserving ANSI codes),
	// wrapping matched visual-column ranges with highlight styles.
	var out strings.Builder
	visCol := 0
	inESC := false
	segIdx := 0

	for i := 0; i < len(line); i++ {
		c := line[i]
		if inESC {
			out.WriteByte(c)
			if c >= 0x40 && c <= 0x7E {
				inESC = false
			}
			continue
		}
		if c == '\x1b' {
			out.WriteByte(c)
			inESC = true
			continue
		}

		// Check if this visual column is inside any highlight segment.
		inMatch := false
		isCurrent := false
		for segIdx < len(segs) && segs[segIdx].end <= visCol {
			segIdx++
		}
		if segIdx < len(segs) && visCol >= segs[segIdx].start && visCol < segs[segIdx].end {
			inMatch = true
			isCurrent = segs[segIdx].isCurrent
		}

		if inMatch {
			style := searchMatchStyle
			if isCurrent {
				style = searchCurrentStyle
			}
			out.WriteString(style.Render(string(c)))
		} else {
			out.WriteByte(c)
		}
		visCol++
	}

	return out.String()
}

func mergeSegs(segs []highlightSeg) []highlightSeg {
	if len(segs) <= 1 {
		return segs
	}
	// Sort by start.
	for i := 0; i < len(segs)-1; i++ {
		for j := i + 1; j < len(segs); j++ {
			if segs[j].start < segs[i].start {
				segs[i], segs[j] = segs[j], segs[i]
			}
		}
	}
	merged := []highlightSeg{segs[0]}
	for i := 1; i < len(segs); i++ {
		last := &merged[len(merged)-1]
		if segs[i].start <= last.end {
			if segs[i].end > last.end {
				last.end = segs[i].end
			}
			if segs[i].isCurrent {
				last.isCurrent = true
			}
		} else {
			merged = append(merged, segs[i])
		}
	}
	return merged
}
