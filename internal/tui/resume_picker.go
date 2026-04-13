package tui

import (
	"bytes"
	"encoding/json"
	"fmt"
	"os"
	"strings"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"

	"github.com/blueberrycongee/wuu/internal/jsonl"
	"github.com/blueberrycongee/wuu/internal/session"
)

// resumePickerEntry holds one session and a lazily-loaded preview.
type resumePickerEntry struct {
	Session       session.Session
	title         string // first user message, populated eagerly at init
	loaded        bool   // full preview loaded?
	preview       []transcriptEntry
	previewScroll int // top line of preview pane (scrollable)
}

// resumePicker is a self-contained list+preview screen for /resume.
type resumePicker struct {
	sessDir   string
	entries   []*resumePickerEntry
	cursor    int
	scrollTop int // first visible row in the list
	width     int
	height    int

	// Set after Update returns: signals the parent model to take action.
	chosenID string // non-empty when user pressed Enter
	cancel   bool   // true when user pressed Esc / Ctrl+C
}

// newResumePicker builds a picker over the most recent maxItems sessions.
func newResumePicker(sessDir string, maxItems int, width, height int) (*resumePicker, error) {
	sessions, err := session.List(sessDir, maxItems)
	if err != nil {
		return nil, err
	}
	entries := make([]*resumePickerEntry, len(sessions))
	for i, s := range sessions {
		e := &resumePickerEntry{Session: s}
		// Eagerly extract a one-line title (first user message) so the
		// list shows real content immediately, not "(empty session)".
		e.title = peekFirstUserMessage(session.FilePath(sessDir, s.ID))
		entries[i] = e
	}
	p := &resumePicker{
		sessDir: sessDir,
		entries: entries,
		width:   width,
		height:  height,
	}
	p.loadPreview(0)
	return p, nil
}

// peekFirstUserMessage scans a JSONL session file and returns the content
// of the first user message it finds. Reads only what's needed (early exit)
// so it stays cheap when called for every session at picker init.
func peekFirstUserMessage(path string) string {
	f, err := os.Open(path)
	if err != nil {
		return ""
	}
	defer f.Close()

	var title string
	_ = jsonl.ForEachLine(f, func(raw []byte) error {
		line := bytes.TrimSpace(raw)
		if len(line) == 0 {
			return nil
		}
		var rec struct {
			Role    string `json:"role"`
			Content string `json:"content"`
		}
		if json.Unmarshal(line, &rec) != nil {
			return nil
		}
		if strings.EqualFold(rec.Role, "user") {
			content := strings.TrimSpace(rec.Content)
			if content != "" {
				title = content
				return jsonl.ErrStop
			}
		}
		return nil
	})
	return title
}

// loadPreview lazily reads the messages of the entry at idx.
func (p *resumePicker) loadPreview(idx int) {
	if idx < 0 || idx >= len(p.entries) {
		return
	}
	e := p.entries[idx]
	if e.loaded {
		return
	}
	path := session.FilePath(p.sessDir, e.Session.ID)
	loaded, err := loadMemoryEntries(path)
	if err == nil {
		e.preview = loaded
	}
	e.loaded = true
}

// Update handles keys for the picker. Returns the picker (possibly with
// chosenID or cancel set) and an optional command (always nil for now).
func (p *resumePicker) Update(msg tea.Msg) (*resumePicker, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		p.width = msg.Width
		p.height = msg.Height
		return p, nil

	case tea.KeyMsg:
		switch msg.String() {
		case "esc", "ctrl+c", "q":
			p.cancel = true
			return p, nil
		case "enter":
			if p.cursor >= 0 && p.cursor < len(p.entries) {
				p.chosenID = p.entries[p.cursor].Session.ID
			}
			return p, nil
		case "up", "k":
			if p.cursor > 0 {
				p.cursor--
				p.loadPreview(p.cursor)
				p.adjustScroll()
			}
			return p, nil
		case "down", "j":
			if p.cursor < len(p.entries)-1 {
				p.cursor++
				p.loadPreview(p.cursor)
				p.adjustScroll()
			}
			return p, nil
		case "home", "g":
			p.cursor = 0
			p.scrollTop = 0
			p.loadPreview(p.cursor)
			return p, nil
		case "end", "G":
			p.cursor = len(p.entries) - 1
			p.loadPreview(p.cursor)
			p.adjustScroll()
			return p, nil
		case "pgup", "ctrl+u":
			p.scrollPreview(-p.previewPageSize())
			return p, nil
		case "pgdown", "ctrl+d", " ":
			p.scrollPreview(p.previewPageSize())
			return p, nil
		case "shift+up":
			p.scrollPreview(-1)
			return p, nil
		case "shift+down":
			p.scrollPreview(1)
			return p, nil
		}

	case tea.MouseMsg:
		// Mouse wheel scrolls the preview pane.
		switch msg.Button {
		case tea.MouseButtonWheelUp:
			p.scrollPreview(-3)
			return p, nil
		case tea.MouseButtonWheelDown:
			p.scrollPreview(3)
			return p, nil
		}
	}
	return p, nil
}

// scrollPreview adjusts the focused entry's preview scroll offset.
// Bounds are enforced lazily during rendering.
func (p *resumePicker) scrollPreview(delta int) {
	if p.cursor < 0 || p.cursor >= len(p.entries) {
		return
	}
	e := p.entries[p.cursor]
	e.previewScroll += delta
	if e.previewScroll < 0 {
		e.previewScroll = 0
	}
}

func (p *resumePicker) previewPageSize() int {
	rows := p.listVisibleRows()
	half := rows / 2
	if half < 1 {
		half = 1
	}
	return half
}

// adjustScroll keeps the cursor visible in the list pane.
func (p *resumePicker) adjustScroll() {
	visibleRows := p.listVisibleRows()
	if p.cursor < p.scrollTop {
		p.scrollTop = p.cursor
	} else if p.cursor >= p.scrollTop+visibleRows {
		p.scrollTop = p.cursor - visibleRows + 1
	}
	if p.scrollTop < 0 {
		p.scrollTop = 0
	}
}

func (p *resumePicker) listVisibleRows() int {
	// Reserve rows for header (1), separator (1), footer (1).
	rows := p.height - 3
	if rows < 4 {
		rows = 4
	}
	return rows
}

// listWidth returns how wide the list pane is. Uses 40% of width, min 28.
func (p *resumePicker) listWidth() int {
	w := p.width * 40 / 100
	if w < 28 {
		w = 28
	}
	if w > p.width-20 {
		w = p.width - 20
	}
	return w
}

// View renders the full picker.
func (p *resumePicker) View() string {
	if p.width == 0 || p.height == 0 {
		return "loading picker..."
	}
	if len(p.entries) == 0 {
		return p.renderEmpty()
	}

	header := pickerHeaderStyle.Render(
		trimToWidth(fmt.Sprintf(" Resume session  ·  %d available", len(p.entries)), p.width),
	)

	listW := p.listWidth()
	previewW := p.width - listW - 1 // 1 col separator
	if previewW < 20 {
		previewW = 20
	}
	visibleRows := p.listVisibleRows()

	listLines := p.renderListLines(listW, visibleRows)
	previewLines := p.renderPreviewLines(previewW, visibleRows)

	// Combine list + separator + preview line by line.
	var bodyLines []string
	for i := 0; i < visibleRows; i++ {
		l := ""
		if i < len(listLines) {
			l = listLines[i]
		}
		l = padRight(l, listW)

		pv := ""
		if i < len(previewLines) {
			pv = previewLines[i]
		}
		pv = padRight(pv, previewW)

		sep := pickerSepStyle.Render("│")
		bodyLines = append(bodyLines, l+sep+pv)
	}
	body := strings.Join(bodyLines, "\n")

	footer := pickerFooterStyle.Render(
		trimToWidth(" ↑↓ navigate  ·  PgUp/PgDn scroll preview  ·  Enter resume  ·  Esc cancel ", p.width),
	)

	hr := lipgloss.NewStyle().
		Foreground(currentTheme.Border).
		Render(strings.Repeat("─", p.width))

	return strings.Join([]string{header, hr, body, hr, footer}, "\n")
}

func (p *resumePicker) renderEmpty() string {
	header := pickerHeaderStyle.Render(" Resume session ")
	body := lipgloss.NewStyle().
		Foreground(currentTheme.Subtle).
		Render("\n  No previous sessions found.\n  Press Esc to return.\n")
	footer := pickerFooterStyle.Render(" Esc cancel ")
	return strings.Join([]string{header, body, footer}, "\n")
}

func (p *resumePicker) renderListLines(width, rows int) []string {
	end := p.scrollTop + rows
	if end > len(p.entries) {
		end = len(p.entries)
	}
	lines := make([]string, 0, end-p.scrollTop)
	for i := p.scrollTop; i < end; i++ {
		e := p.entries[i]
		focused := i == p.cursor
		title := e.Session.Summary
		if title == "" {
			title = e.title // eagerly populated first user message
		}
		if title == "" {
			title = firstUserPreview(e.preview)
		}
		if title == "" {
			title = "(empty session)"
		}
		date := e.Session.CreatedAt.Local().Format("01-02 15:04")
		msgs := fmt.Sprintf("%dm", e.Session.Entries)

		// Layout: marker + title (truncated) + right-aligned meta.
		marker := "  "
		if focused {
			marker = pickerCursorStyle.Render("▸ ")
		}
		meta := fmt.Sprintf(" %s · %s", date, msgs)
		titleW := width - lipgloss.Width(marker) - lipgloss.Width(meta)
		if titleW < 8 {
			titleW = 8
		}
		titleStr := trimToWidth(title, titleW)

		line := marker + titleStr + meta
		if focused {
			line = pickerFocusStyle.Render(padRight(line, width))
		}
		lines = append(lines, line)
	}
	return lines
}

// buildPreviewLines generates the FULL set of preview lines for an entry
// (already wrapped to width). Caller is responsible for slicing to the
// visible window. The result is what the user can scroll through.
func (p *resumePicker) buildPreviewLines(e *resumePickerEntry, width int) []string {
	if !e.loaded {
		return []string{lipgloss.NewStyle().Foreground(currentTheme.Subtle).Render(" loading...")}
	}

	var lines []string
	// Metadata header.
	created := e.Session.CreatedAt.Local().Format("2006-01-02 15:04:05")
	lines = append(lines, pickerMetaStyle.Render(fmt.Sprintf(" Session: %s", e.Session.ID)))
	lines = append(lines, pickerMetaStyle.Render(fmt.Sprintf(" Created: %s · %d entries", created, e.Session.Entries)))
	lines = append(lines, "")

	// Show ALL user/assistant messages (no cap — preview is scrollable).
	any := false
	for _, entry := range e.preview {
		switch entry.Role {
		case "USER":
			text := truncateForPreview(entry.Content, 800)
			lines = append(lines, userPreviewStyle.Render(" › ")+text)
			any = true
		case "ASSISTANT":
			text := truncateForPreview(entry.Content, 1200)
			lines = append(lines, assistantPreviewStyle.Render(" ‹ ")+text)
			any = true
		}
	}
	if !any {
		lines = append(lines, lipgloss.NewStyle().Foreground(currentTheme.Subtle).Render(" (no messages)"))
	}

	// Wrap each line to width.
	wrapped := make([]string, 0, len(lines))
	for _, l := range lines {
		wrapped = append(wrapped, wrapLineForPreview(l, width)...)
	}
	return wrapped
}

// renderPreviewLines slices the full preview to the current scroll window
// of the focused entry. Also applies bounds-checking to previewScroll so
// it never goes off the end.
func (p *resumePicker) renderPreviewLines(width, rows int) []string {
	if p.cursor < 0 || p.cursor >= len(p.entries) {
		return nil
	}
	e := p.entries[p.cursor]
	full := p.buildPreviewLines(e, width)

	maxScroll := len(full) - rows
	if maxScroll < 0 {
		maxScroll = 0
	}
	if e.previewScroll > maxScroll {
		e.previewScroll = maxScroll
	}
	if e.previewScroll < 0 {
		e.previewScroll = 0
	}

	end := e.previewScroll + rows
	if end > len(full) {
		end = len(full)
	}
	visible := full[e.previewScroll:end]

	// Append a scroll indicator on the last line if there's more below.
	if end < len(full) && len(visible) > 0 {
		marker := lipgloss.NewStyle().Foreground(currentTheme.Subtle).Render(" ↓ more ")
		visible = append([]string(nil), visible...)
		visible[len(visible)-1] = padRight(visible[len(visible)-1], width-lipgloss.Width(marker)) + marker
	}
	return visible
}

func firstUserPreview(entries []transcriptEntry) string {
	for _, e := range entries {
		if e.Role == "USER" {
			return strings.TrimSpace(e.Content)
		}
	}
	return ""
}

func truncateForPreview(s string, maxLen int) string {
	s = strings.TrimSpace(s)
	s = strings.ReplaceAll(s, "\n", " ⏎ ")
	if len(s) <= maxLen {
		return s
	}
	return s[:maxLen] + "…"
}

func wrapLineForPreview(line string, width int) []string {
	if width <= 0 {
		return []string{line}
	}
	if lipgloss.Width(line) <= width {
		return []string{line}
	}
	var out []string
	// Naive byte wrap; the existing wrapText util elsewhere is fine but
	// here we keep it minimal since preview text is short.
	for lipgloss.Width(line) > width {
		// Take width-1 cells, then continue.
		out = append(out, ansiTruncatePreview(line, width))
		line = ansiSlicePreview(line, width)
	}
	if line != "" {
		out = append(out, line)
	}
	return out
}

// ansiTruncatePreview/Slice are simple helpers; they don't preserve ANSI
// sequences across cuts but the preview only uses lipgloss spans on whole
// lines so we never split inside one.
func ansiTruncatePreview(s string, w int) string {
	r := []rune(s)
	if len(r) <= w {
		return s
	}
	return string(r[:w])
}

func ansiSlicePreview(s string, w int) string {
	r := []rune(s)
	if len(r) <= w {
		return ""
	}
	return string(r[w:])
}

func padRight(s string, w int) string {
	cur := lipgloss.Width(s)
	if cur >= w {
		return s
	}
	return s + strings.Repeat(" ", w-cur)
}

// Styles for the picker UI.
var (
	pickerHeaderStyle     lipgloss.Style
	pickerFooterStyle     lipgloss.Style
	pickerSepStyle        lipgloss.Style
	pickerCursorStyle     lipgloss.Style
	pickerFocusStyle      lipgloss.Style
	pickerMetaStyle       lipgloss.Style
	userPreviewStyle      lipgloss.Style
	assistantPreviewStyle lipgloss.Style
)

func initPickerStyles() {
	pickerHeaderStyle = lipgloss.NewStyle().Bold(true).Foreground(currentTheme.Brand)
	pickerFooterStyle = lipgloss.NewStyle().Foreground(currentTheme.Subtle)
	pickerSepStyle = lipgloss.NewStyle().Foreground(currentTheme.Border)
	pickerCursorStyle = lipgloss.NewStyle().Foreground(currentTheme.Brand).Bold(true)
	pickerFocusStyle = lipgloss.NewStyle().
		Background(currentTheme.UserBubbleBg).
		Foreground(currentTheme.UserBubbleFg)
	pickerMetaStyle = lipgloss.NewStyle().Foreground(currentTheme.Subtle)
	userPreviewStyle = lipgloss.NewStyle().Bold(true).Foreground(currentTheme.Brand)
	assistantPreviewStyle = lipgloss.NewStyle().Bold(true).Foreground(currentTheme.Success)
}
