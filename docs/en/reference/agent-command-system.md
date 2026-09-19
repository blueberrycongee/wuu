# Agent command system

The built-in Wuu engine uses `bash` for commands, tests, builds, and background processes. Commands run on the host machine in the session's workspace. External engines have their own command implementations; this page describes Wuu's tools.

## Run a command

`action=run` is the default. Use it when the result determines the next step:

```json
{
  "command": "go test ./internal/process",
  "timeout_seconds": 300,
  "purpose": "Check process lifecycle behavior",
  "scope": "targeted"
}
```

The result includes the exit code, duration, bounded output, output tails, and workspace revision. A full-log reference is supplied when available. Recognized test, build, lint, and type-check commands also receive verification metadata. `scope` describes the intended coverage (`targeted`, `affected`, or `full`); it does not change which tests the command runs.

Commands must be non-interactive: do not depend on an editor, pager, or terminal prompt. The default wait is 300 seconds, with a range of 1–3600. On timeout, Wuu normally transfers the running process to its background manager and returns its ID. This is **not** a successful verification or proof that the command stopped. If transfer is unavailable, Wuu stops the process. Cancelling the calling turn also stops a foreground command rather than promoting it.

## Background and interactive work

Start a server, watcher, or interactive command with `action=start_background`, rather than appending `&`:

```json
{
  "action": "start_background",
  "command": "npm run dev",
  "completion_mode": "detached"
}
```

Background starts use a pseudo-terminal by default. Set `tty=false` for log-only automation. macOS and Linux support PTYs; Windows falls back to pipes, so terminal behavior is different.

`completion_mode=resume`, the default, makes a natural exit schedule a continuation in the owning conversation. It also keeps `wuu exec` waiting for that continuation. Choose `detached` for a long-lived service whose eventual exit should not resume or hold open the current task.

| Action | Use |
| --- | --- |
| `list_background` | Inspect the session's managed processes |
| `read_background` | Read output using `process_id`; pass the previous `end_offset` as `offset_bytes` for incremental reads |
| `write_background` | Send `input` to a process with a live input handle; include a newline when needed |
| `stop_background` | Stop the process tree |
| `update_background` | Change `recheck_minutes`; `0` cancels the schedule |

Reads return up to 32 KiB by default. Without `wait_ms`, a read is a snapshot. With it, the call waits for new output or exit, up to 300,000 ms; an initial start can wait up to 60,000 ms. Continuously arriving output is paced rather than returning on every byte.

Use `recheck_minutes` from 1 to 1440 for progress wake-ups on long or quiet tasks. Completion cancels the schedule. Do not chain waits just to keep a turn open: use a foreground run for an immediate dependency, or let completion and scheduled wake-ups resume the conversation.

## Directory, environment, and permissions

`cwd` defaults to the workspace root and must resolve to an existing, permitted directory. Worktree-bound workers execute in their assigned checkout. Each ordinary command starts a new shell; `cd`, aliases, and temporary environment changes do not persist between calls.

Wuu resolves Bash through its shell launcher, including Git Bash on Windows. Both foreground and background commands use the shared environment preparation: inherited build-time `GOROOT` is removed and non-interactive defaults are applied. PTY startup restores terminal-oriented settings where needed.

Command classification helps with permission checks and scheduling, but is not the filesystem sandbox. In confined modes, both foreground and background launches use the configured filesystem process sandbox. A missing enforcement provider causes launch failure rather than silently running unconfined. The built-in provider is currently macOS-only; network isolation is outside this contract. See [permissions](permissions.md) for modes and platform limits.

A denied write is not an invitation to retry through another shell command. Add an authorized workspace root or explicitly change the session mode when the task requires access outside its boundary. Wuu does not offer per-command approval prompts.

## Apply a patch and validate it

When the follow-up command is already known, `apply_patch` can run it after applying the complete patch:

```json
{
  "patchText": "*** Begin Patch\n*** Add File: example.txt\n+hello\n*** End Patch",
  "then_run": {
    "command": "test -f example.txt",
    "timeout_seconds": 30,
    "purpose": "Check the new file exists"
  }
}
```

`then_run` accepts `command`, `cwd`, `timeout_seconds`, `purpose`, and `scope`, with normal command permission checks and records. It cannot be combined with `dry_run`. A failed patch skips the command; a failed command leaves the successful patch in place. If validation moves to the background, wait for its actual terminal result before treating it as passed. Do not reapply the patch just to rerun validation.

Each resolved path may belong to only one file section, including move sources and destinations. Aliases such as `a.txt` and `./a.txt` count as the same path. Combine multiple edits to one file in a single `Update File` section with several `@@` chunks. Conflicting paths fail validation before any writes or `then_run`, including in `dry_run` mode.

## Logs and lifetime

Normal command output and its saved full log are redacted before being returned or persisted as a tool log. Background process records and merged stdout/stderr logs can contain raw text on disk; redaction happens when the tool returns them. Keep secrets out of arguments and output, and review logs before sharing them.

Live background logs can grow. Finished logs are compacted to an 8 MiB tail, and startup maintenance expires terminal records and logs after 30 days when no completion remains undelivered. A returned byte offset may therefore precede retained output; use the returned offsets rather than assuming the whole log remains available. Foreground commands also buffer output in memory while running, so unusually noisy commands can consume substantial memory.

The default `lifecycle=session` participates in runtime cleanup; `managed` skips that session-lifecycle cleanup. Neither is an operating-system service manager. Restarting Wuu cannot restore a lost PTY or stdin handle, and a recovered process may lack a reliable exit code. Stop unneeded processes explicitly instead of relying on a conversation tab's lifetime. Before stopping, Wuu checks process identity to avoid targeting a reused PID, then requests a graceful stop before escalating to the process tree.

Desktop process controls and the model's tools use the same manager. App-server process methods check thread ownership; clients cannot operate on another thread's process simply by guessing its ID. For embedding details, see the [app-server protocol](../integrations/app-server-protocol.md).
