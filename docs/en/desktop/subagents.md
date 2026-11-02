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

## Retry and delivery guarantees

**First upgrade:** stop the old Subagent runtime and its in-flight tool calls before activating this version, or restart Wuu. Older runtimes write the index without compare-and-exchange; mixing those writers with this version is not safe. Overlapping generations are supported once all writers use the new protocol.

A task reserves its correlation record before creating a child. Overlapping plugin generations update that record with compare-and-exchange, so one task cannot overwrite another task's reservation. A retry with the same session, tool, call ID, and supplied turn/step identity reuses the creation or follow-up request; conflicting input is rejected while its receipt is retained. A follow-up keeps the existing child session and either steers its active turn or starts a new one.

After an uncertain create response, recovery retries the same creation identity. Cancellation instead discovers an already-created owned child by its creation identity; it never creates a replacement just to cancel it. Unresolved admission and cancellation records continue to occupy their reserved slots. The index retains up to 128 task records and each retained task accepts up to 128 distinct follow-up identities and retains up to 128 feedback correlations; settled records can be reclaimed for new tasks.

A child finishing and its feedback being delivered are separate events. Queued feedback remains pending across restarts. The plugin inspects its receipt and retries feedback discarded retryably by the host, using the same request identity. A started delivery or a non-retryable terminal receipt settles the feedback; neither proves the model understood or acted on it. The parent can give the same child its next step while earlier steered feedback still awaits acknowledgment. Earlier feedback keeps its original target and reply anchor; new work is refused before admission if retained feedback exhausts capacity. Result text stays in the host terminal receipt rather than being copied into the task index. Nested feedback reconstructs the same input from that receipt while admission is uncertain.

Cancellation suppresses feedback not yet admitted for delivery and targets the child's accepted turn or queue entry. It cannot retract feedback already in flight or consumed by the parent. A foreground wait timing out still does not cancel the child. `close_agent` returns `cancelled: false, cancel_pending: true` while physical cancellation is unconfirmed. If newer follow-up work wins before cancellation is recorded, close returns `cancelled: false, superseded: true` and leaves that newer request untouched. Recovery re-inspects the same request after a failed or uncertain cancellation, including a queued request that starts before cancellation reaches it.

Child execution remains subject to the host's session and permission rules. A returned result is still evidence to review, not proof of correctness. Check changed files and tests before integrating it, and do not assume work completed if the runtime was interrupted.

A subagent is a model session, unlike a [background command](../reference/agent-command-system.md), which is a local process, or a [scheduled task](../automation/scheduled-tasks.md), which dispatches work later.
