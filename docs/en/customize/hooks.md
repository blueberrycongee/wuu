# Hooks

Hooks run a command or model check at a supported Wuu lifecycle event. Use them to reject a tool call, adjust its arguments, add check results, or record an event. They run trusted code or send data to a model; they are not an operating-system sandbox.

## Configure a hook

Merge a `hooks` section into the user configuration, normally `~/.wuu/config.json` or `$WUU_HOME/config.json`, then restart the runtime that should use it. Enabled plugin packages can contribute hooks too. A skill's `hooks` frontmatter is parsed for compatibility but does not register hooks.

```json
{
  "hooks": {
    "PreToolUse": [
      {
        "matcher": "bash",
        "type": "command",
        "command": "python3 /absolute/path/check-shell.py",
        "timeout": 10
      }
    ]
  }
}
```

This is a fragment, not a replacement for your provider and agent configuration. Project configuration follows the [normal loading and trust rules](../reference/configuration.md); explicit automation configuration should contain only hooks you intend to execute.

| Field | Meaning |
|---|---|
| `matcher` | Case-insensitive exact tool name; empty or `*` matches everything |
| `type` | `command` by default, or `prompt` |
| `command` | Shell command for a command hook |
| `prompt` | Model-check template; `$ARGUMENTS` inserts the event JSON |
| `model` | Optional model name for a prompt hook; otherwise the configured hook client default |
| `timeout` | Per-hook seconds; omitted or nonpositive means 30 |

Matchers do not support glob expressions. Events without a tool name need an empty matcher or `*`. Matching hooks run in configuration order and stop at the first error. When multiple successful hooks set an output field, the later value wins.

## Events

These events describe Wuu runtime paths; they do not promise equivalent interception inside an external engine.

| Event | Use and effect |
|---|---|
| `PreToolUse` | Before execution; can block or replace tool arguments |
| `PermissionRequest` | During tool authorization; can block, but does not grant broader permissions |
| `PostToolUse` | After success; can add model context, not undo the operation |
| `PostToolUseFailure` | After failure; hook errors do not replace the tool's original error |
| `UserPromptSubmit` | Before a prompt starts a turn; can block it |
| `PreCompact` | Before compaction; can prevent it |
| `PostCompact` | After compaction returns; can reject adoption of the result |
| `SubagentStart`, `SubagentStop` | Around a subagent turn; errors fail the associated operation |
| `SessionStart`, `SessionEnd` | On session binding and cleanup; errors propagate to those operations |
| `Stop` | At turn completion; can mark the turn failed |
| `FileChanged` | After a successful Wuu file-tool write or edit; output does not alter the result |

`FileChanged` is not a filesystem watcher. Manual edits, shell commands, and other programs need not trigger it. Post-tool hooks cannot turn a completed side effect into an action that never happened.

## Command input and output

Wuu starts the command through a shell, inherits its process environment, and sends one JSON input object on stdin. Read `cwd` from that object rather than assuming the hook process starts in the workspace.

```json
{
  "hook_event_name": "PreToolUse",
  "session_id": "example-session",
  "cwd": "/path/to/project",
  "tool_name": "bash",
  "tool_input": { "command": "go test ./..." }
}
```

Event-specific fields include `tool_response` for successful result text, `error` for failure details, `prompt`, `file_path`, `compact_reason`, and `agent_id`. Not every path supplies a nonempty `session_id`. Rich tool results are projected to text for hooks rather than passed as the full internal object.

Write one JSON object to stdout and diagnostics to stderr. All output fields are optional:

| Field | Effect |
|---|---|
| `decision: "block"` or `continue: false` | Reject the operation where the event supports blocking |
| `reason` | Explain the decision |
| `updated_input` | Replace the full argument object in `PreToolUse` |
| `additional_context` | Add context after a successful `PostToolUse` |

For example, this script demonstrates explicit blocking of one command pattern:

```python
import json
import sys

event = json.load(sys.stdin)
command = (event.get("tool_input") or {}).get("command", "")
if "git push" in command:
    json.dump({"decision": "block", "reason": "Publish changes manually."}, sys.stdout)
else:
    json.dump({}, sys.stdout)
```

This substring example illustrates the protocol, not a complete shell security policy. Equivalent shell commands can have different text.

A successfully decoded JSON object supplies the decision. Without one, exit 0 continues and exit 2 blocks; other nonzero codes are execution failures. To block reliably, emit an explicit blocking JSON decision or use exit 2 with empty stdout. Do not emit `{}` and assume exit 2 will override it. Invalid or mixed stdout is not interpreted as structured output.

## Prompt hooks

A prompt hook asks a model for an `ok` boolean and a reason:

```json
{
  "type": "prompt",
  "matcher": "bash",
  "prompt": "Check whether this action fits the requested review-only task: $ARGUMENTS",
  "timeout": 20
}
```

Place this entry under the chosen event. `ok: false` blocks. A missing model client, failed model request, or unparseable response currently passes through, so prompt hooks must not be your only security boundary. They also add model requests, latency, cost, and disclosure of event data to the selected service.

## Diagnose a hook

Start with a harmless event and verify its input, matcher, and output separately. Keep stdout free of logging. Check the command path, dependencies, timeout, and whether the script is waiting for interactive input. Restart the runtime after editing file-based configuration.

Hook input can contain source code, prompts, paths, and tool results. Remove temporary logging when finished and do not put secrets in command strings or shared configuration. Hooks do not become read-only simply because the agent uses Read only mode; see the [security model](../reference/security-model.md).
