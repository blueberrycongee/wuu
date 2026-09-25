# Agent command system

The built-in Wuu engine uses two tools for commands. `bash` runs one bounded, non-interactive command: tests, builds, lint, git, scripts. `process` manages long-lived or interactive programs: servers, watchers, REPLs, downloads. Both run on the host machine in the session's workspace and share one process manager. External engines have their own command implementations; this page describes Wuu's tools.

## Run a command

`bash` takes `command`, `timeout_seconds`, `cwd`, `purpose`, and `scope`:

```json
{
  "command": "go test ./internal/process",
  "timeout_seconds": 300,
  "purpose": "Check process lifecycle behavior",
  "scope": "targeted"
}
```

The model receives plain text: an exit line with the duration, the bounded stdout and stderr excerpts, and only the facts that change the next step, such as a truncated stream with its full-log path, a sandbox denial, a timeout hand-off, or the verification outcome. Each stream keeps its head and tail, so a test runner's summary survives even when the middle is cut. Recognized test, build, lint, and type-check commands add verification metadata; `scope` describes the intended coverage (`targeted`, `affected`, or `full`) and does not change which tests run. `purpose` is kept in the audit log only.

Clients and durable records keep the full JSON envelope: exit code, duration, both excerpts, byte counts, classification, workspace revision, the full-log reference and hash, and verification details. The desktop terminal panel renders from that envelope.

Commands must be non-interactive: do not depend on an editor, pager, or terminal prompt. The default wait is 300 seconds, with a range of 1–3600. On timeout, Wuu normally transfers the running process to its background manager and reports the process ID; the result says the command is still running and its completion starts a new turn. This is **not** a successful verification or proof that the command stopped. If transfer is unavailable, Wuu stops the process. Cancelling the calling turn also stops a foreground command rather than promoting it.

## Background and interactive work

Start a server, watcher, or interactive command with `process` rather than appending `&`:

```json
{
  "action": "start",
  "command": "npm run dev",
  "completion_mode": "detached"
}
```

Starts use a pseudo-terminal by default. Set `tty=false` for log-only automation. macOS and Linux support PTYs; Windows falls back to pipes, so terminal behavior is different.

`completion_mode=resume`, the default, makes a natural exit schedule a continuation in the owning conversation. It also keeps `wuu exec` waiting for that continuation. Choose `detached` for a long-lived service whose eventual exit should not resume or hold open the current task.

| Action | Use |
| --- | --- |
| `list` | Inspect the session's managed processes |
| `read` | Read output using `process_id`; pass the previous `end_offset` as `offset_bytes` for incremental reads |
| `write` | Send `input` to a process with a live input handle; include a newline when needed |
| `stop` | Stop the process tree |
| `update` | Change `recheck_minutes`; `0` cancels the schedule |

Reads return up to 32 KiB by default. Without `wait_ms`, a read is a snapshot. With it, the call waits for new output or exit, up to 300,000 ms; a start can wait up to 60,000 ms. Continuously arriving output is paced rather than returning on every byte.

Use `recheck_minutes` from 1 to 1440 for progress wake-ups on long or quiet tasks. Completion cancels the schedule. Do not chain waits just to keep a turn open: use a foreground `bash` run for an immediate dependency, or let completion and scheduled wake-ups resume the conversation.

Transcripts recorded before the split show the background actions under `bash` (`start_background`, `read_background`, and so on). The desktop still renders those items; new sessions use `process`.

## Directory, environment, and permissions

`cwd` defaults to the workspace root and must resolve to an existing, permitted directory. Worktree-bound workers execute in their assigned checkout. Each `bash` call starts a new shell; `cd`, aliases, and temporary environment changes do not persist between calls.

Wuu resolves Bash through its shell launcher, including Git Bash on Windows. Both tools use the shared environment preparation: inherited build-time `GOROOT` is removed and non-interactive defaults are applied. PTY startup restores terminal-oriented settings where needed.

Command classification helps with permission checks and scheduling, but is not the filesystem sandbox. In confined modes, both tools use the configured filesystem process sandbox. A missing enforcement provider causes launch failure rather than silently running unconfined. The built-in provider is currently macOS-only; network isolation is outside this contract. See [permissions](permissions.md) for modes and platform limits.

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

`then_run` accepts `command`, `cwd`, `timeout_seconds`, `purpose`, and `scope`, with normal command permission checks and records. It cannot be combined with `dry_run`. A failed patch skips the command; a failed command leaves the successful patch in place. The combined result shows the model the patch outcome followed by the command's plain-text view. If validation moves to the background, wait for its actual terminal result before treating it as passed. Do not reapply the patch just to rerun validation.

Each resolved path may belong to only one file section, including move sources and destinations. Aliases such as `a.txt` and `./a.txt` count as the same path. Combine multiple edits to one file in a single `Update File` section with several `@@` chunks. Conflicting paths fail validation before any writes or `then_run`, including in `dry_run` mode.

## Logs and lifetime

Normal command output and its saved full log are redacted before being returned or persisted as a tool log. Background process records and merged stdout/stderr logs can contain raw text on disk; redaction happens when the tool returns them. Keep secrets out of arguments and output, and review logs before sharing them.

Live background logs can grow. Finished logs are compacted to an 8 MiB tail, and startup maintenance expires terminal records and logs after 30 days when no completion remains undelivered. A returned byte offset may therefore precede retained output; use the returned offsets rather than assuming the whole log remains available. Foreground commands also buffer output in memory while running, so unusually noisy commands can consume substantial memory.

The default `lifecycle=session` participates in runtime cleanup; `managed` skips that session-lifecycle cleanup. Neither is an operating-system service manager. Restarting Wuu cannot restore a lost PTY or stdin handle, and a recovered process may lack a reliable exit code. Stop unneeded processes explicitly instead of relying on a conversation tab's lifetime. Before stopping, Wuu checks process identity to avoid targeting a reused PID, then requests a graceful stop before escalating to the process tree.

Desktop process controls and the model's tools use the same manager. App-server process methods check thread ownership; clients cannot operate on another thread's process simply by guessing its ID. For embedding details, see the [app-server protocol](../integrations/app-server-protocol.md).
