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

Initialization also includes `catalog_providers`, the local connectable service
directory, so clients can display service choices immediately. A successful
`config/model-catalog/refresh` returns the updated directory in the same field;
keep the existing choices visible while refreshing. Older hosts may omit this
field; use `config/model-catalog/providers` in that case.

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

Thread snapshots carry an optional `title_revision`, a monotonically increasing
version saved with each title change. Treat an omitted revision as zero. When
merging lists, resumes, or `thread/updated` notifications, preserve the title from
the higher revision; `updated_at` tracks conversation activity and cannot order
title changes. Automatic generation fills only an empty title and may retry after
a later successful turn. It never replaces a saved manual title.

A conversation keeps its own model and permission selection. Change it through
`config/model/update` with `thread_id` while the conversation is idle. This does
not change workspace defaults. A busy conversation returns `thread_busy`, allowing
the client to wait and retry. A request without `thread_id` updates defaults
for future conversations; add `keep_selection: true` to save a provider's
connection without making it the default. Do not try to override a turn's permission mode through
`turn/start`.

## Archive a running conversation

`thread/archive` takes `thread_id` and `archived`. A running conversation requires
explicit confirmation followed by `archived: true, force: true`. The request
interrupts its captured execution, holds queued input, and remains pending until
partial history, terminal state, and runner cleanup settle. It does not ask the
user to repeat confirmation merely because cleanup takes more than two seconds.
Over stdio, later requests for the same conversation remain ordered behind it;
other conversations and server shutdown remain responsive.

The desktop has no archive-response timeout. Navigating away retires the old UI
result owner, not the confirmed request. Server shutdown or an embedding's
cancelled request context stops archive before its metadata commit; authorization
is checked again after settlement. If the connection or commit acknowledgement
is lost, inspect the persisted session before retrying: the outcome may be
unknown. An error that explicitly reports committed archive metadata with an
unavailable refreshed view does not mean the mutation failed. A committed change
is not undone by later cancellation.

## Page conversation history

For a compact conversation view, send `conversation_history: true` with
`thread/resume`. The response folds settled tool activity and commentary into
`process_summary` items before applying the history page budget. Replies, errors
and published attachments remain visible; active turns retain live items.
Pass the returned opaque `history_cursor` to `thread/history/read` to load older
conversation pages. These pages currently allow up to 100 turns and 1 MiB of
serialized item data; a page can split a turn, but never a process summary.
The iOS timeline starts loading older pages automatically after opening a
conversation and continues until history is complete. Requests run one at a
time and stop when the view closes, the conversation changes, or the connection
is lost. Reading position is retained while browsing older messages; a failed
page can be retried at the top of the timeline. Tool details and full oversized
content remain on demand.

When the user opens a summary, call `thread/activity/read` with `thread_id`,
`turn_id`, the summary’s `item_id`, and `activity.last_item_id` as `last_item_id`.
The response contains `turn` and an optional `next_cursor`. Pass that cursor as
`cursor` to read the next detail page. Keep these detail pages separate from the
conversation state. Oversized individual items retain `remote_content_ref` for
`thread/content/read`. If history editing removes a boundary, reopen the
conversation instead of continuing with the stale cursor.

Clients that omit `conversation_history` keep the existing item history format.
Older hosts ignore the option and return their usual history pages.

## Delete archived conversations

`thread/listArchived` returns archived conversations across the configured
session store, including other workspaces. To delete that snapshot, call
`thread/delete` for each ID with `only_if_archived: true`. Each request is
permanent and independent; report any failures. A conversation restored before
its deletion transaction is rejected and keeps its history and side conversation.
Running conversations, active agents, and running side conversations also block
deletion. Omitting `only_if_archived` still allows ordinary idle conversations
to be deleted.

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

## Select an engine

`thread/start` uses `engines.default_engine` when `engine` is omitted. An empty
default selects `wuu`. A disabled, removed or unavailable engine rejects creation
before a session is persisted; it never substitutes another implementation.
`engine/update` retains the configured default when disabling that engine. Restore
it or explicitly set `default_engine` to an available engine, such as `wuu`, to
resume creating conversations. Existing conversations retain their saved engine
and history; changing the default does not migrate them.

This replaces the former automatic Wuu fallback. Clients must handle an
unavailable default and offer an explicit selection instead of rewriting it.

## Answer native engine questions

Codex questions use the existing `user-question/requested` and
`user-question/resolved` notifications and `user-question/list`,
`user-question/respond`, `user-question/cancel`, and `user-question/hold` methods.
Requests carry the owning Wuu `thread_id` and `turn_id`, plus the native item in
`call_id`. Question IDs are scoped to this host request; echo them unchanged.

A request with `reply_to_tool: true` returns answers to the waiting engine call.
Do not also submit a message or steer a turn. `mode: "ask"` uses the regular
question form; `mode: "offer"` uses the optional card with the existing 20-second
expiry, paused by `user-question/hold`. Cancelling, skipping, or expiry supplies
no answer and never selects an option. Turn completion, interruption, and native
process disconnect retire the question; late responses fail. Missing native
blocking metadata defaults to a blocking question. Secret-input requests are
explicitly unsupported and are never shown in a plain-text field.

Wuu enables questions in ordinary native turns when the host question bridge is
available. An explicit native `features.default_mode_request_user_input = false`
is retained, and Wuu never overrides the native tool-level disable setting.
Feature configuration is applied at thread creation or cold resume. Loaded native
threads keep their initial feature configuration; reconnect the native engine or
start another conversation to apply a changed native question setting.
