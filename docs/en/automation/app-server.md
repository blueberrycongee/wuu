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
for future conversations. Do not try to override a turn's permission mode through
`turn/start`.

## Run a project

`thread/start` with `project: {"name": "..."}` creates a project coordinator in the
workspace. The returned thread has `source: "project"` and `permission_mode:
"read_only"`; `config/model/update` and `turn/start` refuse to widen it. The
coordinator manages sessions through its `session` tool. Each managed session is an
ordinary thread with `source: "project-session"` and `project_id` naming its
coordinator, and its `session_control` names the project as `manager_name`. Project
threads carry `pending_candidates`: a session's undecided candidates, or all of a
coordinator's. The count is refreshed and announced when a candidate is frozen or
decided.

The coordinator receives host events as user items with `origin: "plugin"`,
`related_session_id` naming the session, and a `cause`:

| Cause | Event | Starts a turn when idle |
|---|---|---|
| `project_result` | A managed turn ended, with what the user wrote into it | Yes |
| `project_takeover`, `project_pause` | The user took over or paused a session | No, joins the next turn |
| `project_return` | The user returned a session | Yes |
| `project_applied`, `project_discarded`, `project_published` | The user decided a candidate | Yes |
| `project_adopted` | The user added a conversation to the project | Yes |
| `project_released` | The user removed a session from the project | No, joins the next turn |

Starting, steering or queuing a turn in a managed session keeps it under the project.
`thread/control/take` with `thread_id` and the current `revision` takes it over,
interrupting pauses it, and `thread/control/return` hands it back. Turns that end
while the user holds control are frozen for review but not reported.

`project/session` changes membership: `adopt` with `project_id` and `session_id`
brings an ordinary conversation of the project's workspace under the project, and
`release` makes a managed session an ordinary conversation again once its pending
candidate is decided. Both return the updated `thread`.

`project/candidate` reviews worktree changes. A candidate holds every change of its
session not yet applied or published; a newer one supersedes the session's undecided
candidates.

| Action | Parameters | Result |
|---|---|---|
| `list` | `project_id` or `session_id` | `candidates`, oldest first |
| `get` | `session_id`, `turn_id` | `candidate` with `diff` |
| `apply` | `session_id`, `turn_id` | `candidate` with `disposition: "applied"` |
| `discard` | `session_id`, `turn_id` | `candidate` with `disposition: "discarded"` |
| `publish` | `session_id`, `turn_id`, `url` | `candidate` with `disposition: "published"` and `url` |

`apply` writes only the frozen change into the workspace and leaves it unstaged. A
conflict returns an error and changes neither the workspace nor the candidate. An
extension publishes a candidate; `publish` records the link it returned. A candidate
takes one decision, and a superseded one takes none.

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
