package subagent

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"sync"
	"time"

	"github.com/blueberrycongee/wuu/internal/agent"
	"github.com/blueberrycongee/wuu/internal/providers"
)

// Manager registers and orchestrates sub-agents. It is safe for
// concurrent use from multiple goroutines.
type Manager struct {
	client       providers.Client
	defaultModel string

	mu        sync.Mutex
	agents    map[string]*SubAgent
	listeners []chan<- Notification
}

// NewManager constructs a Manager backed by the given LLM client.
// defaultModel is used when SpawnOptions.Model is empty.
func NewManager(client providers.Client, defaultModel string) *Manager {
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
		ID:           id,
		Type:         opts.Type,
		Description:  opts.Description,
		Status:       StatusRunning, // set synchronously so CountRunning sees it immediately
		StartedAt:    time.Now(),
		prompt:       opts.Prompt,
		systemPrompt: opts.SystemPrompt,
		model:        model,
		toolkit:      opts.Toolkit,
		historyPath:  opts.HistoryPath,
		client:       m.client,
		cancelFunc:   cancel,
		doneCh:       make(chan struct{}),
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

	runner := &agent.Runner{
		Client:       sa.client,
		Tools:        sa.toolkit,
		Model:        sa.model,
		SystemPrompt: sa.systemPrompt,
		MaxSteps:     opts.MaxSteps,
		Temperature:  0.2,
	}

	// Live token accumulation: every Chat round-trip updates the
	// SubAgent's running totals so the activity panel can display
	// progress while the worker is still going.
	onUsage := func(input, output int) {
		sa.mu.Lock()
		sa.InputTokens += input
		sa.OutputTokens += output
		sa.mu.Unlock()
	}
	res, err := runner.RunWithUsage(ctx, sa.prompt, onUsage)

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
		sa.Result = res.Content
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
