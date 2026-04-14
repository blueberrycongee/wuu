package tui

import (
	"encoding/base64"
	"fmt"
	"os"
	"os/exec"
	"strconv"
	"strings"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"github.com/charmbracelet/x/ansi"
	"github.com/mattn/go-runewidth"
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
		lineWidth := lipgloss.Width(stripped)
		baseCol := selectionBaseColForLine(stripped)
		colStart := baseCol
		if row == start.Row {
			colStart = baseCol + start.Col
		}
		colEnd := lineWidth
		if row == end.Row {
			colEnd = baseCol + end.Col + 1
		}
		if colStart < 0 {
			colStart = 0
		}
		if colEnd > lineWidth {
			colEnd = lineWidth
		}
		if row > start.Row {
			sb.WriteByte('\n')
		}
		if colStart < colEnd {
			sb.WriteString(slicePlainTextByVisualCols(stripped, colStart, colEnd))
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
	right := m.layout.Chat.X + m.layout.Chat.Width - 2 - contentPadRight
	return x >= left && x <= right && y >= top && y < bottom
}

// isInInlineStatusArea reports whether screen coordinates fall on the
// inline status line (the "Finished" / "Running tool" row just below
// the chat viewport). Wheel events here should scroll the chat too.
func (m *Model) isInInlineStatusArea(_, y int) bool {
	statusY := m.layout.Chat.Y + m.layout.Chat.Height
	return y == statusY
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
	colBase := m.selectionBaseColForRow(edgeRow)
	col := m.selectionAutoScroll.lastX - m.layout.Chat.X - colBase
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
	contentRow = visibleRow + m.viewport.YOffset
	colBase := m.selectionBaseColForRow(contentRow)
	vpCol = x - m.layout.Chat.X - colBase
	if vpCol < 0 {
		vpCol = 0
	}
	return contentRow, vpCol
}

// selectionBaseColForRow returns the visual column offset where text
// content starts on the given absolute content row.
func (m *Model) selectionBaseColForRow(contentRow int) int {
	if contentRow < 0 {
		return contentPadLeft
	}
	lines := strings.Split(m.renderedContent, "\n")
	if contentRow >= len(lines) {
		return contentPadLeft
	}
	base := selectionBaseColForLine(ansi.Strip(lines[contentRow]))
	if base < contentPadLeft {
		return contentPadLeft
	}
	return base
}

// selectionBaseColForLine returns the visual column offset where real
// text begins in a rendered line. It skips leading spaces used for
// viewport/message indentation.
func selectionBaseColForLine(stripped string) int {
	if stripped == "" {
		return 0
	}
	spaces := 0
	for _, r := range stripped {
		if r != ' ' {
			break
		}
		spaces++
	}
	return spaces
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
		m.setCopyStatusLine("copy failed: install pbcopy / xclip / wl-copy")
		return
	}
	if method == "osc52" {
		// We can't actually verify OSC 52 reached the system clipboard
		// — many terminals accept the sequence and drop it. Be honest.
		m.setCopyStatusLine("copied via OSC 52 (terminal-dependent)")
		return
	}
	m.setCopyStatusLine("copied")
}

// setCopyStatusLine records copy feedback only when it would not hide an
// active reply/tool/thinking indicator. While a request is in flight, the
// waiting status is more important than transient clipboard feedback.
func (m *Model) setCopyStatusLine(status string) {
	if m.streaming || m.pendingRequest || m.currentWorkStatus().Phase != workPhaseIdle {
		return
	}
	m.statusLine = status
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
//
// The highlight extends edge-to-edge across the full viewportWidth:
// middle lines are highlighted from column 0 to the viewport edge,
// the start row begins at the click position and extends right, and
// the end row begins at column 0 and stops at the click position.
// Each line is padded with spaces up to viewportWidth before the
// highlight is applied so that short lines and empty lines produce
// a continuous, gap-free selection block — matching the behavior of
// native terminal emulators and editors like VS Code.
//
// The highlight is a background-only overlay (it does not touch the
// foreground color or any other SGR attribute), so any markdown
// styling, syntax highlighting, or role-label color in the original
// text remains visible underneath.
func overlaySelection(output string, sel *selectionState, yOffset, viewportWidth int) string {
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
		stripped := ansi.Strip(lines[visible])
		lineWidth := lipgloss.Width(stripped)
		baseCol := selectionBaseColForLine(stripped)

		// For middle lines (not start/end), highlight from column 0
		// to viewport edge — like native terminal or editor selection.
		// Start row begins at the click position; end row stops there.
		colStart := 0
		if row == start.Row {
			colStart = baseCol + start.Col
		}
		colEnd := viewportWidth
		if row == end.Row {
			colEnd = baseCol + end.Col + 1
		}

		// Pad the line with spaces so the highlight can extend
		// edge-to-edge across the full viewport width.
		if lineWidth < viewportWidth {
			lines[visible] += strings.Repeat(" ", viewportWidth-lineWidth)
		}

		lines[visible] = highlightLineRange(lines[visible], colStart, colEnd)
	}
	return strings.Join(lines, "\n")
}

// highlightLineRange paints the selection background between two
// visual columns of one rendered line. The original line keeps every
// foreground attribute it had — color, bold, italic, dim, underline —
// and only the bg slot is replaced with the selection color across
// the selected range.
//
// Why bg-only:
//
//   - Lipgloss's Render(text) emits a leading SGR open and a TRAILING
//     full reset (`\x1b[0m`). Wrapping the slice that way kills any
//     foreground color that the parent line had set before the slice,
//     so the text AFTER the selection rendered in the wrong color.
//     That was the user-visible "selecting colored text looks weird"
//     bug.
//
//   - SGR-7 inverse looks even worse: each different fg color in the
//     selection becomes a different bg stripe, so a syntax-highlighted
//     code block under the selection turns into a barcode. Claude Code's
//     screen layer documents the same finding — they avoid SGR-7 for
//     selection for the same reason.
//
//   - The fix is to emit ONLY a bg-set sequence at the start and a
//     bg-default sequence at the end, and to re-emit the bg-set
//     sequence after any reset that appears INSIDE the selected slice
//     (otherwise a mid-slice `\x1b[0m` resets bg and the highlight
//     vanishes for the rest of the slice).
func highlightLineRange(line string, colStart, colEnd int) string {
	if colStart >= colEnd {
		return line
	}
	lineWidth := lipgloss.Width(ansi.Strip(line))
	if colStart < 0 {
		colStart = 0
	}
	if colStart >= lineWidth {
		return line
	}
	if colEnd > lineWidth {
		colEnd = lineWidth
	}

	// Slicing primitives from the upstream charm/x/ansi package handle
	// ANSI state preservation correctly: leading SGR codes are kept
	// when slicing from the right, trailing SGR codes are kept when
	// truncating from the left, and wide-character / grapheme
	// boundaries are respected. wuu used to roll its own slicer here
	// (sliceStyledTextFromVisualCol) which dropped leading SGR state
	// — that was the second source of the colored-selection bug.
	before := ansi.Truncate(line, colStart, "")
	selected := ansi.Cut(line, colStart, colEnd)
	after := ansi.TruncateLeft(line, colEnd, "")
	if selected == "" {
		return line
	}

	bgOpen := selectionBgSGROpen()
	bgClose := selectionBgSGRClose()

	selected = stripBackgroundSGR(selected)

	// Re-establish the selection bg after any in-slice SGR reset.
	// Markdown rendering and syntax highlighting commonly emit
	// `\x1b[0m` mid-line; without this re-emit the bg overlay would
	// vanish from the reset point onward and the highlight would
	// look "broken" mid-selection.
	selected = strings.ReplaceAll(selected, "\x1b[0m", "\x1b[0m"+bgOpen)
	selected = strings.ReplaceAll(selected, "\x1b[m", "\x1b[m"+bgOpen)

	return before + bgOpen + selected + bgClose + after
}

// selectionBgSGROpen returns the raw SGR sequence that sets the
// selection background color WITHOUT touching any other attribute.
// Built directly from the theme color via the ansi package's
// background-color helper so we never depend on lipgloss's full-style
// renderer (which would also emit a trailing reset that breaks the
// surrounding ANSI state).
func selectionBgSGROpen() string {
	hex := string(currentTheme.SelectionBg)
	r, g, b, ok := parseHexRGB(hex)
	if !ok {
		// Theme has no valid hex; fall back to the default background
		// inverse (`\x1b[7m`) which at least makes selection visible.
		return "\x1b[7m"
	}
	style := ansi.Style{}.BackgroundColor(ansi.TrueColor(packRGB(r, g, b)))
	return style.String()
}

// selectionBgSGRClose returns the SGR sequence that resets the
// background to the terminal default WITHOUT touching foreground or
// any other attribute. Critically NOT a full reset (`\x1b[0m`) —
// that would also wipe whatever foreground/bold/italic the original
// line had active when the selection ended.
func selectionBgSGRClose() string {
	return "\x1b[49m"
}

// stripBackgroundSGR removes background-related SGR params from a styled
// string while preserving all non-background styling (foreground, bold, etc).
// This prevents pre-existing message bubble backgrounds from competing with
// the selection overlay color.
func stripBackgroundSGR(s string) string {
	if s == "" {
		return s
	}

	var b strings.Builder
	for i := 0; i < len(s); {
		if i+1 >= len(s) || s[i] != '\x1b' || s[i+1] != '[' {
			b.WriteByte(s[i])
			i++
			continue
		}
		j := i + 2
		for j < len(s) && s[j] != 'm' {
			j++
		}
		if j >= len(s) {
			b.WriteString(s[i:])
			break
		}

		params := strings.Split(s[i+2:j], ";")
		kept := make([]string, 0, len(params))
		for p := 0; p < len(params); p++ {
			param := params[p]
			if param == "" {
				kept = append(kept, param)
				continue
			}

			// SGR also allows colon-form params (e.g. 48:2::r:g:b).
			// Strip background prefixes while preserving non-background styles.
			if strings.HasPrefix(param, "48:") || strings.HasPrefix(param, "49:") {
				continue
			}

			n, err := strconv.Atoi(param)
			if err != nil {
				kept = append(kept, param)
				continue
			}

			// 40-47 and 100-107 are standard/indexed background colors.
			if (n >= 40 && n <= 47) || (n >= 100 && n <= 107) || n == 49 {
				continue
			}
			// 48;5;<idx> and 48;2;<r>;<g>;<b> are extended background colors.
			if n == 48 {
				if p+1 < len(params) {
					mode, modeErr := strconv.Atoi(params[p+1])
					if modeErr == nil {
						skip := 1
						switch mode {
						case 5:
							skip = 2
						case 2:
							skip = 4
						}
						p += skip
						continue
					}
				}
				continue
			}
			kept = append(kept, param)
		}

		if len(kept) > 0 {
			b.WriteString("\x1b[")
			b.WriteString(strings.Join(kept, ";"))
			b.WriteByte('m')
		}
		i = j + 1
	}
	return b.String()
}

// parseHexRGB decodes a "#rrggbb" hex color string into 8-bit RGB
// components. Returns ok=false on any malformed input.
func parseHexRGB(hex string) (r, g, b uint8, ok bool) {
	if len(hex) != 7 || hex[0] != '#' {
		return 0, 0, 0, false
	}
	parse := func(s string) (uint8, bool) {
		var v uint8
		for i := 0; i < len(s); i++ {
			c := s[i]
			var nibble uint8
			switch {
			case c >= '0' && c <= '9':
				nibble = c - '0'
			case c >= 'a' && c <= 'f':
				nibble = c - 'a' + 10
			case c >= 'A' && c <= 'F':
				nibble = c - 'A' + 10
			default:
				return 0, false
			}
			v = v<<4 | nibble
		}
		return v, true
	}
	rr, ok := parse(hex[1:3])
	if !ok {
		return 0, 0, 0, false
	}
	gg, ok := parse(hex[3:5])
	if !ok {
		return 0, 0, 0, false
	}
	bb, ok := parse(hex[5:7])
	if !ok {
		return 0, 0, 0, false
	}
	return rr, gg, bb, true
}

// packRGB packs 8-bit RGB into the 24-bit integer ansi.TrueColor wants.
func packRGB(r, g, b uint8) uint32 {
	return uint32(r)<<16 | uint32(g)<<8 | uint32(b)
}

// slicePlainTextByVisualCols extracts a substring from `s` (which must
// be ANSI-stripped already) by visual column range. Used by
// selection.selectedText for clipboard copy. Wide characters and
// zero-width combining marks are handled correctly.
func slicePlainTextByVisualCols(s string, start, end int) string {
	if start < 0 {
		start = 0
	}
	if end < start {
		end = start
	}
	if start == end || s == "" {
		return ""
	}

	var b strings.Builder
	col := 0
	for _, r := range s {
		w := runewidth.RuneWidth(r)
		if w == 0 {
			if col > start && col <= end {
				b.WriteRune(r)
			}
			continue
		}
		nextCol := col + w
		if nextCol <= start {
			col = nextCol
			continue
		}
		if col >= end {
			break
		}
		if col >= start && nextCol <= end {
			b.WriteRune(r)
		}
		col = nextCol
	}
	return b.String()
}

// --- Input textarea selection helpers ---

// screenToInputCoords converts screen (x, y) to input-local
// coordinates where row is the visual line within the textarea
// and col is the visual column within the text (after the prompt).
func (m *Model) screenToInputCoords(x, y int) (row, col int) {
	const promptW = 2 // "> "
	row = y - m.layout.Input.Y
	if row < 0 {
		row = 0
	}
	if row >= m.layout.Input.Height {
		row = m.layout.Input.Height - 1
	}
	col = x - m.layout.Input.X - promptW
	if col < 0 {
		col = 0
	}
	return row, col
}

// inputSelectedText extracts the highlighted substring from the
// textarea's plain text Value(). Row/Col in inputSelection are
// visual lines in the textarea, mapped to logical line breaks.
func (m *Model) inputSelectedText() string {
	if !m.inputSelection.hasSelection() {
		return ""
	}
	start, end := m.inputSelection.bounds()
	if start == nil {
		return ""
	}
	lines := strings.Split(m.input.Value(), "\n")
	var sb strings.Builder
	for row := start.Row; row <= end.Row && row < len(lines); row++ {
		if row < 0 {
			continue
		}
		line := lines[row]
		lineWidth := runewidth.StringWidth(line)
		colStart := 0
		if row == start.Row {
			colStart = start.Col
		}
		colEnd := lineWidth
		if row == end.Row {
			colEnd = end.Col + 1
		}
		if colStart < 0 {
			colStart = 0
		}
		if colEnd > lineWidth {
			colEnd = lineWidth
		}
		if row > start.Row {
			sb.WriteByte('\n')
		}
		if colStart < colEnd {
			sb.WriteString(slicePlainTextByVisualCols(line, colStart, colEnd))
		}
	}
	return sb.String()
}

// copyInputSelectionToClipboard copies the current input selection
// to the system clipboard.
func (m *Model) copyInputSelectionToClipboard() {
	text := m.inputSelectedText()
	if text == "" {
		return
	}
	method, err := writeClipboard(text)
	if err != nil {
		m.setCopyStatusLine("copy failed: install pbcopy / xclip / wl-copy")
		return
	}
	if method == "osc52" {
		m.setCopyStatusLine("copied via OSC 52 (terminal-dependent)")
		return
	}
	m.setCopyStatusLine("copied")
}

// overlayInputSelection paints the selection highlight onto the
// input textarea's View() output. The inputSelection coordinates
// are input-local (row = visual line, col = visual column after
// prompt). Reuses highlightLineRange for ANSI-aware bg overlay.
func overlayInputSelection(inputView string, sel *selectionState) string {
	if sel == nil || !sel.hasSelection() {
		return inputView
	}
	start, end := sel.bounds()
	if start == nil {
		return inputView
	}
	const promptW = 2
	lines := strings.Split(inputView, "\n")
	for row := start.Row; row <= end.Row; row++ {
		if row < 0 || row >= len(lines) {
			continue
		}
		stripped := ansi.Strip(lines[row])
		lineWidth := lipgloss.Width(stripped)
		colStart := promptW
		if row == start.Row {
			colStart = promptW + start.Col
		}
		colEnd := lineWidth
		if row == end.Row {
			colEnd = promptW + end.Col + 1
		}
		lines[row] = highlightLineRange(lines[row], colStart, colEnd)
	}
	return strings.Join(lines, "\n")
}
