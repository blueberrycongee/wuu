# App-Server Protocol

The Wuu app-server protocol is the UI-neutral boundary between shells and the
Go core runtime.

Wuu has no TUI. Electron is the human shell. `wuu exec`, scripts, CI,
automation, and future IDE shells should drive Wuu through this protocol rather
than building separate agent loops.

## Transport

For the named-agent `session` tool's explicit attachment selection, source access
checks, queue recovery and model-input contract, see
[media handoff](../automation/app-server.md#named-agent-media-handoff).

The current app-server transport is newline-delimited JSON over stdio.

A cloud control plane starts the same core binary with an explicit process
identity and config:

```bash
WUU_HOME=/state/wuu wuu app-server \
  --host cloud \
  --instance-id run_123 \
  --workspace-id workspace_123 \
  --workdir /workspace \
  --config /run/wuu/config.json
```

Cloud mode requires `--instance-id`, `--workspace-id`, and `--config`. It never
creates a local starter config from ambient host state. The sandbox or container
boundary, transport authentication, organization membership, secrets injection,
and quota enforcement belong to the host control plane; they are not implemented
by `app-server`.

Requests have this shape:

```json
{"id":"1","method":"initialize","params":{}}
```

Responses have this shape:

```json
{"id":"1","result":{}}
```

Errors have this shape:

```json
{"id":"1","error":{"code":"error","message":"..."}}
```

Notifications have no `id`:

```json
{"method":"turn/completed","params":{}}
```

The protocol version is reported by `initialize` as
`wuu-app-server/v0.1`.

## Required `wuu exec` Lifecycle

`wuu exec` must use the same lifecycle as Electron:

```text
initialize
thread/start or thread/resume
run/start
consume turn notifications and run/updated until terminal run state
shutdown
```

It must not call `StreamRunner.RunWithCallback` directly for the target path.

## Core Methods

This is the common subset the text entrypoint exercises end-to-end. The full
method table lives in `internal/appserver/protocol.go` (every method constant in
the `Method*` block at the top of that file is a valid JSON-RPC method, with
siblings like `config/read`, `config/model/update`, `config/general/update`,
`config/advanced/update`, `config/codex/models`, `config/provider/remove`,
`skill/list`, the rest of the
`thread/*` methods (`thread/list`, `thread/listAll`, `thread/search`, `thread/pin`,
`thread/archive`, `thread/edit-message`, `thread/context-composition`,
`thread/organization/update`, `thread/regenerate-title`, `thread/rename`),
session organization methods (`sessionOrganization/list`,
`sessionFolder/create|update|reorder|delete`), all the `turn/*` methods
(`turn/queue`, `turn/update-queued`, `turn/dequeue`, `turn/steer`,
`turn/unsteer`), `process/list`, `process/stop`, the `mcp/*` methods
(`mcp/list`, `mcp/connect`, `mcp/disconnect`, `mcp/refresh`), and
`settings/usage`).

`initialize`

The client may identify itself, require an exact protocol version, and advertise
the server-initiated methods it can handle:

```json
{
  "id": "1",
  "method": "initialize",
  "params": {
    "protocol_version": "wuu-app-server/v0.1",
    "client": {"name": "wuu-desktop", "version": "<desktop-version>"},
    "capabilities": {
      "reverse_rpc": {
        "methods": [
          "browser/cdp",
          "browser/screenshot",
          "browser/open_tab",
          "browser/close_tab",
          "browser/set_visibility",
          "browser/list_tabs"
        ]
      }
    }
  }
}
```

Capabilities are opt-in. When `params` or `reverse_rpc.methods` is omitted, the
core does not send server-initiated requests to that client. A supplied protocol
version must match exactly; an incompatible client fails during initialization
instead of failing later in a turn.

The result returns provider, model, workspace, permission and extension trust
summaries, the effective `max_parallel` value, and `runtime_host`.
`runtime_host.kind` is `local` or `cloud`; a cloud process also reports the
control-plane-supplied `instance_id`. Organization, seat, agent definition,
quota, and sandbox details remain outside the core protocol.

`config/read`

Returns the current runtime configuration summary. Its result includes
`max_parallel`.

`config/model/update`

Updates the workspace defaults for provider, model, variant/effort, permission
mode, and the optional Standard-mode `approve_for_me` review flag. `model` is
required for a request without `thread_id`, which changes only the defaults
inherited by threads created after the update.

When `thread_id` is present, only the explicitly provided selection fields are
applied: they are pinned to that conversation and become the workspace
defaults for future threads. Omitted selection fields inherit from the target
conversation's current selection (so `model` is optional), and no other
existing conversation changes. A permission-mode-only update, for example,
never rewrites the workspace's default model or the conversation's
variant/effort.

A targeted update that changes selection fields is rejected while that thread
owns an active turn, including when another app-server process owns its
execution lease. Model and permission mode are immutable for the admitted
turn; after it settles, the user may update that thread and the defaults for
future threads. Existing non-target threads retain their persisted selections.

The method returns the effective workspace-level `max_parallel` value. Provider
connection fields such as `base_url`, `api_key`, and `auth_token` are always workspace provider
configuration, never thread-scoped selection; a targeted request carrying only
connection fields is processed as a workspace connection update and is allowed
while the target thread runs.

`thread/start`

Creates a new persistent conversation thread backed by normal session storage.
When called with `{"ephemeral": true}`, creates an in-memory thread that is not
written to the session store and cannot be resumed after the server exits.
The optional `permission_mode` selects `standard`, `read_only`, or `unconfined`.
The optional `approve_for_me` flag reviews high-risk native tool calls in
Standard mode for the built-in Wuu engine; other engines and permission modes
keep it off. New external-engine threads default to `unconfined` when callers
omit `permission_mode`.

`thread/resume`

Resumes an existing session. An empty session id means "most recent visible
thread" in the app-server implementation.

`thread/fork`

Creates a new thread from an existing thread, turn, or item. This is part of
the text entrypoint surface through `wuu exec fork`.

`turn/start`

Starts a user turn with prompt text and optional attachments. The turn snapshots
the target thread's persisted model and permission mode at admission. The
legacy optional `permission_mode` request field must match the thread selection;
clients change the selection through `config/model/update` before starting the
turn rather than overriding one turn in isolation. Interactive shells use this
single-turn interface; `wuu exec` uses `run/start` instead.

`run/start`

Starts the automation lifecycle used by `wuu exec`. The request identifies the
thread, supplies the prompt and attachments, and may include an output schema.
Clients consume the resulting `turn/*` notifications and wait for
`run/updated` to report a terminal Run state.

`turn/interrupt`

Interrupts the active turn for single-turn clients.

`run/interrupt`

Interrupts an active Run. `wuu exec` uses this for Ctrl+C and timeout cleanup.

`shutdown`

Requests a clean app-server shutdown.

## Anonymous Worker Capacity

`agent.max_parallel` is the host-owned execution capacity for anonymous workers.
It uses `5` when omitted or set to zero. `InitializeResult`, `ConfigReadResult`,
and `ConfigModelUpdateResult` report the effective `max_parallel` value.

Proactive delegation is not an app-server mode. It is supplied by the Subagent
plugin through namespaced storage, an `agent.pre_step` state-change message,
and a Desktop composer contribution. Consequently the core protocol has no `ultra` request or
result field, and `wuu exec` has no `--ultra` switch.

## Anonymous Worker Lifecycle States

Anonymous-worker state is exposed through `Agent.status`, including in
`agent/updated` notifications. In addition to the terminal states, clients must
handle these non-terminal states:

| Status | Meaning |
| --- | --- |
| `queued` | The spawn was accepted but is waiting for worker execution capacity. It consumes no `max_parallel` slot and starts automatically when capacity opens. |
| `waiting_children` | The worker produced a final message while direct children are still non-terminal. Its result is held, it consumes no execution slot, and it resumes when child delivery arrives so it can integrate the results before one final parent delivery. |

`waiting_children` is not a completed delivery. The worker becomes terminal
only after no direct child remains live and no pending message remains to
be integrated. Parent delivery remains exactly once.

## Turn Interrupt and Tree Freeze

`turn/interrupt` means "freeze this work", not "leave background workers
running". It cancels the active root turn and its complete anonymous-worker
tree, clears queued spawns in that tree, and preserves partial worker results
as resumable state. Deliveries that arrive during the freeze are stored as
pending messages and must not trigger worker follow-ups or synthetic completion
turns.

The next **user-initiated** turn on the thread clears the freeze. The root then
receives the whole-tree status snapshot, including completed results and
cancelled workers' partial results and resume hints. This does not automatically
restart every worker; the root can resume selected workers with `send_message`.
Natural turn completion still allows asynchronous workers to finish and wake
their parent, while thread/session close remains the true termination path.

## Notifications Used By Text Clients

Text clients consume these notifications and map them to human stderr or JSONL
stdout:

- `thread/started`
- `thread/resumed`
- `thread/updated`
- `run/started`
- `run/updated`
- `turn/started`
- `turn/queued`
- `turn/dequeued`
- `turn/event`
- `turn/usage`
- `turn/completed`
- `turn/error`
- `item/started`
- `item/completed`
- `item/removed`
- `item/agentMessage/delta`
- `item/agentMessage/replace`
- `item/reasoning/delta`
- `item/reasoning/replace`
- `item/toolCall/delta`
- `item/toolCall/outputDelta`
- `agent/updated`
- `agent/mailbox`
- `mcp/status/updated`

Interactive clients may render the reasoning notifications. `wuu exec` receives
them but deliberately omits their payloads from JSONL because providers do not
reliably distinguish safe summaries from hidden chain-of-thought.

`item/removed` retracts an attempt-scoped item previously announced with
`item/started`. Clients must remove the matching `item_id` from the indicated
turn; the item did not become a durable operation and has no completion status.

## Non-Interactive Client Requests

`wuu exec` does not support server-initiated requests. Its app-server client
accepts responses and notifications only, and treats a server request as a
protocol error. Exec runs make permission decisions without an interactive
approval exchange.

## Debug Commands

The debug commands expose the same text protocol without adding a TUI. They are
for agents, scripts, and developers that need to inspect the core path directly.

```bash
wuu debug app-server initialize [--workdir DIR] [--provider NAME] [--model MODEL] [--no-tools]
wuu debug app-server send [--workdir DIR] <method> '<json>'
wuu debug channel e2e (--sandbox|--sandbox-name NAME) [--keep-sandbox] [--agent NAME] [--room NAME] [--message TEXT] [--expect TEXT] [--timeout DURATION]
wuu debug channel inspect [--sandbox NAME] [--room ID|NAME] [--after SEQ] [--limit N]
wuu debug channel send [--sandbox NAME] --room ID|NAME [--wait DURATION] [--replies N] "message"
wuu debug sandbox list
wuu debug sandbox delete NAME
wuu debug protocol events [--json] [--workdir DIR] <thread-id>
```

`wuu debug app-server initialize` starts a local app-server instance, sends
`initialize`, prints the JSON result, and shuts the server down.

`wuu debug app-server send` starts a local app-server instance, sends one
method with optional JSON params, prints the JSON result, and shuts the server
down. This is the lowest-level CLI probe for app-server methods.

`wuu debug channel inspect` and `wuu debug channel send` are higher-level,
agent-facing probes for persistent group chat. They still use the public
app-server methods rather than reading the channel database directly. `inspect`
lists the real rooms and agents and can include one room's messages. `send`
keeps the same local app-server alive while it waits for asynchronous named-agent
replies, so it exercises the same persistence, mention, wake, provider, and
message paths as the desktop. Both commands print one JSON value to stdout;
`send` returns the timeout exit code when `--wait` does not observe the requested
number of replies.

`wuu debug channel e2e --sandbox` is the disposable one-command acceptance path
for a coding agent after changing group chat. It loads the normal provider and
model configuration and credentials into memory, then redirects all runtime
state into a temporary Wuu home. No credential file is copied into the sandbox.
It creates a named agent and room, sends a direct mention through
`channel/message/send`, waits for the real provider turn, and requires the reply
to contain `--expect` (default `E2E_OK`). The temporary channel database, agent
memory, session, and trace are removed on shutdown; the user's existing rooms
and history are not touched. Pass `--keep-sandbox` to retain failed-run state;
the JSON `sandbox_dir` field identifies it. The result also includes the phase,
provider, model, created records, wake state, named-Agent thread diagnostics,
observed messages, protocol notifications, assertion, and duration. Provider,
turn, timeout, and reply-mismatch failures are printed before the command
returns a non-zero exit code.

Pass `--sandbox-name NAME` (or `--sandbox=NAME`) to keep a reusable experiment
instead. Bare `--sandbox` remains the backward-compatible disposable form, so
an existing positional message after it is not mistaken for a sandbox name:

```bash
wuu debug channel e2e --sandbox-name group-chat-exp --agent Andy --room Experiment \
  --message "First round" --expect FIRST_OK
wuu debug channel inspect --sandbox group-chat-exp --room Experiment
wuu debug channel send --sandbox group-chat-exp --room Experiment --wait 2m \
  "@Andy Continue from the previous round"
wuu debug channel e2e --sandbox-name group-chat-exp --agent Andy --room Experiment \
  --message "Final check" --expect FINAL_OK
```

A named sandbox uses a stable isolated Wuu home. Later commands with the same
name reuse its channel database, Agent memory, sessions, traces, logs, rooms,
and message history. E2E reuses the requested Agent and room when they already
exist, so subsequent runs continue the same experiment rather than creating a
parallel scenario. Named sandboxes never copy credential files and never write
to normal channel state. They are preserved until explicitly removed. Use
`wuu debug sandbox list` to discover them and `wuu debug sandbox delete NAME`
to clean one up. Names are limited to letters, numbers, dot, underscore, and
hyphen; path separators and traversal names are rejected.

For example:

```bash
go run ./cmd/wuu debug channel e2e \
  --sandbox \
  --agent Andy \
  --workdir "$PWD" \
  --message "回复 E2E_OK" \
  --expect E2E_OK \
  --timeout 2m
```

`wuu debug protocol events` reads the stored session trace and prints the raw
JSONL trace events. With `--json`, it wraps the events with the thread id and
trace path for machine consumers.

## Session Contract

Persistent runs must create or update normal Wuu sessions so that:

- `wuu exec` sessions can be inspected by `wuu session`.
- `wuu exec` sessions can be resumed by Electron.
- Electron sessions can be resumed by `wuu exec`.
- traces live under workspace-scoped session artifacts.

## Runtime Model Selection

Model selection has one authoritative semantic. This section is the arbitration
reference for reviews: any new execution surface (background task, derived call,
display) must resolve its model through the rules below rather than re-guessing
which state to read.

### Three concepts

1. **Selection** — the `(provider, model, variant, effort, permission)` tuple.
   It is the only persistent model semantic. Each conversation pins exactly one
   (stored on its session row). The workspace holds one additional *default*
   selection whose only jobs are to seed new conversations and to serve
   surfaces that have no conversation (settings, a new empty tab). An existing
   conversation never re-reads the workspace default at run time.

2. **Derivation** — worker model, title model, and context budgets are not
   independent state; they are a pure function `derive(conversation selection,
   role config)`. Role config (e.g. "titles always use a cheap model") is a
   workspace-level *function definition*, but its evaluation input is always the
   owning conversation's selection. Corollary: copying a budget or worker
   default from one runtime to another is a bug — a derived quantity must be
   recomputed on the owning conversation. In code this is
   `runtime.Session.DeriveThreadModel`.

3. **Override** — only two kinds, and no more are added. *Process-scoped* (e.g.
   `exec --permission-mode`) beats everything and is never persisted.
   *Call-scoped* (a spawn / participant profile's explicit pin) affects only
   that one execution and is persisted with the run for recovery. An override
   resolves its own connection (client); it never borrows the host
   conversation's connection, and the "same provider as the worker, reuse the
   connection" check compares against the conversation's own derived worker
   provider, not workspace state.

### Two global rules

- **Attribution** — every inference request belongs to some conversation (main
  turn, side thread, subagent, auto-title, compaction). Resolve from the owning
  conversation's selection; only a request with no owning conversation may read
  the workspace default.
- **Dual-write and immutability** — changing a conversation's selection applies
  immediately to that conversation and makes the explicitly-provided fields the
  default for future conversations. The reverse never holds: a workspace-default
  change never edits an existing conversation. A running execution keeps its
  admission snapshot; a selection change is rejected, not hot-swapped.

### Resolution per surface

| Surface | Resolution |
|---|---|
| main turn / side thread / compaction budget | conversation selection (and its derived budget) |
| subagent (unpinned) / auto-title | `derive_worker` / `derive_title`(conversation selection) |
| subagent (explicit pin) | call-scoped override, own connection |
| fork | copy the source conversation's selection |
| resume / continuation | restore selection from disk |
| new conversation, automation, group create | seed the workspace default |
| settings / no-conversation surface | workspace default |
| exec flag | process-scoped override, not persisted |

### Frontend expression

Display semantics obey attribution too, and the desktop keeps intent and fact
from diverging without any explanatory copy:

- **One place.** The composer model button (`RuntimePicker`) is the only place a
  conversation's model semantic appears; it shows the conversation's selection.
  No-conversation surfaces show the workspace default. No other UI restates the
  model. (The run-debug panel's model row follows the active conversation, not
  the global default.)
- **One lock.** While any execution runs in the conversation — a streaming turn
  or an unsettled background agent — the model button is disabled. So the value
  shown is always both "what will run next" and "what is running now"; intent
  and fact cannot fork, so no second annotation is needed.
- **Causality from existing elements.** The streaming reply and the background
  agent cards already show they are running; they are the reason the button is
  disabled. The backend rejection message is only a cross-process race backstop,
  not a normal path.

## Protocol Compatibility

Changes to method names, notification names, field names, stdout/stderr
behavior, or exit code meaning are product-level compatibility changes. Treat
them as public API once automation depends on them.

Clients should tolerate unknown result fields so compatible protocol additions
do not require lockstep shell releases.
