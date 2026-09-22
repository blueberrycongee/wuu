# App-server protocol

App-server connects clients to Wuu's Go runtime. It owns session admission,
execution, persistence, and notifications; a client supplies interaction and
presentation. Start with the [integration guide](../automation/app-server.md) for
a minimal exchange, or use [`wuu exec`](../automation/exec.md) for one-shot tasks.

## Transport and initialization

`wuu app-server` reads and writes newline-delimited JSON over stdio. Requests use
`id`, `method`, and optional `params`; responses echo the ID. Errors have string
codes rather than JSON-RPC numeric codes. There is no required `jsonrpc` field.

```json
{"id":"1","method":"initialize","params":{"protocol_version":"wuu-app-server/v0.1","client":{"name":"example-client","version":"1"}}}
```

Response envelopes and a notification look like this; their payloads are abbreviated:

```json
{"id":"1","result":{"protocol_version":"wuu-app-server/v0.1","status":"ready"}}
{"id":"2","error":{"code":"invalid_params","message":"thread_id is required"}}
{"method":"turn/completed","params":{"thread_id":"thread-id","turn":{"id":"turn-id"}}}
```

The current protocol version is `wuu-app-server/v0.1`. If a client supplies
`protocol_version`, it must match exactly. Initialization returns `core` build
identity, `runtime_host`, workspace and model summaries, permissions, extension
inventory, feature flags, and effective `max_parallel`. `status: needs_setup`
with `issues` means the connection works but runtime configuration needs attention.

Correlate responses by ID and keep reading notifications while requests are
outstanding. Do not assume every line is the response to the most recent request.
The stdio scanner has a 64 MiB line buffer limit; attachment/provider limits are
separate and may be lower. Keep stdout free of banners and debug logging.

## Session methods

| Method | Input | Result |
| --- | --- | --- |
| `thread/start` | Optional `cwd`, `workspace_id`, `engine`, `provider`, `model`, `effort`, `permission_mode`, `approve_for_me`, `ephemeral` | `{ "thread": ... }` |
| `thread/resume` | Optional `session_id`, `response_only`, `history_page` | Thread snapshot and available held/pending user messages |
| `thread/fork` | `thread_id`; optional `turn_id`, `item_id`, `target`, `mode` | New thread and optional worktree information |
| `thread/list`, `thread/listAll`, `thread/listArchived`, `thread/search` | Method-specific filters | Session metadata |
| `thread/rename`, `thread/pin`, `thread/archive`, `thread/delete` | Target and method-specific changes | Updated state or operation result |

`thread/start` persists by default. `ephemeral: true` creates an in-memory session
that cannot be restored after the server exits. Engine binding is fixed at
creation. New external-engine sessions default to `unconfined` when permission
mode is omitted; explicitly choose the intended mode. The built-in engine's
`approve_for_me` review applies only in Standard mode.

An omitted `session_id` in `thread/resume` selects the most recent visible session
for the workspace. `response_only` avoids a duplicate resume broadcast to the
requester. `history_page` requests bounded recent history rather than the full
snapshot. Forks retain the source conversation and copy its selection.

## Turns and execution runs

Interactive clients start a turn with text or attachments:

```json
{"id":"10","method":"turn/start","params":{"thread_id":"thread-id","prompt":"review the current changes"}}
```

The response contains `turn`; consume `turn/started`, item updates,
`turn/completed`, or `turn/error`. Each admitted turn keeps its model and permission
snapshot. The legacy `permission_mode` request field must match the thread's
selection rather than override it. `turn/queue`, `turn/update-queued`,
`turn/dequeue`, `turn/steer`, and `turn/unsteer` manage pending user input;
they are distinct from starting another concurrent turn.

Failed Wuu-engine streams can include `turn.error.recovery`. Its `attempt_count`
and `retry_count` count recovery-executor calls that actually started; a prepared
retry that never runs is excluded. `submission_count` counts physical provider
requests, including transport fallbacks, not token charges. These counters apply
to the final failed logical stream, not the entire multi-step turn.
`max_attempts` is its frozen attempt limit. `stop_reason` distinguishes
`non_retryable`, `retry_limit`, `workflow_budget_exceeded`,
`workflow_cost_indeterminate`, `replay_unsafe`, `recovery_unavailable`, and
`recovery_failed`; `failure_category` and optional `budget_dimension` retain the
decision's classification. `operation_id` identifies the stream in inference
diagnostics. Clients must tolerate absent fields on older servers and unknown
future reason values. Persisted terminal errors retain these facts on
`thread/resume`; older history without recovery facts does not imply zero retries.

For an invocation that can span automatic continuation or output-correction turns,
use `run/start`:

```json
{
  "id": "11",
  "method": "run/start",
  "params": {
    "thread_id": "thread-id",
    "prompt": "return a short repository summary",
    "request": {"mode": "start"},
    "output_schema": {
      "type": "object",
      "properties": {"summary": {"type": "string"}},
      "required": ["summary"],
      "additionalProperties": false
    }
  }
}
```

Send the object on one line. `run/start` returns `{ "run": ... }`, followed by
`run/started` and turn notifications. Wait for `run/updated` with the same run ID
and a terminal status: `completed`, `failed`, `interrupted`, `timed_out`, or
`cancelled`. `accepted` and `running` are nonterminal. The CLI's JSONL `timeout`
status is a projection of this protocol's `timed_out`.

A Run records `id`, `thread_id`, `request`, resolved `runtime`, `workspace`,
ordered `turns`, timestamps, and optional `result` and `error`. `result` refers
to the final turn/item and trace and records the exit code; it does not duplicate
the whole conversation. `request` describes invocation intent. Fields such as
`request.requested`, `max_turns`, or `timeout_ms` are not a substitute for configuring
the runtime, selecting the thread model, or implementing client deadline handling.
The CLI sets up those controls before recording its manifest.

Only one active execution can own a thread. Handle admission errors such as
`thread_busy` without starting a duplicate task. `run/start` rejects manual compact
commands; use the dedicated session compaction interface instead.

### Attachments

Both turn and run requests accept `images` and `files` arrays. Each image has
`media_type`, base64 `data`, and optional `original`. Each file has `media_type`,
base64 `data`, and optional `filename`; the file path supports PDFs and supported
[video inputs](../customize/video-input.md).
These are attachment bytes, not local path strings. Image normalization and model
capability checks occur in the core. `turn/start` additionally supports
`active_document` and ordered `content_parts` for interactive clients.

Named agents can separately select stored room attachments through their `session`
tool. That [media handoff contract](../automation/app-server.md#named-agent-media-handoff)
defines source access, durable delivery, and required-evidence behavior; it is not
an additional JSON-RPC method.

### Interruption

`turn/interrupt` targets `thread_id` and can include `turn_id`. `run/interrupt`
takes `run_id` and optional `reason`; `reason: timeout` records timeout settlement.
A run already in a terminal state is returned as-is. A live run attached to a
different app-server cannot be interrupted through this connection and returns
`run_not_attached`.

For built-in anonymous workers, interruption freezes the whole worker tree,
cancels active work, clears queued spawns, and retains partial results. The next
user turn receives the tree snapshot; workers do not all restart automatically.
Natural turn completion, by contrast, can leave asynchronous work running.
See [subagents](../desktop/subagents.md) for worker use and recovery.

## Model and permission selection

`config/model/update` has separate workspace and conversation scopes. Without
`thread_id`, it updates defaults inherited by future conversations and requires
`model`. Existing conversations keep their saved selections.

With `thread_id`, selection-only requests change that conversation without
changing workspace defaults. Omitted fields inherit its current selection.
Selection changes return `thread_busy` while the conversation has active execution,
including outstanding workers or a cross-process execution lease. Collaboration
sessions with a pinned named-agent selection reject this change as well.

```json
{"id":"20","method":"config/model/update","params":{"thread_id":"thread-id","permission_mode":"read_only"}}
```

Provider connection changes belong to workspace configuration. Save them separately
from targeted conversation selection: a request combining those operations is
rejected. The update response includes workspace model summaries; consume the
`thread/updated` snapshot for the target conversation's effective selection rather
than treating the top-level response model as its new model.

Resuming restores the conversation selection; a fork copies the source selection.
Worker/title model roles and context budgets derive from the owning conversation
and role configuration. Process or call overrides do not mean that an unrelated
workspace default should be read during execution.

## Engine inventory and authentication

`engine/list` returns `{ engines, settings }`, including missing and disabled
engines. Each entry has an `id`, `enabled`, and `binary_ok`; optional metadata
includes `display_name`, `protocol`, `install_url`, `binary_path`, `error`,
`models`, `models_error`, and `permission_modes`. `permission_modes` is the
composer access menu for that engine: host `standard` / `read_only` /
`unconfined` rows, with optional native `id` and `label` from an ACP
`category=mode` option. Omitted rows are not offered. `enabled` describes runtime
availability, while `settings.<id>.enabled` is the persisted opt-in/opt-out
preference. Do not infer an installed program or authenticated account from the
preference alone.

For subscription views, `engine/list` accepts optional `{ "include_quota": true }`.
It reads account allowances through supported installed CLIs (currently Codex)
and includes `subscription_providers` for built-in subscription services. Engine
`quota` contains `status` (`available` or `unavailable`), `checked_at`, and optional
`windows` with `id`, `label`, `used_percent`, `window_minutes`, and `resets_at`.
Missing quota is unsupported or not queried, never unlimited. Clients must not
infer account allowance from ACP context usage or local tokens, and should mark
old snapshots as needing refresh rather than refill them at the reset time.
Engines and subscription providers may also contain `local_usage`, the reported
input/output/cache token totals and `reported_turns` in retained Wuu history.
These values exclude unreported and external activity and are not billing totals.

`engine/update` accepts `default_engine` and per-engine objects with `enabled`
and `binary_path`. Omitted fields remain unchanged. It persists settings, updates
runtime registration, and returns the same shape as `engine/list`. Discover engine
IDs from the inventory instead of hardcoding a client allowlist.

ACP sign-in has separate explicit operations:

| Method | Params | Result |
|---|---|---|
| `engine/auth/methods` | `engine_id` | `{ methods, authenticated: false }`; starts the agent and initializes ACP, but does not authenticate or create a chat session |
| `engine/authenticate` | `engine_id`, `method_id` | `{ methods, authenticated: true }` on native authentication success |
| `engine/auth/cancel` | `engine_id` | `{ ok: true }`; cancels any active authentication operation for that engine |

Each method has `id`, `name`, optional `description`, and optional `type`.
Only agent-driven methods are returned. Discovery does not report current login
status. Authentication revalidates the selected method with the launched agent;
unsupported methods and native failures return request errors. There is one active
operation per engine per server, with a five-minute limit and a 30-second
initialization limit. Cancellation and server shutdown clean up the child process;
clients should wait for the original operation to settle before retrying.

Do not call discovery automatically when rendering settings. The user must
explicitly request discovery and then authentication, because the installed agent
owns any browser interaction and credential persistence. These methods do not
support Codex, Claude Code, or OpenCode; use their native login flows. See
[external engines](../getting-started/external-engines.md) for installation,
protocol versions, model selection, and permission boundaries.

## Notifications

| Family | Client handling |
| --- | --- |
| `thread/started`, `thread/resumed`, `thread/updated` | Apply session snapshots by ID |
| `run/started`, `run/updated` | Track the invocation and its terminal state |
| `turn/started`, `turn/completed`, `turn/error` | Track individual turns, not the whole run |
| `turn/queued`, `turn/dequeued`, `turn/steered`, `turn/unsteered`, `turn/held` | Update pending input state |
| `item/started`, `item/completed`, `item/removed` | Add, settle, or retract a turn item |
| `item/agentMessage/delta`, `item/agentMessage/replace` | Append text or replace accumulated text |
| `item/toolCall/delta`, `item/toolCall/outputDelta` | Update tool arguments or output |
| `item/reasoning/delta`, `item/reasoning/replace` | Interactive reasoning presentation; omitted by `wuu exec` JSONL |
| `turn/usage`, `turn/event` | Cumulative usage snapshots and typed diagnostic/progress events |
| `agent/updated`, `agent/mailbox` | Worker state and messages |
| `mcp/status/updated` | MCP connection state |

`item/removed` retracts an attempt-scoped item; remove its `item_id` from the
indicated turn rather than inventing a completion status. `turn/usage` counts are
cumulative, not increments. Worker clients must handle nonterminal `queued` and
`waiting_children` states as well as running and terminal states. A worker waiting
for children has not delivered its final result.

## Server-initiated requests

Reverse RPC is opt-in through `initialize.params.capabilities.reverse_rpc.methods`.
The browser client advertises all six methods: `browser/cdp`, `browser/screenshot`,
`browser/open_tab`, `browser/close_tab`, `browser/set_visibility`, and
`browser/list_tabs`. These travel from core to client; they are not client-to-core
methods despite appearing beside other method constants in the source.

Reply with the server request's ID and a result or error while continuing to read
the stream. Browser calls have a 30-second response timeout. Omitted capabilities
do not authorize reverse requests. `wuu exec` does not advertise this interactive
browser surface and makes permission decisions without a client approval exchange.

## Hosted processes

```bash
WUU_HOME=/state/wuu wuu app-server \
  --host cloud --instance-id instance_123 --workspace-id workspace_123 \
  --workdir /workspace --config /run/wuu/config.json
```

Cloud mode requires explicit instance identity, workspace identity, and configuration;
it does not create a starter configuration from ambient host state. Initialization
reports `runtime_host.kind` and `instance_id`. This metadata does not create a
container or authenticate a network connection. Transport authentication,
tenant isolation, secret injection, and quotas remain responsibilities of the host.
Wuu's own [tool permissions](../reference/permissions.md) still apply within it.

## Types and diagnostics

The method and payload definitions are in
[`internal/appserver/protocol.go`](../../../internal/appserver/protocol.go), with
shared TypeScript types in
[`packages/protocol/src/index.ts`](../../../packages/protocol/src/index.ts).
Other method families cover configuration, engines, plugins, channels, session
organization, processes, activities, and MCP. Consult the matching handler for
validation and lifecycle behavior; a method constant alone does not imply direction
or support on every host.

Use `wuu debug app-server initialize` or `wuu debug app-server send` for a single
local probe, and `wuu session trace` for stored events without rerunning a task.
Debug channel commands can send real messages and invoke models; they are not
read-only protocol inspection. See the [CLI reference](../reference/cli-commands.md).

Treat method names, field meanings, and notification handling as integration
contracts. Tolerate additive fields, validate the protocol version, and test against
the core revision you ship. Product CalVer and protocol version describe different
compatibility concerns.
