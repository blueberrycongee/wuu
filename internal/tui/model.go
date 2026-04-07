package tui

import (
	"context"
	"errors"
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
	"github.com/blueberrycongee/wuu/internal/compact"
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

type streamFinishedMsg struct{}

type ctrlCResetMsg struct{}

type queueDrainMsg struct{}

type ToolCallStatus string

const (
	ToolCallRunning ToolCallStatus = "running"
	ToolCallDone    ToolCallStatus = "done"
	ToolCallError   ToolCallStatus = "error"
)

type ToolCallEntry struct {
	ID        string
	Name      string
	Args      string
	Result    string
	Status    ToolCallStatus
	Collapsed bool
}

type transcriptEntry struct {
	Role        string
	Content     string // raw content
	rendered    string // markdown-rendered text (cached)
	renderedLen int    // Content length when rendered was last computed

	// Thinking block.
	ThinkingContent  string
	ThinkingDuration time.Duration
	ThinkingDone     bool
	ThinkingExpanded bool

	// Tool calls in this assistant turn.
	ToolCalls []ToolCallEntry
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

	maxContextTokens int

	viewport viewport.Model
	input    textarea.Model

	layout     layout
	inputLines int

	width  int
	height int

	entries        []transcriptEntry
	chatHistory    []providers.ChatMessage
	pendingNewMsgs *[]providers.ChatMessage // shared with goroutine for returning new messages

	pendingRequest bool
	streaming      bool
	streamRunes    []rune
	streamCursor   int
	streamTarget   int
	streamElapsed  time.Duration
	thinkingStart  time.Time // when thinking began for current turn
	spinnerTick    int

	autoFollow bool
	showJump   bool
	clock      string
	statusLine string

	mdRenderer *glamour.TermRenderer
	mdWidth    int

	// Slash command completion popup.
	completionVisible bool
	completionItems   []command
	completionIndex   int

	// Cancel in-flight stream on quit.
	cancelStream context.CancelFunc

	// Double ctrl+c to quit.
	ctrlCPressed bool
	quitting     bool

	// Lazy session creation — only write to disk on first message.
	sessionCreated bool

	// Input history — user messages for up/down recall.
	inputHistory []string
	historyIndex int    // -1 = not browsing, 0..len-1 = browsing
	historyDraft string // saves current input when entering history

	// Message queue — Tab queues, Enter cuts in line.
	messageQueue []string
}

// NewModel builds the initial UI model.
func NewModel(cfg Config) Model {
	vp := viewport.New(80, minOutputHeight)
	vp.SetContent("")
	vp.MouseWheelDelta = 1

	in := textarea.New()
	in.Placeholder = "Ask anything..."
	in.Focus()
	in.SetWidth(80)
	in.SetHeight(3)
	in.ShowLineNumbers = false
	in.Prompt = "> "
	in.CharLimit = 0

	m := Model{
		provider:         cfg.Provider,
		modelName:        cfg.Model,
		configPath:       cfg.ConfigPath,
		workspaceRoot:    filepath.Dir(cfg.ConfigPath),
		memoryPath:       cfg.MemoryPath,
		sessionDir:       cfg.SessionDir,
		runPrompt:        cfg.RunPrompt,
		streamRunner:     cfg.StreamRunner,
		maxContextTokens: cfg.MaxContextTokens,
		viewport:         vp,
		input:            in,
		autoFollow:       true,
		clock:            time.Now().Format("15:04:05"),
		statusLine:       "ready",
		streamTarget:     -1,
		historyIndex:     -1,
	}

	// Session isolation: create or resume session.
	if m.sessionDir != "" {
		if cfg.ResumeID != "" {
			// Resume existing session.
			path, err := session.Load(m.sessionDir, cfg.ResumeID)
			if err == nil {
				m.sessionID = cfg.ResumeID
				m.memoryPath = path
				m.sessionCreated = true // already on disk
			} else {
				m.statusLine = fmt.Sprintf("resume failed: %v", err)
			}
		}
		if m.sessionID == "" {
			// Generate session ID but don't write to disk yet.
			// Files are created lazily on first message (see ensureSessionFile).
			m.sessionID = session.NewID()
			m.memoryPath = session.FilePath(m.sessionDir, m.sessionID)
		}
	}

	// Seed chatHistory with the system prompt so every API call includes it.
	if m.streamRunner != nil && strings.TrimSpace(m.streamRunner.SystemPrompt) != "" {
		m.chatHistory = append(m.chatHistory, providers.ChatMessage{
			Role:    "system",
			Content: m.streamRunner.SystemPrompt,
		})
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

	// Populate input history from loaded user messages.
	for _, e := range entries {
		if e.Role == "USER" {
			content := strings.TrimSpace(e.Content)
			if content != "" && content != "(empty)" {
				m.inputHistory = append(m.inputHistory, content)
			}
		}
	}

	m.statusLine = fmt.Sprintf("resumed %d entries", len(entries))
	m.refreshViewport(true)

	// Also load structured chat history for API calls.
	chatMsgs, chatErr := loadChatHistory(m.memoryPath)
	if chatErr == nil && len(chatMsgs) > 0 {
		// If we already have a system prompt in chatHistory, keep it and append loaded messages.
		if len(m.chatHistory) > 0 && m.chatHistory[0].Role == "system" {
			m.chatHistory = append(m.chatHistory[:1], chatMsgs...)
		} else {
			m.chatHistory = chatMsgs
		}
	}

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
		m.spinnerTick++
		if m.streaming || m.pendingRequest || m.statusLine == "thinking" {
			m.refreshViewport(true)
		}
		return m, tickCmd()

	case streamFinishedMsg:
		// Runner goroutine completed (channel closed).
		m.streaming = false
		m.pendingRequest = false
		m.streamTarget = -1
		m.thinkingStart = time.Time{}
		m.statusLine = "ready"
		m.cacheRenderedEntries()

		// Merge new messages from the completed turn into chatHistory and persist.
		if m.pendingNewMsgs != nil && len(*m.pendingNewMsgs) > 0 {
			for _, msg := range *m.pendingNewMsgs {
				m.chatHistory = append(m.chatHistory, msg)
				_ = appendChatMessage(m.memoryPath, msg)
			}
			m.pendingNewMsgs = nil
		}

		m.refreshViewport(true)
		return m, func() tea.Msg { return queueDrainMsg{} }

	case ctrlCResetMsg:
		m.ctrlCPressed = false
		if m.statusLine == "press ctrl+c again to exit" {
			m.statusLine = "ready"
		}
		return m, nil

	case queueDrainMsg:
		return m.drainQueue()

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
			return m, func() tea.Msg { return queueDrainMsg{} }
		}
		end := min(m.streamCursor+streamChunkSize, len(m.streamRunes))
		m.entries[m.streamTarget].Content += string(m.streamRunes[m.streamCursor:end])
		m.streamCursor = end
		m.refreshViewport(true)
		if m.streamCursor >= len(m.streamRunes) {
			m.finishStream()
			return m, func() tea.Msg { return queueDrainMsg{} }
		}
		return m, streamTickCmd()

	case streamEventMsg:
		switch msg.event.Type {
		case providers.EventContentDelta:
			if m.streamTarget < 0 || m.streamTarget >= len(m.entries) {
				// New round of streaming — create a fresh assistant entry.
				m.streamTarget = m.appendEntry("assistant", "")
			}
			if m.entries[m.streamTarget].Content == "(empty)" {
				m.entries[m.streamTarget].Content = ""
			}
			m.entries[m.streamTarget].Content += msg.event.Content
			// Incremental markdown render every 80 chars of new content.
			e := &m.entries[m.streamTarget]
			if len(e.Content)-e.renderedLen >= 80 {
				if r, err := m.renderMarkdown(e.Content); err == nil {
					e.rendered = r
					e.renderedLen = len(e.Content)
				}
			}
			m.refreshViewport(true)
			return m, waitStreamEvent(m.streamCh)

		case providers.EventToolUseStart:
			if m.streamTarget < 0 || m.streamTarget >= len(m.entries) {
				m.streamTarget = m.appendEntry("assistant", "")
			}
			toolName := ""
			toolID := ""
			if msg.event.ToolCall != nil {
				toolName = msg.event.ToolCall.Name
				toolID = msg.event.ToolCall.ID
			}
			e := &m.entries[m.streamTarget]
			e.ToolCalls = append(e.ToolCalls, ToolCallEntry{
				ID:     toolID,
				Name:   toolName,
				Status: ToolCallRunning,
			})
			m.statusLine = fmt.Sprintf("tool: %s", toolName)
			m.refreshViewport(true)
			return m, waitStreamEvent(m.streamCh)

		case providers.EventToolUseEnd:
			if m.streamTarget >= 0 && m.streamTarget < len(m.entries) {
				e := &m.entries[m.streamTarget]
				for i := len(e.ToolCalls) - 1; i >= 0; i-- {
					tc := &e.ToolCalls[i]
					if tc.Status == ToolCallRunning {
						if msg.event.ToolCall != nil {
							tc.Args = msg.event.ToolCall.Arguments
						}
						tc.Result = msg.event.ToolResult
						tc.Status = ToolCallDone
						tc.Collapsed = true
						break
					}
				}
			}
			m.statusLine = "streaming"
			m.refreshViewport(true)
			return m, waitStreamEvent(m.streamCh)

		case providers.EventDone:
			// One SSE stream finished. The runner may continue with tool
			// execution and start another stream, so keep listening.
			if m.streamTarget >= 0 && m.streamTarget < len(m.entries) {
				content := strings.TrimSpace(m.entries[m.streamTarget].Content)
				if (content == "" || content == "(empty)") && len(m.entries[m.streamTarget].ToolCalls) == 0 && m.entries[m.streamTarget].ThinkingContent == "" {
					// No text content, no tool calls, no thinking — remove empty entry.
					m.entries = m.entries[:m.streamTarget]
					m.streamTarget = -1
				} else {
					m.cacheEntryRendered(m.streamTarget)
				}
			}
			m.refreshViewport(true)
			return m, waitStreamEvent(m.streamCh)

		case providers.EventError:
			// Ignore context cancellation — this is normal when the user
			// interrupts a stream by pressing Enter.
			if msg.event.Error != nil && (errors.Is(msg.event.Error, context.Canceled) ||
				strings.Contains(msg.event.Error.Error(), "context canceled")) {
				return m, waitStreamEvent(m.streamCh)
			}
			m.streaming = false
			m.pendingRequest = false
			// Show accumulated content so far (if any) before the error.
			if m.streamTarget >= 0 && m.streamTarget < len(m.entries) {
				content := strings.TrimSpace(m.entries[m.streamTarget].Content)
				if content == "" || content == "(empty)" {
					m.entries[m.streamTarget].Content = ""
				}
			}
			m.streamTarget = -1
			errMsg := "unknown stream error"
			if msg.event.Error != nil {
				errMsg = msg.event.Error.Error()
			}
			// Display error in red in the chat area.
			styledErr := lipgloss.NewStyle().
				Foreground(currentTheme.Error).
				Bold(true).
				Render("ERROR: " + errMsg)
			m.appendEntry("system", styledErr)
			m.statusLine = "request failed"
			m.refreshViewport(true)
			return m, nil

		case providers.EventThinkingDelta:
			if m.streamTarget < 0 || m.streamTarget >= len(m.entries) {
				m.streamTarget = m.appendEntry("assistant", "")
			}
			e := &m.entries[m.streamTarget]
			if e.ThinkingContent == "" {
				m.thinkingStart = time.Now()
			}
			e.ThinkingContent += msg.event.Content
			m.statusLine = "thinking"
			m.refreshViewport(true)
			return m, waitStreamEvent(m.streamCh)

		case providers.EventThinkingDone:
			if m.streamTarget >= 0 && m.streamTarget < len(m.entries) {
				e := &m.entries[m.streamTarget]
				e.ThinkingDone = true
				if !m.thinkingStart.IsZero() {
					e.ThinkingDuration = time.Since(m.thinkingStart)
				}
			}
			m.statusLine = "streaming"
			m.refreshViewport(true)
			return m, waitStreamEvent(m.streamCh)

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

		// Mouse click inside input area — reposition cursor.
		if msg.Action == tea.MouseActionPress && msg.Button == tea.MouseButtonLeft {
			borderOff := 0
			if !m.layout.Compact {
				borderOff = 1
			}
			promptW := 2 // "> " prompt width

			// Check if click is inside the input area (accounting for border).
			inputTop := m.layout.Input.Y + borderOff
			inputBot := inputTop + m.layout.Input.Height
			inputLeft := m.layout.Input.X + borderOff

			if msg.Y >= inputTop && msg.Y < inputBot && msg.X >= inputLeft {
				targetRow := msg.Y - inputTop
				targetCol := msg.X - inputLeft - promptW
				if targetCol < 0 {
					targetCol = 0
				}

				// Move to target row.
				// NOTE: Line() returns logical row, targetRow is visual row.
				// This works correctly for hard newlines but may misalign
				// with soft-wrapped lines. Acceptable for typical input widths.
				currentRow := m.input.Line()
				for currentRow < targetRow && currentRow < m.input.LineCount()-1 {
					m.input.CursorDown()
					currentRow++
				}
				for currentRow > targetRow && currentRow > 0 {
					m.input.CursorUp()
					currentRow--
				}

				// Move to target column.
				m.input.SetCursor(targetCol)
				return m, nil
			}
		}

	case tea.KeyMsg:
		// Handle completion popup navigation first.
		if m.completionVisible {
			switch msg.String() {
			case "up":
				if m.completionIndex > 0 {
					m.completionIndex--
				} else {
					m.completionIndex = len(m.completionItems) - 1
				}
				return m, nil
			case "down":
				if m.completionIndex < len(m.completionItems)-1 {
					m.completionIndex++
				} else {
					m.completionIndex = 0
				}
				return m, nil
			case "tab", "enter":
				if m.completionIndex >= 0 && m.completionIndex < len(m.completionItems) {
					selected := m.completionItems[m.completionIndex]
					m.input.SetValue("/" + selected.Name + " ")
					m.input.CursorEnd()
					m.completionVisible = false
					m.completionItems = nil
					return m, nil
				}
			case "esc":
				m.completionVisible = false
				m.completionItems = nil
				return m, nil
			}
		}

		switch msg.String() {
		case "ctrl+c":
			if m.ctrlCPressed {
				if m.cancelStream != nil {
					m.cancelStream()
				}
				m.quitting = true
				return m, tea.Quit
			}
			m.ctrlCPressed = true
			m.statusLine = "press ctrl+c again to exit"
			return m, tea.Tick(2*time.Second, func(time.Time) tea.Msg {
				return ctrlCResetMsg{}
			})
		case "ctrl+u":
			// Cmd+Backspace / Ctrl+U: clear input to beginning of line.
			m.input.SetValue("")
			m.historyIndex = -1
			m.historyDraft = ""
			m.completionVisible = false
			m.completionItems = nil
			return m, nil
		case "ctrl+w":
			// Ctrl+W / Alt+Backspace: delete word backward.
			val := m.input.Value()
			if val == "" {
				return m, nil
			}
			// Trim trailing spaces, then trim non-spaces.
			trimmed := strings.TrimRight(val, " \t")
			lastSpace := strings.LastIndexAny(trimmed, " \t")
			if lastSpace < 0 {
				m.input.SetValue("")
			} else {
				m.input.SetValue(trimmed[:lastSpace+1])
			}
			m.input.CursorEnd()
			return m, nil
		case "enter":
			m.completionVisible = false
			m.completionItems = nil
			return m.submit(false)
		case "tab":
			if !m.completionVisible {
				return m.submit(true)
			}
		case "up":
			if m.canNavigateHistory() && len(m.inputHistory) > 0 {
				return m.historyUp()
			}
		case "down":
			if m.historyIndex >= 0 {
				return m.historyDown()
			}
		case "ctrl+j", "end":
			m.viewport.GotoBottom()
			m.autoFollow = true
			m.showJump = false
			return m, nil
		case "pgup":
			m.viewport.ViewUp()
			m.autoFollow = false
			m.showJump = !m.viewport.AtBottom()
			return m, nil
		case "pgdown":
			m.viewport.ViewDown()
			m.showJump = !m.viewport.AtBottom()
			m.autoFollow = !m.showJump
			return m, nil
		case "t":
			// Toggle thinking block expand/collapse.
			for i := len(m.entries) - 1; i >= 0; i-- {
				if m.entries[i].Role == "ASSISTANT" && m.entries[i].ThinkingContent != "" {
					m.entries[i].ThinkingExpanded = !m.entries[i].ThinkingExpanded
					m.refreshViewport(false)
					break
				}
			}
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

	// Update slash command completion popup.
	m.updateCompletion()

	m.viewport, cmd = m.viewport.Update(msg)
	if cmd != nil {
		cmds = append(cmds, cmd)
	}
	m.showJump = !m.viewport.AtBottom()

	return m, tea.Batch(cmds...)
}

func (m Model) submit(shouldQueue bool) (tea.Model, tea.Cmd) {
	raw := strings.TrimSpace(m.input.Value())
	if raw == "" {
		return m, nil
	}

	if output, handled := m.handleSlash(raw); handled {
		if output == "__exit__" {
			if m.cancelStream != nil {
				m.cancelStream()
			}
			m.quitting = true
			return m, tea.Quit
		}
		m.appendEntry("system", output)
		m.input.Reset()
		m.statusLine = "command executed"
		m.refreshViewport(true)
		return m, nil
	}

	// Record in input history (skip duplicates).
	if len(m.inputHistory) == 0 || m.inputHistory[len(m.inputHistory)-1] != raw {
		m.inputHistory = append(m.inputHistory, raw)
	}
	m.historyIndex = -1
	m.historyDraft = ""
	m.input.Reset()

	if m.pendingRequest && shouldQueue {
		// Tab while busy — queue the message.
		m.messageQueue = append(m.messageQueue, raw)
		m.statusLine = fmt.Sprintf("queued (%d pending)", len(m.messageQueue))
		return m, nil
	}

	if m.pendingRequest {
		// Enter while busy — prioritize this message ahead of queued ones.
		// If the current request is streamable, cancel it and let queue drain
		// start the prioritized message as soon as the runner exits.
		m.messageQueue = append([]string{raw}, m.messageQueue...)
		if m.cancelStream != nil {
			m.cancelStream()
			m.statusLine = fmt.Sprintf("interrupting · %d queued", len(m.messageQueue))
		} else {
			m.statusLine = fmt.Sprintf("prioritized (%d pending)", len(m.messageQueue))
		}
		return m, nil
	}

	// If idle, both Tab and Enter send directly.
	return m.sendMessage(raw)
}

func (m Model) sendMessage(raw string) (tea.Model, tea.Cmd) {
	m.appendEntry("user", raw)
	m.chatHistory = append(m.chatHistory, providers.ChatMessage{Role: "user", Content: raw})
	_ = appendChatMessage(m.memoryPath, providers.ChatMessage{Role: "user", Content: raw})

	// Compact history if approaching context limit.
	if m.maxContextTokens > 0 && compact.ShouldCompact(m.chatHistory, m.maxContextTokens) {
		if m.streamRunner != nil {
			compacted, compactErr := compact.Compact(
				context.Background(),
				m.chatHistory,
				m.streamRunner.Client,
				m.streamRunner.Model,
			)
			if compactErr == nil {
				m.chatHistory = compacted
			}
		}
	}

	m.pendingRequest = true
	m.streaming = true
	m.streamTarget = m.appendEntry("assistant", "")
	queueHint := ""
	if len(m.messageQueue) > 0 {
		queueHint = fmt.Sprintf(" · %d queued", len(m.messageQueue))
	}
	m.statusLine = "streaming" + queueHint
	m.refreshViewport(true)

	if m.streamRunner != nil {
		ch := make(chan providers.StreamEvent, 64)
		m.streamCh = ch
		runner := m.streamRunner
		ctx, cancel := context.WithCancel(context.Background())
		m.cancelStream = cancel

		// Copy history for the goroutine (defensive copy).
		history := make([]providers.ChatMessage, len(m.chatHistory))
		copy(history, m.chatHistory)

		// Shared pointer for goroutine to return new messages.
		newMsgsHolder := &[]providers.ChatMessage{}
		m.pendingNewMsgs = newMsgsHolder

		go func() {
			defer close(ch)
			onEvent := func(event providers.StreamEvent) {
				select {
				case ch <- event:
				case <-ctx.Done():
				}
			}
			_, newMsgs, err := runner.RunWithCallback(ctx, history, onEvent)
			*newMsgsHolder = newMsgs // safe: written before close(ch)
			if err != nil && ctx.Err() == nil {
				select {
				case ch <- providers.StreamEvent{Type: providers.EventError, Error: err}:
				case <-ctx.Done():
				}
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
			// Channel closed — runner goroutine finished.
			return streamFinishedMsg{}
		}
		return streamEventMsg{event: event}
	}
}

// drainQueue sends the next queued message if idle.
func (m Model) drainQueue() (tea.Model, tea.Cmd) {
	if m.pendingRequest || len(m.messageQueue) == 0 {
		return m, nil
	}
	next := m.messageQueue[0]
	m.messageQueue = m.messageQueue[1:]
	return m.sendMessage(next)
}

func (m *Model) finishStream() {
	m.streaming = false
	m.streamCursor = 0
	raw := string(m.streamRunes)
	m.streamRunes = nil

	if m.streamTarget >= 0 && m.streamTarget < len(m.entries) {
		m.entries[m.streamTarget].Content = raw
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
			glamour.WithStandardStyle("dark"),
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

// cacheEntryRendered renders markdown for a single entry and caches the result.
func (m *Model) cacheEntryRendered(idx int) {
	if idx < 0 || idx >= len(m.entries) {
		return
	}
	e := &m.entries[idx]
	if e.Role == "ASSISTANT" {
		if r, err := m.renderMarkdown(e.Content); err == nil {
			e.rendered = r
			e.renderedLen = len(e.Content)
		}
	}
}

// cacheRenderedEntries renders markdown for all uncached assistant entries.
func (m *Model) cacheRenderedEntries() {
	for i := range m.entries {
		if m.entries[i].Role == "ASSISTANT" && m.entries[i].rendered == "" {
			m.cacheEntryRendered(i)
		}
	}
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

	// Lazy session creation: write files on first real message.
	m.ensureSessionFile()

	// Only persist non-chat entries via old format.
	// User/assistant/tool messages are persisted via appendChatMessage elsewhere.
	upperRole := strings.ToUpper(role)
	if upperRole != "USER" && upperRole != "ASSISTANT" && upperRole != "TOOL" {
		if err := appendMemoryEntry(m.memoryPath, entry); err != nil {
			m.statusLine = fmt.Sprintf("memory write failed: %v", err)
		}
	}
	return len(m.entries) - 1
}

// ensureSessionFile creates the session data file and index entry on first use.
func (m *Model) ensureSessionFile() {
	if m.sessionCreated || m.sessionDir == "" || m.sessionID == "" {
		return
	}
	sess, err := session.Create(m.sessionDir, m.sessionID)
	if err != nil {
		m.statusLine = fmt.Sprintf("session write failed: %v", err)
		return
	}
	m.memoryPath = session.FilePath(m.sessionDir, sess.ID)
	m.sessionCreated = true
}

// canNavigateHistory returns true when up/down should browse history
// instead of moving the cursor within the textarea.
func (m *Model) canNavigateHistory() bool {
	val := m.input.Value()
	if val == "" {
		return true
	}
	// If currently browsing and text matches the recalled entry, keep navigating.
	if m.historyIndex >= 0 && m.historyIndex < len(m.inputHistory) {
		return val == m.inputHistory[m.historyIndex]
	}
	return false
}

func (m Model) historyUp() (tea.Model, tea.Cmd) {
	if m.historyIndex < 0 {
		// Entering history mode — save current draft.
		m.historyDraft = m.input.Value()
		m.historyIndex = len(m.inputHistory) - 1
	} else if m.historyIndex > 0 {
		m.historyIndex--
	} else {
		return m, nil // already at oldest
	}
	m.input.SetValue(m.inputHistory[m.historyIndex])
	m.input.CursorEnd()
	return m, nil
}

func (m Model) historyDown() (tea.Model, tea.Cmd) {
	if m.historyIndex < 0 {
		return m, nil
	}
	if m.historyIndex < len(m.inputHistory)-1 {
		m.historyIndex++
		m.input.SetValue(m.inputHistory[m.historyIndex])
		m.input.CursorEnd()
	} else {
		// Past newest — exit history, restore draft.
		m.historyIndex = -1
		m.input.SetValue(m.historyDraft)
		m.input.CursorEnd()
		m.historyDraft = ""
	}
	return m, nil
}

func (m *Model) refreshViewport(forceBottom bool) {
	var b strings.Builder

	if len(m.entries) == 0 && !m.pendingRequest {
		// Show welcome screen when chat is empty.
		b.WriteString(welcomeScreen(m.viewport.Width, m.provider, m.modelName, m.sessionID))
	} else {
		for i, entry := range m.entries {
			if i > 0 {
				b.WriteString("\n\n")
			}
			// Role indicator — icon only, no text label.
			switch entry.Role {
			case "USER":
				b.WriteString(userLabelStyle.Render("❯"))
				b.WriteString("\n")
			case "ASSISTANT":
				// No label for assistant — content speaks for itself.
			default:
				b.WriteString(systemLabelStyle.Render(entry.Role))
				b.WriteString("\n")
			}

			// Thinking block (if present).
			if entry.ThinkingContent != "" {
				elapsed := entry.ThinkingDuration
				if !entry.ThinkingDone && !m.thinkingStart.IsZero() {
					elapsed = time.Since(m.thinkingStart)
				}
				b.WriteString(renderThinkingBlock(
					entry.ThinkingContent,
					entry.ThinkingDone,
					entry.ThinkingExpanded,
					elapsed,
					m.viewport.Width,
					m.spinnerTick,
				))
				b.WriteString("\n")
			}

			// Tool call cards.
			for _, tc := range entry.ToolCalls {
				b.WriteString(renderToolCard(tc, m.viewport.Width))
				b.WriteString("\n")
			}

			// Main content.
			content := truncateForDisplay(entry.Content)
			if content != "(empty)" {
				wrapWidth := max(40, m.viewport.Width-2)
				if entry.Role == "USER" {
					b.WriteString(userContentStyle.Render(wrapText(content, wrapWidth-2)))
				} else if entry.rendered != "" {
					b.WriteString(wrapText(entry.rendered, wrapWidth))
				} else {
					b.WriteString(wrapText(content, wrapWidth))
				}
				// Streaming cursor.
				if m.streaming && i == m.streamTarget {
					b.WriteString("▌")
				}
			}
		}
		if m.pendingRequest && m.streamTarget < 0 {
			if b.Len() > 0 {
				b.WriteString("\n\n")
			}
			elapsed := time.Duration(0)
			if !m.thinkingStart.IsZero() {
				elapsed = time.Since(m.thinkingStart)
			}
			b.WriteString(renderThinkingBlock("", false, false, elapsed, m.viewport.Width, m.spinnerTick))
		}
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
	header := headerStyle.Render(
		trimToWidth(fmt.Sprintf("wuu · %s/%s │ %s tokens", m.provider, m.modelName, tokenStr), m.width),
	)

	// Footer
	var iconStyled string
	state := m.statusLine
	if m.streaming {
		iconStyled = statusStreamStyle.Render("●")
		state = "streaming"
	} else if m.statusLine == "thinking" {
		iconStyled = statusStreamStyle.Render("◐")
		state = "thinking"
	} else if strings.HasPrefix(m.statusLine, "executing tool:") {
		iconStyled = statusToolStyle.Render("◆")
	} else if m.statusLine == "request failed" {
		iconStyled = statusErrorStyle.Render("✗")
	} else {
		iconStyled = statusReadyStyle.Render("○")
	}

	jumpHint := ""
	if m.showJump {
		jumpHint = " · ▼ jump"
	}

	queueHint := ""
	if len(m.messageQueue) > 0 {
		queueHint = fmt.Sprintf(" · %d queued", len(m.messageQueue))
	}

	footerLeft := fmt.Sprintf("%s %s%s%s", iconStyled, state, queueHint, jumpHint)
	footerRight := fmt.Sprintf("t:thinking · %s", m.clock)
	availableW := max(1, m.width-lipgloss.Width(footerRight)-1)
	footerLeft = trimToWidth(footerLeft, availableW)
	gap := max(1, m.width-lipgloss.Width(footerLeft)-lipgloss.Width(footerRight))
	footer := footerStyle.Render(footerLeft + strings.Repeat(" ", gap) + footerRight)

	outputBox := m.viewport.View()

	// Overlay scrollbar on the rightmost column of the viewport.
	sb := renderScrollbar(
		m.layout.Chat.Height,
		m.viewport.TotalLineCount(),
		m.viewport.Height,
		m.viewport.YOffset,
	)
	if sb != "" {
		outputBox = overlayScrollbar(outputBox, sb, m.layout.Chat.Width)
	}
	inputBox := m.input.View()
	if !m.layout.Compact {
		inputBox = inputBorderStyle.Render(inputBox)
	}

	// Overlay completion popup on top of outputBox if visible.
	if m.completionVisible && len(m.completionItems) > 0 {
		popup := renderCompletion(m.completionItems, m.completionIndex, m.width)
		outputBox = overlayBottom(outputBox, popup, m.width)
	}

	parts := []string{header, outputBox, inputBox, footer}

	return strings.Join(parts, "\n")
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
