# Agent command system

The built-in Wuu engine starts every command with `bash`: tests, builds, lint, git, scripts, and, with `run_in_background`, servers and watchers. `process` inspects and controls commands that are already running in the background. Both run on the host machine in the session's workspace and share one process manager. External engines have their own command implementations; this page describes Wuu's tools.

## Run a command

`bash` takes `command`, `timeout_seconds`, `cwd`, `run_in_background`, `purpose`, and `scope`:

```json
{
  "command": "go test ./internal/process",
  "timeout_seconds": 300,
  "purpose": "Check process lifecycle behavior",
  "scope": "targeted"
}
```

The model receives the command's output as a terminal would show it: stdout, then stderr. A successful run adds nothing else. The model sees extra lines only when they change the next step: `Exit code N` for a failure, the full-log path when a stream was cut, a timeout hand-off, or a sandbox denial. Each stream keeps its head and tail, so a test runner's summary survives even when the middle is cut. Terminal color codes and progress-bar redraws are removed from this view. Recognized test, build, lint, and type-check commands also record verification metadata; the model sees the failure summary only when the output was cut, plus a warning once repeated failures at the same workspace revision will block a rerun. `scope` describes the intended coverage (`targeted`, `affected`, or `full`) and does not change which tests run. `purpose` is kept in the audit log only.

Clients and durable records keep the full JSON envelope: exit code, duration, both excerpts, byte counts, classification, workspace revision, the full-log reference and hash, and verification details. The desktop terminal panel renders from that envelope.

Commands must be non-interactive: do not depend on an editor, pager, or terminal prompt. The default wait is 300 seconds, with a range of 1–3600. On timeout, Wuu normally transfers the running process to its background manager and reports the process ID; the result says the command is still running and its completion starts a new turn. This is **not** a successful verification or proof that the command stopped. If transfer is unavailable, Wuu stops the process. Cancelling the calling turn also stops a foreground command rather than promoting it.

## Background and interactive work

Start a server, watcher, or interactive command with `run_in_background` rather than appending `&`:

```json
{
  "command": "npm run dev",
  "run_in_background": true
}
```

The call returns the process ID immediately. Background commands run in a pseudo-terminal so the desktop can take them over; macOS and Linux support PTYs, while Windows falls back to pipes. A natural exit starts a new turn in the owning conversation, and `wuu exec` waits for that continuation. For a long-lived service whose exit should not resume or hold open the task, set `completion_mode=detached` with `process action=update`.

`process` works on running processes:

| Action | Use |
| --- | --- |
| `read` | Read output using `process_id`; pass the returned next offset as `offset_bytes` for incremental reads |
| `write` | Send `input` to a process with a live input handle; include a newline when needed |
| `stop` | Stop the process tree |
| `list` | Inspect the session's managed processes |
| `update` | Change `completion_mode` (`resume` or `detached`) or `recheck_minutes` (`0` cancels) |

Reads return up to 32 KiB by default. Without `wait_ms`, a read is a snapshot. With it, the call waits for new output or exit, up to 300,000 ms. Continuously arriving output is paced rather than returning on every byte. Like `bash`, `process` results reach the model as short plain text, while clients keep the JSON envelope.

Use `recheck_minutes` from 1 to 1440 for progress wake-ups on long or quiet tasks. Completion cancels the schedule. Do not chain waits just to keep a turn open: use a foreground `bash` run for an immediate dependency, or let completion and scheduled wake-ups resume the conversation. Completion and recheck notifications are also plain text: the outcome, the output tail, and how to read earlier output.

Sessions recorded before this change show background actions under `bash` (`start_background`, `read_background`, and so on). The desktop still renders those items; a model that repeats them gets an error naming the replacement.

## Directory, environment, and permissions

`cwd` defaults to the workspace root and must resolve to an existing, permitted directory. Worktree-bound workers execute in their assigned checkout. Each `bash` call starts a new shell; `cd`, aliases, and temporary environment changes do not persist between calls.

Wuu resolves Bash through its shell launcher, including Git Bash on Windows. Foreground and background commands use the shared environment preparation: inherited build-time `GOROOT` is removed and non-interactive defaults are applied. PTY startup restores terminal-oriented settings where needed.

Command classification helps with permission checks and scheduling, but is not the filesystem sandbox. In confined modes, foreground and background commands use the configured filesystem process sandbox. A missing enforcement provider causes launch failure rather than silently running unconfined. The built-in provider is currently macOS-only; network isolation is outside this contract. See [permissions](permissions.md) for modes and platform limits.

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
