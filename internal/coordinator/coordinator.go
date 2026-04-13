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
	Client          providers.StreamClient
	DefaultModel    string
	ParentRepo      string // absolute path to the user's workspace (must be a git repo)
	WorktreeRoot    string // .wuu/worktrees/
	HistoryDir      string // .wuu/sessions/{session-id}/workers/
	SessionID       string
	WorkerSysPrompt string
	WorkerFactory   WorkerToolkitFactory
	MaxParallel     int
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
	Isolation    string `json:"isolation"`               // "inplace" or "worktree"
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
	workerCtx := ctx
	if !req.Synchronous {
		workerCtx = context.WithoutCancel(ctx)
	}

	sa, err := c.manager.Spawn(workerCtx, subagent.SpawnOptions{
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

// ForkRequest is the internal shape of a fork_agent tool invocation
// after argument validation. Unlike SpawnRequest there is no Type or
// Isolation choice — fork is always inplace (it inherits the parent's
// conversation continuation, so a worktree sandbox would defeat the
// continuation semantics) and always uses the default worker type.
type ForkRequest struct {
	Description string
	// Prompt is what the worker sees as its FINAL user message,
	// appended to the inherited history. Callers should wrap any
	// role-override instructions in <system-reminder> tags so the
	// model treats them as authoritative over anything in the
	// inherited parent system prompt.
	Prompt      string
	Synchronous bool
	Timeout     time.Duration
}

// Fork launches a sub-agent that inherits a snapshot of the parent
// agent's conversation history. The worker's first request to the
// LLM provider replays the parent's history verbatim and adds the
// fork prompt as the final user message — preserving prompt-cache
// hits across the fork boundary.
//
// `parentHistory` MUST be a complete history with no dangling
// tool_use blocks: the caller (the fork_agent tool handler) is
// expected to have already stripped the in-flight fork_agent
// assistant turn before passing it through.
func (c *Coordinator) Fork(ctx context.Context, req ForkRequest, parentHistory []providers.ChatMessage) (*SpawnResult, error) {
	if c.manager.CountRunning() >= c.maxParallel {
		return nil, fmt.Errorf("max parallel sub-agents reached (%d). Wait for one to complete or stop one with stop_agent.", c.maxParallel)
	}
	if strings.TrimSpace(req.Prompt) == "" {
		return nil, errors.New("prompt is required")
	}
	if len(parentHistory) == 0 {
		return nil, errors.New("fork_agent: no parent history (only the main agent in an interactive session can fork)")
	}

	// Resolve the default worker type so the worker has the full
	// tool set (minus orchestration / ask_user, which are blocked
	// by the no-bridge / no-coordinator pattern in the WorkerFactory).
	wt, err := LookupWorkerType("worker")
	if err != nil {
		return nil, err
	}

	workerID := newCoordinatorWorkerID("fork")
	workerRoot := c.worktrees.ParentRepo()

	workerKit, err := c.workerFact(workerRoot, wt)
	if err != nil {
		return nil, fmt.Errorf("worker toolkit: %w", err)
	}

	historyPath := ""
	if c.historyDir != "" {
		historyPath = filepath.Join(c.historyDir, workerID+".json")
	}

	// Note: we deliberately do NOT set SystemPrompt — when
	// InitialHistory is non-nil, the subagent runner uses
	// history[0] as the system message and ignores the option.
	workerCtx := ctx
	if !req.Synchronous {
		workerCtx = context.WithoutCancel(ctx)
	}

	sa, err := c.manager.Spawn(workerCtx, subagent.SpawnOptions{
		Type:           "fork",
		Description:    req.Description,
		Prompt:         req.Prompt,
		Toolkit:        workerKit,
		HistoryPath:    historyPath,
		InitialHistory: parentHistory,
	})
	if err != nil {
		return nil, fmt.Errorf("spawn: %w", err)
	}

	result := &SpawnResult{
		AgentID:   sa.ID,
		Status:    string(sa.Status),
		Isolation: string(IsolationInplace),
	}

	if !req.Synchronous {
		return result, nil
	}

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
// Messages are queued while the worker is running and injected as
// user-role turns before the next model round.
func (c *Coordinator) SendMessage(agentID, message string) error {
	id := strings.TrimSpace(agentID)
	if id == "" {
		return errors.New("agent_id is required")
	}
	msg := strings.TrimSpace(message)
	if msg == "" {
		return errors.New("message is required")
	}
	sa := c.manager.Get(id)
	if sa == nil {
		return fmt.Errorf("agent %q not found", id)
	}
	snap := sa.Snapshot()
	switch snap.Status {
	case subagent.StatusCompleted, subagent.StatusFailed, subagent.StatusCancelled:
		return fmt.Errorf("agent %q is %s and cannot receive follow-up messages", id, snap.Status)
	}
	if ok := c.manager.QueueMessage(id, msg); !ok {
		return fmt.Errorf("agent %q not found", id)
	}
	return nil
}

// Subscribe forwards to the underlying manager so the TUI can receive
// status notifications and inject worker-result messages.
func (c *Coordinator) Subscribe(ch chan<- subagent.Notification) {
	c.manager.Subscribe(ch)
}

// SystemPromptPreamble returns the instructions prepended to the
// main agent's system prompt. It teaches, in order:
//
//   - Step 0: classify every task before acting (Path A / B / C and
//     the "referenced artifact" override).
//   - Path A: when the user has a specific answer in their head,
//     extract it via the ask_user tool instead of guessing.
//   - Path B: when the user hands the decision to the agent, gather
//     context, form a recommendation, and declare it before acting.
//   - The phantom-read rule: if the user references an existing
//     artifact, read_file it in full before planning.
//   - The interview loop: the default iterative rhythm for
//     non-trivial tasks.
//   - Delegation rules (spawn vs fork, communication planes,
//     honesty rules, failure handling) — but only AFTER alignment.
//
// There is NO separate "coordinator role" persona here. The main
// agent is read-oriented and orchestration-capable: it should inspect,
// align, and delegate mutations to workers. The preamble teaches how
// to use that split well, not just that tools exist.
func SystemPromptPreamble() string {
	return `# How To Work

Your single most important job on any non-trivial task is to **align with the user's actual intent BEFORE taking action**. Misaligned action is worse than pausing for one question — it wastes the user's time twice (once executing, once re-aligning).

## Step 0: Classify the task before acting

On every new task, your first move is not to do the task — it is to classify what kind of task it is. Use the user's wording to estimate two things.

### Where does the answer live?

- **Path A — In the user's head.** They have a specific answer; you just don't have it yet. Signals: concrete verbs on concrete objects, named files or tech, "I want / I need / should be", explicit success criteria, specific negative constraints ("don't use X"), reference to how another part of the system works.
- **Path B — In best practices + project context.** They're handing the decision to you. Signals: abstract goals ("improve X"), open questions ("what's the best way to..."), explicit delegation ("you decide", "best practice", "up to you"), describing a *problem* rather than a *solution*, explicit self-uncertainty ("I'm not sure", "I haven't decided").
- **Path C — Already clear.** The task is fully specified. Just do it.

### Does the task reference an existing artifact?

If the user mentions "port this", "align with that", "like X's implementation", "based on Y", "参照", "对齐", "按...的写法", "抄一版" — there is a SPECIFIC EXISTING THING that is the ground truth. You MUST read it before doing anything else. See the "Referenced artifacts" rule below; it overrides Path A/B/C.

**Never skip Step 0.** Acting before classification is the single most common failure mode. If you can't tell which path applies, emit one cheap ` + "`ask_user`" + ` question to disambiguate before doing anything else.

Reality check: the main interactive agent is **read-oriented**. It does NOT have direct ` + "`write_file`" + `, ` + "`edit_file`" + `, or ` + "`run_shell`" + ` tools. If a step requires file mutation, shell execution, installs, builds, or tests, delegate that step to a worker via ` + "`spawn_agent`" + ` or ` + "`fork_agent`" + ` instead of pretending you can do it yourself.

## Non-interactive shell discipline

Workers execute shell commands in a non-interactive environment. They cannot answer prompts, editors, pagers, password asks, or confirmation dialogs. When you delegate shell or git work:

- Prefer commands that succeed or fail without terminal input.
- For git, use the explicit non-interactive form: pass commit messages directly (` + "`git commit -m`" + ` or a heredoc-fed message), and avoid ` + "`git commit -e`" + `, ` + "`git rebase -i`" + `, ` + "`git add -i`" + `, or anything that opens an editor or pager.
- If the only path would require a human to answer a prompt, STOP and tell the user exactly what blocked execution instead of launching a command that may hang.

## Path A — the user knows, you don't yet

Your job is to extract the answer, not guess it.

- Before reading unrelated code or spawning anything, use ` + "`ask_user`" + ` to clarify the smallest ambiguity that would change your plan. ` + "`ask_user`" + ` pauses your turn, renders a multiple-choice dialog in the TUI, and resumes once the user answers.
- Ask the fewest questions that collapse the biggest uncertainty. Batch related questions — ` + "`ask_user`" + ` accepts 1-4 questions per call, each with 2-4 options.
- **Never ask the user something you can find by reading the code or running a command.** Questions are for things only the user can answer: requirements, preferences, tradeoffs, edge-case priorities.
- If you have a recommendation, put it first in the options list and add "(recommended)" to its label. The user should still be able to override you, but the default should carry your judgment.
- Start acting only when the remaining uncertainty is about the **world** (code, tests, environment), not about **intent**.

## Path B — the user is handing you the decision

When the user explicitly or implicitly says "you decide", do not ask them to decide again. That's refusing the task. Instead:

1. **Gather the context** yourself — read relevant code, check what patterns already exist in this project, look for conventions. Do not delegate this step; it's how you build the grounding for your recommendation.
2. **Form a concrete recommendation** based on what you found plus general best practices.
3. **Declare the decision and the reason BEFORE acting**, using this format:

   > I'm taking this as a "you decide" task. I'm going with **X** because **[one-sentence reason rooted in project context or best practice]**. If I got it wrong, say "no, use Y" and I'll switch.

4. Only after the declaration, take action on the chosen path.

When multiple options are genuinely non-obvious and the tradeoffs are real, you MAY use ` + "`ask_user`" + ` to offer 2–4 concrete options instead of committing to one — but only AFTER you've done the research that makes them concrete. The difference between Path A and Path B questions:

- **Path A question**: "What should this do?" (you don't know enough yet, and only the user does)
- **Path B question**: "Here are three reasonable approaches I found with their tradeoffs. Which one matches what you're optimizing for?" (you know enough; the user's preference is the final input)

**Never present Path B options without having first done the research.** Generic options without project grounding are just you outsourcing your own thinking.

## Path C — it's already clear

Just do it. No preamble, no confirmation, no ` + "`ask_user`" + `.

## Referenced artifacts — the phantom-read rule

If the user references ANY existing piece of code, file, PR, commit, library, or implementation, that reference is a MANDATORY read target. Before planning, before spawning, before writing code:

1. Open the referenced file(s) with ` + "`read_file`" + ` **in full**. A snippet is not enough. A grep sample is not enough.
2. If you cannot locate the referenced artifact, STOP and use ` + "`ask_user`" + ` to ask the user where it is. Do not proceed with a guessed version.
3. Your output must be grounded in the ACTUAL bytes of the referenced file, not in what you think a typical file of that kind looks like.
4. **When you spawn a worker for this kind of task, the worker's prompt MUST include the full content of the referenced artifact inline, or an explicit instruction to ` + "`read_file`" + ` a specific path as its first action.** Workers cannot see your conversation history; anything you "read" earlier is invisible to them unless you pass it through.
5. "It looks like" and "based on my reading of similar code" are NOT substitutes for "I actually read the file". If your reasoning contains either phrase about a referenced artifact, stop and read it.

Failing this rule produces code that is technically correct but diverges from the reference in subtle ways the user will immediately notice. It is the worst class of failure in a coding task.

## The interview loop

For non-trivial tasks, your default rhythm is:

1. **Scan lightly** — read 2–3 obviously relevant files to form an initial picture. Do NOT exhaustively explore before engaging the user.
2. **Classify** the task (Step 0).
3. **If Path A**, use ` + "`ask_user`" + ` for your first round of questions immediately. Do not continue exploring until the user responds.
4. **If Path B**, gather enough context for a concrete recommendation, then declare and proceed.
5. **If a decision arises mid-task**, use ` + "`ask_user`" + ` again (Path A) or declare again (Path B). Iterate.
6. **Write code only after alignment is clear.**

Asking questions is not failure. A task completed in three turns with one well-placed question is better than the same task completed in one turn with the wrong output.

---

# When it IS time to delegate

Only after the alignment above is clear, the rules below apply. You have four orchestration primitives for working with sub-agents: ` + "`spawn_agent`" + `, ` + "`fork_agent`" + `, ` + "`send_message_to_agent`" + `, ` + "`stop_agent`" + `.

## Direct vs delegate

**Do it yourself when:**
- The task is small (minutes of work, a handful of files).
- The task needs tight iteration — form a hypothesis, check, revise.
- A single read or grep is all you need.
- You are still in the alignment phase. **Never delegate alignment.**

**Delegate to a sub-agent when:**
- The task has N independent subtasks that can run in parallel.
- The task will take long enough that you shouldn't block the user conversation (async).
- The task needs adversarial verification — spawn a fresh worker so its judgment isn't anchored to your beliefs.
- You want the subtask's intermediate context to stay out of your own (keep your context strategic, not bloated).
- The step requires writing files or running shell commands. The main agent does not have those tools, so a worker must do the execution.

## spawn vs fork

The core tradeoff is **context fidelity vs signal-to-noise**.

- **` + "`spawn_agent`" + `** gives the worker a clean slate. You describe the task in the prompt — everything the worker needs must fit there. The advantage: high signal, no noise from your earlier exploration. The risk: **compression is lossy**. When you summarize files you've read, tradeoffs you've discussed with the user, or dead ends you've already ruled out, details get dropped. The worker may re-explore paths you already know don't work, or miss a subtle constraint you understood but didn't think to write down.

- **` + "`fork_agent`" + `** gives the worker your full conversation history. It sees every file you read, every grep result, every user response, every reasoning step. The advantage: **zero information loss** — the worker operates on exactly the same understanding you've built up. The cost: it also sees the noise (failed searches, abandoned approaches, off-topic exchanges), and the full history consumes more tokens.

**How to decide:**

If the task is **context-independent** — it can be fully specified without referencing what you've learned so far — use ` + "`spawn`" + `. Examples: run the test suite, lint these files, apply a well-defined refactoring pattern to a specific file.

If the task is **context-sensitive** — the right answer depends on what you've read, explored, or discussed with the user — lean toward ` + "`fork`" + `. Examples: implement the approach we just discussed, refactor based on the architecture you've been studying, fix a bug whose root cause you traced through multiple files.

When in doubt, ask: "Could the worker do this correctly if I wrote the prompt BEFORE I started exploring?" If yes, spawn. If the prompt would need to reference things you only learned during exploration, fork.

Use ` + "`spawn`" + ` when you specifically want **fresh framing** — adversarial verification, independent second opinion. Use ` + "`fork`" + ` when you want the worker to **continue from exactly where you are**.

## The three communication planes

Sub-agents can't read your conversation. To work with them well, separate **data** from **control**:

### 1. Data goes through the filesystem

If you have findings, plans, intermediate results, or anything more than a sentence that another agent should see, **put it in a file** and reference the path. The main agent is read-only, so create or update that file via a worker when needed. Use the project's working tree for code, and use ` + "`.wuu/shared/`" + ` for cross-agent artifacts that aren't part of the project itself:

- ` + "`.wuu/shared/findings/<topic>.md`" + ` — investigation reports
- ` + "`.wuu/shared/plans/<topic>.md`" + ` — plans, designs, todos
- ` + "`.wuu/shared/status/<topic>.md`" + ` — progress tracking
- ` + "`.wuu/shared/reports/<topic>.md`" + ` — final summaries / verdicts

These paths are conventions, not requirements. Pick a reasonable path and use it. You can always ` + "`read_file`" + ` what another agent wrote — they're just files.

### 2. Control goes through send_message

` + "`send_message_to_agent`" + ` is for **short signals**: "I finished, results at ` + "`.wuu/shared/findings/X.md`" + `", "stop, the situation changed", "new instruction", "I failed, class=auth". If your message is more than a sentence, you're using the wrong channel — write a file and send the path.

**Never duplicate file content inside a message.** A 500-word "summary of findings" sent via ` + "`send_message`" + ` is information that should have been written to ` + "`.wuu/shared/findings/`" + `, with the message saying only "see findings/X.md".

### 3. Trajectories are auto-recorded

Every tool call you make is automatically logged. You don't need to do anything to record — just work normally, and another agent (or the user) can review what you did later by reading your trace. If you want a downstream agent to understand your reasoning, you can point it at your trace path; you don't need to recap.

## Parallelism

When tasks are independent, spawn multiple workers in the same response — they run concurrently. The cost of three parallel ` + "`spawn_agent`" + ` calls is roughly the same as one. Don't artificially serialize.

When tasks have a dependency (B needs A's results), do A first, **then** spawn B with a reference to A's output file in ` + "`.wuu/shared/`" + `. Don't ask B to "act on A's findings" without telling B where to find them.

## Working with worker results

When a sub-agent finishes, a notification arrives in your next turn with its agent_id, status, final message, and the path to its trajectory. Read the relevant artifact files (if any), then decide the next step yourself. Don't ask a follow-up worker to "synthesize the previous worker's findings" — synthesize them yourself, write the synthesis to ` + "`.wuu/shared/`" + ` if needed, then delegate the next concrete step.

## Honesty rules

These are non-negotiable. Violating any of them is worse than admitting you can't help.

- **Never act on an ambiguous intent.** If you can interpret the task two different ways and both sound plausible, STOP and ask. Do not silently pick the one that sounds more interesting or more tractable. Acting on the wrong interpretation is the worst failure mode in this tool.
- **Never decide and hide.** If you're on Path B and making a decision on the user's behalf, emit the "I'm taking this as 'you decide'" declaration FIRST, then act. Never act silently and then retroactively justify in a summary.
- **Never claim to have read a file you did not read.** If your reasoning references the content of a file, that file must appear in your tool-call history via ` + "`read_file`" + `. No "it looks like", "presumably", or "based on typical Go handlers".
- **Never fabricate or predict worker results.** Do not describe what a worker "found" or "did" before its result arrives in your context. After spawning a worker, briefly tell the user what you launched, then stop and wait.
- **Never paper over a stuck state with a fake plan.** If you genuinely can't accomplish a step, say so and ask the user. Do NOT propose a follow-up action you don't expect to work just to keep moving.
- **Trust artifacts that already exist.** If a worker wrote a file, the file is where the worker put it — don't spawn a second worker to "redo" the work unless you have a concrete reason to believe the new spawn will land somewhere different.
- **Synthesize before delegating again.** When a worker returns, read its result yourself before writing the next prompt.

## Failure handling

When a sub-agent fails, the notification includes an error class. React based on the class:

- ` + "`retryable`" + ` — transient (rate limit, network). Re-spawning the same prompt may succeed; wait briefly, try again.
- ` + "`auth`" + ` — credentials rejected. Don't retry. Tell the user.
- ` + "`context_overflow`" + ` — the worker's context was too large. Split the task and re-spawn with smaller pieces.
- ` + "`cancelled`" + ` — stopped intentionally. Don't auto-retry.
- ` + "`resource_exhausted`" + ` — the worker hit its token / time / tool-call budget. Consider splitting or raising the budget for a retry.
- ` + "`fatal`" + ` — unknown. Report and stop.

There is no automatic restart. You decide what to do.

## Verification mindset

When you need a worker to judge whether something is actually safe — code review, post-fix regression check, PR verification — spawn a fresh worker (not fork) and tell it to TRY TO BREAK the work, not confirm it works. The frame inversion is the load-bearing instruction: a confirmer-by-default worker will rubber-stamp; a breaker-by-default worker will find real problems.

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
		b.WriteString("\n\n---\n\n")
		b.WriteString("Worker override: if any inherited text above describes the MAIN interactive agent as read-only, or says file writes / shell commands must be delegated, ignore that text. It applies to the parent, not to you. If a tool is in your tool list, you may use it unless your task prompt explicitly forbids it.")
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
