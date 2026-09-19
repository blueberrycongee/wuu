# JSONL events

`wuu exec --json` writes one JSON object per stdout line. Every object has a `type`;
diagnostics go to stderr. This stream describes an execution run, rather than
exposing the app-server's wire messages unchanged.

## Consume a stream

Dispatch on `type`, tolerate additional fields and unknown event types, and keep
reading until `result`. Track a session by `thread_id`, a turn by `turn_id`, a tool
call by `item_id`, and a subagent by `agent_id`. `run_id` is included on `result`
and the structured-output `error` event, not on every progress event.

One run can contain several turns. Neither a final message replacement nor
`turn_completed` is the run's terminal signal. Check the process exit code too:
invalid CLI input can fail before emitting JSONL, and a killed process or broken
output pipe may leave no complete result. See [`wuu exec`](exec.md) for exit codes.

## Final result

```json
{
  "type": "result",
  "status": "completed",
  "thread_id": "thread-id",
  "turn_id": "turn-id",
  "run_id": "run-id",
  "final_message": "The report is ready.",
  "trace_path": "/path/to/trace.jsonl"
}
```

`status` is `completed`, `failed`, `permission_denied`, `timeout`, or `interrupted`.
Failures can include an `error` string. Identifiers can be empty when setup failed
before a session or run existed. `final_message` on failure can be partial; it is
not proof that the requested work finished. With `--output-schema`, successful
validation also supplies `structured_result`, the parsed JSON value.

The separate `error` event currently reports a disagreement between the CLI's
final schema validation and the server's completed run. It includes `error` and
`retrying: false`. It is not a general error channel: inspect `result` even when
no `error` event appeared.

## Session and turn events

| Type | Payload and meaning |
| --- | --- |
| `session_configured` | `protocol_version`, `provider`, `model`, `max_parallel`, `workspace_root`, `permissions` after initialization |
| `thread_started`, `thread_resumed`, `thread_forked` | `thread_id`, `provider`, `model`, `cwd` for the acquired session |
| `turn_started` | `thread_id`, `turn_id`; begins a new turn's message state |
| `agent_message_delta` | Append `delta` to the current message |
| `agent_message_final` | Replace the accumulated message with `message`; this is a replacement event, not a guaranteed end marker |
| `usage_updated` | Cumulative `input_tokens`, `output_tokens`, `cache_creation_tokens`, `cache_read_tokens` for the turn |
| `turn_completed` | Token counts, `trace_path`, and `awaiting_auto_continuation` |
| `turn_failed` | `error` for the failed turn |
| `turn_interrupted` | `reason`, such as `timeout` or `interrupted` |

`thread_started` also occurs for ephemeral sessions, so it does not imply durable
storage. Usage snapshots are not deltas: use the latest snapshot per turn rather
than summing every event. When `awaiting_auto_continuation` is true another turn
is expected; even when it is false, wait for `result` to determine run settlement.

## Tool and command events

```json
{"type":"tool_started","thread_id":"thread-id","turn_id":"turn-id","item_id":"item-id","name":"read_file","arguments":"{\"path\":\"README.md\"}"}
{"type":"tool_output_delta","thread_id":"thread-id","turn_id":"turn-id","item_id":"item-id","delta":"output fragment"}
{"type":"tool_completed","thread_id":"thread-id","turn_id":"turn-id","item_id":"item-id","name":"read_file","status":"completed","error":""}
```

`arguments` is a JSON-encoded string, not an object. `tool_completed` supplies the
call status and error; it does not repeat the full result. Accumulate output deltas
if your client needs that content. `tool_removed` withdraws an item by `item_id`.

For `bash`, the stream additionally emits `command_started`,
`command_output_delta`, `command_completed`, and `command_removed`. These refer
to the same item, so do not count them as separate tool calls. Command events add
`name`, `command`, and `arguments` when available; completion adds `status` and
`error`. Background-process control calls also use this family. Completion of the
tool call does not necessarily mean the underlying background process has exited.

`file_changed` is derived from structured file-tool results, not a filesystem
watcher. It carries `tool_name`, `path`, and `action`, with available hashes and
`workspace_revision`; it does not include full file contents or diffs. Do not use
it as a complete list of every write made by shell commands or external processes.

## Subagent and task events

`subagent_started`, `subagent_updated`, and `subagent_completed` carry `agent_id`,
`thread_id`, `agent_type`, `status`, and task metadata. Available fields include
`task_name`, `agent_profile`, `agent_path`, `parent_id`, `description`, `result`,
`result_path`, `result_bytes`, `result_truncated`, `error`, and token counts.
A first observed update may already be terminal, so a client must not require a
preceding `subagent_started`. Use `result_path` when the inline result is truncated.

`todo_updated` carries the task-list payload in `todo`. These progress events
depend on the active tools and plugins; a run need not emit every event family.

## Request diagnostics

`provider_state` describes one provider step: `step_index`, `provider`, `protocol`,
`transport`, replay and connection reuse, fallback state, failure phase, and input
item counts. Fallback fields include `fallback_active`, `fallback_reason`,
`fallback_transport`, `fallback_pin_status`, `fallback_retry_after_ms`, and
`fallback_ttl_ms`. They describe transport behavior, not task completion.

`request_context` describes the composed request through counts, byte sizes,
segment categories, hashes, and cache metadata. It includes message and tool
counts; system, stable-prefix, turn-prefix, and tool-surface hashes;
`prompt_cache_key`; and, when available, `system_sections`. It is diagnostic
metadata, not a full request body from which the model input can be reconstructed.

The CLI omits provider reasoning notifications. Tool argument redaction is best
effort; the stream can still contain private source, prompts, paths, and tool
output. Treat it as sensitive task data and review it before publishing logs.

The field mapping is implemented in
[`internal/exec/runner.go`](../../../internal/exec/runner.go). For clients that
need direct session control, use the
[app-server protocol](../integrations/app-server-protocol.md).
