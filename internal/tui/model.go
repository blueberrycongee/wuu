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
	"github.com/charmbracelet/lipgloss"

	"github.com/blueberrycongee/wuu/internal/agent"
	"github.com/blueberrycongee/wuu/internal/compact"
	"github.com/blueberrycongee/wuu/internal/coordinator"
	"github.com/blueberrycongee/wuu/internal/cron"
	"github.com/blueberrycongee/wuu/internal/hooks"
	"github.com/blueberrycongee/wuu/internal/insight"
	"github.com/blueberrycongee/wuu/internal/markdown"
	"github.com/blueberrycongee/wuu/internal/memory"
	processruntime "github.com/blueberrycongee/wuu/internal/process"
	"github.com/blueberrycongee/wuu/internal/providers"
	"github.com/blueberrycongee/wuu/internal/session"
	"github.com/blueberrycongee/wuu/internal/skills"
	"github.com/blueberrycongee/wuu/internal/subagent"
)

const (
	minOutputHeight = 6
	// interactiveStreamDrainLimit caps how many already-queued stream
	// events we opportunistically apply during non-stream UI work
	// (mouse drag/select, spinner ticks). Without this side-drain, a
	// burst of mouse motion can starve the single waitStreamEvent
	// command long enough for live reply rendering to look "stuck".
	interactiveStreamDrainLimit = 8

	queuePreviewMaxItems = 2
	queuePreviewMaxChars = 28

	chatSelectionDragThreshold = 1

	// maxAutoResumeChain caps how many turns the main agent can
	// auto-fire in response to worker completions without seeing
	// fresh user input. A pure safety net — modern models stop
	// naturally well before this in normal use.
	maxAutoResumeChain = 100
)

var defaultInputTextarea = newInputTextarea()

type tickMsg struct {
	now time.Time
}

type streamEventMsg struct {
	event providers.StreamEvent
}

type streamFinishedMsg struct{}
type ctrlCResetMsg struct{}

type queueDrainMsg struct{}

type cronFireMsg struct {
	task cron.Task
}

type inlineSpinMsg struct{}
type processPollMsg struct{}
type processNotifyMsg struct {
	event processruntime.Event
}

// selectionAutoScrollMsg drives the recurring viewport scroll while a
// drag-select is held past the chat area's edge. seq must match the
// model's current selectionAutoScroll.seq for the tick to take effect;
// stale ticks (from a burst the user has already left) self-discard.
type selectionAutoScrollMsg struct {
	seq int
}

// selectionAutoScrollState captures everything needed to keep
// scrolling without further mouse motion events. dir is -1 (up) or
// +1 (down). speed is the number of content lines to advance per
// tick — proportional to how far past the edge the cursor sat at
// the moment we (re)started ticking, so dragging further past the
// edge scrolls faster, mirroring most desktop editors. lastX is the
// most recent screen X so we can re-derive the selection focus
// column on every tick (the user's mouse hasn't moved, but the
// content under it has). seq is bumped on every (de)activation so
// in-flight ticks from a previous burst exit cleanly.
type selectionAutoScrollState struct {
	active bool
	dir    int
	speed  int
	lastX  int
	seq    int
}

type insightProgressMsg struct {
	event insight.ProgressEvent
}

type insightFinishedMsg struct{}

// workerNotifyMsg is delivered when a sub-agent's status changes.
type workerNotifyMsg struct {
	notification subagent.Notification
}

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

	// cachedCard is the fully rendered tool card string. Invalidated
	// when Status, Result, Collapsed, or Args changes. Avoids
	// re-parsing JSON and re-rendering on every viewport refresh.
	cachedCard      string
	cachedCardKey   string // "status:collapsed:argsLen:resultLen"
	cachedCardWidth int
}

type transcriptEntry struct {
	Role          string
	Content       string   // raw content
	rendered      string   // markdown-rendered text (cached)
	renderedLines []string // lines accumulated from StreamCollector commits
	renderStart   int      // inclusive content line in the last rendered viewport snapshot
	renderEnd     int      // inclusive content line in the last rendered viewport snapshot

	// composited is the fully rendered entry output including tool
	// cards, thinking blocks, content, indent wrapping — everything
	// that refreshViewport would compute. Keyed by compositedKey.
	// When valid, refreshViewport skips all per-entry render work
	// and just concatenates cached strings. Aligned with Claude
	// Code's component-level caching and Codex's committed_line_count.
	composited    string
	compositedKey uint64 // hash of inputs that produced composited
	compositedH   int    // line count of composited (for virtual viewport)

	// streamBuf accumulates content deltas during streaming via
	// WriteString (O(1) amortized). When streaming ends, Content is
	// set to streamBuf.String() once. This replaces the old
	// Content += delta pattern which copied the entire string on
	// every token (O(n²) total).
	streamBuf *strings.Builder

	// Thinking block.
	ThinkingContent  string
	ThinkingDuration time.Duration
	ThinkingDone     bool
	ThinkingExpanded bool

	// Tool calls in this assistant turn.
	ToolCalls []ToolCallEntry

	// blockOrder records the stream-order sequence of content blocks.
	// Each entry is either "text" (for Content segments) or "tool:N"
	// (for ToolCalls[N]). Rendering follows this order to match
	// Claude Code's interleaved display. When empty, falls back to
	// legacy order (thinking → tools → content).
	blockOrder []string

	// textSegmentOffsets tracks byte offsets into Content where each
	// "text" segment begins. Used to split Content into per-segment
	// slices for interleaved rendering. len(textSegmentOffsets) ==
	// number of "text" entries in blockOrder.
	textSegmentOffsets []int
}

type queuedMessage struct {
	Text            string
	Images          []providers.InputImage
	ScheduledTaskID string
}

type pendingTurnResult struct {
	newMsgs              []providers.ChatMessage
	historyRewritten     bool
	incrementalPersisted bool
}

type pendingChatClickState struct {
	active bool
	x      int
	y      int
}

type workerUsageSnapshot struct {
	inputTokens  int
	outputTokens int
}

// Model implements the terminal UI state machine.
type Model struct {
	provider        string
	modelName       string
	configPath      string
	workspaceRoot   string
	memoryPath      string
	sessionID       string
	sessionDir      string
	streamRunner    *agent.StreamRunner
	hookDispatcher  *hooks.Dispatcher
	streamCh        chan providers.StreamEvent
	onSessionID     func(string)
	skills          []skills.Skill
	memoryFiles     []memory.File
	coordinator     *coordinator.Coordinator
	processManager  *processruntime.Manager
	processNotifyCh chan processruntime.Event
	workerNotifyCh  chan subagent.Notification

	// Cron scheduler: fires scheduled prompts into messageQueue.
	scheduler     *cron.Scheduler
	cronFireCh    chan cron.Task
	schedulerLock *cron.Lock

	// Auto-resume state: when a worker completes while the main agent
	// is busy, we set pendingAutoResume so the streamFinishedMsg
	// handler knows to fire a fresh turn from the existing history.
	// autoResumeChain counts how many auto-turns have fired in a row
	// without user input — used as a runaway safety net.
	pendingAutoResume bool
	autoResumeChain   int

	// pendingWorkerResults holds worker-result messages that arrived
	// while a turn was still in flight. They are appended only after
	// the turn's messages have been committed so they cannot land
	// between an assistant tool_call and its tool result.
	pendingWorkerResults []providers.ChatMessage

	requestTimeout time.Duration

	viewport viewport.Model
	input    textarea.Model

	layout     layout
	inputLines int

	width  int
	height int

	entries     []transcriptEntry
	chatHistory []providers.ChatMessage
	pendingTurn *pendingTurnResult // shared with goroutine for returning turn result

	pendingRequest bool
	streaming      bool
	streamTarget   int
	streamElapsed  time.Duration
	thinkingStart  time.Time // when thinking began for current turn
	spinnerFrame   int

	autoFollow      bool
	showJump        bool
	clock           string
	statusLine      string
	liveWorkStatus  workStatus
	inlineSpinFrame int

	streamCollector *markdown.StreamCollector

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

	// Message queue — Tab queues follow-up messages.
	messageQueue []queuedMessage
	// Steer queue — Enter while busy adds steer messages.
	pendingSteers []queuedMessage

	// Pending image attachments for the next user message.
	pendingImages    []providers.InputImage
	imageBarFocused  bool // true when user is navigating the image bar
	selectedImageIdx int  // index of the selected image pill

	// renderedContent is the full multi-line string most recently
	// passed to viewport.SetContent. We hold our own copy because
	// the bubbletea viewport's View() only returns the visible
	// window, and selection / copy need access to lines that may
	// have scrolled off-screen.
	renderedContent string

	// Cached token estimate for the header, updated only when entries change.
	cachedTokenEstimate int

	// Cached separator line, invalidated on width change.
	cachedSep string

	// Deferred viewport refresh. When the user scrolls away from the
	// active streaming entry, live deltas update transcript state
	// immediately but postpone viewport.SetContent until that entry is
	// visible again (or the user returns to bottom).
	pendingViewportRefresh bool
	pendingViewportEntry   int

	// Text selection in viewport.
	selection selectionState

	// In-viewport search overlay state.
	search searchState

	// Pending click in the chat area. A plain click should focus the
	// input on release; only once motion exceeds a small threshold do
	// we convert it into an actual text-selection drag.
	pendingChatClick pendingChatClickState

	// Text selection in input textarea.
	inputSelection    selectionState
	pendingInputClick pendingChatClickState

	// Auto-scroll state for drag-select past the viewport edge.
	// While the mouse is held outside the chat area, a recurring tick
	// scrolls the viewport in the held direction so the selection can
	// extend into off-screen content (standard editor behavior).
	// `seq` is bumped on every (de)activation so stale in-flight ticks
	// from a previous burst can recognize themselves and exit cleanly
	// instead of compounding into runaway scroll.
	selectionAutoScroll selectionAutoScrollState

	// Token usage accumulator for current session.
	mainInputTokens    int
	mainOutputTokens   int
	workerInputTokens  int
	workerOutputTokens int
	workerUsageByID    map[string]workerUsageSnapshot
	workerSpawnedByID  map[string]bool
	processEventSeen   map[string]bool
	turnInputTokens    int
	turnOutputTokens   int

	// Projected input-token count for the next provider request —
	// system prompt + tool schemas + chat history. Recomputed in
	// refreshViewport so the header can show the live context
	// utilization %. Intentionally slight overestimate (counts tool
	// schemas even on the first turn when no tool_use has happened
	// yet) so the warning/error colour triggers fire a bit early.
	contextTokens int

	// Insight generation state.
	insightRunning     bool
	insightCh          chan insight.ProgressEvent
	cancelInsight      context.CancelFunc
	insightProgressIdx int // index of the live progress entry in entries, -1 if none

	// Resume picker (modal sub-screen).
	resumePicker *resumePicker

	// Ask-user bridge + active modal. When the main agent calls the
	// ask_user tool, the bridge publishes a pending request to
	// askBridge.Requests(); a tea.Cmd reads it and delivers an
	// askRequestMsg which wires up activeAsk. While activeAsk != nil
	// the modal takes over key routing and View rendering, same
	// pattern as resumePicker.
	askBridge *AskUserBridge
	activeAsk *askUserModal
}

// NewModel builds the initial UI model.
func NewModel(cfg Config) Model {
	vp := viewport.New(80, minOutputHeight)
	vp.SetContent("")
	vp.MouseWheelDelta = 3

	in := defaultInputTextarea
	workspaceRoot := strings.TrimSpace(cfg.WorkspaceRoot)
	if workspaceRoot == "" {
		workspaceRoot = filepath.Dir(cfg.ConfigPath)
	}

	m := Model{
		provider:             cfg.Provider,
		modelName:            cfg.Model,
		configPath:           cfg.ConfigPath,
		workspaceRoot:        workspaceRoot,
		memoryPath:           cfg.MemoryPath,
		sessionDir:           cfg.SessionDir,
		streamRunner:         cfg.StreamRunner,
		hookDispatcher:       cfg.HookDispatcher,
		onSessionID:          cfg.OnSessionID,
		skills:               cfg.Skills,
		memoryFiles:          cfg.Memory,
		coordinator:          cfg.Coordinator,
		processManager:       cfg.ProcessManager,
		askBridge:            cfg.AskUserBridge,
		requestTimeout:       cfg.RequestTimeout,
		viewport:             vp,
		input:                in,
		autoFollow:           true,
		clock:                time.Now().Format("15:04:05"),
		statusLine:           "ready",
		pendingViewportEntry: -1,
		streamTarget:         -1,
		workerUsageByID:      make(map[string]workerUsageSnapshot),
		workerSpawnedByID:    make(map[string]bool),
		processEventSeen:     make(map[string]bool),
		historyIndex:         -1,
		insightProgressIdx:   -1,
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
		if m.onSessionID != nil && m.sessionID != "" {
			m.onSessionID(m.sessionID)
		}
	}

	// Subscribe to coordinator worker notifications, if a coordinator
	// is wired up. The channel is drained by waitWorkerNotify (a tea.Cmd
	// returned from Init / Update).
	if m.coordinator != nil {
		m.workerNotifyCh = make(chan subagent.Notification, 64)
		m.coordinator.Subscribe(m.workerNotifyCh)
	}
	if m.processManager != nil {
		m.processNotifyCh = make(chan processruntime.Event, 64)
		m.processManager.Subscribe(m.processNotifyCh)
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

func (m *Model) resetChatHistory() {
	m.chatHistory = nil
	if m.streamRunner != nil && strings.TrimSpace(m.streamRunner.SystemPrompt) != "" {
		m.chatHistory = append(m.chatHistory, providers.ChatMessage{
			Role:    "system",
			Content: m.streamRunner.SystemPrompt,
		})
	}
}

func finishInputTextareaSetup(in *textarea.Model) {
	in.Placeholder = "Ask anything..."
	in.Focus()
	in.SetWidth(80)
	in.SetHeight(3)
	in.ShowLineNumbers = false
	in.Prompt = "> "
	in.CharLimit = 0
	applyInputTextareaTheme(in)
}

func newInputTextarea() textarea.Model {
	in := textarea.New()
	finishInputTextareaSetup(&in)
	return in
}

func refreshTextareasForTheme() {
	defaultInputTextarea = newInputTextarea()
	defaultOnboardingTextarea = newOnboardingTextarea()
}

func applyInputTextareaTheme(in *textarea.Model) {
	focused := in.FocusedStyle
	focused.Base = lipgloss.NewStyle()
	focused.CursorLine = lipgloss.NewStyle()
	focused.CursorLineNumber = lipgloss.NewStyle().Foreground(currentTheme.Subtle)
	focused.EndOfBuffer = lipgloss.NewStyle().Foreground(currentTheme.Inactive)
	focused.LineNumber = lipgloss.NewStyle().Foreground(currentTheme.Subtle)
	focused.Placeholder = lipgloss.NewStyle().Foreground(currentTheme.Inactive)
	focused.Prompt = lipgloss.NewStyle().Foreground(currentTheme.Brand)
	focused.Text = lipgloss.NewStyle().Foreground(currentTheme.Text)

	blurred := in.BlurredStyle
	blurred.Base = lipgloss.NewStyle()
	blurred.CursorLine = lipgloss.NewStyle()
	blurred.CursorLineNumber = lipgloss.NewStyle().Foreground(currentTheme.Subtle)
	blurred.EndOfBuffer = lipgloss.NewStyle().Foreground(currentTheme.Inactive)
	blurred.LineNumber = lipgloss.NewStyle().Foreground(currentTheme.Subtle)
	blurred.Placeholder = lipgloss.NewStyle().Foreground(currentTheme.Inactive)
	blurred.Prompt = lipgloss.NewStyle().Foreground(currentTheme.Subtle)
	blurred.Text = lipgloss.NewStyle().Foreground(currentTheme.Text)

	in.FocusedStyle = focused
	in.BlurredStyle = blurred
}

func (m Model) loadMemory() Model {
	if strings.TrimSpace(m.memoryPath) == "" {
		return m
	}

	// Load structured history first. This may repair and rewrite old
	// sessions before the transcript view is reconstructed from disk.
	chatMsgs, chatErr := loadChatHistory(m.memoryPath)
	if chatErr == nil && len(chatMsgs) > 0 {
		// If we already have a system prompt in chatHistory, keep it and append loaded messages.
		if len(m.chatHistory) > 0 && m.chatHistory[0].Role == "system" {
			m.chatHistory = append(m.chatHistory[:1], chatMsgs...)
		} else {
			m.chatHistory = chatMsgs
		}
	}

	entries, err := loadMemoryEntries(m.memoryPath)
	if err != nil {
		m.statusLine = fmt.Sprintf("memory load failed: %v", err)
		return m
	}
	if len(entries) > 0 {
		m.entries = append(m.entries, entries...)

		// Populate input history from loaded user messages.
		for _, e := range entries {
			if e.Role == "USER" {
				content := strings.TrimSpace(stripUserImagePlaceholderLines(e.Content))
				if content != "" && content != "(empty)" {
					m.inputHistory = append(m.inputHistory, content)
				}
			}
		}

		m.statusLine = fmt.Sprintf("resumed %d entries", len(entries))
	}
	m.loadPersistedTokenUsage()
	m.cacheRenderedEntries()
	m.refreshViewport(true)

	// Start cron scheduler for durable tasks.
	schedPath := filepath.Join(m.workspaceRoot, ".wuu", "scheduled_tasks.json")
	lockPath := filepath.Join(m.workspaceRoot, ".wuu", "scheduled_tasks.lock")
	schedStore := cron.NewTaskStore(schedPath)
	sessionStore := cron.NewSessionTaskStore(m.workspaceRoot)
	m.cronFireCh = make(chan cron.Task, 8)
	m.schedulerLock = cron.NewLock(lockPath, m.sessionID)
	m.scheduler = cron.NewScheduler(cron.SchedulerConfig{
		Store:        schedStore,
		SessionStore: sessionStore,
		OnFire: func(task cron.Task) {
			select {
			case m.cronFireCh <- task:
			default:
			}
		},
		IsOwner: func() bool {
			ok, _ := m.schedulerLock.TryAcquire()
			return ok
		},
	})
	m.scheduler.Start()

	return m
}

// Init starts the clock ticker.
// dispatchSessionEnd fires the SessionEnd hook with a short timeout.
func (m Model) dispatchSessionEnd() {
	if m.hookDispatcher == nil || !m.hookDispatcher.HasHooks(hooks.SessionEnd) {
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	m.hookDispatcher.Dispatch(ctx, hooks.SessionEnd, &hooks.Input{
		SessionID: m.sessionID,
		CWD:       m.workspaceRoot,
	})
}

func (m Model) Init() tea.Cmd {
	// Dispatch SessionStart hook (fire-and-forget).
	if m.hookDispatcher != nil && m.hookDispatcher.HasHooks(hooks.SessionStart) {
		go m.hookDispatcher.Dispatch(context.Background(), hooks.SessionStart, &hooks.Input{
			SessionID: m.sessionID,
			CWD:       m.workspaceRoot,
		})
	}
	cmds := []tea.Cmd{tickCmd(), statusAnimationCmd()}
	if m.workerNotifyCh != nil {
		cmds = append(cmds, waitWorkerNotify(m.workerNotifyCh))
	}
	if m.askBridge != nil {
		cmds = append(cmds, waitAskRequest(m.askBridge.Requests()))
	}
	if m.processManager != nil {
		cmds = append(cmds, processPollCmd())
		cmds = append(cmds, waitProcessNotify(m.processNotifyCh))
	}
	if m.cronFireCh != nil {
		cmds = append(cmds, waitCronFire(m.cronFireCh))
	}
	return tea.Batch(cmds...)
}

// waitWorkerNotify reads one notification from the worker channel and
// turns it into a workerNotifyMsg.
func waitWorkerNotify(ch <-chan subagent.Notification) tea.Cmd {
	return func() tea.Msg {
		n, ok := <-ch
		if !ok {
			return nil
		}
		return workerNotifyMsg{notification: n}
	}
}

func tickCmd() tea.Cmd {
	return tea.Tick(time.Second, func(t time.Time) tea.Msg {
		return tickMsg{now: t}
	})
}

func statusAnimationCmd() tea.Cmd {
	return tea.Tick(statusAnimationInterval, func(_ time.Time) tea.Msg {
		return inlineSpinMsg{}
	})
}

func inlineSpinTickCmd() tea.Cmd {
	return tea.Tick(statusAnimationInterval, func(_ time.Time) tea.Msg {
		return inlineSpinMsg{}
	})
}

func waitProcessNotify(ch <-chan processruntime.Event) tea.Cmd {
	if ch == nil {
		return nil
	}
	return func() tea.Msg {
		event, ok := <-ch
		if !ok {
			return nil
		}
		return processNotifyMsg{event: event}
	}
}

func processPollCmd() tea.Cmd {
	return tea.Tick(time.Second, func(_ time.Time) tea.Msg {
		return processPollMsg{}
	})
}

// selectionAutoScrollInterval is the cadence of the auto-scroll tick
// fired while a drag-select is held outside the chat viewport. Fast
// enough to feel responsive but slow enough that the per-tick line
// jump (capped by selectionAutoScrollMaxSpeed) gives the user time
// to release before overshooting. ~25 lines/second at 1 line/tick.
const selectionAutoScrollInterval = 40 * time.Millisecond

// selectionAutoScrollMaxSpeed caps how many content lines a single
// tick may advance, so even an extreme drag (mouse parked far below
// the terminal) doesn't blast through hundreds of lines instantly.
const selectionAutoScrollMaxSpeed = 5

func selectionAutoScrollCmd(seq int) tea.Cmd {
	return tea.Tick(selectionAutoScrollInterval, func(_ time.Time) tea.Msg {
		return selectionAutoScrollMsg{seq: seq}
	})
}

// applyResume loads the chosen session into the current Model, replacing
// current entries and chat history. Used by both the picker and direct
// /resume <id> invocation.
func (m Model) applyResume(id string) (tea.Model, tea.Cmd) {
	if m.sessionDir == "" {
		m.statusLine = "resume: no session directory configured"
		return m, nil
	}
	path, err := session.Load(m.sessionDir, id)
	if err != nil {
		m.statusLine = fmt.Sprintf("resume: %v", err)
		m.refreshViewport(false)
		return m, nil
	}
	// Repair the persisted history before rebuilding transcript UI.
	chatMsgs, chatErr := loadChatHistory(path)
	entries, err := loadMemoryEntries(path)
	if err != nil {
		m.statusLine = fmt.Sprintf("resume: failed to load: %v", err)
		m.refreshViewport(false)
		return m, nil
	}
	m.sessionID = id
	m.memoryPath = path
	m.entries = entries
	m.workerUsageByID = make(map[string]workerUsageSnapshot)
	m.workerSpawnedByID = make(map[string]bool)
	m.mainInputTokens = 0
	m.mainOutputTokens = 0
	m.workerInputTokens = 0
	m.workerOutputTokens = 0
	m.loadPersistedTokenUsage()
	m.cacheRenderedEntries()

	// Reload chat history for API calls.
	if chatErr == nil && len(chatMsgs) > 0 {
		if len(m.chatHistory) > 0 && m.chatHistory[0].Role == "system" {
			m.chatHistory = append(m.chatHistory[:1], chatMsgs...)
		} else {
			m.chatHistory = chatMsgs
		}
	}

	if m.onSessionID != nil {
		m.onSessionID(id)
	}
	m.statusLine = fmt.Sprintf("resumed %s (%d entries)", id, len(entries))
	m.refreshViewport(true)
	return m, nil
}

func waitInsightEvent(ch <-chan insight.ProgressEvent) tea.Cmd {
	return func() tea.Msg {
		event, ok := <-ch
		if !ok {
			return insightFinishedMsg{}
		}
		return insightProgressMsg{event: event}
	}
}

func waitCronFire(ch <-chan cron.Task) tea.Cmd {
	return func() tea.Msg {
		return cronFireMsg{task: <-ch}
	}
}

// progressBar renders a text progress bar like [████░░░░░░] 45%
func progressBar(pct float64, width int) string {
	if pct < 0 {
		pct = 0
	}
	if pct > 1 {
		pct = 1
	}
	filled := int(pct * float64(width))
	if filled > width {
		filled = width
	}
	empty := width - filled
	return fmt.Sprintf("[%s%s] %2d%%",
		strings.Repeat("█", filled),
		strings.Repeat("░", empty),
		int(pct*100))
}

// Update handles events.
func (m Model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	// Resume picker takes over when active.
	if m.resumePicker != nil {
		// Always forward WindowSizeMsg so the picker can re-layout.
		switch msg.(type) {
		case tea.WindowSizeMsg, tea.KeyMsg, tea.MouseMsg:
			updated, cmd := m.resumePicker.Update(msg)
			m.resumePicker = updated
			if updated.cancel {
				m.resumePicker = nil
				m.statusLine = "resume cancelled"
				m.refreshViewport(false)
				return m, nil
			}
			if updated.chosenID != "" {
				id := updated.chosenID
				m.resumePicker = nil
				return m.applyResume(id)
			}
			return m, cmd
		}
	}

	// ask_user modal takes over keyboard + window events. Other
	// events (stream, tick, worker notify) still fall through to the
	// normal switch below — the agent goroutine is blocked inside
	// the tool call but other background channels keep flowing.
	if m.activeAsk != nil {
		switch msg.(type) {
		case tea.WindowSizeMsg, tea.KeyMsg:
			updated, cmd := m.activeAsk.Update(msg)
			m.activeAsk = updated
			if updated.done || updated.cancelled {
				// Deliver the response back to the bridge so the
				// blocked tool call unblocks. The respCh is
				// buffered so this never blocks.
				resp := updated.Response()
				updated.pending.respCh <- askBridgeResponse{resp: resp}
				m.activeAsk = nil
				if resp.Cancelled {
					m.statusLine = "ask_user cancelled"
				} else {
					m.statusLine = "ask_user answered"
				}
				m.refreshViewport(false)
				return m, cmd
			}
			return m, cmd
		}
	}

	// askRequestMsg is delivered when the agent calls ask_user. Spin
	// up the modal and immediately re-issue waitAskRequest so the
	// bridge keeps listening for the next call.
	if req, ok := msg.(askRequestMsg); ok {
		if m.activeAsk == nil {
			m.activeAsk = newAskUserModal(req.pending, m.width, m.height)
			m.statusLine = "waiting for your answer"
		} else {
			// Should not happen — bridge channel is buffer 1 and the
			// tool blocks until the previous modal closes — but be
			// defensive and reject the second call cleanly.
			req.pending.respCh <- askBridgeResponse{
				err: errAskUserBusy,
			}
		}
		return m, waitAskRequest(m.askBridge.Requests())
	}

	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		m.width = msg.Width
		m.height = msg.Height
		m.relayout()
		if m.resumePicker != nil {
			m.resumePicker.width = m.width
			m.resumePicker.height = m.height
		}
		return m, nil

	case tea.FocusMsg:
		m.focusInput()
		return m, nil

	case tea.BlurMsg:
		m.blurInput()
		return m, nil

	case tickMsg:
		m.clock = msg.now.Format("15:04:05")
		// Only refresh the viewport when the thinking-block spinner
		// (which lives inside the viewport) needs to advance and is
		// actually visible. Off-screen thinking updates are deferred
		// until the user scrolls back to them.
		// Everything else that ticks — header clock, inline status,
		// worker panel elapsed/spinner — renders in View() outside
		// the viewport, so the frame increment is enough for
		// BubbleTea to re-call View() and pick up the new frame.
		// Worker panel height changes are handled by workerNotifyMsg.
		if m.currentWorkStatus().Phase == workPhaseThinking {
			m.refreshViewportForEntry(m.streamTarget, false)
		}
		return m, tickCmd()

	case inlineSpinMsg:
		m.drainQueuedStreamEvents(interactiveStreamDrainLimit)
		m.spinnerFrame = nextStatusFrame(m.spinnerFrame)
		if m.streaming || m.pendingRequest || m.currentWorkStatus().Phase != workPhaseIdle || len(m.activeWorkerSnapshots()) > 0 || len(m.visibleProcesses()) > 0 {
			// Flush accumulated stream content to viewport on the tick
			// boundary (100ms). Content deltas accumulate without
			// refreshing — this tick is the render heartbeat, aligned
			// with Codex's 80ms commit tick pattern.
			if m.streamCollector != nil && m.streamCollector.Dirty() && m.streamTarget >= 0 && m.streamTarget < len(m.entries) {
				if newLines := m.streamCollector.Commit(); len(newLines) > 0 {
					e := &m.entries[m.streamTarget]
					e.renderedLines = append(e.renderedLines, newLines...)
					e.rendered = strings.Join(e.renderedLines, "\n")
				}
				m.refreshViewportForEntry(m.streamTarget, false)
			} else if m.streaming && m.streamTarget >= 0 && m.streamTarget < len(m.entries) {
				// Refresh for tool status changes even when no text delta.
				m.refreshViewportForEntry(m.streamTarget, false)
			}
			return m, statusAnimationCmd()
		}
		return m, statusAnimationCmd()

	case processPollMsg:
		m.relayout()
		return m, processPollCmd()

	case processNotifyMsg:
		m.recordProcessEvent(msg.event)
		m.relayout()
		m.refreshViewport(false)
		return m, waitProcessNotify(m.processNotifyCh)

	case selectionAutoScrollMsg:
		// Stale ticks (left over from a previous burst that has
		// since stopped or restarted) self-discard via seq mismatch.
		if !m.selectionAutoScroll.active ||
			msg.seq != m.selectionAutoScroll.seq ||
			!m.selection.IsDragging {
			return m, nil
		}
		cmd := m.tickSelectionAutoScroll()
		return m, cmd

	case insightProgressMsg:
		switch msg.event.Phase {
		case "done":
			m.insightRunning = false
			// Replace the progress entry with the final report.
			if m.insightProgressIdx >= 0 && m.insightProgressIdx < len(m.entries) {
				m.entries[m.insightProgressIdx].Content = insight.FormatReport(msg.event.Report)
				m.entries[m.insightProgressIdx].textSegmentOffsets = nil
				m.entries[m.insightProgressIdx].blockOrder = nil
				m.entries[m.insightProgressIdx].rendered = ""
				m.entries[m.insightProgressIdx].composited = ""
			} else if msg.event.Report != nil {
				m.appendEntry("assistant", insight.FormatReport(msg.event.Report))
			}
			m.insightProgressIdx = -1
			m.statusLine = "ready"
			m.refreshViewport(true)
			return m, nil
		case "error":
			m.insightRunning = false
			if m.insightProgressIdx >= 0 && m.insightProgressIdx < len(m.entries) {
				m.entries[m.insightProgressIdx].Content += fmt.Sprintf("\n\n**Error:** %v", msg.event.Err)
			} else {
				m.appendEntry("system", fmt.Sprintf("insight failed: %v", msg.event.Err))
			}
			m.insightProgressIdx = -1
			m.statusLine = "ready"
			m.refreshViewport(true)
			return m, nil
		default:
			// Update the live progress entry in the chat.
			pctBar := progressBar(msg.event.Pct, 20)
			line := fmt.Sprintf("%s %s", pctBar, msg.event.Detail)
			if m.insightProgressIdx < 0 {
				m.insightProgressIdx = m.appendEntry("assistant", line)
			} else if m.insightProgressIdx < len(m.entries) {
				m.entries[m.insightProgressIdx].Content += "\n" + line
				m.entries[m.insightProgressIdx].rendered = ""
			}
			m.statusLine = fmt.Sprintf("insight: %s", msg.event.Detail)
			m.refreshViewport(true)
			return m, waitInsightEvent(m.insightCh)
		}

	case insightFinishedMsg:
		m.insightRunning = false
		if m.insightProgressIdx >= 0 && m.insightProgressIdx < len(m.entries) {
			m.entries[m.insightProgressIdx].Content += "\n\n_Insight generation ended._"
		}
		m.insightProgressIdx = -1
		m.statusLine = "ready"
		m.refreshViewport(true)
		return m, nil

	case workerNotifyMsg:
		// Worker status changed. Show transient progress in chat for
		// "running" / "completed" / "failed". When completed, also
		// inject the worker-result XML into chatHistory so the
		// orchestrator sees it on its next turn.
		n := msg.notification
		m.recordWorkerUsage(n.Snapshot)
		injected := false
		switch n.Status {
		case subagent.StatusRunning:
			if !m.hasWorkerSpawned(n.Snapshot.ID) {
				m.appendEntry("system", fmt.Sprintf("⠋ %s spawned: %s — %s",
					n.Snapshot.Type, n.Snapshot.ID, n.Snapshot.Description))
				m.markWorkerSpawned(n.Snapshot.ID)
			}
			m.relayout()
		case subagent.StatusCompleted, subagent.StatusFailed, subagent.StatusCancelled:
			icon := "✓"
			suffix := ""
			if n.Status == subagent.StatusFailed {
				icon = "✗"
				// Surface the actual error so the user can tell apart
				// auth / rate limit / context overflow / fatal at a
				// glance instead of guessing. The full error string is
				// also in the <worker-result> XML the orchestrator sees.
				if n.Snapshot.Error != nil {
					class := coordinator.ClassifyError(n.Snapshot.Error)
					suffix = fmt.Sprintf(" — [%s] %s", class,
						trimWorkerErrMsg(n.Snapshot.Error.Error(), 240))
				}
			} else if n.Status == subagent.StatusCancelled {
				icon = "⊘"
			}
			m.appendEntry("system", fmt.Sprintf("%s %s %s: %s%s",
				icon, n.Snapshot.Type, n.Status, n.Snapshot.Description, suffix))
			// Inject the worker-result XML into the orchestrator's
			// next API request as a user-role message. If a turn is
			// still in flight, buffer it until streamFinishedMsg so
			// it cannot interleave with assistant/tool messages from
			// the active turn.
			xml := coordinator.FormatWorkerResult(n.Snapshot)
			workerMsg := providers.ChatMessage{
				Role:    "user",
				Content: xml,
			}
			if m.streaming || m.pendingRequest {
				m.pendingWorkerResults = append(m.pendingWorkerResults, workerMsg)
			} else {
				m.chatHistory = append(m.chatHistory, workerMsg)
				_ = appendChatMessage(m.memoryPath, workerMsg)
			}
			injected = true
		}
		// Worker count likely changed — re-layout so the activity
		// panel appears/disappears immediately.
		m.relayout()
		m.refreshViewport(false)

		// If we injected a result and the main agent is idle, fire an
		// auto-resume turn so the orchestrator processes the new
		// information without waiting for user input. If the main
		// agent is currently busy, set a flag and let the
		// streamFinishedMsg handler pick it up after the current turn.
		if injected {
			if m.streaming || m.pendingRequest {
				m.pendingAutoResume = true
				return m, waitWorkerNotify(m.workerNotifyCh)
			}
			updated, cmd := m.triggerAutoResume()
			return updated, tea.Batch(waitWorkerNotify(m.workerNotifyCh), cmd)
		}
		return m, waitWorkerNotify(m.workerNotifyCh)

	case streamFinishedMsg:
		// Runner goroutine completed (channel closed).
		finishedEntry := m.streamTarget
		m.streaming = false
		m.pendingRequest = false
		// Clear the composited cache for the finished entry so the
		// next render reflects the final state without streaming artifacts.
		// Without this, OffscreenFreeze skips re-rendering because
		// compositedH > 0, leaving the cursor artifact visible.
		if finishedEntry >= 0 && finishedEntry < len(m.entries) {
			m.entries[finishedEntry].composited = ""
			m.entries[finishedEntry].compositedH = 0
		}
		m.streamTarget = -1
		m.thinkingStart = time.Time{}
		if m.streamCollector != nil {
			m.streamCollector = nil
		}
		m.clearLiveWorkStatus()
		m.statusLine = "ready"
		m.cacheRenderedEntries()

		// Merge turn result into chatHistory and persist.
		if m.pendingTurn != nil {
			rewriteHistory := false
			switch {
			case m.pendingTurn.historyRewritten:
				base := make([]providers.ChatMessage, len(m.pendingTurn.newMsgs))
				copy(base, m.pendingTurn.newMsgs)
				m.chatHistory = base
				rewriteHistory = true
			default:
				if !m.pendingTurn.incrementalPersisted {
					for _, msg := range m.pendingTurn.newMsgs {
						m.chatHistory = append(m.chatHistory, msg)
						_ = appendChatMessage(m.memoryPath, msg)
					}
				}
			}
			if rewriteHistory {
				if err := rewriteChatHistory(m.memoryPath, m.chatHistory); err != nil {
					m.statusLine = fmt.Sprintf("session write failed: %v", err)
				}
			}
			m.pendingTurn = nil
		}
		for _, msg := range m.pendingWorkerResults {
			m.chatHistory = append(m.chatHistory, msg)
			_ = appendChatMessage(m.memoryPath, msg)
		}
		m.pendingWorkerResults = nil

		// Persist token usage for this turn.
		if m.turnInputTokens > 0 || m.turnOutputTokens > 0 {
			_ = appendTokenUsage(m.memoryPath, m.turnInputTokens, m.turnOutputTokens)
		}
		m.turnInputTokens = 0
		m.turnOutputTokens = 0

		// Update session index with current entries count so the resume
		// picker shows the correct message count instead of 0.
		if m.sessionDir != "" && m.sessionID != "" {
			summary := firstUserSummary(m.entries)
			session.UpdateIndex(m.sessionDir, m.sessionID, len(m.entries), summary)
		}

		// Dispatch Stop hook (fire-and-forget).
		if m.hookDispatcher != nil && m.hookDispatcher.HasHooks(hooks.Stop) {
			go m.hookDispatcher.Dispatch(context.Background(), hooks.Stop, &hooks.Input{
				SessionID: m.sessionID,
				CWD:       m.workspaceRoot,
			})
		}

		m.refreshViewportForEntry(finishedEntry, false)

		// If a worker completed while this turn was running, fire an
		// auto-resume now so the orchestrator processes the queued
		// worker-result(s).
		if m.pendingAutoResume {
			m.pendingAutoResume = false
			updated, autoCmd := m.triggerAutoResume()
			return updated, tea.Batch(func() tea.Msg { return queueDrainMsg{} }, autoCmd)
		}

		return m, func() tea.Msg { return queueDrainMsg{} }

	case ctrlCResetMsg:
		m.ctrlCPressed = false
		if m.statusLine == "press ctrl+c again to exit" {
			m.statusLine = "ready"
		}
		return m, nil

	case queueDrainMsg:
		return m.drainQueue()

	case cronFireMsg:
		m.appendEntry("system", fmt.Sprintf("Running scheduled task (%s)", time.Now().Format("Jan 2 3:04pm")))
		m.messageQueue = append(m.messageQueue, queuedMessage{
			Text:            msg.task.Prompt,
			ScheduledTaskID: msg.task.ID,
		})
		return m, waitCronFire(m.cronFireCh)

	case streamEventMsg:
		return m, m.applyStreamEvent(msg.event, true)

	case tea.MouseMsg:
		// Mouse wheel must be applied BEFORE draining queued stream
		// events. Otherwise, when autoFollow is true and new content
		// is waiting in the channel, the drain calls refreshViewport
		// → GotoBottom over the newly-grown content, and the wheel
		// delta is then applied from that lower offset — so a wheel-up
		// can end up moving the viewport DOWN. Applying the wheel
		// first updates autoFollow via syncViewportState, so the
		// subsequent drain preserves the user's scroll position.
		if msg.Button == tea.MouseButtonWheelUp || msg.Button == tea.MouseButtonWheelDown {
			if m.isInChatArea(msg.X, msg.Y) || m.isInInlineStatusArea(msg.X, msg.Y) {
				var cmd tea.Cmd
				m.viewport, cmd = m.viewport.Update(msg)
				m.syncViewportState()
				m.drainQueuedStreamEvents(interactiveStreamDrainLimit)
				return m, cmd
			}
			// Wheel outside the chat area (e.g. input) — swallow,
			// but still drain so rendering stays responsive.
			m.drainQueuedStreamEvents(interactiveStreamDrainLimit)
			return m, nil
		}

		m.drainQueuedStreamEvents(interactiveStreamDrainLimit)

		if msg.Action == tea.MouseActionRelease {
			if m.selection.IsDragging {
				m.stopSelectionAutoScroll()
				m.selection.finish()
				if m.selection.hasSelection() {
					m.copySelectionToClipboard()
				}
				return m, nil
			}
			if m.inputSelection.IsDragging {
				m.inputSelection.finish()
				if m.inputSelection.hasSelection() {
					m.copyInputSelectionToClipboard()
				}
				return m, nil
			}
			if m.pendingInputClick.active {
				// No drag happened — position cursor at click point.
				const promptW = 2
				targetRow := m.pendingInputClick.y - m.layout.Input.Y
				targetCol := m.pendingInputClick.x - m.layout.Input.X - promptW
				if targetCol < 0 {
					targetCol = 0
				}
				m.pendingInputClick = pendingChatClickState{}
				currentRow := m.input.Line()
				for currentRow < targetRow && currentRow < m.input.LineCount()-1 {
					m.input.CursorDown()
					currentRow++
				}
				for currentRow > targetRow && currentRow > 0 {
					m.input.CursorUp()
					currentRow--
				}
				m.input.SetCursor(targetCol)
				return m, nil
			}
			if m.pendingChatClick.active {
				m.focusInput()
				m.selection.clear()
				return m, nil
			}
		}

		// Input area: pending click → drag threshold → start input selection.
		if msg.Action == tea.MouseActionMotion && m.pendingInputClick.active {
			if exceedsChatSelectionDragThreshold(m.pendingInputClick.x, m.pendingInputClick.y, msg.X, msg.Y) {
				startRow, startCol := m.screenToInputCoords(m.pendingInputClick.x, m.pendingInputClick.y)
				m.pendingInputClick = pendingChatClickState{}
				m.inputSelection.clear()
				m.inputSelection.start(startCol, startRow)
				row, col := m.screenToInputCoords(msg.X, msg.Y)
				m.inputSelection.update(col, row)
			}
			return m, nil
		}

		// Input area: active drag — extend selection.
		if msg.Action == tea.MouseActionMotion && m.inputSelection.IsDragging {
			row, col := m.screenToInputCoords(msg.X, msg.Y)
			m.inputSelection.update(col, row)
			return m, nil
		}

		if msg.Action == tea.MouseActionMotion && m.pendingChatClick.active {
			if exceedsChatSelectionDragThreshold(m.pendingChatClick.x, m.pendingChatClick.y, msg.X, msg.Y) {
				m.blurInput()
				startRow, startCol := m.screenToViewportCoords(m.pendingChatClick.x, m.pendingChatClick.y)
				m.clearPendingChatClick()
				m.stopSelectionAutoScroll()
				m.selection.clear()
				m.selection.start(startCol, startRow)
				vpRow, vpCol := m.screenToViewportCoords(msg.X, msg.Y)
				cmd := m.refreshSelectionAutoScroll(msg.X, msg.Y)
				m.selection.update(vpCol, vpRow)
				return m, cmd
			}
		}

		if msg.Action == tea.MouseActionMotion && m.selection.IsDragging {
			// Auto-scroll bookkeeping: while the cursor is past the
			// chat area's top or bottom edge, kick a recurring tick
			// that keeps scrolling even if the mouse stays still.
			// Without the tick, only motion events advance the
			// selection — meaning a stationary "hold past the edge"
			// would do nothing, which is the surprising behavior
			// the user reported.
			cmd := m.refreshSelectionAutoScroll(msg.X, msg.Y)
			vpRow, vpCol := m.screenToViewportCoords(msg.X, msg.Y)
			m.selection.update(vpCol, vpRow)
			return m, cmd
		}

		if m.showJump &&
			msg.Action == tea.MouseActionPress &&
			msg.Button == tea.MouseButtonLeft &&
			msg.Y == 0 &&
			msg.X >= m.width-20 {
			m.blurInput()
			m.clearPendingChatClick()
			m.selection.clear()
			m.viewport.GotoBottom()
			m.syncViewportState()
			return m, nil
		}

		// Mouse click inside input area — start pending click (may become drag-select).
		if msg.Action == tea.MouseActionPress && msg.Button == tea.MouseButtonLeft {
			inputTop := m.layout.Input.Y
			inputBot := inputTop + m.layout.Input.Height
			inputLeft := m.layout.Input.X

			if msg.Y >= inputTop && msg.Y < inputBot && msg.X >= inputLeft {
				m.focusInput()
				m.selection.clear()
				m.clearPendingChatClick()
				m.inputSelection.clear()
				m.pendingInputClick = pendingChatClickState{active: true, x: msg.X, y: msg.Y}
				return m, nil
			}
		}

		// Start pending click on left-click in viewport area.
		if msg.Action == tea.MouseActionPress && msg.Button == tea.MouseButtonLeft {
			if m.isInChatArea(msg.X, msg.Y) {
				m.clearPendingChatClick()
				m.pendingChatClick = pendingChatClickState{active: true, x: msg.X, y: msg.Y}
				return m, nil
			}
		}

	case tea.KeyMsg:
		// Any key clears input selection.
		if m.inputSelection.hasSelection() {
			m.inputSelection.clear()
		}

		// Handle image bar navigation when focused.
		if m.imageBarFocused {
			switch msg.String() {
			case "left":
				if m.selectedImageIdx > 0 {
					m.selectedImageIdx--
				}
				return m, nil
			case "right":
				if m.selectedImageIdx < len(m.pendingImages)-1 {
					m.selectedImageIdx++
				}
				return m, nil
			case "backspace", "delete":
				if len(m.pendingImages) > 0 && m.selectedImageIdx < len(m.pendingImages) {
					m.pendingImages = append(m.pendingImages[:m.selectedImageIdx], m.pendingImages[m.selectedImageIdx+1:]...)
					if len(m.pendingImages) == 0 {
						m.imageBarFocused = false
						m.selectedImageIdx = 0
					} else if m.selectedImageIdx >= len(m.pendingImages) {
						m.selectedImageIdx = len(m.pendingImages) - 1
					}
					m.relayout()
				}
				return m, nil
			case "esc", "up":
				m.imageBarFocused = false
				return m, nil
			}
			// Ignore other keys while image bar is focused.
			return m, nil
		}

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
			case "tab":
				if m.completionIndex >= 0 && m.completionIndex < len(m.completionItems) {
					selected := m.completionItems[m.completionIndex]
					m.input.SetValue("/" + selected.Name + " ")
					m.input.CursorEnd()
					m.completionVisible = false
					m.completionItems = nil
					return m, nil
				}
			case "enter":
				if m.completionIndex >= 0 && m.completionIndex < len(m.completionItems) {
					selected := m.completionItems[m.completionIndex]
					m.input.SetValue("/" + selected.Name + " ")
					m.input.CursorEnd()
					m.completionVisible = false
					m.completionItems = nil
					if selected.completionEnterBehavior() == slashCompletionExecute {
						return m.submit(false)
					}
					return m, nil
				}
			case "esc":
				m.completionVisible = false
				m.completionItems = nil
				return m, nil
			}
		}

		// Clear text selection on Escape.
		if msg.String() == "esc" && m.selection.hasSelection() {
			m.selection.clear()
			return m, nil
		}

		// Escape interrupts a running stream.
		if msg.String() == "esc" && m.streaming {
			if m.cancelStream != nil {
				m.cancelStream()
			}
			m.statusLine = "interrupted"
			return m, nil
		}

		// Escape clears the input when there is text.
		if msg.String() == "esc" && m.input.Value() != "" {
			m.input.SetValue("")
			m.pendingImages = nil
			m.imageBarFocused = false
			m.selectedImageIdx = 0
			m.historyIndex = -1
			m.historyDraft = ""
			m.relayout()
			return m, nil
		}

		// ── Search mode keyboard handling ──
		// When search is active, keyboard input routes to search instead
		// of the input textarea. This follows Claude Code's overlay pattern
		// where search is a modal-like overlay above the transcript.
		if m.search.Active {
			switch msg.String() {
			case "esc":
				m.search.clear()
				m.statusLine = "search cancelled"
				return m, nil
			case "enter", "n":
				m.search.next()
				if m.search.hasMatches() {
					m.statusLine = fmt.Sprintf("search: %d/%d", m.search.CurrentIdx+1, len(m.search.Matches))
				} else {
					m.statusLine = fmt.Sprintf("search: no matches for %q", m.search.Query)
				}
				return m, nil
			case "N":
				m.search.prev()
				if m.search.hasMatches() {
					m.statusLine = fmt.Sprintf("search: %d/%d", m.search.CurrentIdx+1, len(m.search.Matches))
				}
				return m, nil
			case "backspace":
				if len(m.search.Query) > 0 {
					m.search.Query = m.search.Query[:len(m.search.Query)-1]
					m.search.Matches = searchInContent(m.renderedContent, m.search.Query, m.search.CaseSensitive)
					m.search.CurrentIdx = 0
					if m.search.hasMatches() {
						m.statusLine = fmt.Sprintf("search: %d matches for %q", len(m.search.Matches), m.search.Query)
					} else {
						m.statusLine = fmt.Sprintf("search: no matches for %q", m.search.Query)
					}
				}
				return m, nil
			case "ctrl+c":
				m.search.clear()
				m.statusLine = "search cancelled"
				return m, nil
			default:
				// Append typed character to search query.
				if len(msg.String()) == 1 {
					m.search.Query += msg.String()
					m.search.Matches = searchInContent(m.renderedContent, m.search.Query, m.search.CaseSensitive)
					m.search.CurrentIdx = 0
					if m.search.hasMatches() {
						m.statusLine = fmt.Sprintf("search: %d matches for %q", len(m.search.Matches), m.search.Query)
					} else {
						m.statusLine = fmt.Sprintf("search: no matches for %q", m.search.Query)
					}
					return m, nil
				}
			}
		}

		switch msg.String() {
		case "ctrl+f":
			m.search.Active = true
			m.search.Query = ""
			m.search.Matches = nil
			m.search.CurrentIdx = 0
			m.statusLine = "search: type to search"
			return m, nil
		case "ctrl+c":
			// If insight is running, first ctrl+c cancels it instead of quitting.
			if m.insightRunning && m.cancelInsight != nil {
				m.cancelInsight()
				m.insightRunning = false
				if m.insightProgressIdx >= 0 && m.insightProgressIdx < len(m.entries) {
					m.entries[m.insightProgressIdx].Content += "\n\n**Cancelled** by user."
				}
				m.insightProgressIdx = -1
				m.statusLine = "insight cancelled"
				m.refreshViewport(true)
				return m, nil
			}
			// If any sub-agents are running, first ctrl+c stops all of
			// them AND cancels the main agent's current streaming
			// turn. Without cancelling the main turn, the orchestrator
			// would keep iterating its tool loop (potentially spawning
			// more workers via auto-resume) until it hit max_steps.
			if m.coordinator != nil && m.coordinator.Manager().CountRunning() > 0 {
				count := m.coordinator.Manager().CountRunning()
				m.coordinator.StopAll()
				m.pendingAutoResume = false
				if m.cancelStream != nil {
					m.cancelStream()
				}
				m.appendEntry("system", fmt.Sprintf("⊘ Stopped %d running sub-agent(s) and cancelled main turn", count))
				m.statusLine = "sub-agents cancelled"
				m.refreshViewport(true)
				return m, nil
			}
			if m.ctrlCPressed {
				if m.cancelStream != nil {
					m.cancelStream()
				}
				if m.scheduler != nil {
					m.scheduler.Stop()
				}
				if m.schedulerLock != nil {
					m.schedulerLock.Release()
				}
				m.dispatchSessionEnd()
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
			m.pendingImages = nil
			m.imageBarFocused = false
			m.selectedImageIdx = 0
			m.historyIndex = -1
			m.historyDraft = ""
			m.completionVisible = false
			m.completionItems = nil
			return m, nil
		case "ctrl+v", "alt+v":
			image, err := pasteImageFromClipboard()
			if err != nil {
				m.statusLine = fmt.Sprintf("image paste failed: %v", err)
				return m, nil
			}
			m.pendingImages = append(m.pendingImages, image)
			count := len(m.pendingImages)
			label := "images"
			if count == 1 {
				label = "image"
			}
			m.statusLine = fmt.Sprintf("attached %d %s", count, label)
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
			if len(m.pendingImages) > 0 {
				m.imageBarFocused = true
				m.selectedImageIdx = 0
				return m, nil
			}
		case "ctrl+j", "end":
			m.viewport.GotoBottom()
			m.syncViewportState()
			return m, nil
		case "pgup":
			m.viewport.ViewUp()
			m.syncViewportState()
			return m, nil
		case "pgdown":
			m.viewport.ViewDown()
			m.syncViewportState()
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
		case "o":
			if m.toggleLastToolBurstGroup() {
				m.refreshViewport(false)
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
	m.syncViewportState()

	return m, tea.Batch(cmds...)
}

func (m Model) submit(shouldQueue bool) (tea.Model, tea.Cmd) {
	raw := strings.TrimSpace(m.input.Value())
	hasImages := len(m.pendingImages) > 0
	if raw == "" && !hasImages {
		return m, nil
	}

	// Skill shorthand: /<skill-name> [args] expands to the skill body
	// (with variable substitution) and is sent as a user message.
	if expanded, ok := m.expandSkillShorthand(raw); ok {
		raw = expanded
		m.input.SetValue(raw)
	}

	if raw != "" {
		if output, handled := m.handleSlash(raw); handled {
			if output == "__exit__" {
				if m.cancelStream != nil {
					m.cancelStream()
				}
				if m.scheduler != nil {
					m.scheduler.Stop()
				}
				if m.schedulerLock != nil {
					m.schedulerLock.Release()
				}
				m.dispatchSessionEnd()
				m.quitting = true
				return m, tea.Quit
			}
			m.appendEntry("system", output)
			m.input.Reset()
			m.statusLine = "command executed"
			m.refreshViewport(true)
			// If insight was launched, start listening for progress events.
			if m.insightRunning && m.insightCh != nil {
				return m, waitInsightEvent(m.insightCh)
			}
			// If a slash command queued messages (e.g. /loop), drain them.
			if len(m.messageQueue) > 0 || len(m.pendingSteers) > 0 {
				return m.drainQueue()
			}
			return m, nil
		}
	}

	// Record in input history (skip duplicates).
	if raw != "" && (len(m.inputHistory) == 0 || m.inputHistory[len(m.inputHistory)-1] != raw) {
		m.inputHistory = append(m.inputHistory, raw)
	}
	m.historyIndex = -1
	m.historyDraft = ""
	m.input.Reset()

	message := queuedMessage{
		Text:   raw,
		Images: append([]providers.InputImage(nil), m.pendingImages...),
	}
	m.pendingImages = nil
	m.imageBarFocused = false
	m.selectedImageIdx = 0

	if m.pendingRequest && shouldQueue {
		// Tab while busy — queue the message without hiding the active
		// inline waiting status for the current turn.
		m.messageQueue = append(m.messageQueue, message)
		if !m.streaming && !isWaitingStatus(m.statusLine) {
			m.statusLine = fmt.Sprintf("queued (%d pending)", len(m.messageQueue))
		}
		return m, nil
	}

	if m.pendingRequest {
		// Enter while busy — treat as steer and prioritize over Tab queue.
		m.pendingSteers = append(m.pendingSteers, message)
		if m.cancelStream != nil {
			m.cancelStream()
			m.statusLine = fmt.Sprintf("steering (%d pending)", len(m.pendingSteers))
		} else {
			m.statusLine = fmt.Sprintf("steer queued (%d pending)", len(m.pendingSteers))
		}
		return m, nil
	}

	// If idle, both Tab and Enter send directly.
	return m.sendMessage(message)
}

func (m Model) sendMessage(message queuedMessage) (tea.Model, tea.Cmd) {
	// Dispatch UserPromptSubmit hook — may block the prompt.
	if m.hookDispatcher != nil && m.hookDispatcher.HasHooks(hooks.UserPromptSubmit) {
		out, err := m.hookDispatcher.Dispatch(context.Background(), hooks.UserPromptSubmit, &hooks.Input{
			SessionID: m.sessionID,
			CWD:       m.workspaceRoot,
			Prompt:    message.Text,
		})
		if hooks.IsBlocked(err) {
			reason := "blocked by hook"
			if out != nil && out.Reason != "" {
				reason = out.Reason
			}
			m.statusLine = fmt.Sprintf("prompt blocked: %s", reason)
			return m, nil
		}
	}

	userDisplay := formatUserEntryContent(message.Text, len(message.Images))
	m.appendEntry("user", userDisplay)
	chatMsg := providers.ChatMessage{
		Role:    "user",
		Content: message.Text,
		Images:  append([]providers.InputImage(nil), message.Images...),
	}
	m.chatHistory = append(m.chatHistory, chatMsg)
	_ = appendChatMessage(m.memoryPath, chatMsg)

	// Real user input resets the auto-resume chain counter.
	m.autoResumeChain = 0

	m.pendingRequest = true
	m.streaming = true
	m.streamTarget = -1
	queueHint := ""
	if len(m.messageQueue) > 0 {
		queueHint = fmt.Sprintf(" · %d queued", len(m.messageQueue))
	}

	if m.streamRunner != nil {
		m.streamTarget = m.appendEntry("assistant", "")
		m.setLiveWorkStatus(workStatus{Phase: workPhaseGenerating, Label: "Responding", Meta: "Writing the reply", Running: true})
		m.statusLine = "streaming" + queueHint
		m.refreshViewport(true)
		return m.startStreamingTurn()
	}
	m.refreshViewport(true)
	return m.startStreamingTurn()
}

// startStreamingTurn launches a streaming runner using the current
// chatHistory. Caller must already have set pendingRequest/streaming
// and refreshed the viewport. streamTarget may stay unset until the
// first stream event when a pre-stream compaction pass is running.
func (m Model) startStreamingTurn() (tea.Model, tea.Cmd) {
	ch := make(chan providers.StreamEvent, 64)
	m.streamCh = ch
	runner := m.streamRunner
	ctx, cancel := m.newRequestContext()
	m.cancelStream = cancel

	// Copy history for the goroutine (defensive copy).
	history := make([]providers.ChatMessage, len(m.chatHistory))
	copy(history, m.chatHistory)

	result := &pendingTurnResult{}
	m.pendingTurn = result

	go func() {
		defer close(ch)
		onEvent := func(event providers.StreamEvent) {
			select {
			case ch <- event:
			case <-ctx.Done():
			}
		}

		res, err := runner.RunWithCallback(ctx, history, onEvent)
		result.newMsgs = res.NewMessages
		result.historyRewritten = res.HistoryRewritten
		if err != nil && !errors.Is(ctx.Err(), context.Canceled) {
			select {
			case ch <- providers.StreamEvent{Type: providers.EventError, Error: err}:
			case <-ctx.Done():
			}
		}
	}()
	return m, waitStreamEvent(ch)
}

// triggerAutoResume fires a fresh turn from the current chatHistory
// without appending any new user message. Used when a worker completes
// while the main agent is idle. Returns the model and the started
// command, or (m, nil) if the safety limit has been reached.
func (m Model) triggerAutoResume() (tea.Model, tea.Cmd) {
	if m.streamRunner == nil {
		return m, nil
	}
	if m.autoResumeChain >= maxAutoResumeChain {
		m.appendEntry("system", fmt.Sprintf("auto-resume limit reached (%d). Type a message to continue.", maxAutoResumeChain))
		m.refreshViewport(true)
		return m, nil
	}
	m.autoResumeChain++
	m.pendingRequest = true
	m.streaming = true
	m.streamTarget = m.appendEntry("assistant", "")
	m.setLiveWorkStatus(workStatus{
		Phase:   workPhaseAutoResume,
		Label:   "Continuing",
		Meta:    fmt.Sprintf("Picking up after worker updates (%d/%d)", m.autoResumeChain, maxAutoResumeChain),
		Running: true,
	})
	m.statusLine = fmt.Sprintf("auto-resume (%d/%d)", m.autoResumeChain, maxAutoResumeChain)
	m.refreshViewport(true)
	return m.startStreamingTurn()
}

func (m Model) newRequestContext() (context.Context, context.CancelFunc) {
	if m.requestTimeout > 0 {
		return context.WithTimeout(context.Background(), m.requestTimeout)
	}
	return context.WithCancel(context.Background())
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

// drainQueuedStreamEvents opportunistically applies a small batch of
// already-buffered stream events during unrelated UI work such as mouse
// dragging. This keeps live reply rendering moving while the user is
// selecting text instead of letting queued MouseMsg traffic make the
// stream look frozen.
func (m *Model) drainQueuedStreamEvents(limit int) {
	if !m.streaming || m.streamCh == nil || limit <= 0 {
		return
	}
	for i := 0; i < limit; i++ {
		select {
		case event, ok := <-m.streamCh:
			if !ok {
				return
			}
			_ = m.applyStreamEvent(event, false)
			if !m.streaming {
				return
			}
		default:
			return
		}
	}
}

func (m *Model) applyStreamEvent(event providers.StreamEvent, rearm bool) tea.Cmd {
	nextWait := func() tea.Cmd {
		if !rearm || m.streamCh == nil {
			return nil
		}
		return waitStreamEvent(m.streamCh)
	}

	switch event.Type {
	case providers.EventContentDelta:
		if m.streamTarget < 0 || m.streamTarget >= len(m.entries) {
			// New round of streaming — create a fresh assistant entry.
			m.streamTarget = m.appendEntry("assistant", "")
		}
		if m.entries[m.streamTarget].Content == "(empty)" {
			m.entries[m.streamTarget].Content = ""
			m.entries[m.streamTarget].textSegmentOffsets = nil
			m.entries[m.streamTarget].blockOrder = nil
		}
		// When this is the first delta of a fresh round (collector
		// was reset to nil at the previous EventDone), seed the
		// collector with the entry's existing Content. Without
		// this seed, the collector only contains the new round's
		// deltas, and CommitCompleteLines below would overwrite
		// entry.rendered with ONLY the new round — causing the
		// previous rounds' content to vanish from the viewport
		// until the next EventDone fires (visible flashing in
		// coordinator mode where multi-round turns are common).
		if m.streamCollector == nil {
			m.streamCollector = markdown.NewStreamCollector(
				contentWidth(m.viewport.Width),
				markdown.DefaultStyles(),
			)
		}
		e := &m.entries[m.streamTarget]
		if e.streamBuf == nil {
			e.streamBuf = &strings.Builder{}
		}
		e.streamBuf.WriteString(event.Content)
		e.Content = e.streamBuf.String()
		m.streamCollector.Push(event.Content)
		// Record block order: if the last block isn't "text", start a new text segment.
		if len(e.blockOrder) == 0 || e.blockOrder[len(e.blockOrder)-1] != "text" {
			e.blockOrder = append(e.blockOrder, "text")
			// Record the byte offset where this text segment starts
			// so compositeEntry can render each segment independently.
			offset := len(e.Content) - len(event.Content)
			if offset < 0 {
				offset = 0
			}
			e.textSegmentOffsets = append(e.textSegmentOffsets, offset)
		}
		// During streaming: accumulate only, do NOT refresh viewport.
		// The 100ms inlineSpinMsg tick flushes accumulated content to
		// screen in batches — aligned with Codex's 80ms commit tick.
		// Markdown is rendered once at EventDone (Finalize).
		m.setLiveWorkStatus(workStatus{Phase: workPhaseGenerating, Label: "Responding", Meta: "Writing the reply", Running: true})
		m.statusLine = "streaming"
		return nextWait()

	case providers.EventToolUseStart:
		if m.streamTarget < 0 || m.streamTarget >= len(m.entries) {
			m.streamTarget = m.appendEntry("assistant", "")
		}
		toolName := ""
		toolID := ""
		if event.ToolCall != nil {
			toolName = event.ToolCall.Name
			toolID = event.ToolCall.ID
		}
		e := &m.entries[m.streamTarget]
		toolIdx := len(e.ToolCalls)
		e.ToolCalls = append(e.ToolCalls, ToolCallEntry{
			ID:        toolID,
			Name:      toolName,
			Status:    ToolCallRunning,
			Collapsed: shouldCollapseToolBurstByDefault(toolName),
		})
		e.blockOrder = append(e.blockOrder, fmt.Sprintf("tool:%d", toolIdx))
		m.setLiveWorkStatus(runningToolWorkStatus(toolName))
		m.statusLine = fmt.Sprintf("tool: %s", toolName)
		// Don't refresh viewport here — the 100ms tick handles it.
		// Avoids N×refreshViewport for N parallel tool starts.
		return nextWait()

	case providers.EventToolUseEnd:
		if m.streamTarget >= 0 && m.streamTarget < len(m.entries) {
			e := &m.entries[m.streamTarget]
			for i := len(e.ToolCalls) - 1; i >= 0; i-- {
				tc := &e.ToolCalls[i]
				if tc.Status == ToolCallRunning {
					if event.ToolCall != nil {
						tc.Args = event.ToolCall.Arguments
					}
					tc.Result = event.ToolResult
					tc.Status = ToolCallDone
					tc.Collapsed = true
					break
				}
			}
		}
		m.setLiveWorkStatus(workStatus{Phase: workPhaseGenerating, Label: "Responding", Meta: "Writing the reply", Running: true})
		m.statusLine = "streaming"
		// Don't refresh viewport here — the 100ms tick handles it.
		return nextWait()

	case providers.EventDone:
		finishedEntry := m.streamTarget
		// Accumulate token usage from this stream chunk.
		if event.Usage != nil {
			m.turnInputTokens += event.Usage.InputTokens
			m.turnOutputTokens += event.Usage.OutputTokens
			m.mainInputTokens += event.Usage.InputTokens
			m.mainOutputTokens += event.Usage.OutputTokens
		}
		// One SSE stream finished. The runner may continue with tool
		// execution and start another stream, so keep listening.
		if m.streamCollector != nil {
			if finalLines := m.streamCollector.Finalize(); len(finalLines) > 0 {
				if m.streamTarget >= 0 && m.streamTarget < len(m.entries) {
					e := &m.entries[m.streamTarget]
					e.renderedLines = append(e.renderedLines, finalLines...)
					e.rendered = strings.Join(e.renderedLines, "\n")
					e.streamBuf = nil
					e.renderedLines = nil
				}
			}
			m.streamCollector = nil
		}
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
		m.clearLiveWorkStatus()
		m.refreshViewportForEntry(finishedEntry, false)
		return nextWait()

	case providers.EventMessage:
		if event.Message != nil {
			m.persistStreamMessage(*event.Message)
		}
		return nextWait()

	case providers.EventLifecycle:
		if event.Lifecycle != nil {
			switch event.Lifecycle.Phase {
			case providers.StreamPhaseConnecting:
				current := m.currentWorkStatus()
				if event.Lifecycle.Attempt > 1 || current.Phase == workPhaseReconnecting {
					m.setLiveWorkStatus(reconnectWorkStatus(event.Lifecycle))
				} else {
					m.setLiveWorkStatus(workStatus{Phase: workPhaseGenerating, Label: "Connecting", Meta: "Opening the live response", Running: true})
				}
			case providers.StreamPhaseConnected:
				m.setLiveWorkStatus(workStatus{Phase: workPhaseGenerating, Label: "Responding", Meta: "Writing the reply", Running: true})
			case providers.StreamPhaseReconnecting:
				m.setLiveWorkStatus(reconnectWorkStatus(event.Lifecycle))
			case providers.StreamPhaseFailed:
				m.clearLiveWorkStatus()
			}
		}
		return nextWait()

	case providers.EventReconnect:
		msg := strings.TrimSpace(event.Content)
		if msg == "" {
			msg = "Reconnecting..."
		}
		reconnect := m.currentWorkStatus()
		if reconnect.Phase != workPhaseReconnecting {
			reconnect = reconnectWorkStatus(nil)
		}
		reconnect.Label = compactStatusDetail(msg, 32)
		if reconnect.Label == "" {
			reconnect.Label = "Reconnecting"
		}
		m.setLiveWorkStatus(reconnect)
		m.statusLine = msg
		return nextWait()

	case providers.EventCompact:
		// Auto-compact ran inside the loop. Show it as a system
		// line so the user knows their conversation history was
		// summarized — long sessions silently shrinking would be
		// confusing without any signal.
		notice := strings.TrimSpace(event.Content)
		if notice == "" {
			notice = "✦ Compacted conversation history"
		}
		idx := m.appendEntry("system", notice)
		m.refreshViewportForEntry(idx, false)
		return nextWait()

	case providers.EventError:
		// Ignore context cancellation — this is normal when the user
		// interrupts a stream by pressing Enter.
		if event.Error != nil && (errors.Is(event.Error, context.Canceled) ||
			strings.Contains(event.Error.Error(), "context canceled")) {
			return nextWait()
		}
		m.streaming = false
		m.pendingRequest = false
		m.pendingTurn = nil
		m.clearLiveWorkStatus()
		// Show accumulated content so far (if any) before the error.
		if m.streamTarget >= 0 && m.streamTarget < len(m.entries) {
			content := strings.TrimSpace(m.entries[m.streamTarget].Content)
			if content == "" || content == "(empty)" {
				m.entries[m.streamTarget].Content = ""
				m.entries[m.streamTarget].textSegmentOffsets = nil
				m.entries[m.streamTarget].blockOrder = nil
			}
			// Force re-render to clear any streaming artifacts.
			m.entries[m.streamTarget].composited = ""
			m.entries[m.streamTarget].compositedH = 0
		}
		m.streamTarget = -1
		errMsg := "unknown stream error"
		if event.Error != nil {
			errMsg = providers.StreamErrorDisplay(event.Error)
		}
		// Empty-answer errors get a warning style with a retry hint
		// instead of a hard red ERROR — they're typically a provider
		// compatibility issue, not a fatal failure.
		if event.Error != nil && agent.IsEmptyAnswer(event.Error) {
			styledWarn := lipgloss.NewStyle().
				Foreground(currentTheme.Warning).
				Bold(true).
				Render("⚠ " + errMsg)
			m.appendEntry("system", styledWarn)
			m.statusLine = "empty response — press Enter to retry"
		} else {
			styledErr := lipgloss.NewStyle().
				Foreground(currentTheme.Error).
				Bold(true).
				Render("ERROR: " + errMsg)
			m.appendEntry("system", styledErr)
			m.statusLine = "request failed — press Enter to retry"
		}
		m.refreshViewport(true)
		return nil

	case providers.EventThinkingDelta:
		if m.streamTarget < 0 || m.streamTarget >= len(m.entries) {
			m.streamTarget = m.appendEntry("assistant", "")
		}
		e := &m.entries[m.streamTarget]
		if e.ThinkingContent == "" {
			m.thinkingStart = time.Now()
		}
		e.ThinkingContent += event.Content
		m.setLiveWorkStatus(workStatus{Phase: workPhaseThinking, Label: "Thinking", Meta: "Working through the next step", Running: true})
		m.statusLine = "thinking"
		m.refreshViewportForEntry(m.streamTarget, false)
		return nextWait()

	case providers.EventThinkingDone:
		if m.streamTarget >= 0 && m.streamTarget < len(m.entries) {
			e := &m.entries[m.streamTarget]
			e.ThinkingDone = true
			if !m.thinkingStart.IsZero() {
				e.ThinkingDuration = time.Since(m.thinkingStart)
			}
		}
		m.setLiveWorkStatus(workStatus{Phase: workPhaseGenerating, Label: "Responding", Meta: "Writing the reply", Running: true})
		m.statusLine = "streaming"
		m.refreshViewportForEntry(m.streamTarget, false)
		return nextWait()

	default:
		return nextWait()
	}
}

// drainQueue sends the next queued message if idle.
func (m Model) drainQueue() (tea.Model, tea.Cmd) {
	if m.pendingRequest {
		return m, nil
	}
	if len(m.pendingSteers) > 0 {
		// Merge pending steers into one follow-up that is sent before queued drafts.
		textParts := make([]string, 0, len(m.pendingSteers))
		images := make([]providers.InputImage, 0, len(m.pendingSteers))
		for _, steer := range m.pendingSteers {
			if steer.Text != "" {
				textParts = append(textParts, steer.Text)
			}
			images = append(images, steer.Images...)
		}
		m.pendingSteers = nil
		return m.sendMessage(queuedMessage{
			Text:   strings.Join(textParts, "\n"),
			Images: images,
		})
	}
	if len(m.messageQueue) == 0 {
		return m, nil
	}
	next := m.messageQueue[0]
	m.messageQueue = m.messageQueue[1:]
	return m.sendMessage(next)
}

func (m *Model) loadPersistedTokenUsage() {
	inputTokens, outputTokens, err := loadTokenUsageTotals(m.memoryPath)
	if err != nil {
		m.statusLine = fmt.Sprintf("memory load failed: %v", err)
		return
	}
	m.mainInputTokens = inputTokens
	m.mainOutputTokens = outputTokens
	m.turnInputTokens = 0
	m.turnOutputTokens = 0
}

func (m *Model) recordWorkerUsage(snapshot subagent.SubAgentSnapshot) {
	if m.workerUsageByID == nil {
		m.workerUsageByID = make(map[string]workerUsageSnapshot)
	}
	prev := m.workerUsageByID[snapshot.ID]
	if snapshot.InputTokens < prev.inputTokens {
		snapshot.InputTokens = prev.inputTokens
	}
	if snapshot.OutputTokens < prev.outputTokens {
		snapshot.OutputTokens = prev.outputTokens
	}
	m.workerInputTokens += snapshot.InputTokens - prev.inputTokens
	m.workerOutputTokens += snapshot.OutputTokens - prev.outputTokens
	m.workerUsageByID[snapshot.ID] = workerUsageSnapshot{
		inputTokens:  snapshot.InputTokens,
		outputTokens: snapshot.OutputTokens,
	}
}

func (m *Model) hasWorkerSpawned(id string) bool {
	if m.workerSpawnedByID == nil {
		return false
	}
	return m.workerSpawnedByID[id]
}

func (m *Model) markWorkerSpawned(id string) {
	if id == "" {
		return
	}
	if m.workerSpawnedByID == nil {
		m.workerSpawnedByID = make(map[string]bool)
	}
	m.workerSpawnedByID[id] = true
}

func (m Model) headerUsageSummary() string {
	base := fmt.Sprintf(
		"wuu · %s/%s │ main %s↑/%s↓ · workers %s↑/%s↓",
		m.provider,
		m.modelName,
		formatCompactNum(m.mainInputTokens),
		formatCompactNum(m.mainOutputTokens),
		formatCompactNum(m.workerInputTokens),
		formatCompactNum(m.workerOutputTokens),
	)
	if ctx := m.renderContextUsage(); ctx != "" {
		base += " · " + ctx
	}
	return base
}

// renderContextUsage returns a one-segment "ctx N% (Y/Z)" string
// colored by utilization, or empty when no usable estimate is
// available. Resolution order for the window size:
//   - StreamRunner.ContextWindowOverride (user config wins)
//   - providers.ContextWindowFor(model) (registry lookup)
// Small-context models (8K–32K) show the same format; the percentage
// scales naturally so a 32K model hits the 75% warning three times
// earlier than a 128K one without any special case.
func (m Model) renderContextUsage() string {
	used := m.contextTokens
	if used <= 0 {
		return ""
	}
	window := 0
	if m.streamRunner != nil && m.streamRunner.ContextWindowOverride > 0 {
		window = m.streamRunner.ContextWindowOverride
	}
	if window <= 0 {
		window = providers.ContextWindowFor(m.modelName)
	}
	if window <= 0 {
		// Registry fallback returns a positive default; guard regardless.
		return ""
	}
	pct := used * 100 / window
	// Clamp display at 999 so a bad estimate can't push the header off-screen.
	if pct > 999 {
		pct = 999
	}

	label := fmt.Sprintf("ctx %d%% (%s/%s)",
		pct, formatCompactNum(used), formatCompactNum(window))

	// Color by utilization, matching the thresholds Claude Code uses.
	var style lipgloss.Style
	switch {
	case pct >= 90:
		style = lipgloss.NewStyle().Bold(true).Foreground(currentTheme.Error)
	case pct >= 75:
		style = lipgloss.NewStyle().Foreground(currentTheme.Warning)
	case pct >= 50:
		style = lipgloss.NewStyle().Foreground(currentTheme.Text)
	default:
		style = lipgloss.NewStyle().Foreground(currentTheme.Subtle)
	}
	return style.Render(label)
}

// recomputeContextTokens estimates how many input tokens the next
// provider request will carry: system prompt + tool schema summaries +
// full chat history. The result is cached in m.contextTokens for the
// header to read cheaply in View().
//
// Called from refreshViewport so it keeps pace with history mutations
// (appends, compaction, resume) without adding a per-frame hot path.
// Cost is ~1 ms for a 100-msg, 5 KB-avg history; acceptable given
// refreshViewport is already millisecond-scale.
func (m *Model) recomputeContextTokens() {
	total := compact.EstimateMessagesTokens(m.chatHistory)
	if m.streamRunner != nil {
		total += compact.EstimateTokens(m.streamRunner.SystemPrompt)
		if m.streamRunner.Tools != nil {
			for _, def := range m.streamRunner.Tools.Definitions() {
				total += compact.EstimateTokens(def.Name)
				total += compact.EstimateTokens(def.Description)
			}
		}
	}
	m.contextTokens = total
}

func (m *Model) renderMarkdown(content string) (string, error) {
	if strings.TrimSpace(content) == "" {
		return "(empty)", nil
	}
	rendered := markdown.Render(content, contentWidth(m.viewport.Width), markdown.DefaultStyles())
	if rendered == "" {
		return "(empty)", nil
	}
	return rendered, nil
}

func formatUserEntryContent(text string, imageCount int) string {
	parts := make([]string, 0, imageCount+1)
	trimmed := strings.TrimSpace(text)
	if trimmed != "" {
		parts = append(parts, trimmed)
	}
	for i := 1; i <= imageCount; i++ {
		parts = append(parts, fmt.Sprintf("[Image #%d]", i))
	}
	if len(parts) == 0 {
		return "(empty)"
	}
	return strings.Join(parts, "\n")
}

func summarizeQueuedMessage(message queuedMessage) string {
	inline := formatUserEntryContent(message.Text, len(message.Images))
	inline = strings.Join(strings.Fields(inline), " ")
	return trimToWidth(inline, queuePreviewMaxChars)
}

func summarizeQueuedMessages(messages []queuedMessage) string {
	if len(messages) == 0 {
		return ""
	}
	limit := min(len(messages), queuePreviewMaxItems)
	parts := make([]string, 0, limit+1)
	for i := 0; i < limit; i++ {
		parts = append(parts, summarizeQueuedMessage(messages[i]))
	}
	if len(messages) > limit {
		parts = append(parts, fmt.Sprintf("+%d", len(messages)-limit))
	}
	return strings.Join(parts, " | ")
}

func (m *Model) removeQueuedScheduledTaskMessages(taskID string) int {
	if taskID == "" || len(m.messageQueue) == 0 {
		return 0
	}

	filtered := m.messageQueue[:0]
	removed := 0
	for _, msg := range m.messageQueue {
		if msg.ScheduledTaskID == taskID {
			removed++
			continue
		}
		filtered = append(filtered, msg)
	}
	if removed == 0 {
		return 0
	}
	m.messageQueue = filtered
	return removed
}

func stripUserImagePlaceholderLines(content string) string {
	lines := strings.Split(content, "\n")
	kept := make([]string, 0, len(lines))
	for _, line := range lines {
		trimmed := strings.TrimSpace(line)
		if isUserImagePlaceholder(trimmed) {
			continue
		}
		kept = append(kept, line)
	}
	return strings.TrimSpace(strings.Join(kept, "\n"))
}

func isUserImagePlaceholder(line string) bool {
	if !strings.HasPrefix(line, "[Image #") || !strings.HasSuffix(line, "]") {
		return false
	}
	number := strings.TrimSuffix(strings.TrimPrefix(line, "[Image #"), "]")
	if number == "" {
		return false
	}
	for _, r := range number {
		if r < '0' || r > '9' {
			return false
		}
	}
	return true
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
func (m *Model) focusInput() {
	m.input.Focus()
	m.clearPendingChatClick()
}

func (m *Model) blurInput() {
	m.input.Blur()
}

func (m *Model) clearPendingChatClick() {
	m.pendingChatClick = pendingChatClickState{}
}

func exceedsChatSelectionDragThreshold(startX, startY, x, y int) bool {
	dx := x - startX
	if dx < 0 {
		dx = -dx
	}
	dy := y - startY
	if dy < 0 {
		dy = -dy
	}
	return dx > chatSelectionDragThreshold || dy > chatSelectionDragThreshold
}

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

func (m Model) viewportVisibleLineRange() (start, end int, ok bool) {
	if m.viewport.Height <= 0 {
		return 0, 0, false
	}
	start = m.viewport.YOffset
	end = start + m.viewport.Height - 1
	return start, end, true
}

func (m Model) entryVisibleInViewport(idx int) bool {
	if idx < 0 || idx >= len(m.entries) {
		return false
	}
	start, end, ok := m.viewportVisibleLineRange()
	if !ok {
		return false
	}
	entry := m.entries[idx]
	if entry.renderStart < 0 || entry.renderEnd < entry.renderStart {
		return false
	}
	return entry.renderStart <= end && entry.renderEnd >= start
}

func (m *Model) deferViewportRefresh(idx int) {
	m.pendingViewportRefresh = true
	if idx >= 0 && idx < len(m.entries) {
		m.pendingViewportEntry = idx
	}
}

func (m *Model) refreshViewportForEntry(idx int, forceBottom bool) {
	if forceBottom || m.autoFollow {
		m.refreshViewport(forceBottom)
		return
	}
	if idx < 0 || idx >= len(m.entries) {
		m.refreshViewport(forceBottom)
		return
	}
	if m.entries[idx].renderStart < 0 || m.entries[idx].renderEnd < m.entries[idx].renderStart {
		m.refreshViewport(forceBottom)
		return
	}
	if m.entryVisibleInViewport(idx) {
		m.refreshViewport(forceBottom)
		return
	}
	m.deferViewportRefresh(idx)
}

func (m *Model) flushDeferredViewportRefresh() {
	if !m.pendingViewportRefresh {
		return
	}
	if m.autoFollow {
		m.refreshViewport(false)
		return
	}
	if m.pendingViewportEntry < 0 || m.pendingViewportEntry >= len(m.entries) {
		m.refreshViewport(false)
		return
	}
	if m.entryVisibleInViewport(m.pendingViewportEntry) {
		m.refreshViewport(false)
	}
}

func (m *Model) syncViewportState() {
	m.autoFollow = m.viewport.AtBottom()
	m.showJump = !m.autoFollow
	m.flushDeferredViewportRefresh()
}

func (m *Model) setViewportOffset(offset int) {
	maxOffset := max(0, m.viewport.TotalLineCount()-m.viewport.Height)
	if offset < 0 {
		offset = 0
	} else if offset > maxOffset {
		offset = maxOffset
	}
	m.viewport.YOffset = offset
	m.syncViewportState()
}

// compositeEntryKey computes a fast hash of all inputs that affect an
// entry's rendered output. If the key matches, the cached composited
// string is still valid. Uses FNV-1a for speed (not crypto).
func compositeEntryKey(e *transcriptEntry, vpWidth int, isStreaming bool, spinnerFrame int) uint64 {
	h := uint64(14695981039346656037) // FNV offset basis
	fnv := func(b byte) { h ^= uint64(b); h *= 1099511628211 }
	for i := 0; i < len(e.Content); i++ {
		fnv(e.Content[i])
	}
	fnv(byte(len(e.Content) >> 8))
	fnv(byte(len(e.Content)))
	fnv(byte(vpWidth >> 8))
	fnv(byte(vpWidth))
	fnv(byte(len(e.ToolCalls)))
	for j := range e.ToolCalls {
		for k := 0; k < len(e.ToolCalls[j].Name); k++ {
			fnv(e.ToolCalls[j].Name[k])
		}
		for k := 0; k < len(e.ToolCalls[j].Status); k++ {
			fnv(e.ToolCalls[j].Status[k])
		}
		if e.ToolCalls[j].Collapsed {
			fnv(1)
		}
		for k := 0; k < len(e.ToolCalls[j].Args) && k < 32; k++ {
			fnv(e.ToolCalls[j].Args[k])
		}
		for k := 0; k < len(e.ToolCalls[j].Result) && k < 32; k++ {
			fnv(e.ToolCalls[j].Result[k])
		}
	}
	fnv(byte(len(e.ThinkingContent)))
	if e.ThinkingDone {
		fnv(1)
	}
	if e.ThinkingExpanded {
		fnv(2)
	}
	if isStreaming {
		fnv(byte(spinnerFrame))
	}
	fnv(byte(len(e.rendered)))
	return h
}

// compositeEntry renders a single entry to its full display string
// (tool cards + thinking + content + indent). Returns the cached
// version if the key matches. This is the core of the entry-level
// render cache that makes refreshViewport O(n×concat) instead of
// O(n×render).
func (m *Model) compositeEntry(i int, isStreamTarget bool) string {
	e := &m.entries[i]
	key := compositeEntryKey(e, m.viewport.Width, isStreamTarget, m.spinnerFrame)
	if e.compositedKey == key && e.composited != "" {
		return e.composited
	}

	cw := contentWidth(m.viewport.Width)
	innerWidth := cw
	var parts []string

	// Role label + metadata row.
	switch e.Role {
	case "USER":
		// No label — user messages have the bubble style.
	case "ASSISTANT":
		// Subtle metadata row for assistant messages.
		meta := metadataStyle.Render(fmt.Sprintf("%s · %s", m.modelName, time.Now().Format("15:04")))
		parts = append(parts, indentLines(meta, contentPadLeft))
	default:
		parts = append(parts, indentLines(systemLabelStyle.Render(e.Role), contentPadLeft))
	}

	// Thinking block (always first, before any content/tools).
	if e.ThinkingContent != "" {
		elapsed := e.ThinkingDuration
		if !e.ThinkingDone && !m.thinkingStart.IsZero() {
			elapsed = time.Since(m.thinkingStart)
		}
		parts = append(parts, indentLines(renderThinkingBlock(
			e.ThinkingContent, e.ThinkingDone, e.ThinkingExpanded,
			elapsed, innerWidth, m.spinnerFrame,
		), contentPadLeft))
	}

	// Render content blocks in stream order (text segments and tool
	// cards interleaved as they arrived). Aligned with Claude Code's
	// per-content-block rendering. Falls back to legacy order when
	// blockOrder is empty (e.g. loaded from session history).
	fullContent := e.Content
	// Use the markdown-rendered version when available. Since Commit()
	// now renders markdown on every tick (not just at Finalize), the
	// rendered output is available during streaming too — no more
	// raw→rendered jump at EventDone.
	fullRendered := e.rendered
	isLastSegment := func(segIdx int) bool {
		return segIdx == len(e.textSegmentOffsets)-1
	}

	renderTextSegment := func(segIdx int) {
		// Extract the text for this specific segment.
		var segContent, segRendered string
		if segIdx < len(e.textSegmentOffsets) {
			start := e.textSegmentOffsets[segIdx]
			if start > len(fullContent) {
				start = len(fullContent)
			}
			if isLastSegment(segIdx) {
				segContent = fullContent[start:]
			} else if segIdx+1 < len(e.textSegmentOffsets) {
				end := e.textSegmentOffsets[segIdx+1]
				if end > len(fullContent) {
					end = len(fullContent)
				}
				if end < start {
					end = start
				}
				segContent = fullContent[start:end]
			}
			// Use the rendered markdown for single-segment entries
			// (the common case). For multi-segment (interleaved with
			// tools), use rendered only when this is the sole segment
			// (start == 0), otherwise fall back to raw per-segment.
			if isLastSegment(segIdx) && fullRendered != "" {
				segRendered = fullRendered
				if start > 0 {
					// Multi-segment: can't split rendered markdown by
					// byte offset. Fall back to raw for this segment.
					segRendered = ""
				}
			}
		} else {
			segContent = fullContent
			segRendered = fullRendered
		}
		segContent = truncateForDisplay(segContent)
		if segContent == "(empty)" || strings.TrimSpace(segContent) == "" {
			return
		}
		var textPart string
		if e.Role == "USER" {
			textPart = indentLines(userContentStyle.Render(wrapText(segContent, cw-2)), contentPadLeft)
		} else if segRendered != "" {
			// Rendered markdown is already width-constrained by the
			// renderer (tables use box-drawing, paragraphs are word-
			// wrapped). Do NOT re-wrap — it destroys table layout.
			textPart = indentLines(segRendered, contentPadLeft)
		} else {
			textPart = indentLines(wrapText(segContent, cw), contentPadLeft)
		}
		parts = append(parts, textPart)
	}
	renderTextFull := func() {
		content := truncateForDisplay(e.Content)
		if content == "(empty)" {
			return
		}
		var textPart string
		if e.Role == "USER" {
			textPart = indentLines(userContentStyle.Render(wrapText(content, cw-2)), contentPadLeft)
		} else if e.rendered != "" {
			textPart = indentLines(e.rendered, contentPadLeft)
		} else {
			textPart = indentLines(wrapText(content, cw), contentPadLeft)
		}
		parts = append(parts, textPart)
	}
	renderTool := func(idx int) {
		if idx >= 0 && idx < len(e.ToolCalls) {
			parts = append(parts, indentLines(renderToolCard(&e.ToolCalls[idx], innerWidth, m.spinnerFrame), contentPadLeft))
		}
	}
	renderToolRun := func(indices []int) {
		if len(indices) == 0 {
			return
		}
		if len(indices) == 1 {
			renderTool(indices[0])
			return
		}
		calls := make([]ToolCallEntry, 0, len(indices))
		for _, idx := range indices {
			if idx >= 0 && idx < len(e.ToolCalls) {
				calls = append(calls, e.ToolCalls[idx])
			}
		}
		if len(calls) == 0 {
			return
		}
		parts = append(parts, indentLines(renderToolBurstGroup(calls, innerWidth, m.spinnerFrame), contentPadLeft))
	}

	if len(e.blockOrder) > 0 {
		// Stream-order rendering with per-segment text.
		textSegIdx := 0
		for blockIdx := 0; blockIdx < len(e.blockOrder); {
			block := e.blockOrder[blockIdx]
			if block == "text" {
				renderTextSegment(textSegIdx)
				textSegIdx++
				blockIdx++
				continue
			}
			idx, ok := parseToolBlockIndex(block)
			if !ok {
				blockIdx++
				continue
			}
			if idx < 0 || idx >= len(e.ToolCalls) {
				blockIdx++
				continue
			}
			if classifyToolBurstTool(e.ToolCalls[idx].Name) == toolBurstNone {
				renderTool(idx)
				blockIdx++
				continue
			}
			run := []int{idx}
			next := blockIdx + 1
			for next < len(e.blockOrder) {
				nextIdx, ok := parseToolBlockIndex(e.blockOrder[next])
				if !ok || nextIdx < 0 || nextIdx >= len(e.ToolCalls) {
					break
				}
				if classifyToolBurstTool(e.ToolCalls[nextIdx].Name) == toolBurstNone {
					break
				}
				run = append(run, nextIdx)
				next++
			}
			renderToolRun(run)
			blockIdx = next
		}
		// Render any tools not covered by blockOrder (e.g. added
		// after the stream ended via tool result events).
		covered := make(map[int]bool)
		for _, block := range e.blockOrder {
			if idx, ok := parseToolBlockIndex(block); ok {
				covered[idx] = true
			}
		}
		for idx := 0; idx < len(e.ToolCalls); {
			if covered[idx] {
				idx++
				continue
			}
			if classifyToolBurstTool(e.ToolCalls[idx].Name) == toolBurstNone {
				renderTool(idx)
				idx++
				continue
			}
			run := []int{idx}
			next := idx + 1
			for next < len(e.ToolCalls) && !covered[next] && classifyToolBurstTool(e.ToolCalls[next].Name) != toolBurstNone {
				run = append(run, next)
				next++
			}
			renderToolRun(run)
			idx = next
		}
	} else {
		// Legacy fallback: tools first, then content.
		for idx := 0; idx < len(e.ToolCalls); {
			if classifyToolBurstTool(e.ToolCalls[idx].Name) == toolBurstNone {
				renderTool(idx)
				idx++
				continue
			}
			run := []int{idx}
			next := idx + 1
			for next < len(e.ToolCalls) && classifyToolBurstTool(e.ToolCalls[next].Name) != toolBurstNone {
				run = append(run, next)
				next++
			}
			renderToolRun(run)
			idx = next
		}
		renderTextFull()
	}

	result := strings.Join(parts, "\n")
	e.composited = result
	e.compositedKey = key
	e.compositedH = strings.Count(result, "\n") + 1
	return result
}

// overscanLines is the number of extra lines rendered above/below the
// visible viewport. Aligned with Claude Code's OVERSCAN_ROWS = 80.
const overscanLines = 80

func (m *Model) refreshViewport(forceBottom bool) {
	preserveOffset := !forceBottom && !m.autoFollow
	prevOffset := m.viewport.YOffset
	for i := range m.entries {
		m.entries[i].renderStart = -1
		m.entries[i].renderEnd = -1
	}

	if len(m.entries) == 0 && !m.pendingRequest {
		m.renderedContent = welcomeScreen(m.viewport.Width, m.provider, m.modelName, m.sessionID)
		m.pendingViewportRefresh = false
		m.pendingViewportEntry = -1
		m.viewport.SetContent(m.renderedContent)
		return
	}

	// ── Pass 1: compute heights with OffscreenFreeze ──
	// For entries that have scrolled far out of viewport, skip
	// re-rendering even if their state changed (spinner, elapsed).
	// This is the BubbleTea equivalent of Claude Code's OffscreenFreeze:
	// once a subtree is in scrollback, return the cached element.
	//
	// We do this in two sub-passes:
	//   1a. Quick height-only pass using cached compositedH when available.
	//   1b. Determine visible range.
	//   1c. Re-render only entries inside visible range + margin.
	type entrySlot struct {
		idx    int // index into m.entries
		height int // line count including 2-line gap
		offset int // cumulative start line
	}
	slots := make([]entrySlot, 0, len(m.entries))
	totalLines := 0
	for i := range m.entries {
		if m.entries[i].Role == "TOOL" {
			continue
		}
		// OffscreenFreeze: if we have a cached height, use it without
		// re-rendering. Only the stream target is exempt (it must stay
		// live even when off-screen so the first visible token appears
		// immediately when the user scrolls back).
		isStreamTarget := m.streaming && i == m.streamTarget
		if !isStreamTarget && m.entries[i].compositedH > 0 {
			// Cached height available — skip render for now.
		} else {
			m.compositeEntry(i, isStreamTarget)
		}
		h := m.entries[i].compositedH
		if h <= 0 {
			h = 1 // safety minimum
		}
		if len(slots) > 0 {
			h += 2 // gap between entries ("\n\n")
		}
		slots = append(slots, entrySlot{idx: i, height: h, offset: totalLines})
		totalLines += h
	}

	// ── Pass 2: determine visible range ──
	vpHeight := m.layout.Chat.Height
	scrollTop := prevOffset
	if forceBottom || m.autoFollow {
		scrollTop = totalLines - vpHeight
		if scrollTop < 0 {
			scrollTop = 0
		}
	}
	visibleTop := scrollTop - overscanLines
	visibleBottom := scrollTop + vpHeight + overscanLines
	if visibleTop < 0 {
		visibleTop = 0
	}
	if visibleBottom > totalLines {
		visibleBottom = totalLines
	}

	// Find first and last visible slots.
	firstVisible, lastVisible := -1, -1
	for si, slot := range slots {
		slotEnd := slot.offset + slot.height
		if slotEnd > visibleTop && slot.offset < visibleBottom {
			if firstVisible < 0 {
				firstVisible = si
			}
			lastVisible = si
		}
	}
	if firstVisible < 0 {
		firstVisible = 0
		lastVisible = len(slots) - 1
	}

	// ── Pass 2.5: OffscreenFreeze margin ──
	// Expand the "must render" window by 2x overscan so entries
	// just outside the visible range still get updated (smooth scroll).
	// Entries beyond this margin are truly frozen.
	freezeMargin := overscanLines * 2
	freezeTop := visibleTop - freezeMargin
	freezeBottom := visibleBottom + freezeMargin
	if freezeTop < 0 {
		freezeTop = 0
	}
	if freezeBottom > totalLines {
		freezeBottom = totalLines
	}
	for si, slot := range slots {
		slotEnd := slot.offset + slot.height
		inFreezeZone := slotEnd > freezeTop && slot.offset < freezeBottom
		isStreamTarget := m.streaming && slot.idx == m.streamTarget
		if (inFreezeZone || isStreamTarget) && m.entries[slot.idx].composited == "" {
			// Inside freeze zone but never rendered — render now.
			m.compositeEntry(slot.idx, isStreamTarget)
			slot.height = m.entries[slot.idx].compositedH
			if slot.height <= 0 {
				slot.height = 1
			}
			if si > 0 {
				slot.height += 2
			}
			slots[si] = slot
		}
	}

	// ── Pass 3: build viewport content with virtual padding ──
	var b strings.Builder

	// Top padding: empty lines for entries above visible range.
	topPadLines := 0
	if firstVisible > 0 {
		topPadLines = slots[firstVisible].offset
	}
	if topPadLines > 0 {
		b.WriteString(strings.Repeat("\n", topPadLines))
	}

	// Render visible entries.
	lineCount := topPadLines
	for si := firstVisible; si <= lastVisible && si < len(slots); si++ {
		slot := slots[si]
		e := &m.entries[slot.idx]

		if si > firstVisible {
			b.WriteString("\n\n")
			lineCount += 2
		}

		entryStartLine := lineCount
		b.WriteString(e.composited)
		lineCount += e.compositedH

		entryEndLine := lineCount - 1
		if entryEndLine < entryStartLine {
			entryEndLine = entryStartLine
		}
		m.entries[slot.idx].renderStart = entryStartLine
		m.entries[slot.idx].renderEnd = entryEndLine
	}

	// Bottom padding: empty lines for entries below visible range.
	bottomPadLines := totalLines - lineCount
	if bottomPadLines > 0 {
		b.WriteString(strings.Repeat("\n", bottomPadLines))
	}

	m.renderedContent = b.String()
	m.pendingViewportRefresh = false
	m.pendingViewportEntry = -1
	m.viewport.SetContent(m.renderedContent)

	// Recompute the header's context-utilization estimate whenever the
	// transcript (and by extension m.chatHistory) changes. Keeping it
	// here means it stays in sync with appends, compaction and resume
	// without adding a per-frame recompute in View().
	m.recomputeContextTokens()

	// Refresh search matches when content changes.
	if m.search.Active && m.search.Query != "" {
		m.search.Matches = searchInContent(m.renderedContent, m.search.Query, m.search.CaseSensitive)
		if m.search.CurrentIdx >= len(m.search.Matches) {
			m.search.CurrentIdx = 0
		}
	}

	if forceBottom || m.autoFollow {
		m.viewport.GotoBottom()
	} else if preserveOffset {
		m.setViewportOffset(prevOffset)
	}
	m.showJump = !m.viewport.AtBottom()
}

func (m *Model) relayout() {
	if m.width <= 0 || m.height <= 0 {
		return
	}
	oldChatW := m.layout.Chat.Width
	m.inputLines = clampInputLines(strings.Count(m.input.Value(), "\n")+1, 15)
	processPanelLines := m.processPanelHeight()
	imageBarH := 0
	if len(m.pendingImages) > 0 {
		imageBarH = 1
	}
	m.layout = computeLayout(m.width, m.height, m.inputLines, m.workerPanelHeight()+processPanelLines, imageBarH)

	m.input.SetWidth(m.layout.Input.Width)
	m.input.SetHeight(m.layout.Input.Height)
	m.viewport.Width = m.layout.Chat.Width
	m.viewport.Height = m.layout.Chat.Height
	m.cachedSep = dividerStyle.Render(strings.Repeat("─", m.width))

	// Invalidate cached renders when chat width changes — text
	// rendered for the old width wraps incorrectly at the new width.
	if m.layout.Chat.Width != oldChatW {
		for i := range m.entries {
			m.entries[i].rendered = ""
			m.entries[i].renderedLines = nil
			m.entries[i].composited = ""
			m.entries[i].compositedKey = 0
		}
	}
	m.refreshViewport(false)
}

func (m *Model) setLiveWorkStatus(status workStatus) {
	m.liveWorkStatus = status
}

func (m *Model) clearLiveWorkStatus() {
	m.liveWorkStatus = workStatus{}
}

func compactStatusDetail(raw string, width int) string {
	raw = strings.Join(strings.Fields(strings.TrimSpace(raw)), " ")
	if raw == "" {
		return ""
	}
	return trimToWidth(raw, width)
}

func formatStatusDelay(delay time.Duration) string {
	if delay <= 0 {
		return ""
	}
	if delay < time.Second {
		ms := delay.Round(10 * time.Millisecond).Milliseconds()
		if ms < 1 {
			ms = 1
		}
		return fmt.Sprintf("%dms", ms)
	}
	if delay < 10*time.Second {
		return fmt.Sprintf("%.1fs", delay.Round(100*time.Millisecond).Seconds())
	}
	if delay < time.Minute {
		return fmt.Sprintf("%ds", int(delay.Round(time.Second).Seconds()))
	}
	return fmt.Sprintf("%dm%02ds", int(delay.Minutes()), int(delay.Round(time.Second).Seconds())%60)
}

func reconnectWorkStatus(lifecycle *providers.StreamLifecycle) workStatus {
	ws := workStatus{
		Phase:   workPhaseReconnecting,
		Label:   "Reconnecting",
		Meta:    "Restoring the live response",
		Running: true,
	}
	if lifecycle == nil {
		return ws
	}

	// Time-budget display: "Reconnecting... 45s / 2m0s"
	if lifecycle.Budget > 0 {
		ws.Label = fmt.Sprintf("Reconnecting... %s / %s",
			lifecycle.Elapsed.Round(time.Second),
			lifecycle.Budget.Round(time.Second))
	} else if lifecycle.RetryCount > 0 && lifecycle.MaxRetries > 0 {
		ws.Label = fmt.Sprintf("Reconnecting... %d/%d", lifecycle.RetryCount, lifecycle.MaxRetries)
	} else if lifecycle.Attempt > 1 && lifecycle.MaxAttempts > 0 {
		ws.Label = fmt.Sprintf("Reconnecting... %d/%d", lifecycle.Attempt, lifecycle.MaxAttempts)
	}

	reason := compactStatusDetail(lifecycle.Reason, 44)
	nextTry := ""
	if delay := formatStatusDelay(lifecycle.RetryIn); delay != "" {
		nextTry = "Retrying in " + delay
	}

	switch {
	case reason != "" && nextTry != "":
		ws.Meta = reason
		ws.Detail = nextTry
	case reason != "":
		ws.Meta = reason
	case nextTry != "":
		ws.Meta = nextTry
	case lifecycle.Budget > 0:
		ws.Meta = fmt.Sprintf("Attempt %d", lifecycle.Attempt)
	case lifecycle.RetryCount > 0 && lifecycle.MaxRetries > 0:
		ws.Meta = fmt.Sprintf("Retry %d/%d", lifecycle.RetryCount, lifecycle.MaxRetries)
	}
	return ws
}

func (m Model) currentWorkStatus() workStatus {
	if m.liveWorkStatus.Phase != workPhaseIdle {
		return m.liveWorkStatus
	}
	return deriveWorkStatus(m.statusLine)
}

func (m *Model) persistStreamMessage(msg providers.ChatMessage) {
	m.chatHistory = append(m.chatHistory, msg)
	_ = appendChatMessage(m.memoryPath, msg)
	if m.pendingTurn != nil {
		m.pendingTurn.incrementalPersisted = true
	}
}

func (m Model) shouldRenderInlineStatus() bool {
	if !m.streaming && !m.pendingRequest {
		return false
	}
	ws := m.currentWorkStatus()
	if ws.Phase == workPhaseIdle {
		return false
	}
	if ws.Phase == workPhaseThinking || ws.Phase == workPhaseTool {
		if m.hasVisibleRunningTranscriptStatus() && !ws.PersistentInlineUI {
			return false
		}
	}
	return true
}

func (m Model) hasVisibleRunningTranscriptStatus() bool {
	for i, entry := range m.entries {
		if m.viewport.Height > 0 {
			if entry.renderStart >= 0 && entry.renderEnd >= entry.renderStart {
				if !m.entryVisibleInViewport(i) {
					continue
				}
			}
		}
		if entry.ThinkingContent != "" && !entry.ThinkingDone {
			return true
		}
		for _, tc := range entry.ToolCalls {
			if tc.Status == ToolCallRunning {
				return true
			}
		}
	}
	return false
}

// View renders the full terminal.
func (m Model) View() string {
	if m.width == 0 || m.height == 0 {
		return "loading..."
	}

	// When the resume picker is active, it owns the entire screen.
	if m.resumePicker != nil {
		return m.resumePicker.View()
	}

	// When the ask_user modal is active, it owns the entire screen.
	if m.activeAsk != nil {
		return m.activeAsk.View()
	}

	// Header — left: brand info, right: hints + clock.
	headerLeft := m.headerUsageSummary()

	var hints []string
	if strings.HasPrefix(m.statusLine, "request failed") {
		hints = append(hints, statusErrorStyle.Render("✗ "+m.statusLine))
	}
	if len(m.pendingSteers) > 0 {
		hints = append(hints, fmt.Sprintf("steer:%d", len(m.pendingSteers)))
	}
	if len(m.messageQueue) > 0 {
		hints = append(hints, fmt.Sprintf("queue:%d", len(m.messageQueue)))
	}
	// Image count is now shown in the image bar; no header hint needed.
	if m.showJump {
		hints = append(hints, scrollIndicatorStyle.Render("▼"))
	}
	hints = append(hints, m.clock)
	headerRight := strings.Join(hints, " · ")

	availableW := max(1, m.width-lipgloss.Width(headerRight)-1)
	headerLeft = trimToWidth(headerLeft, availableW)
	gap := max(1, m.width-lipgloss.Width(headerLeft)-lipgloss.Width(headerRight))
	header := headerStyle.Render(headerLeft + strings.Repeat(" ", gap) + headerRight)

	outputBox := m.viewport.View()

	// Overlay text selection highlight. The selection state is in
	// content-absolute coordinates; overlaySelection translates to
	// the visible window via the current YOffset and clips anything
	// that's scrolled off-screen. The highlight is a bg-only overlay
	// (selection.go does the SGR plumbing) so the original text
	// colors — markdown styling, syntax highlighting, role labels —
	// keep showing through under the highlighted bg.
	if m.selection.hasSelection() {
		outputBox = overlaySelection(outputBox, &m.selection, m.viewport.YOffset, m.viewport.Width)
	}

	// Overlay search highlight matches on top of the viewport.
	// Search is a screen overlay — matches are found in renderedContent
	// and highlighted via SGR, not by re-rendering message components.
	if m.search.Active && m.search.hasMatches() {
		outputBox = overlaySearchHighlight(outputBox, &m.search, m.viewport.YOffset, m.viewport.Width)
	}

	inputBox := m.input.View()
	if m.inputSelection.hasSelection() {
		inputBox = overlayInputSelection(inputBox, &m.inputSelection)
	}

	// Overlay completion popup on top of outputBox if visible.
	if m.completionVisible && len(m.completionItems) > 0 {
		popup := renderCompletion(m.completionItems, m.completionIndex, m.width)
		outputBox = overlayBottom(outputBox, popup, m.width)
	}

	// Separator line — precomputed in relayout().
	sep := m.cachedSep

	// Inline status lives outside the viewport so its lightweight spinner
	// can update without rebuilding the viewport content. Keep this area
	// concise so it acts as the single live status line instead of
	// competing with thinking/tool/worker surfaces inside the transcript.
	statusLine := ""
	if m.shouldRenderInlineStatus() {
		statusLine = indentLines(renderInlineWorkStatus(m.currentWorkStatus(), m.spinnerFrame, contentWidth(m.viewport.Width)), contentPadLeft)
	}

	// Search status bar — shown when search overlay is active.
	var searchBar string
	if m.search.Active {
		searchPrompt := "/" + m.search.Query + "▌"
		matchInfo := ""
		if m.search.hasMatches() {
			matchInfo = fmt.Sprintf(" %d/%d", m.search.CurrentIdx+1, len(m.search.Matches))
		}
		searchBar = indentLines(
			waitingStatusLabelStrongStyle.Render("Search: ")+
				waitingStatusLabelStyle.Render(searchPrompt)+
				waitingStatusMetaStyle.Render(matchInfo+"  ·  n next  ·  N prev  ·  Esc close"),
			contentPadLeft,
		)
	}

	parts := []string{header, outputBox, statusLine}
	if searchBar != "" {
		parts = append(parts, searchBar)
	}
	if panel := m.renderWorkerPanel(m.width); panel != "" {
		parts = append(parts, sep, panel)
	}
	if panel := m.renderProcessPanel(m.width); panel != "" {
		parts = append(parts, sep, panel)
	}
	parts = append(parts, sep)
	if bar := renderImageBar(len(m.pendingImages), m.selectedImageIdx, m.imageBarFocused, m.width); bar != "" {
		parts = append(parts, bar)
	}
	parts = append(parts, inputBox)

	return strings.Join(parts, "\n")
}

// trimWorkerErrMsg flattens newlines in a worker error message and
// caps its length so the TUI failure line stays single-row.
func trimWorkerErrMsg(s string, max int) string {
	s = strings.ReplaceAll(s, "\n", " ")
	s = strings.ReplaceAll(s, "\r", " ")
	s = strings.Join(strings.Fields(s), " ")
	if len(s) <= max {
		return s
	}
	return s[:max] + "…"
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

func (m *Model) recordProcessEvent(event processruntime.Event) {
	key := event.Process.ID + ":" + string(event.Type)
	if m.processEventSeen == nil {
		m.processEventSeen = make(map[string]bool)
	}
	if m.processEventSeen[key] {
		return
	}
	m.processEventSeen[key] = true
	line := formatProcessEvent(event)
	if line == "" {
		return
	}
	m.appendEntry("system", line)
	m.statusLine = line
}

func formatProcessEvent(event processruntime.Event) string {
	name := processDisplayName(event.Process)
	switch event.Type {
	case processruntime.EventStarted:
		return fmt.Sprintf("✓ process started: %s (%s)", name, event.Process.ID)
	case processruntime.EventStopped:
		return fmt.Sprintf("⊘ process stopped: %s (%s)", name, event.Process.ID)
	case processruntime.EventFailed:
		if strings.TrimSpace(event.Process.LastError) != "" {
			return fmt.Sprintf("✗ process failed: %s (%s) — %s", name, event.Process.ID, event.Process.LastError)
		}
		return fmt.Sprintf("✗ process failed: %s (%s)", name, event.Process.ID)
	case processruntime.EventCleanedUp:
		return fmt.Sprintf("⊘ process cleaned up: %s (%s)", name, event.Process.ID)
	default:
		return ""
	}
}
