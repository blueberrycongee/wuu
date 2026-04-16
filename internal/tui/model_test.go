package tui

import (
	"context"
	"fmt"
	"path/filepath"
	"regexp"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/blueberrycongee/wuu/internal/agent"
	processruntime "github.com/blueberrycongee/wuu/internal/process"
	"github.com/blueberrycongee/wuu/internal/providers"
	"github.com/blueberrycongee/wuu/internal/subagent"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/x/ansi"
)

// echoStreamClient is a minimal StreamClient for tests. StreamChat sends
// the response as a single content delta + done event and closes the channel.
type echoStreamClient struct {
	answer func(messages []providers.ChatMessage) string
}

func (c *echoStreamClient) Chat(_ context.Context, req providers.ChatRequest) (providers.ChatResponse, error) {
	return providers.ChatResponse{Content: c.answer(req.Messages)}, nil
}

func (c *echoStreamClient) StreamChat(_ context.Context, req providers.ChatRequest) (<-chan providers.StreamEvent, error) {
	ch := make(chan providers.StreamEvent, 3)
	ch <- providers.StreamEvent{Type: providers.EventContentDelta, Content: c.answer(req.Messages)}
	ch <- providers.StreamEvent{Type: providers.EventDone}
	close(ch)
	return ch, nil
}

// newTestModel creates a Model wired to a mock StreamRunner for testing.
// The answer function receives the chat messages and returns the assistant reply.
func newTestModel(answer func([]providers.ChatMessage) string) Model {
	client := &echoStreamClient{answer: answer}
	m := NewModel(Config{
		Provider:   "test",
		Model:      "test-model",
		ConfigPath: "/tmp/.wuu.json",
		StreamRunner: &agent.StreamRunner{
			Client: client,
			Model:  "test-model",
		},
	})
	m.width = 120
	m.height = 40
	m.relayout()
	return m
}

func newTestProcessManager(t *testing.T) *processruntime.Manager {
	t.Helper()
	mgr, err := processruntime.NewManager(t.TempDir())
	if err != nil {
		t.Fatalf("NewManager: %v", err)
	}
	return mgr
}

func startTestProcess(t *testing.T, mgr *processruntime.Manager, command string, owner processruntime.OwnerKind, ownerID string, lifecycle processruntime.Lifecycle) processruntime.Process {
	t.Helper()
	p, err := mgr.Start(context.Background(), processruntime.StartOptions{Command: command, OwnerKind: owner, OwnerID: ownerID, Lifecycle: lifecycle})
	if err != nil {
		t.Fatalf("Start: %v", err)
	}
	t.Cleanup(func() {
		_, _ = mgr.Stop(p.ID)
	})
	return *p
}

// drainStream pumps the bubbletea command loop until streaming finishes.
func drainStream(t *testing.T, m Model, cmd tea.Cmd) Model {
	t.Helper()
	deadline := time.Now().Add(2 * time.Second)
	for cmd != nil && time.Now().Before(deadline) {
		msg := cmd()
		if msg == nil {
			break
		}
		var next tea.Model
		next, cmd = m.Update(msg)
		m = next.(Model)
	}
	if m.streaming {
		t.Fatal("stream did not finish within deadline")
	}
	return m
}

func TestView_ProcessPanelAppearsAndHides(t *testing.T) {
	m := NewModel(Config{Provider: "test", Model: "test-model", ConfigPath: "/tmp/.wuu.json"})
	m.width = 120
	m.height = 24
	m.relayout()
	if strings.Contains(ansi.Strip(m.View()), "Processes") {
		t.Fatal("expected process panel hidden without processes")
	}

	mgr := newTestProcessManager(t)
	startTestProcess(t, mgr, "sleep 30", processruntime.OwnerMainAgent, "main", processruntime.LifecycleSession)
	m.processManager = mgr
	m.relayout()
	view := ansi.Strip(m.View())
	if !strings.Contains(view, "Processes") {
		t.Fatalf("expected process panel visible, got: %s", view)
	}
	if !strings.Contains(view, "owner:main") {
		t.Fatalf("expected process owner in panel, got: %s", view)
	}
	if !strings.Contains(view, "session") {
		t.Fatalf("expected process lifecycle in panel, got: %s", view)
	}
}

func TestView_WorkerAndProcessPanelsCanRenderTogether(t *testing.T) {
	mgr := newTestProcessManager(t)
	startTestProcess(t, mgr, "sleep 30", processruntime.OwnerMainAgent, "main", processruntime.LifecycleSession)

	m := NewModel(Config{Provider: "test", Model: "test-model", ConfigPath: "/tmp/.wuu.json", ProcessManager: mgr})
	m.width = 120
	m.height = 24
	m.statusLine = "streaming"
	m.pendingRequest = true
	m.relayout()

	view := ansi.Strip(m.View())
	if !strings.Contains(view, "Processes") {
		t.Fatalf("expected process panel in view, got: %s", view)
	}
	if !strings.Contains(view, "Responding") {
		t.Fatalf("expected inline status to coexist with process panel, got: %s", view)
	}
	if m.processPanelHeight() == 0 {
		t.Fatal("expected process panel height > 0")
	}
	if m.layout.Chat.Height <= 0 {
		t.Fatal("expected chat height to remain positive")
	}
}

func TestSlashProcessesIncludesWorkspaceScopedLabelLifecycleOwnerAndStatus(t *testing.T) {
	mgr := newTestProcessManager(t)
	p := startTestProcess(t, mgr, "sleep 30", processruntime.OwnerMainAgent, "main", processruntime.LifecycleManaged)
	m := NewModel(Config{Provider: "test", Model: "test-model", ConfigPath: "/tmp/.wuu.json", ProcessManager: mgr})

	out := cmdProcesses("", &m)
	if !strings.Contains(out, "workspace managed processes (1 total):") {
		t.Fatalf("expected workspace-scoped header, got: %s", out)
	}
	if !strings.Contains(out, p.ID) {
		t.Fatalf("expected process id in output, got: %s", out)
	}
	if !strings.Contains(out, "lifecycle:managed") {
		t.Fatalf("expected lifecycle in output, got: %s", out)
	}
	if !strings.Contains(out, "owner:main") {
		t.Fatalf("expected owner in output, got: %s", out)
	}
	if !strings.Contains(out, "status:running") {
		t.Fatalf("expected status in output, got: %s", out)
	}
}

func TestSlashProcessesEmptyStateUsesWorkspaceScopedLanguage(t *testing.T) {
	mgr := newTestProcessManager(t)
	m := NewModel(Config{Provider: "test", Model: "test-model", ConfigPath: "/tmp/.wuu.json", ProcessManager: mgr})

	out := cmdProcesses("", &m)
	if out != "processes: no workspace managed processes found" {
		t.Fatalf("unexpected empty state output: %s", out)
	}
	if strings.Contains(out, "this session yet") {
		t.Fatalf("output should not claim session scope: %s", out)
	}
}

func TestProcessLifecycleVisibleThroughModelState(t *testing.T) {
	mgr := newTestProcessManager(t)
	p := startTestProcess(t, mgr, "sleep 30", processruntime.OwnerMainAgent, "main", processruntime.LifecycleSession)
	m := NewModel(Config{Provider: "test", Model: "test-model", ConfigPath: "/tmp/.wuu.json", ProcessManager: mgr})
	m.width = 120
	m.height = 24
	m.relayout()

	processes := m.visibleProcesses()
	if len(processes) != 1 {
		t.Fatalf("expected 1 visible process, got %d", len(processes))
	}
	if processes[0].ID != p.ID {
		t.Fatalf("expected process %s, got %s", p.ID, processes[0].ID)
	}
	if processes[0].Lifecycle != processruntime.LifecycleSession {
		t.Fatalf("expected session lifecycle, got %s", processes[0].Lifecycle)
	}
	if processes[0].OwnerKind != processruntime.OwnerMainAgent {
		t.Fatalf("expected main owner, got %s", processes[0].OwnerKind)
	}
}

func TestView_HeaderShowsMainAndWorkerUsage(t *testing.T) {
	m := NewModel(Config{Provider: "test", Model: "test-model", ConfigPath: "/tmp/.wuu.json"})
	m.width = 120
	m.height = 20
	m.mainInputTokens = 12_345
	m.mainOutputTokens = 3_210
	m.workerInputTokens = 8_000
	m.workerOutputTokens = 6_000
	m.relayout()

	view := ansi.Strip(m.View())
	if !strings.Contains(view, "main 12k↑/3.2k↓") {
		t.Fatalf("expected main usage in header, got: %s", view)
	}
	if !strings.Contains(view, "workers 8.0k↑/6.0k↓") {
		t.Fatalf("expected worker usage in header, got: %s", view)
	}
	if strings.Contains(view, " tokens") {
		t.Fatalf("expected old token estimate text removed, got: %s", view)
	}
}

func TestRecordWorkerUsage_DeduplicatesAcrossNotifications(t *testing.T) {
	m := NewModel(Config{Provider: "test", Model: "test-model", ConfigPath: "/tmp/.wuu.json"})

	m.recordWorkerUsage(subagent.SubAgentSnapshot{ID: "worker-1", InputTokens: 10, OutputTokens: 4})
	m.recordWorkerUsage(subagent.SubAgentSnapshot{ID: "worker-1", InputTokens: 10, OutputTokens: 4})
	m.recordWorkerUsage(subagent.SubAgentSnapshot{ID: "worker-2", InputTokens: 3, OutputTokens: 2})

	if m.workerInputTokens != 13 || m.workerOutputTokens != 6 {
		t.Fatalf("unexpected worker totals after dedupe: in=%d out=%d", m.workerInputTokens, m.workerOutputTokens)
	}
}

func TestRecordWorkerUsage_TracksRunningGrowthAndCompletion(t *testing.T) {
	m := NewModel(Config{Provider: "test", Model: "test-model", ConfigPath: "/tmp/.wuu.json"})

	m.recordWorkerUsage(subagent.SubAgentSnapshot{ID: "worker-1", Status: subagent.StatusRunning, InputTokens: 5, OutputTokens: 2})
	m.recordWorkerUsage(subagent.SubAgentSnapshot{ID: "worker-1", Status: subagent.StatusRunning, InputTokens: 9, OutputTokens: 4})
	m.recordWorkerUsage(subagent.SubAgentSnapshot{ID: "worker-1", Status: subagent.StatusCompleted, InputTokens: 9, OutputTokens: 4})

	if m.workerInputTokens != 9 || m.workerOutputTokens != 4 {
		t.Fatalf("unexpected worker totals after growth/completion: in=%d out=%d", m.workerInputTokens, m.workerOutputTokens)
	}
}

func TestApplyStreamEvent_EventDoneAccumulatesMainUsage(t *testing.T) {
	m := NewModel(Config{Provider: "test", Model: "test-model", ConfigPath: "/tmp/.wuu.json"})
	m.width = 100
	m.height = 20
	m.relayout()
	m.streaming = true
	m.pendingRequest = true
	m.streamTarget = m.appendEntry("assistant", "partial")

	_ = m.applyStreamEvent(providers.StreamEvent{
		Type: providers.EventDone,
		Usage: &providers.TokenUsage{
			InputTokens:  123,
			OutputTokens: 45,
		},
	}, false)

	if m.mainInputTokens != 123 || m.mainOutputTokens != 45 {
		t.Fatalf("unexpected main usage totals: in=%d out=%d", m.mainInputTokens, m.mainOutputTokens)
	}
	if got := m.headerUsageSummary(); !strings.Contains(got, "main 123↑/45↓") {
		t.Fatalf("expected header summary to show main input/output, got %q", got)
	}
}

func TestStreamFinished_PersistsTurnTokenDeltasOnly(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "session.jsonl")
	m := NewModel(Config{
		Provider:   "test",
		Model:      "test-model",
		ConfigPath: filepath.Join(dir, ".wuu.json"),
		MemoryPath: path,
	})
	m.mainInputTokens = 100
	m.mainOutputTokens = 40
	m.turnInputTokens = 7
	m.turnOutputTokens = 3
	m.streaming = true
	m.pendingRequest = true

	updated, _ := m.Update(streamFinishedMsg{})
	after := updated.(Model)

	if after.mainInputTokens != 100 || after.mainOutputTokens != 40 {
		t.Fatalf("expected main totals preserved after persistence, got in=%d out=%d", after.mainInputTokens, after.mainOutputTokens)
	}
	if after.turnInputTokens != 0 || after.turnOutputTokens != 0 {
		t.Fatalf("expected turn totals reset after persistence, got in=%d out=%d", after.turnInputTokens, after.turnOutputTokens)
	}

	inputTokens, outputTokens, err := loadTokenUsageTotals(path)
	if err != nil {
		t.Fatalf("loadTokenUsageTotals: %v", err)
	}
	if inputTokens != 7 || outputTokens != 3 {
		t.Fatalf("expected persisted turn delta only, got in=%d out=%d", inputTokens, outputTokens)
	}

	if got := after.headerUsageSummary(); !strings.Contains(got, "main 100↑/40↓") {
		t.Fatalf("expected header summary to keep cumulative main totals, got %q", got)
	}
}

func TestLoadPersistedTokenUsage_RestoresMainTotalsFromTurnDeltas(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "session.jsonl")
	if err := appendTokenUsage(path, 11, 5); err != nil {
		t.Fatalf("append first token usage: %v", err)
	}
	if err := appendTokenUsage(path, 7, 3); err != nil {
		t.Fatalf("append second token usage: %v", err)
	}

	m := NewModel(Config{
		Provider:   "test",
		Model:      "test-model",
		ConfigPath: filepath.Join(dir, ".wuu.json"),
		MemoryPath: path,
	})

	if m.mainInputTokens != 18 || m.mainOutputTokens != 8 {
		t.Fatalf("expected restored main totals from persisted deltas, got in=%d out=%d", m.mainInputTokens, m.mainOutputTokens)
	}
	if m.turnInputTokens != 0 || m.turnOutputTokens != 0 {
		t.Fatalf("expected restored turn totals to start at zero, got in=%d out=%d", m.turnInputTokens, m.turnOutputTokens)
	}
	if got := m.headerUsageSummary(); !strings.Contains(got, "main 18↑/8↓") {
		t.Fatalf("expected header summary to use restored totals, got %q", got)
	}
}

func TestWorkerNotifyRunningUsageAccumulatesAndPreservesCompletedTotals(t *testing.T) {
	m := NewModel(Config{Provider: "test", Model: "test-model", ConfigPath: "/tmp/.wuu.json"})
	m.width = 100
	m.height = 20
	m.relayout()

	updated, _ := m.Update(workerNotifyMsg{notification: subagent.Notification{
		Status:   subagent.StatusRunning,
		Snapshot: subagent.SubAgentSnapshot{ID: "worker-1", Type: "worker", Description: "first", Status: subagent.StatusRunning, InputTokens: 4, OutputTokens: 1},
	}})
	m = updated.(Model)
	updated, _ = m.Update(workerNotifyMsg{notification: subagent.Notification{
		Status:   subagent.StatusCompleted,
		Snapshot: subagent.SubAgentSnapshot{ID: "worker-1", Type: "worker", Description: "first", Status: subagent.StatusCompleted, InputTokens: 7, OutputTokens: 3},
	}})
	m = updated.(Model)
	updated, _ = m.Update(workerNotifyMsg{notification: subagent.Notification{
		Status:   subagent.StatusRunning,
		Snapshot: subagent.SubAgentSnapshot{ID: "worker-2", Type: "worker", Description: "second", Status: subagent.StatusRunning, InputTokens: 2, OutputTokens: 5},
	}})
	m = updated.(Model)

	if m.workerInputTokens != 9 || m.workerOutputTokens != 8 {
		t.Fatalf("unexpected worker totals across notifications: in=%d out=%d", m.workerInputTokens, m.workerOutputTokens)
	}
}

func TestWorkerNotifyRunningAppendsSpawnedEntryOnce(t *testing.T) {
	m := NewModel(Config{Provider: "test", Model: "test-model", ConfigPath: "/tmp/.wuu.json"})

	updated, _ := m.Update(workerNotifyMsg{notification: subagent.Notification{
		Status: subagent.StatusRunning,
		Snapshot: subagent.SubAgentSnapshot{
			ID:          "worker-1",
			Type:        "worker",
			Description: "first",
			Status:      subagent.StatusRunning,
		},
	}})
	m = updated.(Model)

	if len(m.entries) != 1 {
		t.Fatalf("expected one spawned entry, got %d", len(m.entries))
	}
	if m.entries[0].Role != "SYSTEM" {
		t.Fatalf("expected system entry, got %q", m.entries[0].Role)
	}
	if !strings.Contains(m.entries[0].Content, "worker spawned: worker-1") {
		t.Fatalf("expected spawned entry content, got %q", m.entries[0].Content)
	}
}

func TestWorkerNotifyRunningDeduplicatesSpawnedEntry(t *testing.T) {
	m := NewModel(Config{Provider: "test", Model: "test-model", ConfigPath: "/tmp/.wuu.json"})

	for i := 0; i < 2; i++ {
		updated, _ := m.Update(workerNotifyMsg{notification: subagent.Notification{
			Status: subagent.StatusRunning,
			Snapshot: subagent.SubAgentSnapshot{
				ID:           "worker-1",
				Type:         "worker",
				Description:  "first",
				Status:       subagent.StatusRunning,
				InputTokens:  4 + i,
				OutputTokens: 1 + i,
			},
		}})
		m = updated.(Model)
	}

	if len(m.entries) != 1 {
		t.Fatalf("expected one spawned entry after duplicate running notifications, got %d", len(m.entries))
	}
	if !strings.Contains(m.entries[0].Content, "worker spawned: worker-1") {
		t.Fatalf("expected spawned entry content, got %q", m.entries[0].Content)
	}
}

func TestWorkerNotifyTerminalStatusesStillAppendEntries(t *testing.T) {
	cases := []struct {
		name   string
		status subagent.Status
		icon   string
		err    error
		suffix string
	}{
		{name: "completed", status: subagent.StatusCompleted, icon: "✓"},
		{name: "failed", status: subagent.StatusFailed, icon: "✗", err: fmt.Errorf("boom"), suffix: "boom"},
		{name: "cancelled", status: subagent.StatusCancelled, icon: "⊘"},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			m := NewModel(Config{Provider: "test", Model: "test-model", ConfigPath: "/tmp/.wuu.json"})
			updated, _ := m.Update(workerNotifyMsg{notification: subagent.Notification{
				Status: tc.status,
				Snapshot: subagent.SubAgentSnapshot{
					ID:          "worker-1",
					Type:        "worker",
					Description: "first",
					Status:      tc.status,
					Error:       tc.err,
				},
			}})
			m = updated.(Model)

			if len(m.entries) != 1 {
				t.Fatalf("expected one terminal entry, got %d", len(m.entries))
			}
			if !strings.Contains(m.entries[0].Content, tc.icon+" worker "+string(tc.status)+": first") {
				t.Fatalf("expected terminal entry for %s, got %q", tc.status, m.entries[0].Content)
			}
			if tc.suffix != "" && !strings.Contains(m.entries[0].Content, tc.suffix) {
				t.Fatalf("expected terminal entry suffix %q, got %q", tc.suffix, m.entries[0].Content)
			}
		})
	}
}

func TestSubmitPromptFlow(t *testing.T) {
	m := newTestModel(func(msgs []providers.ChatMessage) string {
		last := msgs[len(msgs)-1].Content
		return "answer to: " + last
	})

	m.input.SetValue("hello world")
	nextModel, cmd := m.submit(false)
	if cmd == nil {
		t.Fatal("expected async command from submit")
	}
	next := nextModel.(Model)
	if !next.pendingRequest {
		t.Fatal("expected pendingRequest=true after submit")
	}

	after := drainStream(t, next, cmd)
	content := renderEntries(after.entries)
	if !strings.Contains(content, "USER\nhello world") {
		t.Fatalf("missing user entry: %s", content)
	}
	if !strings.Contains(content, "ASSISTANT\nanswer to: hello world") {
		t.Fatalf("missing assistant entry: %s", content)
	}
}

func TestJumpToBottomToggle(t *testing.T) {
	m := NewModel(Config{
		Provider:   "test",
		Model:      "test-model",
		ConfigPath: "/tmp/.wuu.json",
		StreamRunner: &agent.StreamRunner{
			Client: &echoStreamClient{answer: func(msgs []providers.ChatMessage) string { return msgs[len(msgs)-1].Content }},
			Model:  "test-model",
		},
	})
	m.width = 100
	m.height = 16
	for i := 0; i < 30; i++ {
		m.appendEntry("assistant", "line")
	}
	m.relayout()
	m.refreshViewport(true)

	if m.showJump {
		t.Fatal("expected no jump hint at bottom")
	}

	updated, _ := m.Update(tea.KeyMsg{Type: tea.KeyPgUp})
	paged := updated.(Model)
	if !paged.showJump {
		t.Fatal("expected jump hint after page up")
	}

	updated, _ = paged.Update(tea.KeyMsg{Type: tea.KeyCtrlJ})
	jumped := updated.(Model)
	if jumped.showJump {
		t.Fatal("expected jump hint cleared after jump-to-bottom")
	}

	updated, _ = paged.Update(tea.MouseMsg{
		Action: tea.MouseActionPress,
		Button: tea.MouseButtonLeft,
		X:      paged.width - 5,
		Y:      0,
	})
	clicked := updated.(Model)
	if clicked.showJump {
		t.Fatal("expected jump hint cleared after mouse click")
	}
}

func TestRelayoutFitsWindow(t *testing.T) {
	m := NewModel(Config{
		Provider:   "test",
		Model:      "test-model",
		ConfigPath: "/tmp/.wuu.json",
		StreamRunner: &agent.StreamRunner{
			Client: &echoStreamClient{answer: func(msgs []providers.ChatMessage) string { return msgs[len(msgs)-1].Content }},
			Model:  "test-model",
		},
	})

	m.width = 80
	m.height = 24
	m.relayout()

	l := computeLayout(m.width, m.height, m.inputLines, 0, 0)
	totalHeight := l.Header.Height + l.Chat.Height + l.Input.Height
	if totalHeight > m.height {
		t.Fatalf("layout exceeds window height: used=%d window=%d", totalHeight, m.height)
	}

	// Chat has no border, viewport uses full width.
	if m.viewport.Width > m.width {
		t.Fatalf("layout exceeds window width: used=%d window=%d", m.viewport.Width, m.width)
	}
}

func TestMouseClickPositionsCursor(t *testing.T) {
	m := NewModel(Config{
		Provider:   "test",
		Model:      "test-model",
		ConfigPath: "/tmp/.wuu.json",
		StreamRunner: &agent.StreamRunner{
			Client: &echoStreamClient{answer: func(msgs []providers.ChatMessage) string { return msgs[len(msgs)-1].Content }},
			Model:  "test-model",
		},
	})
	m.width = 100
	m.height = 24
	m.relayout()

	m.input.SetValue("hello world")
	m.input.Blur()
	m.input.SetCursor(0) // cursor at start

	// Click inside the input area.
	// Prompt "> " adds 2 cols.
	// To hit text column 4, click at X = 2 (prompt) + 4 = 6.
	inputY := m.layout.Input.Y
	updated, _ := m.Update(tea.MouseMsg{
		Action: tea.MouseActionPress,
		Button: tea.MouseButtonLeft,
		X:      6,
		Y:      inputY,
	})
	pressed := updated.(Model)

	if !pressed.input.Focused() {
		t.Fatal("expected input to be focused after clicking input area")
	}

	// Release completes the click and positions the cursor.
	updated, _ = pressed.Update(tea.MouseMsg{
		Action: tea.MouseActionRelease,
		Button: tea.MouseButtonLeft,
		X:      6,
		Y:      inputY,
	})
	after := updated.(Model)

	li := after.input.LineInfo()
	if li.CharOffset != 4 {
		t.Fatalf("expected cursor at column 4, got %d", li.CharOffset)
	}
}

func TestMouseClickChatAreaFocusesInputOnRelease(t *testing.T) {
	m := newScrollableModelForScrollbarTest(t)
	m.input.Blur()

	updated, _ := m.Update(tea.MouseMsg{
		Action: tea.MouseActionPress,
		Button: tea.MouseButtonLeft,
		X:      m.layout.Chat.X + 2,
		Y:      m.layout.Chat.Y + 1,
	})
	pressed := updated.(Model)

	if !pressed.pendingChatClick.active {
		t.Fatal("expected chat press to start a pending click")
	}
	if pressed.selection.IsDragging {
		t.Fatal("expected chat press to avoid starting selection immediately")
	}
	if pressed.input.Focused() {
		t.Fatal("expected input to remain blurred until release")
	}

	updated, _ = pressed.Update(tea.MouseMsg{
		Action: tea.MouseActionRelease,
		Button: tea.MouseButtonLeft,
		X:      m.layout.Chat.X + 2,
		Y:      m.layout.Chat.Y + 1,
	})
	after := updated.(Model)

	if !after.input.Focused() {
		t.Fatal("expected chat click release to focus input")
	}
	if after.pendingChatClick.active {
		t.Fatal("expected pending click to clear after release")
	}
	if after.selection.IsDragging {
		t.Fatal("expected no selection drag after plain click")
	}
}

func TestMouseDragChatAreaStartsSelectionAfterThreshold(t *testing.T) {
	m := newScrollableModelForScrollbarTest(t)
	m.input.Focus()
	pressX := m.layout.Chat.X + 2
	pressY := m.layout.Chat.Y + 1

	updated, _ := m.Update(tea.MouseMsg{
		Action: tea.MouseActionPress,
		Button: tea.MouseButtonLeft,
		X:      pressX,
		Y:      pressY,
	})
	pressed := updated.(Model)

	updated, _ = pressed.Update(tea.MouseMsg{
		Action: tea.MouseActionMotion,
		Button: tea.MouseButtonLeft,
		X:      pressX + chatSelectionDragThreshold + 1,
		Y:      pressY,
	})
	after := updated.(Model)

	if after.pendingChatClick.active {
		t.Fatal("expected drag to clear pending click")
	}
	if !after.selection.IsDragging {
		t.Fatal("expected drag past threshold to start selection")
	}
	if after.selection.Anchor == nil {
		t.Fatal("expected selection anchor to be set")
	}
	if after.selection.Anchor.Row != after.viewport.YOffset+1 {
		t.Fatalf("expected anchor row %d, got %d", after.viewport.YOffset+1, after.selection.Anchor.Row)
	}
	if after.input.Focused() {
		t.Fatal("expected input to blur once selection drag begins")
	}
}

func TestFocusAndBlurMessagesUpdateInputState(t *testing.T) {
	m := NewModel(Config{
		Provider:   "test",
		Model:      "test-model",
		ConfigPath: "/tmp/.wuu.json",
		StreamRunner: &agent.StreamRunner{
			Client: &echoStreamClient{answer: func(msgs []providers.ChatMessage) string { return msgs[len(msgs)-1].Content }},
			Model:  "test-model",
		},
	})
	m.input.Blur()

	updated, _ := m.Update(tea.FocusMsg{})
	afterFocus := updated.(Model)
	if !afterFocus.input.Focused() {
		t.Fatal("expected input focused after tea.FocusMsg")
	}

	updated, _ = afterFocus.Update(tea.BlurMsg{})
	afterBlur := updated.(Model)
	if afterBlur.input.Focused() {
		t.Fatal("expected input blurred after tea.BlurMsg")
	}
}

func TestMouseClickPositionsCursorMultiLine(t *testing.T) {
	m := NewModel(Config{
		Provider:   "test",
		Model:      "test-model",
		ConfigPath: "/tmp/.wuu.json",
		StreamRunner: &agent.StreamRunner{
			Client: &echoStreamClient{answer: func(msgs []providers.ChatMessage) string { return msgs[len(msgs)-1].Content }},
			Model:  "test-model",
		},
	})
	m.width = 100
	m.height = 24
	m.relayout()

	m.input.SetValue("first line\nsecond line")
	m.input.CursorStart() // cursor at start of first line

	// Click on second line (row 1), column 3.
	promptW := 2
	inputY := m.layout.Input.Y + 1 // +1 for second row
	clickX := m.layout.Input.X + promptW + 3

	updated, _ := m.Update(tea.MouseMsg{
		Action: tea.MouseActionPress,
		Button: tea.MouseButtonLeft,
		X:      clickX,
		Y:      inputY,
	})
	pressed := updated.(Model)

	// Release to complete click and position cursor.
	updated, _ = pressed.Update(tea.MouseMsg{
		Action: tea.MouseActionRelease,
		Button: tea.MouseButtonLeft,
		X:      clickX,
		Y:      inputY,
	})
	after := updated.(Model)

	if after.input.Line() != 1 {
		t.Fatalf("expected cursor on line 1, got %d", after.input.Line())
	}
	li := after.input.LineInfo()
	if li.CharOffset != 3 {
		t.Fatalf("expected cursor at column 3, got %d", li.CharOffset)
	}
}

func TestMouseDragScrollbarThumbTracksMotion(t *testing.T) {
	m := newScrollableModelForScrollbarTest(t)

	thumbPos, thumbSize, trackSpace, maxOffset, ok := scrollbarThumbGeometry(
		m.layout.Chat.Height,
		m.viewport.TotalLineCount(),
		m.viewport.Height,
		m.viewport.YOffset,
	)
	if !ok {
		t.Fatal("expected visible scrollbar thumb")
	}
	if thumbSize < 2 {
		t.Fatalf("expected thumb size >= 2 for midpoint drag test, got %d", thumbSize)
	}

	clickX := m.layout.Chat.X + m.layout.Chat.Width - 1
	grabRow := thumbPos + thumbSize/2
	pressY := m.layout.Chat.Y + grabRow

	updated, _ := m.Update(tea.MouseMsg{
		Action: tea.MouseActionPress,
		Button: tea.MouseButtonLeft,
		X:      clickX,
		Y:      pressY,
	})
	dragging := updated.(Model)
	if !dragging.scrollbarDragging {
		t.Fatal("expected scrollbarDragging=true after pressing thumb")
	}
	if dragging.scrollbarDragGrabOffset != grabRow-thumbPos {
		t.Fatalf("expected grab offset %d, got %d", grabRow-thumbPos, dragging.scrollbarDragGrabOffset)
	}

	targetRow := min(dragging.layout.Chat.Height-1, grabRow+3)
	updated, _ = dragging.Update(tea.MouseMsg{
		Action: tea.MouseActionMotion,
		Button: tea.MouseButtonLeft,
		X:      dragging.layout.Chat.X,
		Y:      dragging.layout.Chat.Y + targetRow,
	})
	afterMotion := updated.(Model)
	want := scrollbarOffsetForThumbPos(targetRow-dragging.scrollbarDragGrabOffset, trackSpace, maxOffset)
	if afterMotion.viewport.YOffset != want {
		t.Fatalf("expected viewport offset %d after drag motion, got %d", want, afterMotion.viewport.YOffset)
	}
	if !afterMotion.scrollbarDragging {
		t.Fatal("expected scrollbarDragging=true during drag")
	}

	updated, _ = afterMotion.Update(tea.MouseMsg{
		Action: tea.MouseActionRelease,
		Button: tea.MouseButtonLeft,
		X:      clickX,
		Y:      dragging.layout.Chat.Y + targetRow,
	})
	afterRelease := updated.(Model)
	if afterRelease.scrollbarDragging {
		t.Fatal("expected scrollbarDragging=false after release")
	}
}

func TestMouseClickScrollbarTrackJumpsProportionally(t *testing.T) {
	m := newScrollableModelForScrollbarTest(t)

	thumbPos, thumbSize, trackSpace, maxOffset, ok := scrollbarThumbGeometry(
		m.layout.Chat.Height,
		m.viewport.TotalLineCount(),
		m.viewport.Height,
		m.viewport.YOffset,
	)
	if !ok {
		t.Fatal("expected visible scrollbar thumb")
	}

	row := thumbPos + thumbSize
	if row >= m.layout.Chat.Height {
		row = thumbPos - 1
	}
	if row < 0 || row >= m.layout.Chat.Height {
		t.Fatalf("failed to choose a track row outside thumb: row=%d", row)
	}

	absoluteTarget := scrollbarOffsetForThumbPos(row-thumbSize/2, trackSpace, maxOffset)
	clickX := m.layout.Chat.X + m.layout.Chat.Width - 1
	clickY := m.layout.Chat.Y + row
	updated, _ := m.Update(tea.MouseMsg{
		Action: tea.MouseActionPress,
		Button: tea.MouseButtonLeft,
		X:      clickX,
		Y:      clickY,
	})
	after := updated.(Model)
	if after.scrollbarDragging {
		t.Fatal("expected scrollbarDragging=false after track click")
	}
	if after.viewport.YOffset <= m.viewport.YOffset {
		t.Fatalf("expected track click to move toward target, before=%d after=%d", m.viewport.YOffset, after.viewport.YOffset)
	}
	if absoluteTarget-m.viewport.YOffset > m.viewport.Height && after.viewport.YOffset >= absoluteTarget {
		t.Fatalf("expected softened track click to stop before absolute target %d, got %d", absoluteTarget, after.viewport.YOffset)
	}
}

func TestMouseDragScrollbarReanchorsWhenThumbGeometryChanges(t *testing.T) {
	m := newScrollableModelForScrollbarTest(t)
	maxOffset := max(0, m.viewport.TotalLineCount()-m.viewport.Height)
	m.setViewportOffset(maxOffset / 2)

	thumbPos, thumbSize, _, _, ok := scrollbarThumbGeometry(
		m.layout.Chat.Height,
		m.viewport.TotalLineCount(),
		m.viewport.Height,
		m.viewport.YOffset,
	)
	if !ok {
		t.Fatal("expected visible scrollbar thumb")
	}
	grabRow := thumbPos
	if thumbSize > 1 {
		grabRow = thumbPos + thumbSize/2
	}
	clickX := m.layout.Chat.X + m.layout.Chat.Width - 1
	pressY := m.layout.Chat.Y + grabRow
	updated, _ := m.Update(tea.MouseMsg{
		Action: tea.MouseActionPress,
		Button: tea.MouseButtonLeft,
		X:      clickX,
		Y:      pressY,
	})
	dragging := updated.(Model)
	if !dragging.scrollbarDragging {
		t.Fatal("expected scrollbarDragging=true after thumb press")
	}
	offsetBeforeGrowth := dragging.viewport.YOffset

	dragging.appendEntry("ASSISTANT", strings.Repeat("new line\n", 120)+"end")
	dragging.refreshViewport(false)
	updated, _ = dragging.Update(tea.MouseMsg{
		Action: tea.MouseActionMotion,
		Button: tea.MouseButtonLeft,
		X:      clickX,
		Y:      pressY,
	})
	after := updated.(Model)

	diff := after.viewport.YOffset - offsetBeforeGrowth
	if diff < 0 {
		diff = -diff
	}
	if diff > 2 {
		t.Fatalf("expected offset stable after geometry change with zero drag delta, got before=%d after=%d", offsetBeforeGrowth, after.viewport.YOffset)
	}
}

func TestMouseMotionScrollbarHoverState(t *testing.T) {
	m := newScrollableModelForScrollbarTest(t)
	rightX := m.layout.Chat.X + m.layout.Chat.Width - 1
	hoverRow := min(2, m.layout.Chat.Height-1)

	updated, _ := m.Update(tea.MouseMsg{
		Action: tea.MouseActionMotion,
		Button: tea.MouseButtonNone,
		X:      rightX,
		Y:      m.layout.Chat.Y + hoverRow,
	})
	hovered := updated.(Model)
	if !hovered.scrollbarHoverActive {
		t.Fatal("expected hover active while pointer is on scrollbar")
	}
	if hovered.scrollbarHoverRow != hoverRow {
		t.Fatalf("expected hover row %d, got %d", hoverRow, hovered.scrollbarHoverRow)
	}

	updated, _ = hovered.Update(tea.MouseMsg{
		Action: tea.MouseActionMotion,
		Button: tea.MouseButtonNone,
		X:      hovered.layout.Chat.X,
		Y:      hovered.layout.Chat.Y + hoverRow,
	})
	afterLeave := updated.(Model)
	if afterLeave.scrollbarHoverActive {
		t.Fatal("expected hover inactive after leaving scrollbar")
	}
	if afterLeave.scrollbarHoverRow != -1 {
		t.Fatalf("expected hover row reset to -1, got %d", afterLeave.scrollbarHoverRow)
	}
}

func TestMouseHoverScrollbarWithinHitboxTolerance(t *testing.T) {
	m := newScrollableModelForScrollbarTest(t)
	rightX := m.layout.Chat.X + m.layout.Chat.Width - 1
	leftToleranceX := rightX - 1
	if leftToleranceX < m.layout.Chat.X {
		leftToleranceX = m.layout.Chat.X
	}
	hoverY := m.layout.Chat.Y + min(2, m.layout.Chat.Height-1)

	updated, _ := m.Update(tea.MouseMsg{
		Action: tea.MouseActionMotion,
		Button: tea.MouseButtonNone,
		X:      leftToleranceX,
		Y:      hoverY,
	})
	after := updated.(Model)
	if !after.scrollbarHoverActive {
		t.Fatal("expected hover active inside scrollbar hitbox tolerance")
	}
}

func TestMouseAltClickScrollbarAnchorJumpsToUserMessage(t *testing.T) {
	m := NewModel(Config{
		Provider:   "test",
		Model:      "test-model",
		ConfigPath: "/tmp/.wuu.json",
		StreamRunner: &agent.StreamRunner{
			Client: &echoStreamClient{answer: func(msgs []providers.ChatMessage) string { return msgs[len(msgs)-1].Content }},
			Model:  "test-model",
		},
	})
	m.width = 100
	m.height = 20
	m.relayout()

	for i := 0; i < 3; i++ {
		m.appendEntry("USER", fmt.Sprintf("user %d", i))
		m.appendEntry("ASSISTANT", strings.Repeat("line\n", 20)+"end")
	}
	m.refreshViewport(false)

	if len(m.userMessageLineAnchors) < 2 {
		t.Fatalf("expected at least 2 user anchors, got %d", len(m.userMessageLineAnchors))
	}
	maxOffset := max(0, m.viewport.TotalLineCount()-m.viewport.Height)
	target := 1
	if m.userMessageLineAnchors[target] >= maxOffset {
		target = 0
	}
	anchorRows := contentLinesToScrollbarRows(
		m.userMessageLineAnchors,
		m.layout.Chat.Height,
		m.viewport.TotalLineCount(),
	)
	clickX := m.layout.Chat.X + m.layout.Chat.Width - 1
	clickY := m.layout.Chat.Y + anchorRows[target]

	updated, _ := m.Update(tea.MouseMsg{
		Action: tea.MouseActionPress,
		Button: tea.MouseButtonLeft,
		Alt:    true,
		X:      clickX,
		Y:      clickY,
	})
	after := updated.(Model)

	want := after.userMessageLineAnchors[target]
	maxOffset = max(0, after.viewport.TotalLineCount()-after.viewport.Height)
	if want > maxOffset {
		want = maxOffset
	}
	if after.viewport.YOffset != want {
		t.Fatalf("expected viewport offset %d after anchor click, got %d", want, after.viewport.YOffset)
	}
}

// TestMouseDragSelectionAutoScrollsPastEdge covers the bug where a
// drag-select held past the chat viewport's bottom edge couldn't
// extend the selection into off-screen content. The motion handler
// must (a) scroll the viewport on each motion event that lands
// outside the chat area, and (b) keep scrolling on a recurring
// tick when the user holds the cursor still past the edge — that
// second part is the part the user explicitly asked for.
func TestMouseDragSelectionAutoScrollsPastEdge(t *testing.T) {
	m := newScrollableModelForScrollbarTest(t)
	m.setViewportOffset(0)
	startOffset := m.viewport.YOffset

	// Press inside the chat area to begin a selection.
	pressX := m.layout.Chat.X + 2
	pressY := m.layout.Chat.Y + 1
	updated, _ := m.Update(tea.MouseMsg{
		Action: tea.MouseActionPress,
		Button: tea.MouseButtonLeft,
		X:      pressX,
		Y:      pressY,
	})
	pressed := updated.(Model)
	if !pressed.pendingChatClick.active {
		t.Fatal("expected pending click after press in chat area")
	}

	// Drag past the bottom of the chat area. The motion handler
	// must scroll the viewport AND start the auto-scroll ticker.
	belowY := pressed.layout.Chat.Y + pressed.layout.Chat.Height + 2
	updated, cmd := pressed.Update(tea.MouseMsg{
		Action: tea.MouseActionMotion,
		Button: tea.MouseButtonLeft,
		X:      pressX + 4,
		Y:      belowY,
	})
	afterMotion := updated.(Model)
	if afterMotion.viewport.YOffset <= startOffset {
		t.Fatalf("expected motion past edge to scroll viewport: start=%d after=%d",
			startOffset, afterMotion.viewport.YOffset)
	}
	if !afterMotion.selection.IsDragging {
		t.Fatal("expected motion past threshold to start selection drag")
	}
	if !afterMotion.selectionAutoScroll.active {
		t.Fatal("expected auto-scroll state to be active after dragging past edge")
	}
	if afterMotion.selectionAutoScroll.dir != 1 {
		t.Fatalf("expected dir=+1 (down), got %d", afterMotion.selectionAutoScroll.dir)
	}
	if cmd == nil {
		t.Fatal("expected motion past edge to return a tick Cmd")
	}

	// Now simulate "user holds the mouse still past the edge":
	// no further motion events arrive, but the recurring tick
	// must keep advancing the viewport AND extending the
	// selection focus into newly-revealed content.
	offsetBeforeTick := afterMotion.viewport.YOffset
	maxOffset := max(0, afterMotion.viewport.TotalLineCount()-afterMotion.viewport.Height)

	current := afterMotion
	for i := 0; i < 3 && current.viewport.YOffset < maxOffset; i++ {
		next, _ := current.Update(selectionAutoScrollMsg{seq: current.selectionAutoScroll.seq})
		current = next.(Model)
	}

	if current.viewport.YOffset <= offsetBeforeTick {
		t.Fatalf("expected ticks with no further motion to keep scrolling: before=%d after=%d",
			offsetBeforeTick, current.viewport.YOffset)
	}
	if !current.selection.hasSelection() {
		t.Fatal("expected selection to be extended by auto-scroll ticks")
	}
	// Focus row should track the bottom edge of the (now scrolled) viewport.
	wantFocusRow := current.viewport.YOffset + current.layout.Chat.Height - 1
	if current.selection.Focus == nil || current.selection.Focus.Row != wantFocusRow {
		gotRow := -1
		if current.selection.Focus != nil {
			gotRow = current.selection.Focus.Row
		}
		t.Fatalf("expected focus row %d after auto-scroll, got %d", wantFocusRow, gotRow)
	}

	// Stale ticks (from a previous burst, seq mismatch) must be no-ops.
	staleSeq := current.selectionAutoScroll.seq - 1
	offsetBeforeStale := current.viewport.YOffset
	updated, _ = current.Update(selectionAutoScrollMsg{seq: staleSeq})
	stale := updated.(Model)
	if stale.viewport.YOffset != offsetBeforeStale {
		t.Fatalf("expected stale tick to be a no-op: before=%d after=%d",
			offsetBeforeStale, stale.viewport.YOffset)
	}

	// Moving the cursor back inside the viewport must stop the
	// ticker (active=false, seq bumped so any in-flight tick exits).
	insideY := current.layout.Chat.Y + 1
	updated, _ = current.Update(tea.MouseMsg{
		Action: tea.MouseActionMotion,
		Button: tea.MouseButtonLeft,
		X:      pressX + 4,
		Y:      insideY,
	})
	stopped := updated.(Model)
	if stopped.selectionAutoScroll.active {
		t.Fatal("expected auto-scroll to stop after cursor returned inside viewport")
	}

	// Release should also clear the auto-scroll state defensively.
	updated, _ = stopped.Update(tea.MouseMsg{
		Action: tea.MouseActionRelease,
		Button: tea.MouseButtonLeft,
		X:      pressX + 4,
		Y:      insideY,
	})
	released := updated.(Model)
	if released.selection.IsDragging {
		t.Fatal("expected drag to finish after release")
	}
	if released.selectionAutoScroll.active {
		t.Fatal("expected auto-scroll to be cleared after release")
	}
}

func TestMouseSelectionDrainsQueuedStreamEvents(t *testing.T) {
	m := newScrollableModelForScrollbarTest(t)
	m.streaming = true
	m.pendingRequest = true
	m.streamTarget = len(m.entries) - 1
	m.streamCh = make(chan providers.StreamEvent, 4)

	pressX := m.layout.Chat.X + 2
	pressY := m.layout.Chat.Y + 1
	updated, _ := m.Update(tea.MouseMsg{
		Action: tea.MouseActionPress,
		Button: tea.MouseButtonLeft,
		X:      pressX,
		Y:      pressY,
	})
	dragging := updated.(Model)
	dragging.streamCh <- providers.StreamEvent{
		Type:    providers.EventContentDelta,
		Content: "\nqueued update",
	}
	before := dragging.entries[dragging.streamTarget].Content

	updated, _ = dragging.Update(tea.MouseMsg{
		Action: tea.MouseActionMotion,
		Button: tea.MouseButtonLeft,
		X:      pressX + 3,
		Y:      pressY + 1,
	})
	after := updated.(Model)

	if !strings.Contains(after.entries[after.streamTarget].Content, "queued update") {
		t.Fatalf("expected queued stream delta to be applied during mouse drag, got %q", after.entries[after.streamTarget].Content)
	}
	if after.entries[after.streamTarget].Content == before {
		t.Fatal("expected streaming content to advance during selection drag")
	}
	if !after.selection.IsDragging {
		t.Fatal("expected drag selection to remain active")
	}
}

func TestSetCopyStatusLine_PreservesActiveStreamingStatus(t *testing.T) {
	m := NewModel(Config{
		Provider:   "test",
		Model:      "test-model",
		ConfigPath: "/tmp/.wuu.json",
		StreamRunner: &agent.StreamRunner{
			Client: &echoStreamClient{answer: func(msgs []providers.ChatMessage) string { return msgs[len(msgs)-1].Content }},
			Model:  "test-model",
		},
	})
	m.streaming = true
	m.pendingRequest = true
	m.statusLine = "streaming"

	m.setCopyStatusLine("copied")

	if m.statusLine != "streaming" {
		t.Fatalf("expected active streaming status to be preserved, got %q", m.statusLine)
	}
}

func TestSetCopyStatusLine_UpdatesIdleStatus(t *testing.T) {
	m := NewModel(Config{
		Provider:   "test",
		Model:      "test-model",
		ConfigPath: "/tmp/.wuu.json",
		StreamRunner: &agent.StreamRunner{
			Client: &echoStreamClient{answer: func(msgs []providers.ChatMessage) string { return msgs[len(msgs)-1].Content }},
			Model:  "test-model",
		},
	})
	m.statusLine = "ready"

	m.setCopyStatusLine("copied")

	if m.statusLine != "copied" {
		t.Fatalf("expected idle status to be updated with copy feedback, got %q", m.statusLine)
	}
}

func TestSetCopyStatusLine_PreservesStructuredLiveWorkStatus(t *testing.T) {
	m := NewModel(Config{
		Provider:   "test",
		Model:      "test-model",
		ConfigPath: "/tmp/.wuu.json",
		StreamRunner: &agent.StreamRunner{
			Client: &echoStreamClient{answer: func(msgs []providers.ChatMessage) string { return msgs[len(msgs)-1].Content }},
			Model:  "test-model",
		},
	})
	m.liveWorkStatus = workStatus{Phase: workPhaseGenerating, Label: "Responding", Meta: "Writing the reply", Running: true}
	m.statusLine = "ready"

	m.setCopyStatusLine("copied")

	if m.statusLine != "ready" {
		t.Fatalf("expected structured live work status to block copy feedback, got %q", m.statusLine)
	}
}

func TestRefreshViewportKeepsOffsetWhileStreamingWhenUserScrolledUp(t *testing.T) {
	m := newScrollableModelForScrollbarTest(t)
	m.streaming = true
	m.pendingRequest = true
	m.streamTarget = len(m.entries) - 1
	m.setViewportOffset(4)
	m.autoFollow = false
	m.showJump = true
	beforeOffset := m.viewport.YOffset

	m.entries[m.streamTarget].Content += "\nstream update\nmore output"
	m.refreshViewport(false)

	if m.viewport.YOffset != beforeOffset {
		t.Fatalf("expected viewport offset to stay at %d while scrolled up, got %d", beforeOffset, m.viewport.YOffset)
	}
	if m.autoFollow {
		t.Fatal("expected autoFollow to remain false while user is away from bottom")
	}
}

func TestRefreshViewportFollowsBottomWhileStreamingAtBottom(t *testing.T) {
	m := newScrollableModelForScrollbarTest(t)
	m.streaming = true
	m.pendingRequest = true
	m.streamTarget = len(m.entries) - 1
	m.refreshViewport(true)
	if !m.viewport.AtBottom() {
		t.Fatal("expected initial viewport at bottom")
	}
	m.autoFollow = true
	beforeOffset := m.viewport.YOffset

	m.entries[m.streamTarget].Content += "\nstream update\nmore output\nand even more"
	m.refreshViewport(false)

	if !m.viewport.AtBottom() {
		t.Fatal("expected viewport to keep following bottom during streaming")
	}
	if m.viewport.YOffset < beforeOffset {
		t.Fatalf("expected bottom-follow offset to stay at or below previous bottom, before=%d after=%d", beforeOffset, m.viewport.YOffset)
	}
	if !m.autoFollow {
		t.Fatal("expected autoFollow to remain true at bottom")
	}
}

func TestApplyStreamEvent_AccumulatesWithoutViewportRefresh(t *testing.T) {
	m := newScrollableModelForScrollbarTest(t)
	m.streaming = true
	m.pendingRequest = true
	m.streamTarget = len(m.entries) - 1
	m.statusLine = "streaming"
	m.setLiveWorkStatus(workStatus{Phase: workPhaseGenerating, Label: "Responding", Meta: "Writing the reply", Running: true})
	m.refreshViewport(true)

	m.viewport.SetYOffset(0)
	m.autoFollow = false
	m.showJump = true

	const marker = "STREAM_OFFSCREEN_REBUILD_CANARY"
	m.entries[0].Content = marker

	_ = m.applyStreamEvent(providers.StreamEvent{
		Type:    providers.EventContentDelta,
		Content: "\nmore offscreen output\n",
	}, false)

	// Content delta should NOT trigger a viewport refresh — it just
	// accumulates in the entry and stream collector. The 100ms tick
	// flushes to screen.
	if strings.Contains(m.viewport.View(), marker) {
		t.Fatal("stream delta must not rebuild the visible viewport content")
	}
	if !strings.Contains(m.entries[m.streamTarget].Content, "more offscreen output") {
		t.Fatalf("expected stream content to accumulate, got %q", m.entries[m.streamTarget].Content)
	}
}

func TestShouldRenderInlineStatus_WhenRunningTranscriptStatusIsOffscreen(t *testing.T) {
	m := newScrollableModelForScrollbarTest(t)
	m.streaming = true
	m.pendingRequest = true
	m.streamTarget = len(m.entries) - 1
	m.statusLine = "thinking"
	m.setLiveWorkStatus(workStatus{Phase: workPhaseThinking, Label: "Thinking", Meta: "Working through the next step", Running: true})
	m.entries[m.streamTarget].ThinkingContent = "inspect repo"
	m.entries[m.streamTarget].ThinkingDone = false
	m.refreshViewport(true)

	m.viewport.SetYOffset(0)
	m.autoFollow = false
	m.showJump = true

	if !m.shouldRenderInlineStatus() {
		t.Fatal("expected inline status to stay visible when the running thinking block is offscreen")
	}

	m.viewport.GotoBottom()
	m.syncViewportState()

	if m.shouldRenderInlineStatus() {
		t.Fatal("expected inline status to hide once the running thinking block is visible again")
	}
}

func newScrollableModelForScrollbarTest(t *testing.T) Model {
	t.Helper()
	m := NewModel(Config{
		Provider:   "test",
		Model:      "test-model",
		ConfigPath: "/tmp/.wuu.json",
		StreamRunner: &agent.StreamRunner{
			Client: &echoStreamClient{answer: func(msgs []providers.ChatMessage) string { return msgs[len(msgs)-1].Content }},
			Model:  "test-model",
		},
	})
	m.width = 100
	m.height = 20
	m.relayout()

	for i := 0; i < 3; i++ {
		m.appendEntry("USER", fmt.Sprintf("user %d", i))
		m.appendEntry("ASSISTANT", strings.Repeat("line\n", 20)+"end")
	}
	m.refreshViewport(false)
	m.setViewportOffset(0)
	return m
}

func renderEntries(entries []transcriptEntry) string {
	var b strings.Builder
	for i, e := range entries {
		if i > 0 {
			b.WriteString("\n\n")
		}
		b.WriteString(e.Role)
		b.WriteString("\n")
		b.WriteString(e.Content)
	}
	return b.String()
}

func TestRenderInlineStatus_AnimatesAcrossFrames(t *testing.T) {
	frameARaw := renderInlineStatus("streaming", statusShimmerPadding, 80)
	frameBRaw := renderInlineStatus("streaming", statusShimmerPadding+3, 80)
	if frameARaw == frameBRaw {
		t.Fatalf("expected different frames to render differently: %q", frameARaw)
	}
	frameA := ansi.Strip(frameARaw)
	frameB := ansi.Strip(frameBRaw)
	if !strings.Contains(frameA, "Responding") {
		t.Fatalf("expected label to remain visible in frame A: %q", frameA)
	}
	if !strings.Contains(frameB, "Responding") {
		t.Fatalf("expected label to remain visible in frame B: %q", frameB)
	}
	if count := strings.Count(frameA, "Responding"); count != 1 {
		t.Fatalf("expected frame A to render one responding label, got %d in %q", count, frameA)
	}
	if count := strings.Count(frameB, "Responding"); count != 1 {
		t.Fatalf("expected frame B to render one responding label, got %d in %q", count, frameB)
	}
}

func TestRenderInlineStatus_UsesItalicSentence(t *testing.T) {
	raw := renderInlineStatus("streaming", 0, 80)
	reItalic := regexp.MustCompile(`\x1b\[(?:\d{1,3};)*3(?:;\d{1,3})*m`)
	if !reItalic.MatchString(raw) {
		t.Fatalf("expected italic ANSI style in inline status, got %q", raw)
	}
	got := ansi.Strip(raw)
	if !strings.Contains(got, "Responding · Writing the reply") {
		t.Fatalf("expected italic full sentence in inline status, got %q", got)
	}
}

func TestRenderInlineStatus_UsesWaveUnderline(t *testing.T) {
	reUnderline := regexp.MustCompile(`\x1b\[(?:\d{1,3};)*4(?:;\d{1,3})*m`)
	cycle := statusShimmerCycleLength(statusTextSegments(deriveWorkStatus("streaming"), true))
	for frame := 0; frame < cycle; frame++ {
		raw := renderInlineStatus("streaming", frame, 80)
		if reUnderline.MatchString(raw) {
			return
		}
	}
	t.Fatal("expected at least one inline status frame to include underline for wave crest")
}

func TestRenderInlineStatus_ShimmerContinuesIntoMeta(t *testing.T) {
	frameAtLabelCycle := renderInlineStatus("streaming", len([]rune("Responding"))+statusShimmerPadding, 80)
	frameAtStart := renderInlineStatus("streaming", 0, 80)
	if frameAtLabelCycle == frameAtStart {
		t.Fatalf("expected shimmer to continue past the label into meta text instead of restarting")
	}
	got := ansi.Strip(frameAtLabelCycle)
	if !strings.Contains(got, "Responding · Writing the reply") {
		t.Fatalf("expected full status sentence in output, got %q", got)
	}
}

func TestRenderInlineWorkStatus_IncludesDetail(t *testing.T) {
	raw := renderInlineWorkStatus(workStatus{
		Phase:   workPhaseReconnecting,
		Label:   "Reconnecting... 2/5",
		Meta:    "Stream timed out",
		Detail:  "Retrying in 1.5s",
		Running: true,
	}, 0, 120)
	got := ansi.Strip(raw)
	if !strings.Contains(got, "Reconnecting... 2/5") {
		t.Fatalf("expected reconnect label in output, got %q", got)
	}
	if !strings.Contains(got, "Stream timed out") {
		t.Fatalf("expected reconnect reason in output, got %q", got)
	}
	if !strings.Contains(got, "Retrying in 1.5s") {
		t.Fatalf("expected reconnect retry delay in output, got %q", got)
	}
}

func TestStatusShimmerCycleLength_CoversWholeRespondingSentence(t *testing.T) {
	ws := deriveWorkStatus("streaming")
	segments := statusTextSegments(ws, true)
	want := len([]rune("Responding · Writing the reply")) + statusShimmerPadding*2
	if got := statusShimmerCycleLength(segments); got != want {
		t.Fatalf("expected full sentence shimmer cycle length %d, got %d", want, got)
	}
}

func TestNextStatusFrame_CoversWholeRespondingShimmerCycle(t *testing.T) {
	frame := 0
	cycle := statusShimmerCycleLength(statusTextSegments(deriveWorkStatus("streaming"), true))
	for i := 0; i < cycle; i++ {
		frame = nextStatusFrame(frame)
	}
	if frame != cycle {
		t.Fatalf("expected shimmer frame to advance across the whole cycle: got %d want %d", frame, cycle)
	}
}

func TestStatusWavePhaseOscillates(t *testing.T) {
	positive := false
	negative := false
	for idx := 0; idx < 16; idx++ {
		phase := statusWavePhase(idx, 12)
		if phase > 0.3 {
			positive = true
		}
		if phase < -0.3 {
			negative = true
		}
	}
	if !positive || !negative {
		t.Fatalf("expected wave phase to oscillate across glyphs, got positive=%v negative=%v", positive, negative)
	}
}

func TestRenderInlineStatus_ShowsWaitingLabels(t *testing.T) {
	cases := map[string]string{
		"thinking":             "Thinking",
		"streaming":            "Responding",
		"tool: run_shell":      "Running run_shell",
		"executing tool: read": "Running read",
		"tool: spawn_agent":    "Spawning worker",
		"tool: fork_agent":     "Spawning worker",
	}
	for status, want := range cases {
		got := ansi.Strip(renderInlineStatus(status, 1, 80))
		if !strings.Contains(got, strings.ReplaceAll(want, "  ", " ")) {
			t.Fatalf("status %q rendered %q, want label containing %q", status, got, want)
		}
	}
}

func TestRenderInlineStatus_HidesForNonWaitingStatus(t *testing.T) {
	if got := renderInlineStatus("ready", 0, 80); got != "" {
		t.Fatalf("expected non-waiting status to render empty string, got %q", got)
	}
	if got := renderInlineStatus("response in 1.2s", 0, 80); got != "" {
		t.Fatalf("expected completed status to render empty string, got %q", got)
	}
}

func TestRenderThinkingBlock_Active(t *testing.T) {
	result := ansi.Strip(renderThinkingBlock("analyzing...", false, false, 2*time.Second, 80, 0))
	if !strings.Contains(result, "Thinking") {
		t.Fatalf("expected 'Thinking' in output: %s", result)
	}
	if !strings.Contains(result, "Elapsed 2.0s") {
		t.Fatalf("expected elapsed time in output: %s", result)
	}
}

func TestRenderThinkingBlock_Done_Collapsed(t *testing.T) {
	result := ansi.Strip(renderThinkingBlock("analyzed the code", true, false, 3200*time.Millisecond, 80, 0))
	if !strings.Contains(result, "Thinking complete") {
		t.Fatalf("expected completed label in output: %s", result)
	}
	if !strings.Contains(result, "Finished in 3.2s") {
		t.Fatalf("expected duration in output: %s", result)
	}
	// Content should show as a short preview when not expanded.
	if !strings.Contains(result, "analyzed the code") {
		t.Fatalf("expected preview content when collapsed: %s", result)
	}
}

func TestRenderThinkingBlock_Done_Expanded(t *testing.T) {
	result := ansi.Strip(renderThinkingBlock("analyzed the code", true, true, 3200*time.Millisecond, 80, 0))
	if !strings.Contains(result, "Thinking complete") {
		t.Fatalf("expected completed label in output: %s", result)
	}
	if !strings.Contains(result, "analyzed the code") {
		t.Fatalf("content should be visible when expanded: %s", result)
	}
}

func TestRenderToolCard_Running(t *testing.T) {
	tc := ToolCallEntry{
		Name:   "run_shell",
		Args:   `{"cmd":"go build ./..."}`,
		Status: ToolCallRunning,
	}
	result := ansi.Strip(renderToolCard(&tc, 80, 0))
	if !strings.Contains(result, "run_shell") {
		t.Fatalf("expected tool name in output: %s", result)
	}
	if !strings.Contains(result, "Calling") {
		t.Fatalf("expected 'Calling' verb for running tool: %s", result)
	}
}

func TestNewModel_RequestTimeout(t *testing.T) {
	timeout := 2 * time.Second
	m := NewModel(Config{
		Provider:       "test",
		Model:          "test-model",
		ConfigPath:     "/tmp/.wuu.json",
		RequestTimeout: timeout,
		StreamRunner: &agent.StreamRunner{
			Client: &echoStreamClient{answer: func(msgs []providers.ChatMessage) string { return msgs[len(msgs)-1].Content }},
			Model:  "test-model",
		},
	})

	if m.requestTimeout != timeout {
		t.Fatalf("expected requestTimeout=%s, got %s", timeout, m.requestTimeout)
	}
}

func TestNewRequestContext_NoTimeoutHasNoDeadline(t *testing.T) {
	m := Model{}
	ctx, cancel := m.newRequestContext()
	defer cancel()

	if _, ok := ctx.Deadline(); ok {
		t.Fatal("expected no deadline when request timeout is disabled")
	}
}

func TestNewRequestContext_WithTimeoutSetsDeadline(t *testing.T) {
	m := Model{requestTimeout: 2 * time.Second}
	ctx, cancel := m.newRequestContext()
	defer cancel()

	if _, ok := ctx.Deadline(); !ok {
		t.Fatal("expected deadline when request timeout is configured")
	}
}

func TestRenderToolCard_Done_Collapsed(t *testing.T) {
	tc := ToolCallEntry{
		Name:      "read_file",
		Args:      `{"path":"model.go"}`,
		Result:    "48 lines read",
		Status:    ToolCallDone,
		Collapsed: true,
	}
	result := ansi.Strip(renderToolCard(&tc, 80, 0))
	if !strings.Contains(result, "read_file") {
		t.Fatalf("expected tool name: %s", result)
	}
	if !strings.Contains(result, "Called") {
		t.Fatalf("expected 'Called' verb for done tool: %s", result)
	}
	if !strings.Contains(result, "model.go") {
		t.Fatalf("expected summary 'model.go': %s", result)
	}
}

func TestRenderToolCard_Error(t *testing.T) {
	tc := ToolCallEntry{
		Name:   "run_shell",
		Status: ToolCallError,
	}
	result := ansi.Strip(renderToolCard(&tc, 80, 0))
	if !strings.Contains(result, "✗") {
		t.Fatalf("expected error glyph ✗: %s", result)
	}
	if !strings.Contains(result, "Called") {
		t.Fatalf("expected 'Called' verb for error tool: %s", result)
	}
}

func TestFormatUserEntryContent_WithImages(t *testing.T) {
	got := formatUserEntryContent("show me", 2)
	want := "show me\n[Image #1]\n[Image #2]"
	if got != want {
		t.Fatalf("formatUserEntryContent() = %q, want %q", got, want)
	}
}

func TestSummarizeQueuedMessages_ShowsPreviewAndOverflowCount(t *testing.T) {
	msgs := []queuedMessage{
		{Text: "first queued"},
		{Text: "second queued"},
		{Text: "third queued"},
	}
	got := summarizeQueuedMessages(msgs)
	want := "first queued | second queued | +1"
	if got != want {
		t.Fatalf("summarizeQueuedMessages() = %q, want %q", got, want)
	}
}

func TestSummarizeQueuedMessage_InlinesImages(t *testing.T) {
	got := summarizeQueuedMessage(queuedMessage{
		Text: "check this",
		Images: []providers.InputImage{
			{MediaType: "image/png", Data: "AAA"},
		},
	})
	want := "check this [Image #1]"
	if got != want {
		t.Fatalf("summarizeQueuedMessage() = %q, want %q", got, want)
	}
}

func TestView_ShowsSteerAndQueuePreview(t *testing.T) {
	m := NewModel(Config{
		Provider:   "test",
		Model:      "test-model",
		ConfigPath: "/tmp/.wuu.json",
		StreamRunner: &agent.StreamRunner{
			Client: &echoStreamClient{answer: func(msgs []providers.ChatMessage) string { return "answer to: " + msgs[len(msgs)-1].Content }},
			Model:  "test-model",
		},
	})
	m.width = 180
	m.height = 24
	m.relayout()
	m.pendingSteers = []queuedMessage{{Text: "steer now"}}
	m.messageQueue = []queuedMessage{{Text: "queued after steer"}}

	view := m.View()
	if !strings.Contains(view, "steer:1") {
		t.Fatalf("expected steer hint in header, got: %s", view)
	}
	if !strings.Contains(view, "queue:1") {
		t.Fatalf("expected queue hint in header, got: %s", view)
	}
}

func TestStripUserImagePlaceholderLines(t *testing.T) {
	input := "please review\n[Image #1]\n[Image #2]"
	got := stripUserImagePlaceholderLines(input)
	if got != "please review" {
		t.Fatalf("stripUserImagePlaceholderLines() = %q", got)
	}
}

func TestStreamReconnectEventUpdatesStatusLine(t *testing.T) {
	m := NewModel(Config{
		Provider:   "test",
		Model:      "test-model",
		ConfigPath: "/tmp/.wuu.json",
		StreamRunner: &agent.StreamRunner{
			Client: &echoStreamClient{answer: func(_ []providers.ChatMessage) string { return "" }},
			Model:  "test-model",
		},
	})
	m.streaming = true
	m.pendingRequest = true
	m.streamCh = make(chan providers.StreamEvent)

	updated, _ := m.Update(streamEventMsg{
		event: providers.StreamEvent{
			Type:    providers.EventReconnect,
			Content: "Reconnecting... 1/6",
		},
	})
	after := updated.(Model)
	if after.statusLine != "Reconnecting... 1/6" {
		t.Fatalf("unexpected status line: %q", after.statusLine)
	}
}

func TestStreamLifecycleEventUpdatesStructuredWorkStatus(t *testing.T) {
	m := NewModel(Config{
		Provider:   "test",
		Model:      "test-model",
		ConfigPath: "/tmp/.wuu.json",
		StreamRunner: &agent.StreamRunner{
			Client: &echoStreamClient{answer: func(_ []providers.ChatMessage) string { return "" }},
			Model:  "test-model",
		},
	})
	m.streaming = true
	m.pendingRequest = true
	m.streamCh = make(chan providers.StreamEvent)

	updated, _ := m.Update(streamEventMsg{
		event: providers.StreamEvent{
			Type: providers.EventLifecycle,
			Lifecycle: &providers.StreamLifecycle{
				Phase:      providers.StreamPhaseReconnecting,
				RetryCount: 2,
				MaxRetries: 5,
			},
		},
	})
	after := updated.(Model)
	if after.currentWorkStatus().Phase != workPhaseReconnecting {
		t.Fatalf("expected reconnecting phase, got %+v", after.currentWorkStatus())
	}
	if after.currentWorkStatus().Meta != "Retry 2/5" {
		t.Fatalf("unexpected reconnect meta: %q", after.currentWorkStatus().Meta)
	}
}

func TestStreamLifecycleReconnectIncludesReasonAndDelay(t *testing.T) {
	m := NewModel(Config{
		Provider:   "test",
		Model:      "test-model",
		ConfigPath: "/tmp/.wuu.json",
		StreamRunner: &agent.StreamRunner{
			Client: &echoStreamClient{answer: func(_ []providers.ChatMessage) string { return "" }},
			Model:  "test-model",
		},
	})
	m.streaming = true
	m.pendingRequest = true

	updated, _ := m.Update(streamEventMsg{
		event: providers.StreamEvent{
			Type: providers.EventLifecycle,
			Lifecycle: &providers.StreamLifecycle{
				Phase:      providers.StreamPhaseReconnecting,
				RetryCount: 2,
				MaxRetries: 5,
				RetryIn:    1500 * time.Millisecond,
				Reason:     "Stream timed out",
			},
		},
	})
	after := updated.(Model)
	ws := after.currentWorkStatus()
	if ws.Label != "Reconnecting... 2/5" {
		t.Fatalf("unexpected reconnect label: %q", ws.Label)
	}
	if ws.Meta != "Stream timed out" {
		t.Fatalf("unexpected reconnect meta: %q", ws.Meta)
	}
	if ws.Detail != "Retrying in 1.5s" {
		t.Fatalf("unexpected reconnect detail: %q", ws.Detail)
	}
}

func TestStreamReconnectEventPreservesLifecycleDetails(t *testing.T) {
	m := NewModel(Config{
		Provider:   "test",
		Model:      "test-model",
		ConfigPath: "/tmp/.wuu.json",
		StreamRunner: &agent.StreamRunner{
			Client: &echoStreamClient{answer: func(_ []providers.ChatMessage) string { return "" }},
			Model:  "test-model",
		},
	})
	m.streaming = true
	m.pendingRequest = true

	updated, _ := m.Update(streamEventMsg{
		event: providers.StreamEvent{
			Type: providers.EventLifecycle,
			Lifecycle: &providers.StreamLifecycle{
				Phase:      providers.StreamPhaseReconnecting,
				RetryCount: 1,
				MaxRetries: 5,
				RetryIn:    200 * time.Millisecond,
				Reason:     "Connection ended before completion",
			},
		},
	})
	withLifecycle := updated.(Model)

	updated, _ = withLifecycle.Update(streamEventMsg{
		event: providers.StreamEvent{
			Type:    providers.EventReconnect,
			Content: "Reconnecting... 1/5",
		},
	})
	after := updated.(Model)
	ws := after.currentWorkStatus()
	if ws.Label != "Reconnecting... 1/5" {
		t.Fatalf("unexpected reconnect label: %q", ws.Label)
	}
	if ws.Meta != "Connection ended before completion" {
		t.Fatalf("expected reconnect reason to survive EventReconnect, got %q", ws.Meta)
	}
	if ws.Detail != "Retrying in 200ms" {
		t.Fatalf("expected reconnect delay detail to survive EventReconnect, got %q", ws.Detail)
	}
}

func TestStreamLifecycleConnectingDuringRetryKeepsReconnectState(t *testing.T) {
	m := NewModel(Config{
		Provider:   "test",
		Model:      "test-model",
		ConfigPath: "/tmp/.wuu.json",
		StreamRunner: &agent.StreamRunner{
			Client: &echoStreamClient{answer: func(_ []providers.ChatMessage) string { return "" }},
			Model:  "test-model",
		},
	})
	m.streaming = true
	m.pendingRequest = true

	updated, _ := m.Update(streamEventMsg{
		event: providers.StreamEvent{
			Type: providers.EventLifecycle,
			Lifecycle: &providers.StreamLifecycle{
				Phase:      providers.StreamPhaseReconnecting,
				RetryCount: 1,
				MaxRetries: 5,
				RetryIn:    250 * time.Millisecond,
				Reason:     "Provider is overloaded",
			},
		},
	})
	withReconnect := updated.(Model)

	updated, _ = withReconnect.Update(streamEventMsg{
		event: providers.StreamEvent{
			Type: providers.EventLifecycle,
			Lifecycle: &providers.StreamLifecycle{
				Phase:       providers.StreamPhaseConnecting,
				Attempt:     2,
				MaxAttempts: 6,
				RetryCount:  1,
				MaxRetries:  5,
			},
		},
	})
	after := updated.(Model)
	ws := after.currentWorkStatus()
	if ws.Phase != workPhaseReconnecting {
		t.Fatalf("expected reconnecting phase, got %+v", ws)
	}
	if ws.Label != "Reconnecting... 1/5" {
		t.Fatalf("unexpected reconnect label: %q", ws.Label)
	}
}

func TestStreamErrorEventUsesFriendlyDisplay(t *testing.T) {
	m := NewModel(Config{
		Provider:   "test",
		Model:      "test-model",
		ConfigPath: "/tmp/.wuu.json",
		StreamRunner: &agent.StreamRunner{
			Client: &echoStreamClient{answer: func(_ []providers.ChatMessage) string { return "" }},
			Model:  "test-model",
		},
	})
	m.streaming = true
	m.pendingRequest = true
	m.streamTarget = m.appendEntry("assistant", "")

	updated, _ := m.Update(streamEventMsg{
		event: providers.StreamEvent{
			Type:  providers.EventError,
			Error: providers.NewProviderStreamError("1305", "该模型当前访问量过大，请您稍后再试"),
		},
	})
	after := updated.(Model)
	last := after.entries[len(after.entries)-1]
	if !strings.Contains(ansi.Strip(last.Content), "Provider is overloaded. Try again in a moment.") {
		t.Fatalf("unexpected final error entry: %q", ansi.Strip(last.Content))
	}
}

func TestStreamMessageEventPersistsChatMessageIncrementally(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "session.jsonl")
	m := NewModel(Config{
		Provider:   "test",
		Model:      "test-model",
		ConfigPath: filepath.Join(dir, ".wuu.json"),
		MemoryPath: path,
		StreamRunner: &agent.StreamRunner{
			Client: &echoStreamClient{answer: func(_ []providers.ChatMessage) string { return "" }},
			Model:  "test-model",
		},
	})
	m.pendingTurn = &pendingTurnResult{}

	assistant := providers.ChatMessage{Role: "assistant", Content: "partial answer"}
	updated, _ := m.Update(streamEventMsg{
		event: providers.StreamEvent{
			Type:    providers.EventMessage,
			Message: &assistant,
		},
	})
	after := updated.(Model)

	if len(after.chatHistory) != 1 {
		t.Fatalf("expected chat history append, got %d", len(after.chatHistory))
	}
	if after.chatHistory[0].Content != "partial answer" {
		t.Fatalf("unexpected persisted content: %+v", after.chatHistory[0])
	}
	if after.pendingTurn == nil || !after.pendingTurn.incrementalPersisted {
		t.Fatal("expected pending turn to record incremental persistence")
	}

	msgs, err := loadChatHistory(path)
	if err != nil {
		t.Fatalf("loadChatHistory: %v", err)
	}
	if len(msgs) != 1 || msgs[0].Content != "partial answer" {
		t.Fatalf("unexpected persisted history: %+v", msgs)
	}
}

func TestStreamFinishedSkipsDuplicateAppendAfterIncrementalPersistence(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "session.jsonl")
	m := NewModel(Config{
		Provider:   "test",
		Model:      "test-model",
		ConfigPath: filepath.Join(dir, ".wuu.json"),
		MemoryPath: path,
		StreamRunner: &agent.StreamRunner{
			Client: &echoStreamClient{answer: func(_ []providers.ChatMessage) string { return "" }},
			Model:  "test-model",
		},
	})
	msg := providers.ChatMessage{Role: "assistant", Content: "already persisted"}
	if err := appendChatMessage(path, msg); err != nil {
		t.Fatalf("appendChatMessage: %v", err)
	}
	m.chatHistory = []providers.ChatMessage{msg}
	m.pendingTurn = &pendingTurnResult{
		newMsgs:              []providers.ChatMessage{msg},
		incrementalPersisted: true,
	}
	m.streaming = true
	m.pendingRequest = true

	updated, _ := m.Update(streamFinishedMsg{})
	after := updated.(Model)

	msgs, err := loadChatHistory(path)
	if err != nil {
		t.Fatalf("loadChatHistory: %v", err)
	}
	if len(msgs) != 1 {
		t.Fatalf("expected no duplicate messages, got %+v", msgs)
	}
	if len(after.chatHistory) != 1 {
		t.Fatalf("expected in-memory history to remain deduplicated, got %+v", after.chatHistory)
	}
}

func TestSubmitBusyTabQueuesMessage(t *testing.T) {
	m := NewModel(Config{
		Provider:   "test",
		Model:      "test-model",
		ConfigPath: "/tmp/.wuu.json",
		StreamRunner: &agent.StreamRunner{
			Client: &echoStreamClient{answer: func(msgs []providers.ChatMessage) string { return "answer to: " + msgs[len(msgs)-1].Content }},
			Model:  "test-model",
		},
	})
	m.pendingRequest = true
	m.streaming = true
	m.statusLine = "streaming"
	m.input.SetValue("tab queued")

	nextModel, cmd := m.submit(true)
	if cmd != nil {
		t.Fatal("expected no command while request is pending")
	}
	next := nextModel.(Model)
	if len(next.messageQueue) != 1 {
		t.Fatalf("expected 1 queued message, got %d", len(next.messageQueue))
	}
	if next.messageQueue[0].Text != "tab queued" {
		t.Fatalf("unexpected queued text: %q", next.messageQueue[0].Text)
	}
	if len(next.pendingSteers) != 0 {
		t.Fatalf("expected no pending steers, got %d", len(next.pendingSteers))
	}
	if next.statusLine != "streaming" {
		t.Fatalf("expected streaming status to remain active while queueing, got %q", next.statusLine)
	}
}

func TestSubmitBusyEnterQueuesSteerAndCancelsStream(t *testing.T) {
	m := NewModel(Config{
		Provider:   "test",
		Model:      "test-model",
		ConfigPath: "/tmp/.wuu.json",
		StreamRunner: &agent.StreamRunner{
			Client: &echoStreamClient{answer: func(msgs []providers.ChatMessage) string { return "answer to: " + msgs[len(msgs)-1].Content }},
			Model:  "test-model",
		},
	})
	m.pendingRequest = true
	cancelCalled := false
	m.cancelStream = func() { cancelCalled = true }
	m.input.SetValue("steer now")

	nextModel, cmd := m.submit(false)
	if cmd != nil {
		t.Fatal("expected no command while request is pending")
	}
	if !cancelCalled {
		t.Fatal("expected cancelStream to be called for steer")
	}
	next := nextModel.(Model)
	if len(next.pendingSteers) != 1 {
		t.Fatalf("expected 1 pending steer, got %d", len(next.pendingSteers))
	}
	if next.pendingSteers[0].Text != "steer now" {
		t.Fatalf("unexpected steer text: %q", next.pendingSteers[0].Text)
	}
	if len(next.messageQueue) != 0 {
		t.Fatalf("expected no queued follow-up, got %d", len(next.messageQueue))
	}
	if next.statusLine != "steering (1 pending)" {
		t.Fatalf("unexpected status line: %q", next.statusLine)
	}
}

func TestCompletionTabInsertsWithoutExecuting(t *testing.T) {
	m := NewModel(Config{
		Provider:   "test",
		Model:      "test-model",
		ConfigPath: "/tmp/.wuu.json",
		StreamRunner: &agent.StreamRunner{
			Client: &echoStreamClient{answer: func(msgs []providers.ChatMessage) string { return "answer to: " + msgs[len(msgs)-1].Content }},
			Model:  "test-model",
		},
	})
	m.width = 100
	m.height = 24
	m.relayout()
	m.input.SetValue("/he")
	m.updateCompletion()

	updated, cmd := m.Update(tea.KeyMsg{Type: tea.KeyTab})
	after := updated.(Model)
	if cmd != nil {
		t.Fatal("expected no command when tab-completing slash command")
	}
	if got := after.input.Value(); got != "/help " {
		t.Fatalf("expected tab to insert /help, got %q", got)
	}
	if after.completionVisible {
		t.Fatal("expected completion popup to close after tab insert")
	}
	if after.pendingRequest {
		t.Fatal("tab completion should not execute command")
	}
	if len(after.entries) != 0 {
		t.Fatal("tab completion should not append transcript entries")
	}
}

func TestCompletionEnterExecutesSafeCommand(t *testing.T) {
	m := NewModel(Config{
		Provider:   "test",
		Model:      "test-model",
		ConfigPath: "/tmp/.wuu.json",
		StreamRunner: &agent.StreamRunner{
			Client: &echoStreamClient{answer: func(msgs []providers.ChatMessage) string { return "answer to: " + msgs[len(msgs)-1].Content }},
			Model:  "test-model",
		},
	})
	m.width = 100
	m.height = 24
	m.relayout()
	m.input.SetValue("/he")
	m.updateCompletion()

	updated, cmd := m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	after := updated.(Model)
	if cmd != nil {
		t.Fatal("expected no async command for local safe slash command")
	}
	if got := after.input.Value(); got != "" {
		t.Fatalf("expected executed command to clear input, got %q", got)
	}
	if after.completionVisible {
		t.Fatal("expected completion popup to close after enter")
	}
	if len(after.entries) != 1 {
		t.Fatalf("expected 1 system entry after execution, got %d", len(after.entries))
	}
	if after.entries[0].Role != "SYSTEM" {
		t.Fatalf("expected system entry, got %q", after.entries[0].Role)
	}
	if !strings.Contains(after.entries[0].Content, "Available commands") {
		t.Fatalf("expected help output, got %q", after.entries[0].Content)
	}
}

func TestCompletionEnterInsertsCommandThatNeedsArgs(t *testing.T) {
	m := NewModel(Config{
		Provider:   "test",
		Model:      "test-model",
		ConfigPath: "/tmp/.wuu.json",
		StreamRunner: &agent.StreamRunner{
			Client: &echoStreamClient{answer: func(msgs []providers.ChatMessage) string { return "answer to: " + msgs[len(msgs)-1].Content }},
			Model:  "test-model",
		},
	})
	m.width = 100
	m.height = 24
	m.relayout()
	m.input.SetValue("/mo")
	m.updateCompletion()

	updated, cmd := m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	after := updated.(Model)
	if cmd != nil {
		t.Fatal("expected no command when enter only inserts slash command")
	}
	if got := after.input.Value(); got != "/model " {
		t.Fatalf("expected enter to insert /model, got %q", got)
	}
	if after.completionVisible {
		t.Fatal("expected completion popup to close after enter insert")
	}
	if after.pendingRequest {
		t.Fatal("enter insert should not execute command")
	}
	if len(after.entries) != 0 {
		t.Fatal("enter insert should not append transcript entries")
	}
}

func TestDrainQueuePrioritizesPendingSteers(t *testing.T) {
	m := NewModel(Config{
		Provider:   "test",
		Model:      "test-model",
		ConfigPath: "/tmp/.wuu.json",
		StreamRunner: &agent.StreamRunner{
			Client: &echoStreamClient{answer: func(msgs []providers.ChatMessage) string { return "answer to: " + msgs[len(msgs)-1].Content }},
			Model:  "test-model",
		},
	})
	m.pendingSteers = []queuedMessage{
		{Text: "first steer"},
		{Text: "second steer"},
	}
	m.messageQueue = []queuedMessage{
		{Text: "queued follow-up"},
	}

	nextModel, cmd := m.drainQueue()
	if cmd == nil {
		t.Fatal("expected async command from drainQueue")
	}
	next := nextModel.(Model)
	if len(next.pendingSteers) != 0 {
		t.Fatalf("expected pending steers drained, got %d", len(next.pendingSteers))
	}
	if len(next.messageQueue) != 1 {
		t.Fatalf("expected queued follow-up preserved, got %d", len(next.messageQueue))
	}
	if len(next.chatHistory) == 0 {
		t.Fatal("expected steer message appended to history")
	}
	last := next.chatHistory[len(next.chatHistory)-1]
	if last.Role != "user" {
		t.Fatalf("expected last message role user, got %q", last.Role)
	}
	if last.Content != "first steer\nsecond steer" {
		t.Fatalf("unexpected merged steer content: %q", last.Content)
	}
}

type blockingCompactStreamClient struct {
	compactSleep time.Duration
	chatCalls    atomic.Int32
}

func (c *blockingCompactStreamClient) Chat(_ context.Context, _ providers.ChatRequest) (providers.ChatResponse, error) {
	c.chatCalls.Add(1)
	time.Sleep(c.compactSleep)
	return providers.ChatResponse{Content: "summary"}, nil
}

func (c *blockingCompactStreamClient) StreamChat(_ context.Context, _ providers.ChatRequest) (<-chan providers.StreamEvent, error) {
	ch := make(chan providers.StreamEvent, 2)
	ch <- providers.StreamEvent{Type: providers.EventContentDelta, Content: "ok"}
	ch <- providers.StreamEvent{Type: providers.EventDone}
	close(ch)
	return ch, nil
}

func TestSendMessage_DoesNotBlockOnCompaction(t *testing.T) {
	client := &blockingCompactStreamClient{compactSleep: 500 * time.Millisecond}
	m := NewModel(Config{
		Provider:   "test",
		Model:      "test-model",
		ConfigPath: "/tmp/.wuu.json",
		StreamRunner: &agent.StreamRunner{
			Client: client,
			Model:  "test-model",
		},
	})
	m.chatHistory = []providers.ChatMessage{
		{Role: "user", Content: strings.Repeat("seed ", 40)},
		{Role: "assistant", Content: strings.Repeat("seed ", 40)},
		{Role: "user", Content: strings.Repeat("seed ", 40)},
		{Role: "assistant", Content: strings.Repeat("seed ", 40)},
	}

	start := time.Now()
	nextModel, cmd := m.sendMessage(queuedMessage{
		Text: strings.Repeat("long message ", 20),
	})
	elapsed := time.Since(start)

	if cmd == nil {
		t.Fatal("expected async command from sendMessage")
	}
	if elapsed > 150*time.Millisecond {
		t.Fatalf("sendMessage blocked too long: %s", elapsed)
	}

	next := nextModel.(Model)
	if !next.pendingRequest {
		t.Fatal("expected pendingRequest=true")
	}

	time.Sleep(100 * time.Millisecond)
	if got := client.chatCalls.Load(); got != 0 {
		t.Fatalf("expected no pre-turn compact without usage ground truth, got %d compact calls", got)
	}
}

func TestSendMessage_StartsReplyWithoutPreTurnCompactStatus(t *testing.T) {
	client := &blockingCompactStreamClient{compactSleep: 500 * time.Millisecond}
	m := NewModel(Config{
		Provider:   "test",
		Model:      "test-model",
		ConfigPath: "/tmp/.wuu.json",
		StreamRunner: &agent.StreamRunner{
			Client: client,
			Model:  "test-model",
		},
	})
	m.chatHistory = []providers.ChatMessage{
		{Role: "user", Content: strings.Repeat("seed ", 40)},
		{Role: "assistant", Content: strings.Repeat("seed ", 40)},
		{Role: "user", Content: strings.Repeat("seed ", 40)},
		{Role: "assistant", Content: strings.Repeat("seed ", 40)},
	}

	nextModel, cmd := m.sendMessage(queuedMessage{Text: strings.Repeat("long message ", 20)})
	if cmd == nil {
		t.Fatal("expected async command from sendMessage")
	}

	next := nextModel.(Model)
	if next.statusLine != "streaming" {
		t.Fatalf("expected streaming status, got %q", next.statusLine)
	}
	if next.streamTarget < 0 {
		t.Fatalf("expected assistant placeholder for live reply, got streamTarget=%d", next.streamTarget)
	}
}

// TestComputeLayout_ReservesInlineStatusLine locks in the layout invariant
// that computeLayout reserves exactly one line below the chat viewport for
// the inline status indicator. If this slot is removed from the math, the
// View() total row count will exceed the terminal height and bubbletea will
// truncate, breaking the header-first render order.
func TestComputeLayout_ReservesInlineStatusLine(t *testing.T) {
	const termWidth, termHeight = 100, 40
	const inputLines = 3

	// No worker panel, no image bar.
	l := computeLayout(termWidth, termHeight, inputLines, 0, 0)
	// Expected total lines in View(): header + chat + inlineStatus + sep + input
	// = 1 + chatH + 1 + 1 + inputLines = termHeight
	expected := 1 + l.Chat.Height + 1 + 1 + inputLines
	if expected != termHeight {
		t.Fatalf("no-worker layout row count mismatch: header(1) + chat(%d) + status(1) + sep(1) + input(%d) = %d, want %d",
			l.Chat.Height, inputLines, expected, termHeight)
	}

	// With worker panel (2 workers → 3 rows: title + 2 rows).
	const workerRows = 3
	lw := computeLayout(termWidth, termHeight, inputLines, workerRows, 0)
	// Expected: header + chat + inlineStatus + sep + panel + sep + input
	expectedW := 1 + lw.Chat.Height + 1 + 1 + workerRows + 1 + inputLines
	if expectedW != termHeight {
		t.Fatalf("worker-panel layout row count mismatch: header(1) + chat(%d) + status(1) + sep(1) + panel(%d) + sep(1) + input(%d) = %d, want %d",
			lw.Chat.Height, workerRows, inputLines, expectedW, termHeight)
	}

	// Worker panel steals from chat, not from the inline status slot.
	if lw.Chat.Height != l.Chat.Height-workerRows-1 {
		t.Fatalf("worker panel should only steal from chat height: noWorker=%d, withWorker=%d, delta=%d, want delta=%d",
			l.Chat.Height, lw.Chat.Height, l.Chat.Height-lw.Chat.Height, workerRows+1)
	}
}

// TestInlineSpinMsg_DoesNotRebuildViewport locks in the fix: inlineSpinMsg
// must not trigger a viewport content rebuild. If it does, the viewport's
// YOffset will shift on every 150ms spinner tick and break bubbletea's
// line-diff, causing tool card flicker and scrollbar jitter.
func TestInlineSpinMsg_DoesNotRebuildViewport(t *testing.T) {
	m := NewModel(Config{
		Provider:   "test",
		Model:      "test-model",
		ConfigPath: "/tmp/.wuu.json",
		StreamRunner: &agent.StreamRunner{
			Client: &echoStreamClient{answer: func(msgs []providers.ChatMessage) string { return msgs[len(msgs)-1].Content }},
			Model:  "test-model",
		},
	})
	m.width = 100
	m.height = 40
	m.relayout()

	// Simulate streaming state: add content and point streamTarget at
	// the active assistant entry. In the pre-fix code, refreshViewport
	// would render renderInlineStatus inside the viewport for the
	// streamTarget entry when streaming=true, adding an extra line.
	m.streaming = true
	m.statusLine = "streaming"
	for i := 0; i < 60; i++ {
		m.appendEntry("assistant", fmt.Sprintf("line %d", i))
	}
	m.streamTarget = len(m.entries) - 1 // point at last assistant entry
	m.refreshViewport(true)

	// Scroll to top and disable auto-follow so we can observe entry[0].
	m.viewport.SetYOffset(0)
	m.autoFollow = false

	// Mutate entry[0] with a unique marker. If inlineSpinMsg triggers
	// refreshViewport, the viewport content gets rebuilt from m.entries
	// and the marker appears in the visible window (YOffset=0 → top).
	// If it doesn't call refreshViewport, the stale content remains.
	const marker = "SPIN_REBUILD_CANARY"
	m.entries[0].Content = marker
	beforeFrame := m.spinnerFrame

	// Dispatch the spinner tick.
	updated, _ := m.Update(inlineSpinMsg{})
	after := updated.(Model)

	if after.spinnerFrame != nextStatusFrame(beforeFrame) {
		t.Fatalf("expected spinnerFrame to advance to next shared frame: before=%d after=%d",
			beforeFrame, after.spinnerFrame)
	}
	// The canary must NOT appear in the viewport — that would mean
	// refreshViewport was called, rebuilding content from m.entries.
	if strings.Contains(after.viewport.View(), marker) {
		t.Fatal("inlineSpinMsg must not call refreshViewport: canary marker appeared in viewport content after spinner tick")
	}
}

// TestView_InlineStatusRenderedOutsideViewport verifies the inline status
// indicator lives in a View() segment below the viewport, not as part of
// the viewport content. The viewport content must be free of "Generating"/
// "Working" text so scrolling cannot affect spinner visibility.
func TestView_InlineStatusRenderedOutsideViewport(t *testing.T) {
	m := NewModel(Config{
		Provider:   "test",
		Model:      "test-model",
		ConfigPath: "/tmp/.wuu.json",
		StreamRunner: &agent.StreamRunner{
			Client: &echoStreamClient{answer: func(msgs []providers.ChatMessage) string { return msgs[len(msgs)-1].Content }},
			Model:  "test-model",
		},
	})
	m.width = 100
	m.height = 40
	m.streaming = true
	m.statusLine = "streaming"
	m.appendEntry("user", "hello")
	m.appendEntry("assistant", "partial reply")
	m.streamTarget = len(m.entries) - 1
	m.relayout()

	viewportContent := ansi.Strip(m.viewport.View())
	if strings.Contains(viewportContent, "Responding") {
		t.Fatal("viewport content must not contain inline status 'Responding' — it should be rendered outside the viewport")
	}

	fullView := ansi.Strip(m.View())
	if !strings.Contains(fullView, "Responding") {
		t.Fatalf("full View() must contain inline status 'Responding'; got:\n%s", fullView)
	}
}

func TestRenderWorkerPanel_UsesSharedWaitingLanguage(t *testing.T) {
	m := NewModel(Config{Provider: "test", Model: "test-model", ConfigPath: "/tmp/.wuu.json"})
	if got := m.renderWorkerPanel(80); got != "" {
		t.Fatalf("expected empty worker panel without coordinator, got %q", got)
	}
}

func TestNewInputTextarea_UsesThemeStyles(t *testing.T) {
	m := newInputTextarea()
	if got := m.FocusedStyle.Text.GetForeground(); got != currentTheme.Text {
		t.Fatalf("expected focused text color %v, got %v", currentTheme.Text, got)
	}
	if got := m.FocusedStyle.Placeholder.GetForeground(); got != currentTheme.Inactive {
		t.Fatalf("expected placeholder color %v, got %v", currentTheme.Inactive, got)
	}
	if got := m.FocusedStyle.Prompt.GetForeground(); got != currentTheme.Brand {
		t.Fatalf("expected prompt color %v, got %v", currentTheme.Brand, got)
	}
	if got := m.BlurredStyle.Prompt.GetForeground(); got != currentTheme.Subtle {
		t.Fatalf("expected blurred prompt color %v, got %v", currentTheme.Subtle, got)
	}
}

func TestApplyThemeRefreshesTextareaDefaults(t *testing.T) {
	orig := currentTheme
	defer applyTheme(orig)

	applyTheme(lightTheme)
	lightInput := newInputTextarea()
	if got := lightInput.FocusedStyle.Text.GetForeground(); got != lightTheme.Text {
		t.Fatalf("expected light theme text color %v, got %v", lightTheme.Text, got)
	}

	applyTheme(darkTheme)
	darkInput := newInputTextarea()
	if got := darkInput.FocusedStyle.Text.GetForeground(); got != darkTheme.Text {
		t.Fatalf("expected dark theme text color %v, got %v", darkTheme.Text, got)
	}
}

func TestNewOnboardingTextarea_UsesThemeStyles(t *testing.T) {
	m := newOnboardingTextarea()
	if got := m.FocusedStyle.Text.GetForeground(); got != currentTheme.Text {
		t.Fatalf("expected onboarding text color %v, got %v", currentTheme.Text, got)
	}
}

func TestProcessNotifyAppendsLifecycleEntriesOnce(t *testing.T) {
	m := NewModel(Config{Provider: "test", Model: "test-model", ConfigPath: "/tmp/.wuu.json"})
	proc := processruntime.Process{ID: "proc-1", Command: "npm run dev"}

	updated, _ := m.Update(processNotifyMsg{event: processruntime.Event{Type: processruntime.EventStarted, Process: proc}})
	m = updated.(Model)
	updated, _ = m.Update(processNotifyMsg{event: processruntime.Event{Type: processruntime.EventStarted, Process: proc}})
	m = updated.(Model)
	updated, _ = m.Update(processNotifyMsg{event: processruntime.Event{Type: processruntime.EventStopped, Process: proc}})
	m = updated.(Model)

	if len(m.entries) != 2 {
		t.Fatalf("expected deduped start plus stop entries, got %d", len(m.entries))
	}
	if !strings.Contains(m.entries[0].Content, "✓ process started: npm run dev") {
		t.Fatalf("unexpected start entry: %q", m.entries[0].Content)
	}
	if !strings.Contains(m.entries[1].Content, "⊘ process stopped: npm run dev") {
		t.Fatalf("unexpected stop entry: %q", m.entries[1].Content)
	}
}

func TestStopProcessSlashByIDSuccess(t *testing.T) {
	mgr := newTestProcessManager(t)
	p := startTestProcess(t, mgr, "sleep 30", processruntime.OwnerMainAgent, "main", processruntime.LifecycleManaged)
	m := NewModel(Config{Provider: "test", Model: "test-model", ConfigPath: "/tmp/.wuu.json", ProcessManager: mgr})

	out := cmdStopProcess(p.ID, &m)
	if !strings.Contains(out, "stop-process: stopped "+p.ID) {
		t.Fatalf("expected stop success output, got: %s", out)
	}
}

func TestStopProcessSlashBySubstringSuccess(t *testing.T) {
	mgr := newTestProcessManager(t)
	p := startTestProcess(t, mgr, "npm run electron-dev", processruntime.OwnerMainAgent, "main", processruntime.LifecycleManaged)
	m := NewModel(Config{Provider: "test", Model: "test-model", ConfigPath: "/tmp/.wuu.json", ProcessManager: mgr})

	out := cmdStopProcess("electron-dev", &m)
	if !strings.Contains(out, "stop-process: stopped "+p.ID) {
		t.Fatalf("expected substring match stop success, got: %s", out)
	}
}

func TestStopProcessSlashAmbiguousError(t *testing.T) {
	mgr := newTestProcessManager(t)
	startTestProcess(t, mgr, "npm run dev --port 3000", processruntime.OwnerMainAgent, "main", processruntime.LifecycleManaged)
	startTestProcess(t, mgr, "npm run dev --port 3001", processruntime.OwnerMainAgent, "main", processruntime.LifecycleManaged)
	m := NewModel(Config{Provider: "test", Model: "test-model", ConfigPath: "/tmp/.wuu.json", ProcessManager: mgr})

	out := cmdStopProcess("npm run dev", &m)
	if !strings.Contains(out, "stop-process: ambiguous process match") {
		t.Fatalf("expected ambiguous error, got: %s", out)
	}
}

func TestLogsSlashIncludesProcessAndOutput(t *testing.T) {
	mgr := newTestProcessManager(t)
	p := startTestProcess(t, mgr, "printf 'ready\n'; sleep 30", processruntime.OwnerMainAgent, "main", processruntime.LifecycleManaged)
	time.Sleep(150 * time.Millisecond)
	m := NewModel(Config{Provider: "test", Model: "test-model", ConfigPath: "/tmp/.wuu.json", ProcessManager: mgr})

	out := cmdLogs(p.ID, &m)
	if !strings.Contains(out, "process: "+p.ID) {
		t.Fatalf("expected process id in logs output, got: %s", out)
	}
	if !strings.Contains(out, "command: printf 'ready") {
		t.Fatalf("expected command summary in logs output, got: %s", out)
	}
	if !strings.Contains(out, "truncated: false") {
		t.Fatalf("expected truncated marker in logs output, got: %s", out)
	}
	if !strings.Contains(out, "ready") {
		t.Fatalf("expected log content in logs output, got: %s", out)
	}
}

func TestLogsSlashErrorsForNotFoundAndAmbiguous(t *testing.T) {
	mgr := newTestProcessManager(t)
	startTestProcess(t, mgr, "npm run dev --port 3000", processruntime.OwnerMainAgent, "main", processruntime.LifecycleManaged)
	startTestProcess(t, mgr, "npm run dev --port 3001", processruntime.OwnerMainAgent, "main", processruntime.LifecycleManaged)
	m := NewModel(Config{Provider: "test", Model: "test-model", ConfigPath: "/tmp/.wuu.json", ProcessManager: mgr})

	notFound := cmdLogs("missing-proc", &m)
	if !strings.Contains(notFound, "logs: no process matched") {
		t.Fatalf("expected not found error, got: %s", notFound)
	}
	ambiguous := cmdLogs("npm run dev", &m)
	if !strings.Contains(ambiguous, "logs: ambiguous process match") {
		t.Fatalf("expected ambiguous error, got: %s", ambiguous)
	}
}

func TestProcessNotifyCleanupUpdatesTranscriptAndStatusLine(t *testing.T) {
	m := NewModel(Config{Provider: "test", Model: "test-model", ConfigPath: "/tmp/.wuu.json"})
	proc := processruntime.Process{ID: "proc-2", Command: "vite", LastError: "exit status 1"}

	updated, _ := m.Update(processNotifyMsg{event: processruntime.Event{Type: processruntime.EventFailed, Process: proc}})
	m = updated.(Model)
	updated, _ = m.Update(processNotifyMsg{event: processruntime.Event{Type: processruntime.EventCleanedUp, Process: proc}})
	m = updated.(Model)

	if len(m.entries) != 2 {
		t.Fatalf("expected failed and cleanup entries, got %d", len(m.entries))
	}
	if !strings.Contains(m.entries[0].Content, "✗ process failed: vite") || !strings.Contains(m.entries[0].Content, "exit status 1") {
		t.Fatalf("unexpected failed entry: %q", m.entries[0].Content)
	}
	if !strings.Contains(m.entries[1].Content, "⊘ process cleaned up: vite") {
		t.Fatalf("unexpected cleanup entry: %q", m.entries[1].Content)
	}
	if !strings.Contains(m.statusLine, "process cleaned up: vite") {
		t.Fatalf("expected status line to reflect latest process event, got %q", m.statusLine)
	}
}
