package tui

import (
	"context"
	"fmt"
	"path/filepath"
	"strings"
	"time"

	"github.com/charmbracelet/bubbles/textarea"
	"github.com/charmbracelet/bubbles/viewport"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/glamour"
	"github.com/charmbracelet/lipgloss"

	"github.com/blueberrycongee/wuu/internal/agent"
	"github.com/blueberrycongee/wuu/internal/providers"
	"github.com/blueberrycongee/wuu/internal/session"
)

const (
	minOutputHeight = 6
	streamChunkSize = 24
	streamTickDelay = 30 * time.Millisecond
)

type tickMsg struct {
	now time.Time
}

type streamTickMsg struct{}

type responseMsg struct {
	answer  string
	err     error
	elapsed time.Duration
}

type streamEventMsg struct {
	event providers.StreamEvent
}

type transcriptEntry struct {
	Role    string
	Content string
}

// Model implements the terminal UI state machine.
type Model struct {
	provider      string
	modelName     string
	configPath    string
	workspaceRoot string
	memoryPath    string
	sessionID     string
	sessionDir    string
	runPrompt     func(ctx context.Context, prompt string) (string, error)
	streamRunner  *agent.StreamRunner
	streamCh      chan providers.StreamEvent

	viewport viewport.Model
	input    textarea.Model

	layout     layout
	inputLines int

	width  int
	height int

	entries []transcriptEntry

	pendingRequest bool
	streaming      bool
	streamRunes    []rune
	streamCursor   int
	streamTarget   int
	streamElapsed  time.Duration

	autoFollow bool
	showJump   bool
	clock      string
	statusLine string

	mdRenderer *glamour.TermRenderer
	mdWidth    int
}

// NewModel builds the initial UI model.
func NewModel(cfg Config) Model {
	vp := viewport.New(80, minOutputHeight)
	vp.SetContent("")

	in := textarea.New()
	in.Placeholder = "Type prompt or slash command (/resume /fork /worktree /skills /insight)"
	in.Focus()
	in.SetWidth(80)
	in.SetHeight(3)
	in.ShowLineNumbers = false
	in.Prompt = "> "
	in.CharLimit = 0

	m := Model{
		provider:      cfg.Provider,
		modelName:     cfg.Model,
		configPath:    cfg.ConfigPath,
		workspaceRoot: filepath.Dir(cfg.ConfigPath),
		memoryPath:    cfg.MemoryPath,
		sessionDir:    cfg.SessionDir,
		runPrompt:     cfg.RunPrompt,
		streamRunner:  cfg.StreamRunner,
		viewport:      vp,
		input:         in,
		autoFollow:    true,
		clock:         time.Now().Format("15:04:05"),
		statusLine:    "ready",
		streamTarget:  -1,
	}

	// Session isolation: create or resume session.
	if m.sessionDir != "" {
		if cfg.ResumeID != "" {
			// Resume existing session.
			path, err := session.Load(m.sessionDir, cfg.ResumeID)
			if err == nil {
				m.sessionID = cfg.ResumeID
				m.memoryPath = path
			} else {
				m.statusLine = fmt.Sprintf("resume failed: %v", err)
			}
		}
		if m.sessionID == "" {
			// Create new session.
			sess, err := session.Create(m.sessionDir)
			if err == nil {
				m.sessionID = sess.ID
				m.memoryPath = session.FilePath(m.sessionDir, sess.ID)
			} else {
				m.statusLine = fmt.Sprintf("session create failed: %v", err)
			}
		}
	}

	return m.loadMemory()
}

func (m Model) loadMemory() Model {
	if strings.TrimSpace(m.memoryPath) == "" {
		return m
	}

	entries, err := loadMemoryEntries(m.memoryPath)
	if err != nil {
		m.statusLine = fmt.Sprintf("memory load failed: %v", err)
		return m
	}
	if len(entries) == 0 {
		return m
	}
	m.entries = append(m.entries, entries...)
	m.statusLine = fmt.Sprintf("resumed %d entries", len(entries))
	m.refreshViewport(true)
	return m
}

// Init starts the clock ticker.
func (m Model) Init() tea.Cmd {
	return tickCmd()
}

func tickCmd() tea.Cmd {
	return tea.Tick(time.Second, func(t time.Time) tea.Msg {
		return tickMsg{now: t}
	})
}

func streamTickCmd() tea.Cmd {
	return tea.Tick(streamTickDelay, func(_ time.Time) tea.Msg {
		return streamTickMsg{}
	})
}

// Update handles events.
func (m Model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		m.width = msg.Width
		m.height = msg.Height
		m.relayout()
		return m, nil

	case tickMsg:
		m.clock = msg.now.Format("15:04:05")
		return m, tickCmd()

	case responseMsg:
		m.pendingRequest = false
		if msg.err != nil {
			m.appendEntry("system", fmt.Sprintf("error: %v", msg.err))
			m.statusLine = "request failed"
			m.refreshViewport(true)
			return m, nil
		}

		m.streaming = true
		m.streamElapsed = msg.elapsed
		m.streamRunes = []rune(msg.answer)
		m.streamCursor = 0
		m.streamTarget = m.appendEntry("assistant", "")
		m.statusLine = "streaming response"
		m.refreshViewport(true)
		return m, streamTickCmd()

	case streamTickMsg:
		if !m.streaming || m.streamTarget < 0 || m.streamTarget >= len(m.entries) {
			return m, nil
		}
		if m.streamCursor >= len(m.streamRunes) {
			m.finishStream()
			return m, nil
		}
		end := min(m.streamCursor+streamChunkSize, len(m.streamRunes))
		m.entries[m.streamTarget].Content += string(m.streamRunes[m.streamCursor:end])
		m.streamCursor = end
		m.refreshViewport(true)
		if m.streamCursor >= len(m.streamRunes) {
			m.finishStream()
			return m, nil
		}
		return m, streamTickCmd()

	case streamEventMsg:
		switch msg.event.Type {
		case providers.EventContentDelta:
			if m.streamTarget >= 0 && m.streamTarget < len(m.entries) {
				if m.entries[m.streamTarget].Content == "(empty)" {
					m.entries[m.streamTarget].Content = ""
				}
				m.entries[m.streamTarget].Content += msg.event.Content
				m.refreshViewport(true)
			}
			return m, waitStreamEvent(m.streamCh)

		case providers.EventToolUseStart:
			toolName := ""
			if msg.event.ToolCall != nil {
				toolName = msg.event.ToolCall.Name
			}
			m.statusLine = fmt.Sprintf("executing tool: %s", toolName)
			return m, waitStreamEvent(m.streamCh)

		case providers.EventToolUseEnd:
			m.statusLine = "streaming"
			return m, waitStreamEvent(m.streamCh)

		case providers.EventDone:
			m.streaming = false
			m.pendingRequest = false
			if m.streamTarget >= 0 && m.streamTarget < len(m.entries) {
				raw := m.entries[m.streamTarget].Content
				rendered, err := m.renderMarkdown(raw)
				if err == nil {
					m.entries[m.streamTarget].Content = rendered
				}
			}
			m.streamTarget = -1
			m.statusLine = "ready"
			m.refreshViewport(true)
			return m, nil

		case providers.EventError:
			m.streaming = false
			m.pendingRequest = false
			m.streamTarget = -1
			errMsg := "stream error"
			if msg.event.Error != nil {
				errMsg = msg.event.Error.Error()
			}
			m.appendEntry("system", fmt.Sprintf("error: %s", errMsg))
			m.statusLine = "request failed"
			m.refreshViewport(true)
			return m, nil

		default:
			return m, waitStreamEvent(m.streamCh)
		}

	case tea.MouseMsg:
		if m.showJump &&
			msg.Action == tea.MouseActionPress &&
			msg.Button == tea.MouseButtonLeft &&
			msg.Y >= m.height-1 &&
			msg.X <= 32 {
			m.viewport.GotoBottom()
			m.autoFollow = true
			m.showJump = false
			return m, nil
		}

	case tea.KeyMsg:
		switch msg.String() {
		case "ctrl+c":
			return m, tea.Quit
		case "enter":
			return m.submit()
		case "ctrl+j", "end":
			m.viewport.GotoBottom()
			m.autoFollow = true
			m.showJump = false
			return m, nil
		case "pgup", "ctrl+u":
			m.viewport.ViewUp()
			m.autoFollow = false
			m.showJump = !m.viewport.AtBottom()
			return m, nil
		case "pgdown", "ctrl+d":
			m.viewport.ViewDown()
			m.showJump = !m.viewport.AtBottom()
			m.autoFollow = !m.showJump
			return m, nil
		}
	}

	var cmds []tea.Cmd
	var cmd tea.Cmd
	m.input, cmd = m.input.Update(msg)
	if cmd != nil {
		cmds = append(cmds, cmd)
	}

	// Re-layout when input line count changes.
	newLines := clampInputLines(strings.Count(m.input.Value(), "\n")+1, 15)
	if newLines != m.inputLines {
		m.relayout()
	}

	m.viewport, cmd = m.viewport.Update(msg)
	if cmd != nil {
		cmds = append(cmds, cmd)
	}
	m.showJump = !m.viewport.AtBottom()

	return m, tea.Batch(cmds...)
}

func (m Model) submit() (tea.Model, tea.Cmd) {
	raw := strings.TrimSpace(m.input.Value())
	if raw == "" {
		return m, nil
	}

	if output, handled := m.handleSlash(raw); handled {
		m.appendEntry("system", output)
		m.input.Reset()
		m.statusLine = "command executed"
		m.refreshViewport(true)
		return m, nil
	}

	if m.pendingRequest {
		m.statusLine = "request already running"
		return m, nil
	}

	m.appendEntry("user", raw)
	m.input.Reset()
	m.pendingRequest = true
	m.streaming = true
	m.streamTarget = m.appendEntry("assistant", "")
	m.statusLine = "streaming"
	m.refreshViewport(true)

	if m.streamRunner != nil {
		ch := make(chan providers.StreamEvent, 64)
		m.streamCh = ch
		runner := m.streamRunner
		go func() {
			defer close(ch)
			runner.OnEvent = func(event providers.StreamEvent) {
				ch <- event
			}
			_, err := runner.Run(context.Background(), raw)
			if err != nil {
				ch <- providers.StreamEvent{Type: providers.EventError, Error: err}
			}
		}()
		return m, waitStreamEvent(ch)
	}

	// Fallback to blocking path.
	start := time.Now()
	return m, func() tea.Msg {
		answer, err := m.runPrompt(context.Background(), raw)
		return responseMsg{
			answer:  answer,
			err:     err,
			elapsed: time.Since(start),
		}
	}
}

func waitStreamEvent(ch <-chan providers.StreamEvent) tea.Cmd {
	return func() tea.Msg {
		event, ok := <-ch
		if !ok {
			return streamEventMsg{event: providers.StreamEvent{Type: providers.EventDone}}
		}
		return streamEventMsg{event: event}
	}
}

func (m *Model) finishStream() {
	m.streaming = false
	m.streamCursor = 0
	raw := string(m.streamRunes)
	m.streamRunes = nil

	if m.streamTarget >= 0 && m.streamTarget < len(m.entries) {
		rendered, err := m.renderMarkdown(raw)
		if err == nil {
			m.entries[m.streamTarget].Content = rendered
		}
	}
	m.streamTarget = -1
	m.statusLine = fmt.Sprintf("response in %s", m.streamElapsed.Truncate(10*time.Millisecond))
	m.refreshViewport(true)
}

func (m *Model) renderMarkdown(content string) (string, error) {
	if strings.TrimSpace(content) == "" {
		return "(empty)", nil
	}

	width := max(40, m.viewport.Width-6)
	if m.mdRenderer == nil || m.mdWidth != width {
		renderer, err := glamour.NewTermRenderer(
			glamour.WithAutoStyle(),
			glamour.WithWordWrap(width),
		)
		if err != nil {
			return "", err
		}
		m.mdRenderer = renderer
		m.mdWidth = width
	}

	rendered, err := m.mdRenderer.Render(content)
	if err != nil {
		return "", err
	}
	return strings.TrimSpace(rendered), nil
}

func (m *Model) appendEntry(role, content string) int {
	text := strings.TrimSpace(content)
	if text == "" {
		text = "(empty)"
	}
	entry := transcriptEntry{
		Role:    strings.ToUpper(role),
		Content: text,
	}
	m.entries = append(m.entries, entry)
	if err := appendMemoryEntry(m.memoryPath, entry); err != nil {
		m.statusLine = fmt.Sprintf("memory write failed: %v", err)
	}
	return len(m.entries) - 1
}

func (m *Model) refreshViewport(forceBottom bool) {
	var b strings.Builder
	for i, entry := range m.entries {
		if i > 0 {
			b.WriteString("\n\n")
		}
		b.WriteString(entry.Role)
		b.WriteString("\n")
		b.WriteString(entry.Content)
	}
	if m.pendingRequest {
		if b.Len() > 0 {
			b.WriteString("\n\n")
		}
		b.WriteString("ASSISTANT\nthinking...")
	}

	m.viewport.SetContent(b.String())
	if forceBottom || m.autoFollow {
		m.viewport.GotoBottom()
	}
	m.showJump = !m.viewport.AtBottom()
}

func (m *Model) relayout() {
	if m.width <= 0 || m.height <= 0 {
		return
	}
	m.inputLines = clampInputLines(strings.Count(m.input.Value(), "\n")+1, 15)
	m.layout = computeLayout(m.width, m.height, m.inputLines)

	m.input.SetWidth(m.layout.Input.Width)
	m.input.SetHeight(m.layout.Input.Height)
	m.viewport.Width = m.layout.Chat.Width
	m.viewport.Height = m.layout.Chat.Height
	m.refreshViewport(false)
}

// View renders the full terminal.
func (m Model) View() string {
	if m.width == 0 || m.height == 0 {
		return "loading..."
	}

	// Header
	tokenEstimate := 0
	for _, e := range m.entries {
		tokenEstimate += len(e.Content) / 4
	}
	tokenStr := formatTokenCount(tokenEstimate)
	header := lipgloss.NewStyle().Bold(true).Render(
		trimToWidth(fmt.Sprintf("wuu · %s/%s · %s tokens", m.provider, m.modelName, tokenStr), m.width),
	)

	// Footer
	icon := "○"
	state := m.statusLine
	if m.streaming {
		icon = "●"
		state = "streaming"
	} else if strings.HasPrefix(m.statusLine, "executing tool:") {
		icon = "◆"
	} else if m.statusLine == "request failed" {
		icon = "✗"
	}

	jumpHint := ""
	if m.showJump {
		jumpHint = " · ▼ jump"
	}

	footerLeft := fmt.Sprintf("%s %s%s", icon, state, jumpHint)
	footerRight := m.clock
	availableW := max(1, m.width-lipgloss.Width(footerRight)-1)
	footerLeft = trimToWidth(footerLeft, availableW)
	gap := max(1, m.width-lipgloss.Width(footerLeft)-lipgloss.Width(footerRight))
	footer := lipgloss.NewStyle().Faint(true).Render(footerLeft + strings.Repeat(" ", gap) + footerRight)

	outputBox := m.viewport.View()
	inputBox := m.input.View()
	if !m.layout.Compact {
		outputBox = lipgloss.NewStyle().
			Border(lipgloss.NormalBorder()).
			Render(outputBox)
		inputBox = lipgloss.NewStyle().
			Border(lipgloss.NormalBorder()).
			Render(inputBox)
	}

	return strings.Join([]string{
		header,
		outputBox,
		inputBox,
		footer,
	}, "\n")
}

func trimToWidth(value string, width int) string {
	if width <= 0 {
		return ""
	}
	if lipgloss.Width(value) <= width {
		return value
	}
	if width == 1 {
		return "…"
	}

	var b strings.Builder
	for _, r := range value {
		next := b.String() + string(r)
		if lipgloss.Width(next+"…") > width {
			break
		}
		b.WriteRune(r)
	}
	return b.String() + "…"
}

func formatTokenCount(tokens int) string {
	if tokens >= 1000 {
		return fmt.Sprintf("%.1fk", float64(tokens)/1000)
	}
	return fmt.Sprintf("%d", tokens)
}
