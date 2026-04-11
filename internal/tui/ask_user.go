package tui

import (
	"context"
	"encoding/json"
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

// -------------------------------------------------------------------
// Chat-viewport card rendering
// -------------------------------------------------------------------
//
// renderToolCard dispatches ask_user calls to renderAskUserCard
// instead of the generic JSON-dump formatter, so the chat history
// shows a clean "User answered:" card with question→answer pairs.
// This mirrors Claude Code's AskUserQuestionResultMessage layout.

// renderAskUserCard formats one ask_user tool call entry for the
// chat viewport. Handles both the success path (answers map) and
// the error path (cancelled / validation failure / bridge missing).
func renderAskUserCard(tc ToolCallEntry, width int) string {
	t := currentTheme
	iconStyle := lipgloss.NewStyle().Foreground(t.ToolBorder)
	nameStyle := lipgloss.NewStyle().Bold(true).Foreground(t.ToolBorder)
	statusDone := lipgloss.NewStyle().Foreground(t.Success)
	statusRunning := lipgloss.NewStyle().Foreground(t.Warning)
	statusError := lipgloss.NewStyle().Foreground(t.Error)
	summaryStyle := lipgloss.NewStyle().Foreground(t.Inactive)

	questionTexts := parseAskUserQuestionList(tc.Args)
	answers, errMsg := parseAskUserResult(tc.Result)

	var b strings.Builder

	// --- Header line: icon + name + status ---
	b.WriteString(" ")
	b.WriteString(iconStyle.Render("⚡"))
	b.WriteString(" ")
	b.WriteString(nameStyle.Render("ask_user"))

	switch {
	case tc.Status == ToolCallRunning:
		b.WriteString("  ")
		b.WriteString(statusRunning.Render("⏳ waiting for user"))
	case errMsg != "" || tc.Status == ToolCallError:
		b.WriteString("  ")
		display := errMsg
		if display == "" {
			display = "error"
		}
		b.WriteString(statusError.Render("✗ " + truncateInline(display, 60)))
	default:
		b.WriteString("  ")
		b.WriteString(statusDone.Render("✓ answered"))
	}

	// --- Collapsed: short summary on the header line ---
	if tc.Collapsed {
		if errMsg == "" && len(questionTexts) > 0 {
			n := len(questionTexts)
			label := "question"
			if n != 1 {
				label = "questions"
			}
			sample := questionTexts[0]
			maxSample := width - 40
			if maxSample < 20 {
				maxSample = 20
			}
			sample = truncateInline(sample, maxSample)
			b.WriteString(" ── ")
			b.WriteString(summaryStyle.Render(fmt.Sprintf("%d %s: %q", n, label, sample)))
		}
		return b.String()
	}

	// --- Expanded: render each question→answer line ---
	if errMsg != "" || len(answers) == 0 {
		// Header alone is enough — no body needed for errors or
		// empty responses.
		return b.String()
	}

	headerLineStyle := lipgloss.NewStyle().Foreground(t.Text)
	qaQuestionStyle := lipgloss.NewStyle().Foreground(t.Subtle)
	qaAnswerStyle := lipgloss.NewStyle().Bold(true).Foreground(t.Text)
	bullet := lipgloss.NewStyle().Foreground(t.Brand).Bold(true).Render("●")
	dot := lipgloss.NewStyle().Foreground(t.Inactive).Render("·")
	arrow := lipgloss.NewStyle().Foreground(t.Subtle).Render("→")

	innerW := width - 6
	if innerW < 30 {
		innerW = 30
	}
	// Reserve room for the dot, two spaces, the arrow, and one
	// space on each side of it. We split the remainder evenly
	// between question and answer truncation, but only when one
	// of them actually overflows.
	qBudget := innerW - 6 // " · " + " → " padding
	if qBudget < 20 {
		qBudget = 20
	}

	var body strings.Builder
	body.WriteString("  " + bullet + " " + headerLineStyle.Render("User answered:"))

	// Walk the questions in the order they were asked so the
	// rendered list matches the dialog flow.
	seen := make(map[string]bool, len(answers))
	for _, q := range questionTexts {
		ans, ok := answers[q]
		if !ok {
			continue
		}
		seen[q] = true
		body.WriteString("\n")
		body.WriteString(renderAskQALine(q, ans, qBudget, dot, arrow, qaQuestionStyle, qaAnswerStyle))
	}
	// Defensive: any answer not matched to a question (shouldn't
	// happen, but be safe — bridge always keys by question text).
	for q, ans := range answers {
		if seen[q] {
			continue
		}
		body.WriteString("\n")
		body.WriteString(renderAskQALine(q, ans, qBudget, dot, arrow, qaQuestionStyle, qaAnswerStyle))
	}

	return b.String() + "\n" + body.String()
}

// renderAskQALine renders one "  · question → answer" row, splitting
// the budget between question and answer when either is too long.
func renderAskQALine(question, answer string, budget int, dot, arrow string, qStyle, aStyle lipgloss.Style) string {
	q := strings.TrimSpace(question)
	a := strings.TrimSpace(answer)

	// Split the line budget between question and answer. Prefer
	// answer-readability: give the answer at least 20 chars, the
	// rest goes to the question.
	aBudget := budget - len([]rune(q)) - 3 // " → "
	if aBudget < 20 {
		aBudget = 20
		qBudget := budget - aBudget - 3
		if qBudget < 20 {
			qBudget = 20
		}
		q = truncateInline(q, qBudget)
	}
	a = truncateInline(a, aBudget)

	return "    " + dot + " " + qStyle.Render(q) + " " + arrow + " " + aStyle.Render(a)
}

// truncateInline shortens a string to maxLen runes, appending an
// ellipsis when truncated. Operates on runes so multi-byte chars
// (CJK, emoji) don't get sliced mid-character.
func truncateInline(s string, maxLen int) string {
	if maxLen <= 1 {
		return s
	}
	r := []rune(s)
	if len(r) <= maxLen {
		return s
	}
	if maxLen <= 1 {
		return "…"
	}
	return string(r[:maxLen-1]) + "…"
}

// parseAskUserQuestionList extracts the question texts from an
// ask_user tool call args JSON, in the order they were asked.
// Returns nil on parse failure (the caller renders without a
// summary in that case).
func parseAskUserQuestionList(argsJSON string) []string {
	if strings.TrimSpace(argsJSON) == "" {
		return nil
	}
	var parsed struct {
		Questions []struct {
			Question string `json:"question"`
		} `json:"questions"`
	}
	if err := json.Unmarshal([]byte(argsJSON), &parsed); err != nil {
		return nil
	}
	out := make([]string, 0, len(parsed.Questions))
	for _, q := range parsed.Questions {
		if strings.TrimSpace(q.Question) != "" {
			out = append(out, q.Question)
		}
	}
	return out
}

// parseAskUserResult parses an ask_user tool result. Returns either
// the answers map (success) or a non-empty error message (failure
// or cancellation). The two return values are mutually exclusive.
func parseAskUserResult(resultJSON string) (map[string]string, string) {
	if strings.TrimSpace(resultJSON) == "" {
		return nil, ""
	}
	// Tool errors are surfaced as {"error":"..."} JSON by the
	// loop's errorJSON helper. Probe for that shape first.
	var errProbe struct {
		Error string `json:"error"`
	}
	if json.Unmarshal([]byte(resultJSON), &errProbe) == nil && errProbe.Error != "" {
		// Strip the "ask_user: " prefix for cleaner display
		// since the card already labels itself as ask_user.
		msg := strings.TrimPrefix(errProbe.Error, "ask_user: ")
		return nil, msg
	}
	var resp tools.AskUserResponse
	if err := json.Unmarshal([]byte(resultJSON), &resp); err != nil {
		return nil, ""
	}
	if resp.Cancelled {
		return nil, "user cancelled"
	}
	return resp.Answers, ""
}


