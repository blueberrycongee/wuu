# Connect a client to app-server

Use app-server when building a client that needs to manage sessions, stream turns,
or control runs. For a script that only submits a task and captures its result,
[`wuu exec`](exec.md) provides that lifecycle already.

## Start the process

```bash
wuu app-server --workdir /path/to/project
```

Keep stdin and stdout connected to your client. Each protocol message is a JSON
object on one line; stdout is the protocol stream, so keep process diagnostics
separate. The server uses local configuration unless you supply an explicit
`--config` file. `--safe-mode` starts without activating plugins for recovery.

Send `initialize` first:

```json
{"id":"1","method":"initialize","params":{"protocol_version":"wuu-app-server/v0.1","client":{"name":"example-client"}}}
```

Responses carry the same `id` and either `result` or `error`. Notifications have
`method` and `params` without an `id`. Check initialization's `status` and `issues`:
a successful protocol response can still report `needs_setup`.

## Run a task

Create a session, then use the returned `result.thread.id`:

```json
{"id":"2","method":"thread/start","params":{}}
{"id":"3","method":"run/start","params":{"thread_id":"THREAD_ID","prompt":"summarize this repository","request":{"mode":"start"}}}
```

These are sequential requests, not a batch to paste unchanged: replace `THREAD_ID`
with the actual ID after the first response. Keep reading notifications until
`run/updated` reports a terminal status for the returned run ID. A `run/start`
response means the run was admitted, not that the task completed.

Interactive clients can use `turn/start` instead, consuming turn and item updates.
Automation runs can include several continuation or schema-correction turns,
so `turn/completed` alone does not settle a run. Use `run/interrupt` to stop a run,
and request `shutdown` before closing a client-owned server.

## Reuse a session

`thread/resume` takes `session_id`; omitting it selects the most recent visible
session in the workspace. `thread/fork` takes `thread_id` and creates a separate
conversation. `thread/start` with `ephemeral: true` creates an in-memory session
that cannot be resumed after the server exits.

A conversation keeps its own model and permission selection. Change it through
`config/model/update` with `thread_id` while the conversation is idle. This does
not change workspace defaults. A busy conversation returns `thread_busy`, allowing
the client to wait and retry. A request without `thread_id` updates defaults
for future conversations; add `keep_selection: true` to save a provider's
connection without making it the default. Do not try to override a turn's permission mode through
`turn/start`.

## Delete archived conversations

`thread/listArchived` returns archived conversations across the configured
session store, including other workspaces. To delete that snapshot, call
`thread/delete` for each ID with `only_if_archived: true`. Each request is
permanent and independent; report any failures. A conversation restored before
its deletion transaction is rejected and keeps its history and side conversation.
Running conversations, active agents, and running side conversations also block
deletion. Omitting `only_if_archived` still allows ordinary idle conversations
to be deleted.

## Run a project

`thread/start` with `project: {"name": "..."}` creates a project coordinator in the
workspace. The returned thread has `source: "project"` and follows ordinary
session permission and model settings. The lead can work directly and manages
other sessions through its `session` tool. Each managed session is an
ordinary thread with `source: "project-session"` and `project_id` naming its
coordinator, and its `session_control` names the project as `manager_name`.
A worktree session's changes stay in its worktree until the team delivers them with
ordinary Git commands. Commit, merge, push and PR mutations each require user
authorization or an established workflow; delegation cannot expand authority.

Managed threads expose `project_role: "side" | "worker"`; older members default to
worker. `session create` accepts `role` (worker by default) and optional
`model_alias`. Only the lead creates a side; repeated creation returns the existing
live side without sending a new prompt. Lead and side can create workers. All
active members can `list`, `inspect`, and `message`; only the lead can `send` and
`stop`. Messages stay in the project and include `prompt`, `session_id`, and
optional `wake` (false by default). Stopping or directly messaging a member keeps
its project membership. Stopping revokes earlier queued automatic input; a later
explicit dispatch remains possible without returning control.

`session side` creates or resumes the persistent side with a new brief, steering
its running turn. Side defaults to shared workspace; workers default to isolated
Git worktrees when available. Dispatches are durable and replay-safe, including
the first brief committed with session creation. Busy dispatches return
`state: "queued"` and retry after recovery.

`side` waits by default; `create` and `send` return immediately unless `block: true`.
`wait` takes `session_id` and an optional precise `turn_id`; the lead can wait for
members, and the side can wait for workers. Results include `turn_id`, `turn_status`
and terminal `final_output`. `timeout_ms` defaults to 60000 and is capped at 300000;
expiry returns `timed_out: true`. Timeout or caller cancellation never stops the child.

The lead uses its conversation model. `agent.project_models.side` and `.worker`
select provider/model defaults for new members; empty selections inherit the lead.
A creation-time `model_alias` overrides the role default. Existing sessions retain
their saved model. `config/advanced/update` accepts `project_models` with the same
shape; initialize, config reads, updates and config-change events return the current
defaults. Desktop runtime settings expose both choices when `features.project_agent` is true.

The coordinator receives host events as user items with `origin: "host"`,
`related_session_id` naming the session, and a `cause`:

| Cause | Event | Starts a turn when idle |
|---|---|---|
| `project_message` | Message from a team member, received by any member or the lead | Only when `wake` is true |
| `project_result` | A managed turn ended, with what the user wrote into it | Yes, except interrupted turns |
| `project_stopped` | The user stopped a member's current turn | No, joins the next turn |
| `project_user_message` | The user wrote directly to a member | No, joins the next turn |
| `project_adopted` | The user added a conversation to the project | Yes |
| `project_released` | The user removed a session from the project | No, joins the next turn |

Starting, steering, queuing or interrupting a turn in a project member keeps its
membership active. Direct user messages send a notice to the coordinator without
waking it from idle. Interrupted results are also delivered without waking an idle
coordinator; normal results wake it. Worktree changes stay in the member's worktree.
The same interrupted-result policy applies after recovery and to reports sent to
the Side Agent that dispatched the member.
The `thread/control/take` and `thread/control/return` ownership lifecycle is for
plugin-managed sessions, not project members.

`project/session` changes membership: `adopt` with `project_id` and `session_id`
brings an ordinary conversation of the project's workspace under the project, and
`release` makes a managed session an ordinary conversation again. Both return the
updated `thread`.

## Probe the protocol

```bash
wuu debug app-server initialize --workdir /path/to/project
wuu debug app-server send --workdir /path/to/project config/read '{}'
```

Each debug command starts a server, performs the request, and shuts it down. Use a
long-lived client for streamed execution rather than chaining independent probes.

The stdio protocol is a trusted local control surface, not an authenticated network
service. A remote or hosted deployment must provide its own transport security and
isolation around it. The [protocol reference](../integrations/app-server-protocol.md)
covers message shapes, capabilities, selection rules, and cloud process identity.

## Fusion handoffs

Start a persistent Wuu conversation with `thread/start` and `fusion: true` to use
its configured Lead and Sidekick. The Lead owns decisions and review; the persistent
Sidekick executes complete briefs in the shared workspace with independent history.

`fusion_delegate` blocks by default. With no `timeout_ms`, it waits for completion,
cancellation or new Lead input. Explicit timeouts are capped at five minutes and
leave the Sidekick running. `block: false` enables independent Lead work; a later
`wait` takes over result delivery. If `wait_status` is `report_delivered`, the report
text is already in a background notification; status and failure metadata remain
in the wait response, and `inspect` can recover the full report. Reports require
review of the current task revision before acceptance.

`report.verification` carries host-recorded foreground verification commands from
the report's turn, including failed exits, workspace revisions and full log
references. It survives transcript reload and accompanies background delivery;
an already-delivered wait does not repeat it. These records are evidence, not a
complete verification inventory or task acceptance. Sidekick reports should also
include relevant artifact paths directly, rather than referring to private notes.
Use `update` only while work is queued or running. Request missing evidence after
completion through `review` with `verdict: request_changes` and the current report
ID and revision; this retains the existing Sidekick and stale-report protection.

Read-only research does not reserve workspace writes. An implementing Sidekick
reserves them until it finishes or is stopped and actually becomes idle. This
coordination covers tool calls, so participants must also finish conflicting
background writers before handing over the workspace. A Lead turn ending does
not by itself mean all Fusion work has completed; inspect task and execution state.
