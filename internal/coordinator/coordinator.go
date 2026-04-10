// Package coordinator wires the orchestration tools (spawn_agent,
// send_message, stop_agent, list_agents) to the underlying subagent
// and worktree subsystems.
//
// The coordinator is the brain that the main agent talks to in
// coordinator mode. It owns the SubAgent Manager and Worktree Manager,
// and exposes a small API the toolkit uses to implement the
// orchestration tools.
package coordinator

import (
	"context"
	"errors"
	"fmt"
	"path/filepath"
	"strings"
	"time"

	"github.com/blueberrycongee/wuu/internal/agent"
	"github.com/blueberrycongee/wuu/internal/providers"
	"github.com/blueberrycongee/wuu/internal/subagent"
	"github.com/blueberrycongee/wuu/internal/worktree"
)

// WorkerToolkitFactory builds a fresh ToolExecutor for a worker that
// will run inside the given root directory (typically a worktree path).
// The factory should configure skills, memory, and any per-worker
// restrictions but MUST NOT include orchestration tools — workers
// don't spawn sub-sub-agents.
type WorkerToolkitFactory func(rootDir string) (agent.ToolExecutor, error)

// Coordinator owns the orchestration runtime for one wuu session.
type Coordinator struct {
	manager     *subagent.Manager
	worktrees   *worktree.Manager
	sessionID   string
	historyDir  string
	workerFact  WorkerToolkitFactory
	defaultSys  string // base system prompt prefix added to every worker
	maxParallel int
}

// Config holds the dependencies needed to build a Coordinator.
type Config struct {
	Client       providers.Client
	DefaultModel string
	ParentRepo   string // absolute path to the user's workspace (must be a git repo)
	WorktreeRoot string // .wuu/worktrees/
	HistoryDir   string // .wuu/sessions/{session-id}/workers/
	SessionID    string
	WorkerSysPrompt string
	WorkerFactory WorkerToolkitFactory
	MaxParallel  int
}

// New constructs a Coordinator. Returns an error if the parent repo
// is not a git repository.
func New(cfg Config) (*Coordinator, error) {
	if cfg.Client == nil {
		return nil, errors.New("Client required")
	}
	if cfg.WorkerFactory == nil {
		return nil, errors.New("WorkerFactory required")
	}
	wt, err := worktree.NewManager(cfg.ParentRepo, cfg.WorktreeRoot)
	if err != nil {
		return nil, fmt.Errorf("worktree manager: %w", err)
	}
	mgr := subagent.NewManager(cfg.Client, cfg.DefaultModel)

	maxP := cfg.MaxParallel
	if maxP <= 0 {
		maxP = 5
	}
	return &Coordinator{
		manager:     mgr,
		worktrees:   wt,
		sessionID:   cfg.SessionID,
		historyDir:  cfg.HistoryDir,
		workerFact:  cfg.WorkerFactory,
		defaultSys:  cfg.WorkerSysPrompt,
		maxParallel: maxP,
	}, nil
}

// Manager exposes the underlying subagent.Manager for advanced use
// (Subscribe, etc.).
func (c *Coordinator) Manager() *subagent.Manager {
	return c.manager
}

// SetSessionInfo updates the coordinator's session ID and history dir
// after the TUI has generated them. Safe to call once at startup.
func (c *Coordinator) SetSessionInfo(sessionID, historyDir string) {
	c.sessionID = sessionID
	c.historyDir = historyDir
}

// SessionID returns the bound session ID, or "session-pending" if
// SetSessionInfo hasn't been called yet.
func (c *Coordinator) SessionID() string {
	return c.sessionID
}

// SpawnRequest is the internal shape of a spawn_agent tool invocation
// after argument validation.
type SpawnRequest struct {
	Type        string
	Description string
	Prompt      string
	BaseRepo    string // optional: chain off another worktree
	Synchronous bool
	Timeout     time.Duration
}

// SpawnResult is what the spawn_agent tool returns to the model.
type SpawnResult struct {
	AgentID     string `json:"agent_id"`
	Status      string `json:"status"`
	WorktreePath string `json:"worktree_path"`
	Result      string `json:"result,omitempty"`
	Error       string `json:"error,omitempty"`
	DurationMS  int64  `json:"duration_ms,omitempty"`
}

// Spawn launches a sub-agent. In synchronous mode it blocks until
// the sub-agent finishes; in async mode it returns immediately with
// status "running" and the agent_id the orchestrator can poll.
func (c *Coordinator) Spawn(ctx context.Context, req SpawnRequest) (*SpawnResult, error) {
	// Concurrency cap.
	if c.manager.CountRunning() >= c.maxParallel {
		return nil, fmt.Errorf("max parallel sub-agents reached (%d). Wait for one to complete or stop one with stop_agent.", c.maxParallel)
	}

	if strings.TrimSpace(req.Prompt) == "" {
		return nil, errors.New("prompt is required")
	}
	wtype := req.Type
	if wtype == "" {
		wtype = "worker"
	}

	// Allocate worker ID by asking subagent.Manager for a fresh agent —
	// but we need the ID before spawn to create the worktree. Generate
	// our own here using a similar scheme.
	workerID := newCoordinatorWorkerID(wtype)

	// 1. Create worktree.
	wt, err := c.worktrees.Create(c.sessionID, workerID, req.BaseRepo)
	if err != nil {
		return nil, fmt.Errorf("worktree create: %w", err)
	}

	// 2. Build worker's toolkit rooted at the worktree.
	workerKit, err := c.workerFact(wt.Path)
	if err != nil {
		_ = c.worktrees.Cleanup(wt)
		return nil, fmt.Errorf("worker toolkit: %w", err)
	}

	// 3. Compose system prompt: base + type-specific preamble.
	sys := composeWorkerSystemPrompt(c.defaultSys, wtype, wt.Path)

	// 4. History path.
	historyPath := ""
	if c.historyDir != "" {
		historyPath = filepath.Join(c.historyDir, workerID+".json")
	}

	// 5. Spawn via manager. We pass the worker ID we already created
	// the worktree under so they line up. Manager will pick its own
	// internal ID; we surface BOTH.
	sa, err := c.manager.Spawn(ctx, subagent.SpawnOptions{
		Type:         wtype,
		Description:  req.Description,
		Prompt:       req.Prompt,
		SystemPrompt: sys,
		Toolkit:      workerKit,
		HistoryPath:  historyPath,
	})
	if err != nil {
		_ = c.worktrees.Cleanup(wt)
		return nil, fmt.Errorf("spawn: %w", err)
	}

	result := &SpawnResult{
		AgentID:      sa.ID,
		Status:       string(sa.Status),
		WorktreePath: wt.Path,
	}

	if !req.Synchronous {
		return result, nil
	}

	// Synchronous mode: wait for completion.
	timeout := req.Timeout
	if timeout <= 0 {
		timeout = 10 * time.Minute
	}
	waitCtx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	snap, err := c.manager.Wait(waitCtx, sa.ID)
	if err != nil {
		return nil, fmt.Errorf("wait: %w", err)
	}
	result.Status = string(snap.Status)
	result.Result = snap.Result
	if snap.Error != nil {
		result.Error = snap.Error.Error()
	}
	if !snap.CompletedAt.IsZero() && !snap.StartedAt.IsZero() {
		result.DurationMS = snap.CompletedAt.Sub(snap.StartedAt).Milliseconds()
	}
	return result, nil
}

// StopAll cancels every running worker. Used for Ctrl+C handling.
func (c *Coordinator) StopAll() {
	c.manager.StopAll()
}

// Stop cancels a specific worker by ID. Returns false if not found.
func (c *Coordinator) Stop(id string) bool {
	return c.manager.Stop(id)
}

// List returns snapshots of all sub-agents in this session.
func (c *Coordinator) List() []subagent.SubAgentSnapshot {
	return c.manager.List()
}

// SendMessage delivers a follow-up message to a specific sub-agent.
// In Phase 3 this is a stub that will be filled in once the manager
// supports message injection — for now it returns an error explaining
// the feature is not yet implemented.
func (c *Coordinator) SendMessage(agentID, message string) error {
	if c.manager.Get(agentID) == nil {
		return fmt.Errorf("agent %q not found", agentID)
	}
	return errors.New("send_message: follow-up messaging is not yet implemented in this build (worker is one-shot)")
}

// Subscribe forwards to the underlying manager so the TUI can receive
// status notifications and inject worker-result messages.
func (c *Coordinator) Subscribe(ch chan<- subagent.Notification) {
	c.manager.Subscribe(ch)
}

// FormatWorkerResult turns a sub-agent snapshot into the XML message
// that the orchestrator sees when a worker completes. The format
// mirrors Claude Code's <task-notification>:
//
//	<worker-result agent_id="..." type="..." status="completed">
//	<summary>...</summary>
//	<duration_ms>1234</duration_ms>
//	<result>
//	... worker's final assistant message ...
//	</result>
//	</worker-result>
func FormatWorkerResult(snap subagent.SubAgentSnapshot) string {
	var b strings.Builder
	fmt.Fprintf(&b, "<worker-result agent_id=%q type=%q status=%q>\n",
		snap.ID, snap.Type, snap.Status)
	if snap.Description != "" {
		fmt.Fprintf(&b, "<summary>%s</summary>\n", snap.Description)
	}
	if !snap.CompletedAt.IsZero() && !snap.StartedAt.IsZero() {
		ms := snap.CompletedAt.Sub(snap.StartedAt).Milliseconds()
		fmt.Fprintf(&b, "<duration_ms>%d</duration_ms>\n", ms)
	}
	if snap.Error != nil {
		fmt.Fprintf(&b, "<error>%s</error>\n", snap.Error.Error())
	}
	if snap.Result != "" {
		b.WriteString("<result>\n")
		b.WriteString(snap.Result)
		b.WriteString("\n</result>\n")
	}
	b.WriteString("</worker-result>")
	return b.String()
}

// CleanupSession removes all worktrees belonging to this session.
func (c *Coordinator) CleanupSession() error {
	return c.worktrees.CleanupSession(c.sessionID)
}

// composeWorkerSystemPrompt builds the system prompt for a worker.
// It prepends a small role preamble + the worktree path so the worker
// knows where it lives, then appends the user-provided base prompt
// (typically the main agent's system prompt without coordinator
// instructions).
func composeWorkerSystemPrompt(base, workerType, worktreePath string) string {
	var b strings.Builder
	fmt.Fprintf(&b, "You are a sub-agent of type \"%s\". You were spawned by an orchestrator to perform a focused task.\n", workerType)
	fmt.Fprintf(&b, "Your working directory is %s — a git worktree isolated from other workers.\n", worktreePath)
	b.WriteString("All file paths in your tools are resolved relative to this working directory.\n")
	b.WriteString("You CANNOT spawn further sub-agents. Complete your task with the tools you have, then return a concise summary as your final assistant message.\n")
	if base != "" {
		b.WriteString("\n---\n\n")
		b.WriteString(base)
	}
	return b.String()
}

// newCoordinatorWorkerID generates a worker ID. Mirrors subagent's
// scheme but is generated by the coordinator since worktree creation
// happens before subagent.Manager.Spawn.
func newCoordinatorWorkerID(typ string) string {
	if typ == "" {
		typ = "agent"
	}
	return fmt.Sprintf("%s-%d", typ, time.Now().UnixNano()%1_000_000_000)
}
