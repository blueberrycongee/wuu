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
// don't spawn sub-sub-agents. The worker type is provided so the
// factory can apply tool whitelisting via FilterToolsForWorker.
type WorkerToolkitFactory func(rootDir string, wt WorkerType) (agent.ToolExecutor, error)

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
	// Client is the streaming LLM client every worker spawned by this
	// coordinator will share. It must be a StreamClient (not just a
	// Client) so workers run through the same streaming transport as
	// the interactive main agent.
	Client       providers.StreamClient
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
	BaseRepo    string // optional: chain off another worktree (worktree mode only)
	Synchronous bool
	Timeout     time.Duration
	// Isolation overrides the worker type's DefaultIsolation when set.
	// Empty string means "use the type default". Use this from
	// spawn_agent to opt a normally-inplace worker into a worktree
	// (e.g. an explorer that needs to run a destructive script).
	Isolation string
}

// SpawnResult is what the spawn_agent tool returns to the model.
type SpawnResult struct {
	AgentID      string `json:"agent_id"`
	Status       string `json:"status"`
	Isolation    string `json:"isolation"`              // "inplace" or "worktree"
	WorktreePath string `json:"worktree_path,omitempty"` // empty for inplace spawns
	Result       string `json:"result,omitempty"`
	Error        string `json:"error,omitempty"`
	DurationMS   int64  `json:"duration_ms,omitempty"`
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

	// Resolve worker type (validates the name).
	wt, err := LookupWorkerType(req.Type)
	if err != nil {
		return nil, err
	}
	wtype := wt.Name

	workerID := newCoordinatorWorkerID(wtype)

	// Resolve effective isolation: caller override > type default.
	isolation, err := NormalizeIsolation(req.Isolation, wt)
	if err != nil {
		return nil, err
	}
	// BaseRepo only makes sense for chained worktree spawns.
	if isolation == IsolationInplace && strings.TrimSpace(req.BaseRepo) != "" {
		return nil, errors.New("base_repo is only supported with isolation=worktree")
	}

	// 1. Determine the worker's working directory.
	//    - inplace: share the parent repo (no checkout cost)
	//    - worktree: `git worktree add --detach` based on parent HEAD
	var (
		workerRoot  string
		worktreeRef *worktree.Worktree
	)
	if isolation == IsolationWorktree {
		worktreeRef, err = c.worktrees.Create(c.sessionID, workerID, req.BaseRepo)
		if err != nil {
			return nil, fmt.Errorf("worktree create: %w", err)
		}
		workerRoot = worktreeRef.Path
	} else {
		workerRoot = c.worktrees.ParentRepo()
	}

	// 2. Build worker's toolkit rooted at the chosen working directory.
	workerKit, err := c.workerFact(workerRoot, wt)
	if err != nil {
		if worktreeRef != nil {
			_ = c.worktrees.Cleanup(worktreeRef)
		}
		return nil, fmt.Errorf("worker toolkit: %w", err)
	}

	// 3. Compose system prompt: type-specific role + working dir + base prompt.
	sys := composeWorkerSystemPrompt(c.defaultSys, wt, workerRoot, isolation)

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
		if worktreeRef != nil {
			_ = c.worktrees.Cleanup(worktreeRef)
		}
		return nil, fmt.Errorf("spawn: %w", err)
	}

	result := &SpawnResult{
		AgentID:   sa.ID,
		Status:    string(sa.Status),
		Isolation: string(isolation),
	}
	if worktreeRef != nil {
		result.WorktreePath = worktreeRef.Path
	}

	if !req.Synchronous {
		// Async path: schedule background recycle once the worker
		// finishes. Detached context so it survives a cancelled
		// parent (the worker itself runs detached too).
		if worktreeRef != nil {
			go c.recycleWorktreeWhenDone(sa.ID, worktreeRef)
		}
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

	// Sync recycle: drop the worktree if the worker left it pristine.
	// Anything dirty stays on disk so the orchestrator (or user) can
	// inspect / merge it.
	if worktreeRef != nil {
		if kept, cerr := c.worktrees.CleanupIfClean(worktreeRef); cerr == nil && !kept {
			result.WorktreePath = "" // recycled — no path to surface
		}
	}
	return result, nil
}

// recycleWorktreeWhenDone is the async-spawn cleanup tail. It blocks
// on the worker's completion, then attempts to drop the worktree if
// nothing was modified. Errors are intentionally swallowed: cleanup is
// best-effort and the user can always run `git worktree prune` later.
func (c *Coordinator) recycleWorktreeWhenDone(agentID string, wtRef *worktree.Worktree) {
	if wtRef == nil {
		return
	}
	// Long ceiling — workers can legitimately run for a while. The
	// real cap comes from the worker's own context, not this wait.
	ctx, cancel := context.WithTimeout(context.Background(), 1*time.Hour)
	defer cancel()
	if _, err := c.manager.Wait(ctx, agentID); err != nil {
		return
	}
	_, _ = c.worktrees.CleanupIfClean(wtRef)
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

// SystemPromptPreamble returns the role definition the main
// orchestrator should be told about. Prepend it to the user's
// existing system prompt when coordinator mode is enabled.
func SystemPromptPreamble() string {
	return `# Coordinator Mode

You are a coordinator. Your job is to:
- Help the user achieve their goal.
- Direct sub-agents (workers) to research, implement, and verify code changes.
- Synthesize results and communicate clearly with the user.
- Answer questions directly when you can — don't delegate work that needs no tools.

## Your tools

You have ONLY 6 tools:

- **spawn_agent** — launch a sub-agent. There is exactly one worker type: ` + "`worker`" + `. It has the full tool set (read, write, edit, shell). To put it into a specialized role (verification, focused research) use one of the prompt presets below — see "Worker prompt presets".
- **send_message_to_agent** — send a follow-up to an existing sub-agent.
- **stop_agent** — halt a running sub-agent.
- **list_agents** — see all sub-agents in this session and their status.
- **list_files** — peek at directory contents (cheap, no context pollution).
- **glob** — find file paths by pattern (cheap, no context pollution).

You CANNOT read file contents directly, run shell commands, or edit files.
Anything that touches file contents must go through a sub-agent.

## Where workers run

**Workers share the user's repository by default.** When a worker creates or edits a file, that change lands directly in the user's working tree where they can see it — exactly the same place you'd write if you had file tools yourself. You almost never need to think about "where will this end up"; the answer is "in the repo, like you'd expect."

The rare exception is the ` + "`isolation: \"worktree\"`" + ` opt-in on spawn_agent, which runs the worker in a throwaway git worktree. Reach for it ONLY when:
- the work might break the build and you want a sandbox to verify before merging,
- two writers need to touch the same files concurrently and would otherwise collide,
- the user explicitly asked for an isolated experiment.

If none of those apply, omit ` + "`isolation`" + ` and let the worker write to the main repo. **Do not** use a worktree just because the task involves writing files — additive writes (new docs, new tests, new fixtures) are not a reason for isolation.

## How to work

1. **Understand the task.** If the user asked a question you can answer directly, just answer.
2. **Plan minimal delegation.** What's the smallest set of sub-agents that can complete this?
3. **Write self-contained prompts.** Each sub-agent CANNOT see your conversation. Include file paths, line numbers, requirements, and acceptance criteria explicitly.
4. **Parallelism is your superpower.** When tasks are independent, spawn multiple workers in the same response — they run concurrently.
5. **Synthesize, don't forward.** When a worker returns, include the file paths and line numbers in your follow-up prompts. Never write "based on your findings" — prove you understood. You never hand off understanding to another worker.
6. **One worker can't check on another.** If you need a verification step, spawn a fresh worker with the VERIFICATION preset (see below).
7. **Use list_files / glob for cheap geography.** Knowing the project layout helps you write better worker prompts. But file CONTENTS go through workers.

## Worker prompt presets

Two preset blocks are available below. When the task you're delegating matches one of them, **copy the entire block VERBATIM to the start of the worker prompt**, then add the task-specific instructions after it. Do NOT paraphrase the presets — each line is tuned to a specific failure mode and rephrasing weakens it.

- **VERIFICATION preset** — use when you need a worker to judge whether a change is actually safe (code review, post-fix regression check, PR verification, release readiness gate). The preset inverts the worker's frame from "confirm it works" to "try to break it" and forces it to back every PASS with command output.
- **RESEARCH preset** — use when you need a worker to investigate the codebase before deciding what to do (analyze a module, locate a bug origin, find all callers, study third-party usage). The preset enforces read-only behavior, focused scope, and file:line citations on every claim.

If neither preset applies (most implementation tasks), write the worker prompt directly without a preset.

### VERIFICATION preset

Paste this block VERBATIM at the start of the worker prompt when you need a verification mindset:

` + "```" + `
` + VerificationPreset + `
` + "```" + `

### RESEARCH preset

Paste this block VERBATIM at the start of the worker prompt when you need a focused read-only investigation:

` + "```" + `
` + ResearchPreset + `
` + "```" + `

## Honesty rules

These are non-negotiable. Violating any of them is a worse failure than admitting you couldn't help.

- **Never fabricate or predict worker results.** Do not describe what a worker "found" or "did" before its <worker-result> arrives in your context. After spawning a worker, briefly tell the user what you launched and stop — wait for the result message to come back.
- **Never paper over a stuck state with a fake plan.** If you genuinely cannot accomplish a step (e.g. you tried the obvious worker spawn and it didn't produce what the user asked for), say so plainly and ask the user how to proceed. Do NOT propose a follow-up action you don't actually expect to work just to keep the conversation moving.
- **If a worker already produced an artifact, that artifact exists where the worker put it.** Don't spawn a second worker to "redo" or "move" the first worker's output unless you have a concrete reason to believe the second spawn will reach a different filesystem location than the first.
- **Synthesize before delegating again.** When a worker returns, read its result yourself before writing the next prompt. Don't ask one worker to "act on the previous worker's findings" — translate those findings into a concrete spec yourself, then send the spec.

## Sub-agent results

When a sub-agent completes, you'll see a <worker-result> message in your context with the agent_id, status, and the worker's final summary. Read it carefully and decide the next step.

When a sub-agent fails, the message contains an <error class="..."> tag. Use the class to decide whether to retry:

- ` + "`retryable`" + ` — transient (rate limit, server error, network blip). Re-spawning the SAME prompt has a real chance of succeeding. Wait briefly, then try again.
- ` + "`auth`" + ` — credentials rejected. DO NOT retry. Report to the user that their API key/config needs fixing.
- ` + "`context_overflow`" + ` — the worker's prompt was too big even after auto-compact. Re-spawning won't help; split the task into smaller pieces.
- ` + "`cancelled`" + ` — the worker was stopped on purpose (Ctrl+C). Don't auto-retry.
- ` + "`fatal`" + ` — unknown / non-recoverable. Report the error to the user and stop.

`
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
		class := ClassifyError(snap.Error)
		fmt.Fprintf(&b, "<error class=%q>%s</error>\n", class, snap.Error.Error())
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
// It prepends the worker type's role-specific prompt + a description
// of the working directory and isolation mode, then appends the base
// prompt (typically the main agent's project memory and skills, NOT
// the coordinator instructions).
func composeWorkerSystemPrompt(base string, wt WorkerType, workerRoot string, isolation IsolationMode) string {
	var b strings.Builder
	b.WriteString(wt.SystemPrompt)
	b.WriteString("\n\n")
	switch isolation {
	case IsolationWorktree:
		fmt.Fprintf(&b, "Your working directory is %s — a git worktree isolated from other workers. ", workerRoot)
		b.WriteString("Edits you make stay sandboxed; the orchestrator will inspect the worktree after you finish. ")
	default: // inplace
		fmt.Fprintf(&b, "Your working directory is %s — the SHARED parent repository. ", workerRoot)
		b.WriteString("You are running inplace (no worktree isolation), so be especially careful: ")
		b.WriteString("read-only operations are safe, but any file you modify is visible to the orchestrator and other workers immediately. ")
	}
	b.WriteString("All file paths in your tools resolve relative to this directory. ")
	b.WriteString("You CANNOT spawn further sub-agents.\n")
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
