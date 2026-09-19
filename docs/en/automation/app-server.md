# App-server integration basics

The app-server is the protocol boundary between the Wuu core and the desktop, scripts,
or editor shells. When building a new client, reuse this protocol instead of
reimplementing the agent loop in the shell.

## Transport format

The current protocol transports **line-delimited JSON (JSONL)** over standard input
and output. Every request carries an `id`, a `method`, and optional `params`:

```json
{"id":"1","method":"initialize","params":{}}
```

A successful response uses the same `id`:

```json
{"id":"1","result":{}}
```

An error response contains `error`; notification messages have no `id`, for example:

```json
{"method":"turn/completed","params":{}}
```

`initialize` returns the current protocol version, `wuu-app-server/v0.1`. This is a
controlled integration protocol whose fields may still evolve; clients should handle
messages by method and event type, not by relying on natural-language error text.

## Lifecycle of a task

A client should drive the core in this order:

1. `initialize`: establish the connection and obtain capabilities, configuration, and
   the protocol version;
2. `thread/start` or `thread/resume`: create or resume a session;
3. `turn/start`: interactive clients such as the desktop start a single-turn task, or
   use `run/start` to start the automated run that `wuu exec` uses;
4. consume `turn/*` notifications and wait for `run/updated` to reach a terminal state
   (automation runs);
5. `shutdown`: request a clean shutdown when the client exits.

`thread/start` creates a persistent session by default; a session passed with
`{"ephemeral": true}` exists only in memory and cannot be resumed after the server
exits. `thread/fork` can create a new branch from an existing session, turn, or entry.

## Common methods

| Method | Purpose |
| --- | --- |
| `thread/start` | Create a session |
| `thread/resume` | Resume a session; an empty session ID means the most recent visible session |
| `thread/fork` | Create a branch from an existing session |
| `turn/start` | Start an interactive single turn, which may include attachments |
| `run/start` | Start an automation run, used by `wuu exec` |
| `turn/interrupt` | Interrupt a single-turn task |
| `run/interrupt` | Interrupt an automation run |
| `shutdown` | Ask the server to shut down |

Model and permission mode are session choices. To change them, call
`config/model/update` first rather than overriding them temporarily in a single-turn
request; a running session cannot change the model or permission mode already adopted
for this turn.

## Named Agent media handoff

The named-agent `session` tool accepts an optional `media` array on `create` and
`send` (queue or steer). These are tool arguments, not a new JSON-RPC method:

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

Each reference selects one attachment from a stored room message. `kind` is
`image` or `file`; `index` is one-based within that message's `images` or `files`
array. `chat_read` exposes the message ID and attachment order. Up to 32 selected
attachments travel with their room/message/author provenance, original message
text and optional per-attachment description. Omitting `media` sends text only;
mentioning a path in `prompt` does not attach an image. The copied image is the
stored intake-normalized image; handoff does not resize it again.

The source must belong to the originating turn's room and the named identity
must still have access there. Membership in another room does not authorize a
cross-room reference. The host supplies identity and turn scope; callers cannot
use filesystem paths, arbitrary URLs or remote cache references to bypass them.
The receiving ordinary session gets only the selected evidence, not room access
or additional filesystem permissions. It uses its bound project's runtime and
existing permission policy, even when another project hosts the named identity.

The durable operation stores references, rechecking access and payload availability
on dispatch and queue recovery. Missing messages, missing attachment positions,
empty payloads and unsupported types reject the whole handoff. After admission,
the bytes and provenance are durable session input; deleting a source later does
not recall a copy already delivered. PNG/JPEG/GIF/WebP images and the existing PDF
and video attachment paths are supported. Video still requires a supported model
and connection. Audio, arbitrary documents and external-engine media handoff are
not supported.

Known-incompatible model capabilities fail before input admission. Required
evidence also fails at the provider request boundary rather than becoming an
unsupported-media marker, including retained media after a model switch. Unknown
catalog capabilities preserve normal pass-through to provider validation; they
are not a guarantee of support. Provider/transport failures end the turn and are
reported through normal session results. Queue-time failures are recorded in the
operation and reported while the source conversation remains authorized; never retry with text alone
without deciding how to replace the missing evidence. Normal context compaction
and its media recovery rules still apply to older history.

For development, the data flow is `HarnessSessionTool` → authenticated
`AgentClient` → durable Harness operation → room-scoped attachment resolution →
`ChatMessage.Images/Files` → session admission/history → provider media encoding.
The regression fixtures use generated images and loopback HTTP, not private
photos or a live inference service:

```bash
go test ./internal/appserver ./internal/channels ./internal/providers \
  -run 'TestHarnessMedia|TestHarnessWorkspace|TestRequiredMedia' -count=1
```

## Local debugging

The repository provides CLI debug entry points that start a local server and send a
single protocol request:

```bash
wuu debug app-server initialize --workdir /path/to/project
wuu debug app-server send thread/start '{}'
```

Production authentication, sandboxing, organization membership, secret injection, and
quotas are handled by an external control plane; they are not capabilities the
app-server itself provides. See the [app-server protocol
documentation](../integrations/app-server-protocol.md) for the complete methods and
parameter reference.
