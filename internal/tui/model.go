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
)

const (
	minOutputHeight = 6
	inputHeight     = 3
	headerHeight    = 2
	footerHeight    = 2
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
	runPrompt     func(ctx context.Context, prompt string) (string, error)

	viewport viewport.Model
	input    textarea.Model

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
	in.SetHeight(inputHeight)
	in.ShowLineNumbers = false
	in.Prompt = "> "
	in.CharLimit = 0

	return Model{
		provider:      cfg.Provider,
		modelName:     cfg.Model,
		configPath:    cfg.ConfigPath,
		workspaceRoot: filepath.Dir(cfg.ConfigPath),
		memoryPath:    cfg.MemoryPath,
		runPrompt:     cfg.RunPrompt,
		viewport:      vp,
		input:         in,
		autoFollow:    true,
		clock:         time.Now().Format("15:04:05"),
		statusLine:    "ready",
		streamTarget:  -1,
	}.loadMemory()
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
	m.statusLine = "running prompt"
	m.refreshViewport(true)

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

func (m Model) entryCount() int {
	return len(m.entries)
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

	m.input.SetWidth(max(16, m.width-2))
	outputHeight := m.height - headerHeight - footerHeight - inputHeight
	if outputHeight < minOutputHeight {
		outputHeight = minOutputHeight
	}
	m.viewport.Width = max(16, m.width-2)
	m.viewport.Height = outputHeight
	m.refreshViewport(false)
}

// View renders the full terminal.
func (m Model) View() string {
	if m.width == 0 || m.height == 0 {
		return "loading..."
	}

	title := lipgloss.NewStyle().Bold(true).Render(
		fmt.Sprintf("wuu tui | provider=%s | model=%s", m.provider, m.modelName),
	)
	meta := lipgloss.NewStyle().Faint(true).Render(fmt.Sprintf("config: %s", m.configPath))
	jumpHint := ""
	if m.showJump {
		jumpHint = " | Ctrl+J jump to bottom"
	}
	clock := lipgloss.NewStyle().Faint(true).Render(m.clock)
	statusText := m.statusLine
	if m.streaming {
		statusText += " (editing input is still available)"
	}
	status := lipgloss.NewStyle().Faint(true).Render(statusText + jumpHint)
	footer := lipgloss.JoinHorizontal(
		lipgloss.Left,
		status,
		strings.Repeat(" ", max(1, m.width-lipgloss.Width(status)-lipgloss.Width(clock)-2)),
		clock,
	)

	outputBox := lipgloss.NewStyle().
		Border(lipgloss.NormalBorder()).
		Padding(0, 1).
		Width(max(16, m.width-2)).
		Height(m.viewport.Height).
		Render(m.viewport.View())

	inputBox := lipgloss.NewStyle().
		Border(lipgloss.NormalBorder()).
		Padding(0, 1).
		Width(max(16, m.width-2)).
		Render(m.input.View())

	return strings.Join([]string{
		title,
		meta,
		outputBox,
		inputBox,
		footer,
	}, "\n")
}

func max(a, b int) int {
	if a > b {
		return a
	}
	return b
}

func min(a, b int) int {
	if a < b {
		return a
	}
	return b
}
