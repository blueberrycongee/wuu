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

## Named Agent media handoff

The named-agent `session` tool can attach selected room media when creating or sending to a work session, including queue and steer modes. This is a tool contract, not a new JSON-RPC method:

```json
{
  "action": "create",
  "workspace_root": "/path/to/project",
  "prompt": "Check the screenshot against the implementation",
  "media": [
    {"message_id": "message-id-from-chat_read", "kind": "image", "index": 1,
     "description": "Inspect the clipped bottom row"}
  ]
}
```

Use the message ID and attachment order from `chat_read`. `kind` selects `image` or `file`, and `index` is one-based within the message's corresponding array. Up to 32 distinct selections carry their room, message, author, source text, and optional description. Omitting `media` sends text only; a path mentioned in the prompt does not attach a file. Stored images are copied without another resize.

Sources must belong to the originating turn's room, and the named identity must still have access. Membership in another room does not allow cross-room references. The host supplies identity and turn scope; paths, URLs, and remote cache references cannot replace stored-message references. The receiving session gains the selected evidence, not room access or additional filesystem permissions, and uses its own bound project's runtime.

Before delivery, durable operations retain references and recheck access and payloads during dispatch or recovery. Missing messages, invalid positions, empty payloads, and unsupported types reject the handoff. Once admitted, bytes and provenance become durable session input; later source deletion does not recall a delivered copy. Recovery recognizes an existing receipt before resolving the source again.

PNG, JPEG, GIF, WebP, PDF, and supported video attachments use the existing media path. Video requires a compatible model and connection. Audio, arbitrary documents, and media handoff to external engines are not supported.

Selected media is required evidence. Known-incompatible model capabilities fail before input admission, and the provider request boundary rejects unsupported required media instead of dropping it, including retained evidence after a model switch. Unknown catalog capabilities defer to provider validation and do not guarantee support. Normal context compaction still applies to older history.

Provider failures appear in normal session results; queue-time failures are recorded in the operation and reported while the source conversation remains authorized. Do not silently retry with text alone: choose compatible input or replace the missing evidence first.

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
