package subagent

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"strings"
	"sync"
	"time"

	"github.com/blueberrycongee/wuu/internal/agent"
	"github.com/blueberrycongee/wuu/internal/providers"
)

// Manager registers and orchestrates sub-agents. It is safe for
// concurrent use from multiple goroutines.
type Manager struct {
	client       providers.StreamClient
	defaultModel string

	mu        sync.Mutex
	agents    map[string]*SubAgent
	listeners []chan<- Notification
}

// NewManager constructs a Manager backed by the given streaming LLM
// client. defaultModel is used when SpawnOptions.Model is empty.
func NewManager(client providers.StreamClient, defaultModel string) *Manager {
	return &Manager{
		client:       client,
		defaultModel: defaultModel,
		agents:       make(map[string]*SubAgent),
	}
}

// Subscribe registers a channel that will receive notifications when
// sub-agent statuses change. The channel must be drained promptly;
// notifications are dropped if the channel is full (to avoid blocking
// the runner goroutine).
func (m *Manager) Subscribe(ch chan<- Notification) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.listeners = append(m.listeners, ch)
}

// Spawn launches a new sub-agent asynchronously. The returned SubAgent
// has Status == StatusPending or StatusRunning; callers can poll via
// Snapshot or wait via Wait.
func (m *Manager) Spawn(ctx context.Context, opts SpawnOptions) (*SubAgent, error) {
	if opts.Toolkit == nil {
		return nil, errors.New("toolkit is required")
	}
	if opts.Prompt == "" {
		return nil, errors.New("prompt is required")
	}

	model := opts.Model
	if model == "" {
		model = m.defaultModel
	}
	if model == "" {
		return nil, errors.New("no model configured")
	}

	id := newAgentID(opts.Type)
	subCtx, cancel := context.WithCancel(ctx)

	sa := &SubAgent{
		ID:             id,
		Type:           opts.Type,
		Description:    opts.Description,
		Status:         StatusRunning, // set synchronously so CountRunning sees it immediately
		StartedAt:      time.Now(),
		prompt:         opts.Prompt,
		systemPrompt:   opts.SystemPrompt,
		model:          model,
		toolkit:        opts.Toolkit,
		historyPath:    opts.HistoryPath,
		initialHistory: opts.InitialHistory,
		client:         m.client,
		cancelFunc:     cancel,
		doneCh:         make(chan struct{}),
	}

	m.mu.Lock()
	m.agents[id] = sa
	m.mu.Unlock()

	go m.run(subCtx, sa, opts)

	return sa, nil
}

// run executes the sub-agent's turn loop in a goroutine.
func (m *Manager) run(ctx context.Context, sa *SubAgent, opts SpawnOptions) {
	defer close(sa.doneCh)
	defer sa.cancelFunc()

	// Status was already set to StatusRunning in Spawn (so CountRunning
	// sees it synchronously). Just notify listeners.
	m.notify(sa, StatusRunning)

	// Live token accumulation: every LLM round-trip updates the
	// SubAgent's running totals so the activity panel can display
	// progress while the worker is still going.
	onUsage := func(input, output int) {
		sa.mu.Lock()
		sa.InputTokens += input
		sa.OutputTokens += output
		sa.mu.Unlock()
	}

	runner := &agent.StreamRunner{
		Client:       sa.client,
		Tools:        sa.toolkit,
		Model:        sa.model,
		SystemPrompt: sa.systemPrompt,
		MaxSteps:     opts.MaxSteps,
		Temperature:  0.2,
		OnUsage:      onUsage,
	}

	beforeStep := func() []providers.ChatMessage {
		return sa.popPendingUserMessages()
	}

	var (
		content string
		err     error
	)
	if len(sa.initialHistory) > 0 {
		// Fork path: the parent's history is the worker's starting
		// state. We append the role-override prompt as the final
		// user message and call RunWithCallback directly so the
		// runner does NOT prepend its own [system, user] envelope —
		// the system message already lives at history[0] (parent's
		// system prompt verbatim, for prompt-cache friendliness).
		history := make([]providers.ChatMessage, 0, len(sa.initialHistory)+1)
		history = append(history, sa.initialHistory...)
		history = append(history, providers.ChatMessage{
			Role:    "user",
			Content: sa.prompt,
		})
		runner.BeforeStep = beforeStep
		content, _, err = runner.RunWithCallback(ctx, history, nil)
	} else {
		// Spawn path: fresh conversation built from the worker's
		// own system prompt + the user prompt as the first message.
		runner.BeforeStep = beforeStep
		content, err = runner.Run(ctx, sa.prompt)
	}

	sa.mu.Lock()
	sa.CompletedAt = time.Now()
	if err != nil {
		if errors.Is(err, context.Canceled) {
			sa.Status = StatusCancelled
		} else {
			sa.Status = StatusFailed
			sa.Error = err
		}
	} else {
		sa.Status = StatusCompleted
		sa.Result = content
	}
	finalStatus := sa.Status
	sa.mu.Unlock()

	if sa.historyPath != "" {
		_ = persistHistory(sa)
	}

	m.notify(sa, finalStatus)
}

// notify pushes a notification to all listeners. Drops on full channels.
func (m *Manager) notify(sa *SubAgent, status Status) {
	snap := sa.Snapshot()
	n := Notification{AgentID: sa.ID, Status: status, Snapshot: snap}

	m.mu.Lock()
	listeners := append([]chan<- Notification(nil), m.listeners...)
	m.mu.Unlock()

	for _, ch := range listeners {
		select {
		case ch <- n:
		default:
		}
	}
}

// Get returns the sub-agent with the given ID, or nil if unknown.
func (m *Manager) Get(id string) *SubAgent {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.agents[id]
}

// List returns snapshots of all currently-tracked sub-agents.
func (m *Manager) List() []SubAgentSnapshot {
	m.mu.Lock()
	defer m.mu.Unlock()
	out := make([]SubAgentSnapshot, 0, len(m.agents))
	for _, sa := range m.agents {
		out = append(out, sa.Snapshot())
	}
	return out
}

// Stop cancels the sub-agent with the given ID. Does nothing if it's
// already done. Returns false if no such agent.
func (m *Manager) Stop(id string) bool {
	sa := m.Get(id)
	if sa == nil {
		return false
	}
	sa.cancelFunc()
	return true
}

// StopAll cancels every running sub-agent. Used for Ctrl+C handling.
func (m *Manager) StopAll() {
	m.mu.Lock()
	agents := make([]*SubAgent, 0, len(m.agents))
	for _, sa := range m.agents {
		agents = append(agents, sa)
	}
	m.mu.Unlock()
	for _, sa := range agents {
		sa.cancelFunc()
	}
}

// QueueMessage appends a follow-up user instruction for a running
// sub-agent. The message is injected before the next model round.
// Returns false if the agent is unknown.
func (m *Manager) QueueMessage(id, message string) bool {
	sa := m.Get(id)
	if sa == nil {
		return false
	}
	sa.pushPendingMessage(message)
	return true
}

// NextPendingMessage returns and removes the oldest queued follow-up
// message for an agent. Used by tests and diagnostics.
func (m *Manager) NextPendingMessage(id string) (string, bool) {
	sa := m.Get(id)
	if sa == nil {
		return "", false
	}
	return sa.popPendingMessage()
}

// PendingMessageCount reports how many follow-up messages are queued
// for an agent.
func (m *Manager) PendingMessageCount(id string) int {
	sa := m.Get(id)
	if sa == nil {
		return 0
	}
	return sa.pendingCount()
}

// Wait blocks until the sub-agent finishes or the context is cancelled.
// Returns the final snapshot.
func (m *Manager) Wait(ctx context.Context, id string) (SubAgentSnapshot, error) {
	sa := m.Get(id)
	if sa == nil {
		return SubAgentSnapshot{}, fmt.Errorf("subagent %q not found", id)
	}
	select {
	case <-sa.doneCh:
		return sa.Snapshot(), nil
	case <-ctx.Done():
		return sa.Snapshot(), ctx.Err()
	}
}

// CountRunning returns the number of sub-agents currently in
// StatusRunning. Used for concurrency limit checks.
func (m *Manager) CountRunning() int {
	m.mu.Lock()
	defer m.mu.Unlock()
	n := 0
	for _, sa := range m.agents {
		sa.mu.Lock()
		if sa.Status == StatusRunning {
			n++
		}
		sa.mu.Unlock()
	}
	return n
}

func (s *SubAgent) pushPendingMessage(message string) {
	trimmed := strings.TrimSpace(message)
	if trimmed == "" {
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	s.pendingMessages = append(s.pendingMessages, trimmed)
}

func (s *SubAgent) popPendingMessage() (string, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if len(s.pendingMessages) == 0 {
		return "", false
	}
	msg := s.pendingMessages[0]
	s.pendingMessages[0] = ""
	s.pendingMessages = s.pendingMessages[1:]
	return msg, true
}

func (s *SubAgent) popPendingUserMessages() []providers.ChatMessage {
	s.mu.Lock()
	defer s.mu.Unlock()
	if len(s.pendingMessages) == 0 {
		return nil
	}
	out := make([]providers.ChatMessage, 0, len(s.pendingMessages))
	for _, msg := range s.pendingMessages {
		out = append(out, providers.ChatMessage{Role: "user", Content: msg})
	}
	s.pendingMessages = nil
	return out
}

func (s *SubAgent) pendingCount() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return len(s.pendingMessages)
}

// newAgentID generates a short, sortable identifier for a sub-agent.
// Format: <type>-<8 hex chars>.
func newAgentID(typ string) string {
	if typ == "" {
		typ = "agent"
	}
	b := make([]byte, 4)
	_, _ = rand.Read(b)
	return fmt.Sprintf("%s-%s", typ, hex.EncodeToString(b))
}
