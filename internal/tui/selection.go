package tui

import (
	"encoding/base64"
	"fmt"
	"os"
	"os/exec"
	"strings"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"github.com/charmbracelet/x/ansi"
)

// selectionPoint is a content-coordinate position inside the rendered
// chat history. Row is the absolute 0-indexed line number in the
// FULL rendered content (not the visible viewport window), and Col
// is the visual column offset on that line. Storing in content
// coordinates is what lets the highlight stay glued to the same
// underlying text when the user scrolls after making a selection —
// the alternative (viewport-row coordinates) leaves the highlight
// painted on whichever line happens to be visible at that screen
// position, which is the bug this comment exists to prevent.
type selectionPoint struct {
	Row int
	Col int
}

// selectionState tracks a linear text selection in the chat viewport.
type selectionState struct {
	Anchor     *selectionPoint
	Focus      *selectionPoint
	IsDragging bool
}

func (s *selectionState) hasSelection() bool {
	return s != nil && s.Anchor != nil && s.Focus != nil
}

func (s *selectionState) clear() {
	s.Anchor = nil
	s.Focus = nil
	s.IsDragging = false
}

func (s *selectionState) start(col, row int) {
	s.Anchor = &selectionPoint{Row: row, Col: col}
	s.Focus = nil
	s.IsDragging = true
}

func (s *selectionState) update(col, row int) {
	if !s.IsDragging {
		return
	}
	if s.Focus == nil && s.Anchor != nil &&
		s.Anchor.Col == col && s.Anchor.Row == row {
		return
	}
	s.Focus = &selectionPoint{Row: row, Col: col}
}

func (s *selectionState) finish() {
	s.IsDragging = false
}

func (s *selectionState) bounds() (start, end *selectionPoint) {
	if !s.hasSelection() {
		return nil, nil
	}
	if s.Anchor.Row < s.Focus.Row ||
		(s.Anchor.Row == s.Focus.Row && s.Anchor.Col <= s.Focus.Col) {
		return s.Anchor, s.Focus
	}
	return s.Focus, s.Anchor
}

// selectedText extracts the highlighted substring out of the FULL
// rendered content (not the visible viewport window). Row indices in
// the selection are absolute content rows; passing the full content
// here is what lets a copy after scroll return the correct text
// even if the original selection has scrolled out of view.
func (s *selectionState) selectedText(fullContent string) string {
	start, end := s.bounds()
	if start == nil {
		return ""
	}
	lines := strings.Split(fullContent, "\n")
	var sb strings.Builder
	for row := start.Row; row <= end.Row && row < len(lines); row++ {
		if row < 0 {
			continue
		}
		stripped := ansi.Strip(lines[row])
		runes := []rune(stripped)
		colStart := 0
		if row == start.Row {
			colStart = start.Col
		}
		colEnd := len(runes)
		if row == end.Row {
			colEnd = end.Col + 1
		}
		if colStart < 0 {
			colStart = 0
		}
		if colEnd > len(runes) {
			colEnd = len(runes)
		}
		if row > start.Row {
			sb.WriteByte('\n')
		}
		if colStart < colEnd {
			sb.WriteString(string(runes[colStart:colEnd]))
		}
	}
	return sb.String()
}

// --- Model methods for text selection ---

// isInChatArea reports whether screen coordinates fall within the chat viewport.
func (m *Model) isInChatArea(x, y int) bool {
	top := m.layout.Chat.Y
	bottom := top + m.layout.Chat.Height
	left := m.layout.Chat.X
	right := m.layout.Chat.X + m.layout.Chat.Width - 2
	return x >= left && x <= right && y >= top && y < bottom
}

// refreshSelectionAutoScroll inspects a fresh mouse motion event and
// updates the auto-scroll state machine. If the cursor is past the
// chat area's top or bottom edge, it stores the direction and a
// distance-proportional speed, then either kicks off a recurring
// tick (if newly active) or just lets the existing tick keep firing
// against the updated state. If the cursor returned inside the
// viewport, it tears the state down and bumps seq so any in-flight
// tick exits cleanly on its next delivery.
//
// Returns a tea.Cmd to schedule the next tick (nil when no tick is
// needed). The motion handler should call this BEFORE updating the
// selection focus, since the immediate motion event already extends
// the selection on its own — the tick is only for the no-further-
// motion case where the user is holding the mouse stationary past
// the edge.
func (m *Model) refreshSelectionAutoScroll(x, y int) tea.Cmd {
	chatTop := m.layout.Chat.Y
	chatBottom := m.layout.Chat.Y + m.layout.Chat.Height
	if m.layout.Chat.Height <= 0 {
		m.stopSelectionAutoScroll()
		return nil
	}

	var dir, speed int
	switch {
	case y < chatTop:
		dir = -1
		speed = chatTop - y
	case y >= chatBottom:
		dir = 1
		speed = y - chatBottom + 1
	default:
		// Mouse came back inside — stop ticking. The motion event
		// itself will still extend the selection to the new point
		// via the regular handler path.
		m.stopSelectionAutoScroll()
		return nil
	}

	if speed > selectionAutoScrollMaxSpeed {
		speed = selectionAutoScrollMaxSpeed
	}

	m.selectionAutoScroll.dir = dir
	m.selectionAutoScroll.speed = speed
	m.selectionAutoScroll.lastX = x

	// Scroll immediately on this motion event so a fast drag past
	// the edge feels responsive — without this the user would have
	// to wait one full tick interval before any scroll happened.
	// The recurring tick handles the "held still past the edge"
	// case where no further motion events arrive.
	m.setViewportOffset(m.viewport.YOffset + dir*speed)

	if m.selectionAutoScroll.active {
		// Already ticking — the in-flight tick will pick up the
		// updated dir/speed/lastX next time it fires.
		return nil
	}
	m.selectionAutoScroll.active = true
	m.selectionAutoScroll.seq++
	return selectionAutoScrollCmd(m.selectionAutoScroll.seq)
}

// stopSelectionAutoScroll halts the recurring auto-scroll tick. seq
// is bumped so any tick already in-flight self-discards on delivery.
func (m *Model) stopSelectionAutoScroll() {
	if !m.selectionAutoScroll.active {
		return
	}
	m.selectionAutoScroll.active = false
	m.selectionAutoScroll.seq++
}

// tickSelectionAutoScroll performs one auto-scroll step: advance the
// viewport offset by the stored speed in the stored direction, then
// re-derive the selection focus point from the *current* visible
// edge row + the saved cursor X. The mouse hasn't moved (otherwise
// a regular motion event would have driven the selection), so we
// have to manufacture the new focus ourselves.
//
// Returns a Cmd to schedule the next tick. We always reschedule
// while active is true — even when the viewport has hit the top
// or bottom of the content and can't actually move — so that the
// state machine keeps polling until the user either releases the
// button or moves the cursor back inside the viewport. The cost
// of an idle tick is trivial.
func (m *Model) tickSelectionAutoScroll() tea.Cmd {
	dir := m.selectionAutoScroll.dir
	speed := m.selectionAutoScroll.speed
	if speed < 1 {
		speed = 1
	}

	newOffset := m.viewport.YOffset + dir*speed
	m.setViewportOffset(newOffset)

	// Re-derive the selection focus from the current edge row.
	// When dragging upward, the focus should sit on the topmost
	// visible row (which is now a previously-hidden line); when
	// dragging downward, on the bottommost visible row.
	var edgeRow int
	if dir < 0 {
		edgeRow = m.viewport.YOffset
	} else {
		edgeRow = m.viewport.YOffset + m.layout.Chat.Height - 1
	}
	col := m.selectionAutoScroll.lastX - m.layout.Chat.X
	if col < 0 {
		col = 0
	}
	m.selection.update(col, edgeRow)

	return selectionAutoScrollCmd(m.selectionAutoScroll.seq)
}

// screenToViewportCoords converts screen (x, y) to a CONTENT-row and
// column inside the rendered chat history. The returned row is
// absolute (visible-row + viewport.YOffset), so a row of 50 means
// "the 51st line of m.renderedContent" regardless of how far the
// user has scrolled. This is what lets selection follow scroll.
//
// Despite the name, callers can treat the return as content coords;
// the function is named for backwards compatibility with the call
// sites that previously assumed viewport coords.
func (m *Model) screenToViewportCoords(x, y int) (contentRow, vpCol int) {
	visibleRow := y - m.layout.Chat.Y
	if visibleRow < 0 {
		visibleRow = 0
	}
	if visibleRow >= m.layout.Chat.Height {
		visibleRow = m.layout.Chat.Height - 1
	}
	vpCol = x - m.layout.Chat.X
	if vpCol < 0 {
		vpCol = 0
	}
	contentRow = visibleRow + m.viewport.YOffset
	return contentRow, vpCol
}

// copySelectionToClipboard copies the current mouse selection. It
// prefers a real native clipboard tool (pbcopy / xclip / etc) and only
// falls back to OSC 52 when none are available — OSC 52 is silently
// dropped by many terminals, which is what made drag-to-copy look
// completely broken before.
//
// Reads from the FULL rendered content (m.renderedContent) rather
// than viewport.View() so a selection made before scrolling still
// copies correctly after the user has scrolled it out of view.
func (m *Model) copySelectionToClipboard() {
	text := m.selection.selectedText(m.renderedContent)
	if text == "" {
		return
	}
	method, err := writeClipboard(text)
	if err != nil {
		m.statusLine = "copy failed: install pbcopy / xclip / wl-copy"
		return
	}
	if method == "osc52" {
		// We can't actually verify OSC 52 reached the system clipboard
		// — many terminals accept the sequence and drop it. Be honest.
		m.statusLine = "copied via OSC 52 (terminal-dependent)"
		return
	}
	m.statusLine = "copied"
}

// writeClipboard sends text to the system clipboard. It tries native
// helpers in order, then falls back to OSC 52. Returns the method name
// that succeeded ("pbcopy", "wl-copy", "xclip", "xsel", or "osc52").
func writeClipboard(text string) (string, error) {
	candidates := []struct {
		name string
		args []string
	}{
		{"pbcopy", nil},
		{"wl-copy", nil},
		{"xclip", []string{"-selection", "clipboard"}},
		{"xsel", []string{"--clipboard", "--input"}},
	}
	for _, c := range candidates {
		if _, err := exec.LookPath(c.name); err != nil {
			continue
		}
		cmd := exec.Command(c.name, c.args...)
		cmd.Stdin = strings.NewReader(text)
		if err := cmd.Run(); err == nil {
			return c.name, nil
		}
	}
	// OSC 52 fallback. Works in iTerm2 (with the option enabled),
	// tmux (set-clipboard on), VS Code, kitty, wezterm, etc.
	encoded := base64.StdEncoding.EncodeToString([]byte(text))
	seq := fmt.Sprintf("\x1b]52;c;%s\x1b\\", encoded)
	if _, err := os.Stdout.WriteString(seq); err != nil {
		return "", err
	}
	return "osc52", nil
}

// --- Rendering helpers ---

// overlaySelection paints the selection highlight onto the visible
// viewport window. `output` is the viewport's View() string (only the
// rows from yOffset to yOffset+H-1). The selection coordinates are
// absolute CONTENT rows, so we translate them into the visible
// window's local row indices and clip to the window before painting.
//
// Lines outside the visible window are silently skipped — when the
// user scrolls a selection out of view, the highlight just becomes
// invisible until they scroll back. The selection state itself is
// untouched, so a copy still returns the right text.
func overlaySelection(output string, sel *selectionState, yOffset int, style lipgloss.Style) string {
	if sel == nil || !sel.hasSelection() {
		return output
	}
	start, end := sel.bounds()
	if start == nil {
		return output
	}
	lines := strings.Split(output, "\n")
	for row := start.Row; row <= end.Row; row++ {
		// Translate absolute content row → visible-window row index.
		visible := row - yOffset
		if visible < 0 || visible >= len(lines) {
			continue
		}
		colStart := 0
		if row == start.Row {
			colStart = start.Col
		}
		colEnd := lipgloss.Width(lines[visible])
		if row == end.Row {
			colEnd = end.Col + 1
		}
		lines[visible] = highlightLineRange(lines[visible], colStart, colEnd, style)
	}
	return strings.Join(lines, "\n")
}

func highlightLineRange(line string, colStart, colEnd int, style lipgloss.Style) string {
	if colStart >= colEnd {
		return line
	}
	stripped := ansi.Strip(line)
	runes := []rune(stripped)
	lineWidth := len(runes)
	if colStart >= lineWidth {
		return line
	}
	if colEnd > lineWidth {
		colEnd = lineWidth
	}
	before := ""
	if colStart > 0 {
		before = ansi.Truncate(line, colStart, "")
	}
	middle := style.Render(string(runes[colStart:colEnd]))
	after := ""
	if colEnd < lineWidth {
		after = cutLeadingVisualCols(line, colEnd)
	}
	return before + middle + after
}

func cutLeadingVisualCols(s string, n int) string {
	visualCol := 0
	i := 0
	bytes := []byte(s)
	for i < len(bytes) && visualCol < n {
		if bytes[i] == 0x1b && i+1 < len(bytes) && bytes[i+1] == '[' {
			j := i + 2
			for j < len(bytes) && bytes[j] >= 0x30 && bytes[j] <= 0x3F {
				j++
			}
			for j < len(bytes) && bytes[j] >= 0x20 && bytes[j] <= 0x2F {
				j++
			}
			if j < len(bytes) {
				j++
			}
			i = j
		} else if bytes[i] == 0x1b && i+1 < len(bytes) && bytes[i+1] == ']' {
			j := i + 2
			for j < len(bytes) {
				if bytes[j] == 0x1b && j+1 < len(bytes) && bytes[j+1] == '\\' {
					j += 2
					break
				}
				if bytes[j] == 0x07 {
					j++
					break
				}
				j++
			}
			i = j
		} else {
			sz := 1
			b := bytes[i]
			if b >= 0xC0 && b < 0xE0 {
				sz = 2
			} else if b >= 0xE0 && b < 0xF0 {
				sz = 3
			} else if b >= 0xF0 {
				sz = 4
			}
			if i+sz > len(bytes) {
				sz = len(bytes) - i
			}
			i += sz
			visualCol++
		}
	}
	if i >= len(bytes) {
		return ""
	}
	return string(bytes[i:])
}
