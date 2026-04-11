# Agent Mode Architecture

## Context

wuu currently enters a "limited coordinator mode" whenever the workspace is a git repository (`cmd/wuu/main.go:439`, `toolkit.SetCoordinatorOnly(true)`). In this mode the main agent loses its file / shell tools and is reduced to 6 orchestration tools (`spawn_agent`, `send_message_to_agent`, `stop_agent`, `list_agents`, `list_files`, `glob`). The assumption is that removing tools from the main agent simplifies its role to "pure coordinator" and produces cleaner multi-agent behavior.

That assumption is wrong, and this document explains why, and what to do instead. The target is a design that (a) covers the cases limited coordinator mode handles badly today, (b) stays correct as models get more capable, and (c) requires minimal code change to reach from the current codebase.

## The Thing We Got Wrong

The limited coordinator mode is an answer to a non-problem. Its implicit premise is "the main agent can't hold too many tools, so let's give it fewer". But traditional-mode agents with 10+ tools (Read, Edit, Bash, Grep, Glob, Web*) work well today — traditional mode is the proof that model tool-use capacity is not the bottleneck. Removing tools doesn't help the main agent think better; it forces it to delegate trivial things (read a single file, check a function) through a worker round-trip, which is strictly worse.

The real value of multi-agent systems is structural, not cognitive:

- **Parallelism** — multiple agents run concurrently. Single-agent traditional mode is sequential.
- **Async / long-running tasks** — one agent can run for 20 minutes while another handles user conversation.
- **Context isolation** — a worker's intermediate state dies with the worker, keeping the parent's context clean.
- **Failure isolation** — a confused worker doesn't corrupt the main agent's context.
- **Adversarial framing** — a fresh worker with a VERIFICATION posture avoids the parent's confirmation bias.
- **Resource budgeting** — each agent has its own token / time / tool-call limit.

None of these require removing tools from the main agent. They require **adding orchestration primitives** on top of a fully-capable agent. The right framing is not "coordinator mode vs traditional mode". It is: **one kind of agent, with orchestration primitives as just more tools.** The difference between "main brain" and "worker" is not structural — it's just "the one the user is currently talking to".

## Core Philosophy

> All agents are the same atomic unit. They share data through the filesystem, share control through messages, share execution history through trajectories. There is no special "main brain" role — only "the agent the user is currently talking to".

Every design decision below is a direct consequence of this sentence. The bitter-lesson discipline is: **design topology and physical constraints, do not impose content schemas on what agents say or how they reason**. The code defines channels and lifetimes; the model decides content.

## 1. The Atomic Unit: Agent

### Tool set (uniform across all agents)

Every agent — including the first one the user talks to — has the same tool set. No hierarchy of capability.

```
# Data plane (world access)
read_file, write_file, edit_file
list_files, glob, grep
run_shell
web_search, web_fetch

# Control plane (inter-agent signaling)
send_message(agent_id, text)
stop(agent_id)

# Derivation (agent creation)
spawn(prompt)      # fresh child, no inherited context
fork(prompt)       # child inherits caller's current context verbatim
```

~13 tools total. Traditional mode already proves models handle this volume. There is no `list_agents`, no `peek`, no `query_kb`, no `verify_claim`, no `declare_plan`. These are redundant with existing capabilities or are the system thinking on the model's behalf.

### Context layout

An agent's context has three sections, in order:

```
[system prompt]
  - generic role description + three-plane discipline (see §5)
  - project memory (AGENTS.md / CLAUDE.md — existing mechanism)
  - skills (existing mechanism)
  - if spawned: the spawn prompt
  - if forked: the parent's full history

[auto-injected state (refreshed every turn)]
  - active agent list (id + status + short description)
  - unread messages from this agent's mailbox
  - resource budget usage (X / Y tokens, X / Y tool calls)

[conversation history]
  - tool calls + results + thinking + messages (the trajectory)
```

Active agent list is **passively injected**, not queried through a `list_agents` tool. This saves a tool call per decision point and eliminates a tool the model would otherwise need to remember to use.

### Lifecycle

```
pending → running → {completed, failed, cancelled, idle}
                         ↑
                      idle can be woken by send_message → running
```

The `idle` state is the important addition. An agent that finishes its initial task does not die immediately — it can suspend, waiting for `send_message` to resume. This enables long-lived "specialist" agents: the main agent keeps a reference to a worker that finished its initial survey, and later sends follow-up questions without respawning. Idle agents that stay idle past a threshold are compacted or reclaimed.

### Resource budget

Every agent has a hard budget:

```
max_tokens:     200000     # context cap
max_time_sec:   1800       # wall clock cap
max_tool_calls: 100        # tool call count cap
```

Budget exhaustion → `failed` with error class `resource_exhausted`. Budgets exist so the system has a physical safety net that is independent of model judgment. They are the only "policy" the code enforces; everything else is the model's call.

## 2. Three Communication Planes

The communication model separates data, control, and history into three distinct channels with different properties. Each plane has a specific role; they do not substitute for each other.

| Plane | Medium | Carries | Bandwidth | Latency | Persistence |
|---|---|---|---|---|---|
| **Data** | filesystem | findings, plans, artifacts, understanding | high | pull | durable |
| **Control** | `send_message` | events, instructions, status signals | low | push (immediate) | ephemeral |
| **Trajectory** | trace files (auto) | execution record (automatic) | passive | post-hoc | durable |

### 2.1 Data plane — the filesystem

**All cross-agent data transfer goes through the filesystem, not through messages.**

The information-theoretic reason: the LLM is a lossy encoder-decoder. Every agent-to-agent prose transfer (A composes a summary → B reads and re-encodes) incurs a double codec hop and adds distortion. Writing to a file is one decode (agent → bytes) and the bytes are lossless ground truth — every later reader pays only one encode (bytes → their own state). File-mediated transfer has strictly fewer lossy hops than message-mediated transfer.

Two regions:

**(a) Project files themselves.** Agents editing code is the normal task — one agent's edit becomes the next agent's observation simply through the shared working tree. Git handles change tracking for free.

**(b) `.wuu/shared/`** — a session-scoped directory for artifacts that are not project content but need cross-agent visibility. No schema. No predefined fields. Just a directory.

Suggested path conventions (carried through system prompt, not enforced by code):

```
.wuu/shared/
  findings/    # investigation reports: findings/auth-module.md
  plans/       # plan documents: plans/refactor-v1.md
  status/      # progress / state: status/migration-progress.md
  reports/     # final reports: reports/pr-review-42.md
```

These paths are **convention**, not schema. An agent decides what category its output fits; the system prompt suggests names. If a new category emerges naturally, the agent just uses a new directory. No registration, no validation.

**The hard rule**: data goes through files. If you have 500 words of findings, write them to `findings/xxx.md` and send a one-line message pointing at the path. **Never put 500 words of findings in a `send_message`.** Messages are signaling, not transport.

### 2.2 Control plane — `send_message`

`send_message` is a **low-bandwidth control signal channel**. Legitimate uses:

- "I finished task X, result at `.wuu/shared/findings/xxx.md`"
- "Stop, the situation changed"
- "New instruction: change X to Y"
- "I failed, class=auth, you should escalate"
- "Please confirm X is what you expected"

Each message should be short. More than one sentence is a smell — it means you should have written a file and sent a path.

**Implementation**: mailbox per agent. `send_message` writes into the target's mailbox. On the target's next turn, the system injects any unread mailbox messages as user-role messages at the start of the turn. Empty mailbox → nothing injected.

**Addressing**: any agent can message any other agent whose `agent_id` it knows. IDs come from:
- IDs returned by own `spawn` / `fork` calls
- IDs handed down via prompt from parent
- IDs in the auto-injected active-agent list

No explicit capability system in v1. Implicit mesh. If abuse emerges later, add capabilities then.

Messages to completed / failed / cancelled agents return an error ("agent X is no longer running") — there is no zombie delivery.

### 2.3 Trajectory plane — passive auto-recording

Every agent's execution is **automatically** recorded to:

```
.wuu/traces/<agent-id>.jsonl
```

Format: **thin trajectory with references**. The trajectory preserves the decision skeleton (tool calls, arguments, thinking, final output) but handles tool results asymmetrically:

- **Reproducible tool results** (`read_file`, `grep`, `glob`, `list_files`, git-backed operations) — stored as a reference: `{git: HEAD-sha, path: "..."}` or a file hash. **Content is not inlined.** The trajectory stays small; content can be recovered by re-running the tool or by `git show`.
- **Non-reproducible tool results** (`run_shell`, `web_fetch`, `web_search`) — **inlined** in full. There is no way to re-fetch these later with fidelity.

Example trajectory entry sequence:

```jsonl
{"t":"prompt","content":"Audit the auth module"}
{"t":"think","content":"Start with login.go"}
{"t":"tool_call","name":"read_file","args":{"path":"internal/auth/login.go"},"ref":{"git":"abc123","path":"internal/auth/login.go"}}
{"t":"think","content":"JWT comes from config"}
{"t":"tool_call","name":"read_file","args":{"path":"internal/auth/config.go"},"ref":{"git":"abc123","path":"internal/auth/config.go"}}
{"t":"tool_call","name":"run_shell","args":{"cmd":"go test ./..."},"inline":"PASS: 12/12 internal/auth 0.2s"}
{"t":"final","content":"Audit complete, findings at .wuu/shared/findings/auth.md"}
```

**Key properties**:

- **Automatic, not agent-authored.** The trajectory is a side effect of execution, not a tool the agent calls. Agents don't have to "remember" to record — they cannot accidentally forget.
- **Readable by any agent via normal file tools.** `read_file .wuu/traces/worker-ab12.jsonl` just works. No new primitive.
- **Always fresh on re-run.** Because reproducible results are references, a reviewer re-running them sees current state, not a frozen snapshot. This gives automatic staleness detection — if the world changed, the reviewer notices.
- **Small enough to accumulate.** A 20-step task thin trajectory is a few KB, not a few hundred KB. Traces can stack across a long session without context bloat.

**Uses of trajectories**:

1. **Self-correction** — a verifier reads another agent's trajectory and checks whether the final conclusion is supported by the actual tool results. Hallucinations where the agent claimed to see something it never read are mechanically detectable (grep the trajectory).
2. **Debugging failures** — the trajectory is a complete log of what the failed agent did, available for the user and for other agents.
3. **Experience reuse** — a new agent spawned for a similar task can be pointed at a past trajectory in its prompt. Dead-end approaches from prior attempts don't get repeated.
4. **User audit** — the user can `cat .wuu/traces/<id>.jsonl` to see exactly what a worker did, without a TUI-level reconstruction.

**Trajectory is free provenance.** It tracks "this claim was made after reading these files at these commits" without any schema, any required metadata, any structured claim format. It is bitter-lesson-compliant because it has no invented content structure — it is the raw execution record.

## 3. Derivation: spawn vs fork

Two primitives, two clean semantics.

### `spawn(prompt)` — clean room

Creates a child with **zero inherited context**. Child sees:
- Generic system prompt
- The spawn prompt (the task description)
- Empty conversation history

Use when:
- The subtask is independent, and parent's context has no value to it
- You specifically **want** fresh framing (VERIFICATION: the verifier should not inherit your beliefs)
- The parent's reasoning might bias the child (clean-room implementation from a spec)
- You have 50 near-independent subtasks to parallelize

### `fork(prompt)` — state inheritance

Creates a child with the **parent's complete current context**. Child sees:
- Parent's system prompt
- Parent's full history (all tool calls, thinking, messages)
- One new user-role message: the fork prompt

Use when:
- You've spent many turns building up understanding, and the child would waste work re-acquiring it
- The task is "continue what I'm doing but in a branch"
- You need near-lossless state transfer between agents

**The fork channel is the only near-lossless inter-agent channel.** Every other communication involves at least one encode-decode cycle. Fork hands the child the same tokens the parent has — the child re-encodes, but with maximally informed input. Use it when state fidelity matters more than token cost.

### The judgment heuristic

Carry this as a one-liner in the system prompt:

> **If you can describe the task in under 100 words without reconstructing context, use `spawn`. If you need to recap your understanding to make the task legible, use `fork`.**

### Parent notification on child completion

When a child transitions to `completed` / `failed` / `cancelled`, a notification is auto-delivered to the parent's mailbox:

```
<agent-notification agent_id="worker-xyz" status="completed">
final: <child's final message>
trace: .wuu/traces/worker-xyz.jsonl
</agent-notification>
```

The parent sees this on its next turn automatically — no polling, no explicit query. The parent then decides the next step: read the trajectory, read output files, spawn follow-up work, answer the user. **No auto-retry, no auto-restart, no supervisor-level recovery strategy.** All reaction logic is the parent's call.

## 4. Failure and Resources

### Error classes

Inherit wuu's existing classification (`coordinator/errors.go`), with one addition:

| Class | Meaning | Parent's typical reaction |
|---|---|---|
| `retryable` | Transient (rate limit, network) | Same task, spawn again |
| `auth` | Credentials rejected | Escalate to user |
| `context_overflow` | Worker's context ran out | Split task smaller |
| `cancelled` | Stopped intentionally | Don't auto-retry |
| `fatal` | Unknown / non-recoverable | Report and stop |
| `resource_exhausted` | **New.** Budget (token/time/calls) ran out | Consider splitting or raising budget |

The error class is recorded in the agent's final trajectory entry and in the notification delivered to the parent.

### No auto-restart policy

Deliberately **no** OTP-style supervisor strategies (`one_for_one`, `rest_for_one`, etc.). Reasons:

1. Restart policies are pre-defined content rules ("for error class X, do Y"). The bitter-lesson path is letting the model judge.
2. The parent agent has the full context of what the failed child was doing and why. Its judgment is strictly better than a stateless policy table.
3. Modern models can absolutely handle "worker X failed with class=foo, what should I do?" reasoning. There is no cognitive capacity reason to hand this off to a supervisor system.

The code guarantees notification delivery. It does nothing automatic beyond that.

## 5. System Prompt Strategy

The code layer is aspirational (designed for current and future models). The prompt layer is directive (today's model needs explicit guidance). **When the model gets smarter, simplify the prompt — never the code.**

The system prompt teaches five things, in order of importance:

### (a) Role

One sentence. "You are a coding agent with full file / shell tools and orchestration primitives. You can do tasks directly or delegate to spawned / forked sub-agents."

### (b) Three-plane discipline (most important)

The model must be taught this explicitly or it will default to chat-era habits (putting everything in messages):

- **Data goes through files.** "If you have findings, plans, or intermediate results that another agent needs, write them to `.wuu/shared/<category>/<name>.md` and send only the path via `send_message`. Never duplicate the content in the message itself."
- **Control goes through messages.** "Messages are short signals — completion events, instruction changes, state notifications. If a message is more than a sentence, you should be writing a file instead."
- **Your execution is automatically recorded.** "Every tool call you make is auto-logged to `.wuu/traces/<your-id>.jsonl`. Other agents can read this. If you want a downstream agent to understand your reasoning, point it at your trace — don't recap."

### (c) Delegate vs do-directly judgment

Principles, not rules. Never write hardcoded thresholds.

- Small task (minutes), tight iteration, exploratory → do directly
- N independent subtasks that can run concurrently → parallel spawn
- Long-running task where you shouldn't block user conversation → async spawn
- Adversarial verification (avoid your own confirmation bias) → spawn a fresh verifier
- You have understanding the subtask needs → fork
- You do not want your assumptions to contaminate the subtask → spawn

### (d) spawn vs fork judgment

The 100-word rule from §3:

> If you can describe the task in under 100 words without reconstructing context, use `spawn`. If you need to recap your understanding to make the task legible, use `fork`.

### (e) Safety boundaries

- Confirm before destructive operations
- Consider budget before resource-intensive operations
- On failure, report the cause clearly — do not disguise failure as success

**What to be precise about**: principles, judgment reasoning, safety.
**What to leave vague**: specific thresholds, hard categories, verbatim-paste rules. These are fragile and bitter-lesson-hostile.

## 6. Concrete wuu Changes

Ordered by risk-vs-value. First 4 are MVP; the rest are follow-on.

### MVP (must do)

#### ✅ 1. Remove forced coordinator-only mode

**File**: `cmd/wuu/main.go:439`
**Change**: delete `toolkit.SetCoordinatorOnly(true)`. Keep the `coordinator` as the source of spawn/fork/send/stop tools, but do not strip the main agent's file and shell tools.

**Impact**: the main agent regains the full tool set. This is the smallest and highest-value change — it releases the artificial handicap.

#### ✅ 2. Rewrite the coordinator system prompt preamble

**File**: `internal/coordinator/coordinator.go:325` (`SystemPromptPreamble`)
**Change**: complete rewrite per §5. Remove "You have ONLY 6 tools", "You CANNOT read file contents", and the verbatim-paste preset requirement. Introduce three-plane discipline, spawn/fork judgment, delegate/do-directly judgment.

Existing preset content (`internal/coordinator/presets.go`, `VerificationPreset` / `ResearchPreset`) stays available as **reference material** the main agent may draw from when writing worker prompts — but "copy this verbatim" is no longer a rule.

#### ✅ 3. Implement `SendMessage` via mailbox

**Files**: `internal/subagent/manager.go`, `internal/coordinator/coordinator.go:309`
**Change**:
- Add `mailbox []string` + `mailboxMu sync.Mutex` to `SubAgent`
- `Coordinator.SendMessage` writes into the target's mailbox
- In `subagent.run`, before each step, drain the mailbox and inject messages as user-role entries into history
- The existing `workerNotifyMsg` path (TUI → parent) becomes the foundation for "worker done" notifications delivered back to the parent agent's context

**Test**: parent agent sends a follow-up instruction to an idle worker; the worker receives it and continues.

#### ✅ 4. Implement `Fork`

**Files**: new `coordinator.Fork` method alongside `Spawn`
**Change**:
- `Fork` accepts the parent's current `[]ChatMessage` as seed history
- Child's system prompt is the same as `Spawn`'s worker system prompt
- Child's initial history = parent's history + one user-role message containing the fork instruction
- Child runs as its own goroutine, independent of parent

**Test**: parent reads several files and forms an understanding, then forks a child to execute a change; the child does not need to re-read those files.

### Follow-on (should do)

#### ⬜ 5. Create `.wuu/shared/` on session start

Auto-create `.wuu/shared/{findings,plans,status,reports}` at session boot. A few lines in `cmd/wuu/main.go` or the TUI app init. System prompt references these as suggested conventions.

#### ⬜ 6. Thin trajectory auto-recording

**New**: `internal/agent/trajectory.go` (or equivalent) — a writer that the `StreamRunner` calls after each step.

Schema: JSONL with entries `{t: "prompt"|"think"|"tool_call"|"final", ...}`. Tool results use git references for reproducible tools (`read_file`, `grep`, `glob`, `list_files`) and inline content for non-reproducible tools (`run_shell`, `web_fetch`, `web_search`).

Output: `.wuu/traces/<agent-id>.jsonl`.

Even without downstream consumers, trajectories are immediately valuable as user-facing debugging artifacts.

#### ⬜ 7. Workers can also spawn / fork / send_message

**File**: `cmd/wuu/main.go:425` (`WorkerFactory`)
**Current**: `// Workers do NOT get a coordinator (no recursive spawns).`
**Change**: inject coordinator into worker toolkits too, with a max depth (e.g., 3).

**Rationale**: the current flat hierarchy is too strict. A worker handling "refactor 5 files in parallel" should be able to spawn 5 sub-workers without bouncing back to the main agent. Workers do not become the user's interlocutor; they just get the same primitives.

**Safeguard**: the worker's system prompt mentions that it can spawn, with a reminder about depth and budget.

### Optional (can defer)

#### ⬜ 8. Resource budget enforcement
Attach `budget: {max_tokens, max_time, max_tool_calls}` to `SubAgent`; `StreamRunner` checks and returns `resource_exhausted` on overrun.

#### ⬜ 9. Parent-side notification injection
Extend the existing `workerNotifyMsg` path so that worker-done events become user-role messages injected into the creating agent's history (not just displayed in the TUI).

#### ⬜ 10. Trajectory index
Maintain `.wuu/traces/index.jsonl`: one line per agent with `{id, parent_id, start_time, prompt_summary, status}`. Makes "find relevant past trajectories" a cheap grep.

## 7. Explicitly Not Doing

To prevent relapse into designs that were considered and rejected:

- ❌ **KB schemas or structured findings fields.** The filesystem is the KB. Paths and plain text are the structure.
- ❌ **Structured JSON output contracts for workers.** Workers return prose. Data goes through files.
- ❌ **Task graph DAG primitives.** The model can express parallelism by spawning multiple children in one turn. Dependencies are expressed by waiting for notifications.
- ❌ **Audit primitives (`verify_claim`, `verify_modifications`).** Verification is spawning a fresh worker to check, plus re-running tool calls from trajectories. No rule-based auditor.
- ❌ **Worker type classification system.** Keep the single `worker` type. Role posture is injected by prompt (the existing `VerificationPreset` / `ResearchPreset`), not by a type registry.
- ❌ **Persistent worker identity as a first-class concept.** Long-lived workers exist via the `idle` state + `send_message`. No separate "identity" abstraction.
- ❌ **`peek` tool.** Not needed. Trajectories are readable via normal file tools.
- ❌ **Explicit capability system.** Early v1 uses implicit mesh (any agent can message any known id). Add capabilities later only if abuse emerges.
- ❌ **OTP-style restart strategies.** No `one_for_one`, no auto-restart tables. Parent decides on notification.
- ❌ **Living plan as a structured document.** The conversation history naturally carries planning state.

All of these are content-level structures. They are what I (the system designer) would want to impose on the model's thinking. They fail the bitter-lesson test: they get more obstructive as the model gets smarter.

## 8. Future Alignment

What this architecture bets on, and what changes gracefully:

### Stable under model scaling

- **Code-layer primitives** (read/write, spawn/fork, send/stop, filesystem, trajectories). These are physical and topological. They do not reference any model-specific assumption.
- **Trajectory format.** No schema. Works with any model.
- **Directory conventions.** They are suggestions, not enforcement.

### Evolves with model capability

- **System prompt** gets simpler over time. Today it must explicitly teach "data through files, control through messages"; tomorrow the model will know this from training. The prompt is the training wheels — remove them when the model is ready.
- **Delegation habits.** Today's models tend to do more directly and spawn less; future models will shift toward bigger task trees, more async work, longer-lived specialists. Same primitives, different usage distribution.
- **Concurrency level.** Today ~5 parallel children is plenty; future scales to tens and beyond.

### What does not need to change

The code. This is the payoff. If we build the physical substrate correctly now, we do not rebuild it when the model changes. We only update the prompt.

## 9. Summary

wuu's architecture is:

```
uniform Agent
  × three communication planes (filesystem data / send_message control / trajectory history)
  × spawn and fork derivation
  × resource budgets
  × system-prompt-level discipline for today's models
```

No "coordinator" vs "worker" distinction. No restricted tool sets. No content schemas. No rule-based auditors. No supervisor trees.

The MVP is four changes:
1. Delete `SetCoordinatorOnly(true)`.
2. Rewrite the system prompt preamble.
3. Implement `SendMessage` mailbox.
4. Implement `Fork`.

Everything else is additive and can land incrementally.

## Appendix: Rationale Threads Not Captured Above

A few framing insights worth preserving in case the design is revisited:

- **Traditional mode is proof that the main agent's tool count is not the bottleneck.** Any design that removes tools from the main agent is optimizing the wrong variable.
- **LLMs are lossy codecs.** Every prose message between agents is a double encode-decode cycle. Files and fork are the only near-lossless channels. Data transfer should prefer the filesystem (one encode, then durable bytes) over messages (one encode + one decode on each hop).
- **The filesystem is the bus.** OS design converged on shared filesystems (Unix, Plan 9) as the universal inter-process communication medium. The same answer applies to agents: don't invent a bus primitive when files already work.
- **Trajectories are free provenance.** The deepest objection to provenance systems is that they require schemas. Trajectories satisfy provenance (what was claimed, from what evidence, in what order) without any schema — they are just the raw execution log with git references for reproducible results.
- **The main agent is just an agent.** The user-facing entry point does not need special structure. It is an agent like any other, with the full tool set, that happens to be the one talking to the user. This framing makes "coordinator mode" dissolve as a distinct concept.
- **Scaffolding belongs in the prompt, not the code.** Any "help the model behave well" logic that is hard to remove once the model improves is in the wrong layer.
