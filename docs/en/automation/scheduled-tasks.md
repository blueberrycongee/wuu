# Automations

Automations run a task prompt on a schedule in a chosen workspace. Use them for
periodic checks, summaries, or a one-time follow-up. Wuu must be running on an awake
device, with the Automation plugin enabled.

## Create an automation

1. Open **Automations**. If the entry is missing, check that the bundled Automation
   plugin is available and enabled.
2. Choose **Workspace** at the top. This selects where tasks run without switching
   your open conversation.
3. Choose **New automation**, fill in the name and instructions, or start from
   **Suggestions**.
4. In **Runs in**, choose **New chat each run** or search for an existing chat.
5. Choose Daily, Weekdays, Weekly, or **Custom**. **More settings** contains the
   timezone, **Run once**, and **Isolated run** options.
6. Choose **Create** to save.

State the scope, expected result, and restrictions in the prompt, for example:

```text
Check TODOs added in this workspace over the past day. Group unresolved items by
file. Do not modify files. If there are no new TODOs, say so.
```

## Time and execution

Schedules use the task's timezone, initially your system timezone. Custom schedules
accept five-field Cron expressions: `0 9 * * 1-5` means 9:00 on weekdays. Timezones
use IANA names such as `Asia/Shanghai`. Due tasks are checked about every 15 seconds;
execution is not guaranteed to start at an exact second.

**New chat each run** creates a visible conversation in the selected workspace.
Enable **Isolated run** to give each run a Git worktree based on the project's current
`HEAD`. Uncommitted changes are not included. Review results in the created conversation.

Choosing an existing chat continues its context. If it is busy, the scheduled message
queues behind its current work. That chat keeps its own workspace, so worktree isolation
is only available for new chats. The Agent's `cron` tool calls these modes `new_thread`
and `thread_heartbeat`.

New-chat runs can overlap; they do not automatically wait for the previous run to
finish. Use worktree isolation for concurrent edits or choose an existing chat to queue
follow-ups in one place.

## Manage tasks and results

Choose the workspace, then search or filter by All, Active, Paused, or Completed.
Select a task to edit it and choose **Save changes**. Closing the editor or switching
tasks discards unsaved edits. Use **Pause** or **Resume** to control future triggers;
**Delete** is in the more-actions menu. Deleting a task does not delete the conversations
it created or stop work already running in them. Stop that work in its conversation.

The detail panel shows the five most recent runs, including failures. **Completed**
contains read-only snapshots of successful one-shot tasks whose run records are still
retained. A successful recurring run does not complete its task. Each workspace keeps
up to 100 tasks and 500 recent run records. There is no **Run now** button.

## When a run is missing or fails

Check that the Automation plugin was enabled, Wuu was running, the device was awake,
the task was not paused, and the workspace still exists. Check the timezone and next
run time. Model availability, quotas, network access, and permissions can also cause
a run to fail; inspect its error and conversation.

After Wuu returns, a missed one-shot task runs once. Missed recurring triggers are
combined rather than replayed one by one. One-shot tasks leave the schedule after
running; look in recent runs for their outcome. For a missing target chat, select an
existing one or switch to **New chat each run**.

For CI or an external scheduler, use [`wuu exec`](exec.md).
