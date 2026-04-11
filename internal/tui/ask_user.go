package tui

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/charmbracelet/bubbles/textinput"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"

	"github.com/blueberrycongee/wuu/internal/tools"
)

// errAskUserBusy is delivered to a second concurrent ask_user call
// (which should not normally happen — the tool blocks until the
// previous modal closes) so the second caller fails fast instead of
// queueing or deadlocking.
var errAskUserBusy = errors.New("ask_user: another ask is already in progress")

// This file implements the ask_user tool's TUI side: the bridge that
// the Toolkit calls into, and the modal dialog the bridge delegates
// to. The overall flow is:
//
//   1. Agent loop calls Toolkit.Execute("ask_user", args).
//   2. Toolkit.askUser decodes, validates, and calls
//      AskUserBridge.AskUser(ctx, req).
//   3. AskUser pushes a pending request onto a channel and blocks on
//      its response channel.
//   4. The Bubble Tea event loop drains the channel via waitAskRequest
//      (a tea.Cmd) and delivers askRequestMsg to Model.Update.
//   5. Model.Update creates an askUserModal and stores it in
//      m.activeAsk. While activeAsk != nil, Update routes key/window
//      events to the modal and View() renders the modal full-screen.
//   6. The modal walks the user through the questions, collecting
//      answers. When done (or cancelled), Model sends the response
//      back onto the pending request's response channel.
//   7. AskUser unblocks, the tool call returns a JSON payload with
//      the answers, and the agent loop resumes.
//
// The bridge lives in the TUI package (not the tools package) because
// it needs tea-specific plumbing; the tools package only depends on
// the tools.AskUserBridge interface, which this file implements.

// -------------------------------------------------------------------
// Bridge — tools.AskUserBridge implementation
// -------------------------------------------------------------------

// askPendingRequest ties one ask_user tool call to a response channel.
// The TUI takes ownership of respCh after the bridge sends the
// request onto requests; the bridge's AskUser goroutine blocks on
// respCh until the TUI delivers a response (or the context is
// cancelled).
type askPendingRequest struct {
	req    tools.AskUserRequest
	ctx    context.Context
	respCh chan askBridgeResponse
}

type askBridgeResponse struct {
	resp tools.AskUserResponse
	err  error
}

// askRequestMsg is the Bubble Tea message delivered to Model.Update
// when the agent has called ask_user and is waiting for the user.
type askRequestMsg struct {
	pending *askPendingRequest
}

// AskUserBridge is the TUI-side implementation of tools.AskUserBridge.
// It exposes a Requests() channel the TUI model reads from to spin up
// the modal, and a blocking AskUser method the tools package calls.
type AskUserBridge struct {
	requests chan *askPendingRequest
}

// NewAskUserBridge constructs a bridge. The buffered channel size is
// 1 because only one ask_user dialog can be open at a time anyway —
// the tool blocks the agent turn, so a second call cannot start until
// the first returns.
func NewAskUserBridge() *AskUserBridge {
	return &AskUserBridge{
		requests: make(chan *askPendingRequest, 1),
	}
}

// Requests returns the channel the TUI drains via waitAskRequest.
// Kept as a method (not a public field) so the TUI is the only
// consumer and the bridge owns the channel lifecycle.
func (b *AskUserBridge) Requests() <-chan *askPendingRequest {
	return b.requests
}

// AskUser implements tools.AskUserBridge. It publishes the request
// onto the queue, then blocks until the TUI writes a response or
// the caller's context is cancelled (typically via Ctrl+C on the
// main agent).
func (b *AskUserBridge) AskUser(ctx context.Context, req tools.AskUserRequest) (tools.AskUserResponse, error) {
	pending := &askPendingRequest{
		req:    req,
		ctx:    ctx,
		respCh: make(chan askBridgeResponse, 1),
	}
	select {
	case b.requests <- pending:
	case <-ctx.Done():
		return tools.AskUserResponse{}, ctx.Err()
	}
	select {
	case r := <-pending.respCh:
		return r.resp, r.err
	case <-ctx.Done():
		return tools.AskUserResponse{}, ctx.Err()
	}
}

// waitAskRequest returns a tea.Cmd that reads one request from the
// bridge and wraps it as an askRequestMsg. Called from Model.Init
// (to start the first wait) and re-issued inside Update after each
// modal finishes, so the TUI keeps listening for the next call.
func waitAskRequest(ch <-chan *askPendingRequest) tea.Cmd {
	return func() tea.Msg {
		p, ok := <-ch
		if !ok || p == nil {
			return nil
		}
		return askRequestMsg{pending: p}
	}
}

// -------------------------------------------------------------------
// Modal component — askUserModal
// -------------------------------------------------------------------

// askUserModal is the full-screen dialog that walks the user through
// one ask_user call's questions. It follows the same pattern as
// resumePicker: Update handles events, View renders everything, and
// the parent Model checks done / cancelled after each Update to know
// whether to deliver the response back to the bridge.
type askUserModal struct {
	pending *askPendingRequest
	width   int
	height  int

	// Progress through the question list.
	curQ    int
	answers map[string]string

	// State for the current question.
	cursor        int          // 0..cursorLen()-1
	multiSelected map[int]bool // indices, for multi-select

	// "Other" text input mode (single-select only).
	inOtherInput bool
	otherInput   textinput.Model

	// Terminal state — set when the user finishes the last question
	// or dismisses the dialog. The parent reads these to decide
	// what to deliver back on the response channel.
	cancelled bool
	done      bool
}

func newAskUserModal(pending *askPendingRequest, width, height int) *askUserModal {
	ti := textinput.New()
	ti.Placeholder = "type your answer (empty cancels back to options)"
	ti.CharLimit = 2000
	ti.Prompt = "› "
	ti.Width = 60

	m := &askUserModal{
		pending:    pending,
		width:      width,
		height:     height,
		answers:    make(map[string]string, len(pending.req.Questions)),
		otherInput: ti,
	}
	m.resetForQuestion()
	return m
}

func (m *askUserModal) resetForQuestion() {
	m.cursor = 0
	m.multiSelected = make(map[int]bool)
	m.inOtherInput = false
	m.otherInput.SetValue("")
	m.otherInput.Blur()
}

func (m *askUserModal) currentQuestion() *tools.AskUserQuestion {
	if m.curQ < 0 || m.curQ >= len(m.pending.req.Questions) {
		return nil
	}
	return &m.pending.req.Questions[m.curQ]
}

// cursorLen returns the number of cursor positions for the current
// question: real options + 1 ("Other") for single-select, or just
// real options for multi-select (multi-select has no "Other" row —
// free text plus a toggle-set would muddle the semantics).
func (m *askUserModal) cursorLen() int {
	q := m.currentQuestion()
	if q == nil {
		return 0
	}
	if q.MultiSelect {
		return len(q.Options)
	}
	return len(q.Options) + 1
}

// isOtherRow returns true when the cursor is on the synthetic
// "Other" row. Only meaningful for single-select questions.
func (m *askUserModal) isOtherRow() bool {
	q := m.currentQuestion()
	if q == nil || q.MultiSelect {
		return false
	}
	return m.cursor == len(q.Options)
}

// Update handles one event. Returns the (possibly-mutated) modal
// and an optional tea.Cmd. Caller should check m.done / m.cancelled
// after each call to know whether to finish.
func (m *askUserModal) Update(msg tea.Msg) (*askUserModal, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		m.width = msg.Width
		m.height = msg.Height
		return m, nil

	case tea.KeyMsg:
		if m.inOtherInput {
			return m.updateOtherInput(msg)
		}
		return m.updateOptions(msg)
	}
	return m, nil
}

func (m *askUserModal) updateOptions(msg tea.KeyMsg) (*askUserModal, tea.Cmd) {
	q := m.currentQuestion()
	if q == nil {
		return m, nil
	}
	switch msg.String() {
	case "esc", "ctrl+c":
		m.cancelled = true
		return m, nil

	case "up", "k":
		if m.cursor > 0 {
			m.cursor--
		}
		return m, nil

	case "down", "j":
		if m.cursor < m.cursorLen()-1 {
			m.cursor++
		}
		return m, nil

	case " ", "space":
		if q.MultiSelect {
			if m.multiSelected[m.cursor] {
				delete(m.multiSelected, m.cursor)
			} else {
				m.multiSelected[m.cursor] = true
			}
		}
		return m, nil

	case "enter":
		if m.isOtherRow() {
			// Switch to free-text input. The text input captures
			// subsequent keystrokes until Enter (submit) or Esc
			// (back to options).
			m.inOtherInput = true
			m.otherInput.Focus()
			return m, textinput.Blink
		}
		return m.commitCurrentAnswer()
	}
	return m, nil
}

func (m *askUserModal) updateOtherInput(msg tea.KeyMsg) (*askUserModal, tea.Cmd) {
	switch msg.String() {
	case "esc":
		m.inOtherInput = false
		m.otherInput.Blur()
		m.otherInput.SetValue("")
		return m, nil

	case "enter":
		text := strings.TrimSpace(m.otherInput.Value())
		if text == "" {
			// Empty input — stay in text mode so the user can try
			// again or press Esc to go back.
			return m, nil
		}
		q := m.currentQuestion()
		if q == nil {
			return m, nil
		}
		m.answers[q.Question] = text
		return m.advanceQuestion()

	case "ctrl+c":
		m.cancelled = true
		return m, nil
	}
	// Everything else: forward to the textinput for character editing.
	var cmd tea.Cmd
	m.otherInput, cmd = m.otherInput.Update(msg)
	return m, cmd
}

// commitCurrentAnswer records the selection for the current question
// and advances to the next one (or finishes if it was the last).
func (m *askUserModal) commitCurrentAnswer() (*askUserModal, tea.Cmd) {
	q := m.currentQuestion()
	if q == nil {
		return m, nil
	}
	if q.MultiSelect {
		if len(m.multiSelected) == 0 {
			// No options chosen — block the advance until the user
			// picks at least one.
			return m, nil
		}
		labels := make([]string, 0, len(m.multiSelected))
		for i, opt := range q.Options {
			if m.multiSelected[i] {
				labels = append(labels, opt.Label)
			}
		}
		m.answers[q.Question] = strings.Join(labels, ", ")
	} else {
		if m.cursor < 0 || m.cursor >= len(q.Options) {
			return m, nil
		}
		m.answers[q.Question] = q.Options[m.cursor].Label
	}
	return m.advanceQuestion()
}

func (m *askUserModal) advanceQuestion() (*askUserModal, tea.Cmd) {
	m.curQ++
	if m.curQ >= len(m.pending.req.Questions) {
		m.done = true
		return m, nil
	}
	m.resetForQuestion()
	return m, nil
}

// Response builds the final payload. Callers read this after the
// modal's Update sets done or cancelled, then deliver it onto the
// pending request's response channel.
func (m *askUserModal) Response() tools.AskUserResponse {
	return tools.AskUserResponse{
		Answers:   m.answers,
		Cancelled: m.cancelled,
	}
}

// -------------------------------------------------------------------
// Rendering
// -------------------------------------------------------------------

func (m *askUserModal) View() string {
	if m.width == 0 || m.height == 0 {
		return "loading..."
	}
	q := m.currentQuestion()
	if q == nil {
		return ""
	}

	t := currentTheme
	askHeaderStyle := lipgloss.NewStyle().Bold(true).Foreground(t.Brand)
	askChipStyle := lipgloss.NewStyle().
		Bold(true).
		Foreground(t.HeaderBg).
		Background(t.Brand).
		Padding(0, 1)
	askQuestionStyle := lipgloss.NewStyle().Bold(true).Foreground(t.Text)
	askFooterStyle := lipgloss.NewStyle().Foreground(t.Subtle)

	// --- Header bar (top line) ---
	var mode string
	if m.inOtherInput {
		mode = " · custom answer"
	} else if q.MultiSelect {
		mode = " · multi-select"
	}
	headerText := fmt.Sprintf(" wuu · question %d of %d%s",
		m.curQ+1, len(m.pending.req.Questions), mode)
	header := askHeaderStyle.Render(trimToWidth(headerText, m.width))

	// --- Content area ---
	chip := askChipStyle.Render(q.Header)
	questionLine := askQuestionStyle.Render(q.Question)
	qHeading := lipgloss.JoinHorizontal(lipgloss.Top, chip, "  ", questionLine)

	var body string
	if m.inOtherInput {
		body = m.renderOtherInputBody(q)
	} else {
		hasPreview := false
		for _, o := range q.Options {
			if strings.TrimSpace(o.Preview) != "" {
				hasPreview = true
				break
			}
		}
		if hasPreview {
			body = m.renderOptionsWithPreview(q)
		} else {
			body = m.renderOptionsPlain(q)
		}
	}

	// --- Footer (hints) ---
	footer := askFooterStyle.Render(m.footerHints(q))

	return lipgloss.JoinVertical(
		lipgloss.Left,
		"",
		header,
		"",
		qHeading,
		"",
		body,
		"",
		footer,
		"",
	)
}

func (m *askUserModal) footerHints(q *tools.AskUserQuestion) string {
	if m.inOtherInput {
		return "  Enter submit · Esc back to options · Ctrl+C cancel"
	}
	if q.MultiSelect {
		return "  ↑/↓ navigate · Space toggle · Enter submit · Esc cancel"
	}
	return "  ↑/↓ navigate · Enter select · Esc cancel"
}

// renderOptionsPlain is the simple vertical list used when no option
// in the current question has a preview.
func (m *askUserModal) renderOptionsPlain(q *tools.AskUserQuestion) string {
	t := currentTheme
	cursorStyle := lipgloss.NewStyle().Bold(true).Foreground(t.Brand)
	labelStyle := lipgloss.NewStyle().Foreground(t.Text)
	focusedLabelStyle := lipgloss.NewStyle().Bold(true).Foreground(t.BrandLight)
	descStyle := lipgloss.NewStyle().Foreground(t.Subtle).Italic(true)
	otherStyle := lipgloss.NewStyle().Foreground(t.Inactive).Italic(true)
	focusedOtherStyle := lipgloss.NewStyle().Bold(true).Foreground(t.BrandLight).Italic(true)
	checkBoxOn := lipgloss.NewStyle().Foreground(t.Success).Bold(true)
	checkBoxOff := lipgloss.NewStyle().Foreground(t.Inactive)

	var lines []string
	for i, opt := range q.Options {
		cursorMark := "  "
		labelRendered := labelStyle.Render(opt.Label)
		if i == m.cursor {
			cursorMark = cursorStyle.Render("❯ ")
			labelRendered = focusedLabelStyle.Render(opt.Label)
		}
		var checkbox string
		if q.MultiSelect {
			if m.multiSelected[i] {
				checkbox = checkBoxOn.Render("[x] ")
			} else {
				checkbox = checkBoxOff.Render("[ ] ")
			}
		}
		lines = append(lines, cursorMark+checkbox+labelRendered)
		if strings.TrimSpace(opt.Description) != "" {
			lines = append(lines, "    "+descStyle.Render(opt.Description))
		}
	}
	// "Other" row (single-select only).
	if !q.MultiSelect {
		otherIdx := len(q.Options)
		cursorMark := "  "
		rendered := otherStyle.Render("Other (type your own answer)")
		if m.cursor == otherIdx {
			cursorMark = cursorStyle.Render("❯ ")
			rendered = focusedOtherStyle.Render("Other (type your own answer)")
		}
		lines = append(lines, cursorMark+rendered)
	}
	return "  " + strings.Join(lines, "\n  ")
}

// renderOptionsWithPreview renders the side-by-side layout: option
// list on the left, the focused option's preview (markdown rendered
// in a monospace box) on the right.
func (m *askUserModal) renderOptionsWithPreview(q *tools.AskUserQuestion) string {
	t := currentTheme
	cursorStyle := lipgloss.NewStyle().Bold(true).Foreground(t.Brand)
	labelStyle := lipgloss.NewStyle().Foreground(t.Text)
	focusedLabelStyle := lipgloss.NewStyle().Bold(true).Foreground(t.BrandLight)
	descStyle := lipgloss.NewStyle().Foreground(t.Subtle).Italic(true)
	otherStyle := lipgloss.NewStyle().Foreground(t.Inactive).Italic(true)
	focusedOtherStyle := lipgloss.NewStyle().Bold(true).Foreground(t.BrandLight).Italic(true)
	previewBorder := lipgloss.NewStyle().
		Border(lipgloss.RoundedBorder()).
		BorderForeground(t.Border).
		Padding(0, 1)
	panelTitle := lipgloss.NewStyle().Foreground(t.Subtle).Italic(true)
	checkBoxOn := lipgloss.NewStyle().Foreground(t.Success).Bold(true)
	checkBoxOff := lipgloss.NewStyle().Foreground(t.Inactive)

	// Left panel: the option list.
	leftW := m.width / 2
	if leftW > 40 {
		leftW = 40
	}
	if leftW < 24 {
		leftW = 24
	}
	rightW := m.width - leftW - 4 // 2 padding + 2 for borders
	if rightW < 30 {
		rightW = 30
	}

	var leftLines []string
	for i, opt := range q.Options {
		cursorMark := "  "
		labelRendered := labelStyle.Render(opt.Label)
		if i == m.cursor {
			cursorMark = cursorStyle.Render("❯ ")
			labelRendered = focusedLabelStyle.Render(opt.Label)
		}
		var checkbox string
		if q.MultiSelect {
			if m.multiSelected[i] {
				checkbox = checkBoxOn.Render("[x] ")
			} else {
				checkbox = checkBoxOff.Render("[ ] ")
			}
		}
		leftLines = append(leftLines, cursorMark+checkbox+labelRendered)
	}
	if !q.MultiSelect {
		otherIdx := len(q.Options)
		cursorMark := "  "
		rendered := otherStyle.Render("Other")
		if m.cursor == otherIdx {
			cursorMark = cursorStyle.Render("❯ ")
			rendered = focusedOtherStyle.Render("Other")
		}
		leftLines = append(leftLines, cursorMark+rendered)
	}
	leftPanel := lipgloss.JoinVertical(
		lipgloss.Left,
		panelTitle.Render("  Options"),
		"",
		strings.Join(leftLines, "\n"),
	)

	// Focused option description + preview content.
	var focusedDesc, focusedPreview string
	switch {
	case m.cursor >= 0 && m.cursor < len(q.Options):
		focusedDesc = q.Options[m.cursor].Description
		focusedPreview = q.Options[m.cursor].Preview
	case !q.MultiSelect && m.cursor == len(q.Options):
		focusedDesc = "Select this to type a free-text answer."
	}

	// Right panel: preview box + description below.
	previewBody := strings.TrimSpace(focusedPreview)
	if previewBody == "" {
		previewBody = panelTitle.Render("(no preview for this option)")
	}
	previewBox := previewBorder.Width(rightW).Render(previewBody)

	var rightLines []string
	rightLines = append(rightLines, panelTitle.Render("  Preview"))
	rightLines = append(rightLines, "")
	rightLines = append(rightLines, previewBox)
	if strings.TrimSpace(focusedDesc) != "" {
		rightLines = append(rightLines, "")
		rightLines = append(rightLines, descStyle.Render(wrapText(focusedDesc, rightW)))
	}
	rightPanel := lipgloss.JoinVertical(lipgloss.Left, rightLines...)

	// Left and right combined side-by-side.
	leftBlock := lipgloss.NewStyle().Width(leftW).Render(leftPanel)
	rightBlock := lipgloss.NewStyle().Width(rightW + 4).Render(rightPanel)

	return lipgloss.JoinHorizontal(lipgloss.Top, leftBlock, rightBlock)
}

// renderOtherInputBody renders the free-text input screen after the
// user selects "Other".
func (m *askUserModal) renderOtherInputBody(q *tools.AskUserQuestion) string {
	t := currentTheme
	hintStyle := lipgloss.NewStyle().Foreground(t.Subtle).Italic(true)
	inputBoxStyle := lipgloss.NewStyle().
		Border(lipgloss.RoundedBorder()).
		BorderForeground(t.Brand).
		Padding(0, 1).
		Width(max(40, m.width-6))

	hint := hintStyle.Render("  Type your answer. Enter to submit, Esc to go back to the options.")
	input := inputBoxStyle.Render(m.otherInput.View())

	return lipgloss.JoinVertical(lipgloss.Left, hint, "", "  "+input)
}

