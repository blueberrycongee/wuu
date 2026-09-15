# Automations

Automations let Wuu execute a task brief on a schedule in a workspace you choose. They
suit periodic checks, recurring summaries, and one-shot tasks you want to run later —
not a cloud cron service running independently.

## Create an automation

1. Install and enable the first-party Automation plugin, then open **Automations** from
   the navigation entry contributed by the plugin.
2. Use the **Workspace** selector at the top of the page to choose the project workspace
   where the task should run. This does not switch the conversation currently open in Desktop.
3. Choose **Create** and fill in the name and task content in the detail panel, or start from a **Suggestion**.
4. Choose Daily, Weekdays, or Weekly, then set the time and timezone. Use **Custom**
   for a five-field Cron expression. Enable **Once at the next scheduled time** for a one-shot task.
5. Choose **Create** to save. The request is routed to the selected workspace's plugin
   runtime, and the task records its workspace ID and root. Task state, next execution time, and run records
   are stored in the plugin's workspace storage.

The task content is the prompt handed to the agent on every trigger. Write the scope,
expected result, and verification explicitly, for example:

```text
Check the TODOs added in this workspace over the past day. Summarize by file and only
report items that are still unresolved; do not modify files. If there are no new TODOs,
say there is no change.
```

## Set the time

Time controls use wall-clock time in the task timezone. Custom schedules accept five-field Cron expressions, for example `0 9 * * 1-5`
means 9:00 on weekdays. The timezone uses an IANA name, such as `Asia/Shanghai`; the
system timezone is used by default at creation. The plugin checks for due tasks about
every 15 seconds, so a trigger may be delayed by up to one check cycle; no extra
random jitter is added.

## Choose the session mode

### New session each time

Desktop-created tasks use `new_thread`, which suits independent checks. Every trigger
creates a new user-visible session in the bound workspace through the public
`host.session.create/send`, and the results appear in that workspace's normal session
list. The app server verifies the workspace ID and root before creating the session.

A task can also govern its own execution workspace. Create it with
`workspace: "worktree"` (the Desktop form exposes this as "Run in an isolated git
worktree") and every trigger runs the new session in a dedicated git worktree based
on the workspace's current HEAD, so scheduled edits never touch the live project
directory. The worktree persists with its session for review and is reclaimed with
the session or workspace state cleanup. The run record and the trigger context block
carry the effective execution root. Worktree isolation applies to `new_thread` tasks
only; a `thread_heartbeat` target session already has its own workspace binding.

### Continue a specific session

The plugin's `cron` tool can also create `thread_heartbeat` tasks that deliver the
generated ordinary query to an existing session. This suits following up in the same
context. The Desktop **Runs in** picker searches existing workspace chats. It has two
limitations:

- the session must still exist and be loadable in the current Wuu data;
- if that session is already running another turn, the automation message queues
  instead of writing into the same session in parallel.

## Manage tasks

The workspace selector controls which workspace's tasks are displayed. Search titles and
prompts, or filter by All, Active, Paused, and Completed. Selecting a task opens the
shared creation and editing panel. Edit the name, prompt, schedule, timezone, target
chat, and isolation, then choose **Save changes**. Failed saves retain the draft;
closing or switching tasks discards unsaved changes. Pause and Resume take effect
immediately; Delete is in the more-actions menu. Narrow windows show the detail panel
on its own; closing it returns to the list.

Completed contains read-only snapshots of successful one-shot tasks from retained run
records. A successful recurring run does not complete its task. The detail panel shows
the five most recent runs with status, time, and any failure details.

Each workspace stores at most 100 automation tasks and the latest 500 run records.
Deleting a task is irreversible, but it does not delete the sessions the task created
or woke.

The current desktop page has no "run now" button. `New session` results appear in the normal session list;
`Continue a specific session` results are written into the target session.

## Run conditions and lifecycle

Automations are maintained by the Automation plugin runtime's own Timer, not by a Wuu
core scheduler. Tasks are only checked while the Wuu process hosting that plugin
generation is running; when the plugin is disabled, upgraded, or closed, the host
waits for the Timer loop to exit. When the computer sleeps or Wuu quits completely,
tasks do not execute in the background.

- a missed one-shot task is executed once as a catch-up when the scheduler next
  starts;
- recurring tasks do not catch up missed time points one by one; multiple missed runs
  merge into the next due execution;
- a one-shot task is removed from the schedule after it runs.

One scheduled occurrence uses stable create and send request IDs. If two Wuu processes
briefly load the same workspace, the app server converges duplicate requests on the same
session and turn instead of executing the task twice.

Each due run in new-session mode creates an independent session; `thread_heartbeat`
uses the core's ordinary Turn queueing and execution lease, so runs queue when the
target session is busy. The plugin currently does not implement an additional
"skip overlapping runs" product policy.

Tasks use the model, tools, and permission configuration at execution time. Even if
the schedule itself is saved, permission limits, network state, model quotas, or a
missing workspace can still make a run fail.

## Troubleshooting

### It did not run at the scheduled time

Confirm that the Wuu core was running at that time, the task is not paused, the
workspace path still exists, and check the timezone and the next execution time in the
list. Due tasks are discovered by a fixed-period background check, so the start may
wait until the next check.

### The task disappeared

A one-shot task is removed after running. A recurring task only disappears when a
user/agent deletes it or the plugin state is corrupted.

### Continuing a session fails

Check that you filled in a session ID, not a title, and confirm the session was not
deleted. If the task does not depend on historical context, switching to `New session`
is usually more robust.

### Need to trigger from CI or a script

Desktop scheduled tasks suit local scheduled execution. When driven by an external
scheduler, CI, or another agent, use [`wuu exec`](exec.md).
