# Automations

Automations send a task prompt on a schedule. Use them for a recurring project check, a summary, or a one-time follow-up. The Automation plugin must be enabled and Wuu must be running on an awake machine; this is not a hosted scheduler.

## Create a task

1. Open **Automations** and choose the workspace. This selects the task's project without changing your open conversation.
2. Choose **New automation**, or start from a suggestion. Enter a name and clear instructions.
3. Under **Runs in**, choose **New chat each run** or an existing chat.
4. Set a daily, weekday, weekly, or custom schedule. **More settings** contains timezone, **Run once**, and **Isolated run**.
5. Choose **Create**. Later edits require **Save changes**.

Include limits in the prompt, not just the desired action:

```text
Summarize changes in this project's main branch since yesterday. Identify failed
checks if their results are available. Do not edit files or publish anything.
```

## Choose where it runs

A new-chat task creates a visible conversation for each run. Runs can overlap. Enable **Isolated run** for a separate Git worktree based on the project's current `HEAD`; uncommitted changes are not copied into it.

An existing-chat task continues that conversation's context and workspace. If it is busy, the message queues behind its current work. This mode cannot create a new worktree. The agent's `cron` tool calls these modes `new_thread` and `thread_heartbeat`.

## Schedule and missed triggers

Custom schedules use five-field cron expressions. `0 9 * * 1-5` means 09:00 on weekdays in the selected timezone. Use IANA timezone names such as `Asia/Shanghai`; the desktop initially selects the system timezone.

The weekday field accepts `0`–`7`: `0` and `7` both mean Sunday, and `1`–`6` mean Monday through Saturday. Lists and ranges keep their numeric step selection before interpreting Sunday: `6,7` and `6-7` select the weekend, `5-7/2` selects Friday and Sunday, but `6-7/2` selects only Saturday. Including both `0` and `7` does not create an extra Sunday occurrence.

Older versions could save a next-run time that skipped a selected Sunday. Restarting Wuu preserves that stored time. After upgrading, save an affected task again to recalculate it immediately; otherwise a recurring task recalculates when its stored deadline is dispatched. Missed Sundays are not replayed.

The plugin checks due tasks about every 15 seconds, so a schedule is not a promise of an exact start second. After downtime, an overdue one-shot task is dispatched once. An overdue recurring task is dispatched once and then scheduled forward from the current time, rather than replaying every missed occurrence.

A one-shot task leaves the schedule when dispatched, even if execution later fails. Check its run record for the outcome; it is not automatically retried as a new one-shot task.

## Manage tasks and results

Pause and resume control future triggers. Delete removes the schedule, not conversations it already created, and does not stop work already running. Stop that work from its conversation. Closing the editor or selecting another task discards unsaved edits.

The detail panel shows recent runs and their errors. The Completed filter shows retained snapshots of successful one-shot tasks; a successful recurring run does not complete its schedule. A workspace retains up to 100 tasks and 500 run records.

If a run is missing, check that Wuu was awake and running, the plugin was enabled, the task was active, and the selected workspace and target chat still exist. Then check its timezone, next run, and errors. Provider access, quotas, network failures, and permissions can all prevent execution.

For CI or a scheduler outside Wuu, use [`wuu exec`](exec.md).
