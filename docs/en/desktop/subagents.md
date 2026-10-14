# Subagents

The Subagent plugin lets an agent delegate a bounded task to another model context and bring the result back to the parent conversation. Enable the plugin before requesting this workflow.

## Give the delegated task a boundary

Subagents are useful for independent investigations, reviews, or changes with clear file ownership. You can ask:

```text
Investigate the two failing import paths in parallel. Keep both investigations
read-only, return the relevant files and evidence, and combine the findings here.
```

The parent decides whether to delegate and remains responsible for checking and combining the results. Splitting a small task, or letting several agents edit the same files, can add more coordination work than it saves. Each child has its own model requests and token usage.

## Context is separate from file isolation

| Option | Effect |
|---|---|
| Fresh context | Starts from the task brief rather than inheriting the parent conversation; this is the default |
| Forked context | Inherits the parent conversation as background for a new task |
| Worktree isolation | Creates a separate Git working directory; the current plugin also selects forked context for this option |

By default, a child shares the workspace. A fresh context alone does not isolate files. Use clear file ownership or worktree isolation for parallel edits, and review how changes will be integrated before starting.

## Wait for results or continue in parallel

The plugin's `spawn_agent` tool defaults to background execution. The parent can work on something else, and a completion notification brings back the child's result. Subtask status appears above the composer when the plugin supplies that UI.

When the next step needs the result immediately, the parent can set `run_in_background: false`. The tool then waits and returns the terminal state, output, and error directly. After ten minutes, an unfinished foreground wait becomes background work; it does not cancel the child.

## Correct or stop a subtask

Ask the parent to send a correction or cancel a named subtask. The plugin uses `send_message` for follow-up input and `close_agent` for cancellation. Stopping the parent's current reply is not a substitute for explicitly cancelling a child.

The plugin saves a dispatch before starting or updating a child. Replaying the same
tool call reuses its session and input identity. After a local process restart,
recovery waits for the host session service before retrying unconfirmed dispatches
and host-confirmed retryable queued inputs;
an explicit cancellation is not. A failed follow-up keeps the earlier result
route intact. If its acceptance is still uncertain, retry that message before
sending a different one to the same child.

Dispatch changes are serialized across overlapping plugin generations. Upgrades
copy active task correlations into a separate ledger, so a retained foreground
call cannot overwrite new recovery records. The ledger keeps up to 128 recent
settled receipts within a 1 MiB storage budget; completed task briefs are removed
while replay identities remain. Pending or live dispatches are never evicted to
admit another task. If storage is full, shorten the new brief or finish/cancel
pending work before retrying.

Child execution remains subject to the host's session and permission rules. A returned result is still evidence to review, not proof of correctness. Check changed files and tests before integrating it, and do not assume work completed if the runtime was interrupted.

A subagent is a model session, unlike a [background command](../reference/agent-command-system.md), which is a local process, or a [scheduled task](../automation/scheduled-tasks.md), which dispatches work later.
