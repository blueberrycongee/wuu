# Conversations and forks

A conversation keeps messages, tool activity, and results together. Continue the same conversation for follow-up work; create a new one for an unrelated task or fork an earlier point to explore another approach.

## Start and organize conversations

Select a workspace, then start a conversation. Click the title in the conversation title bar to rename it. The name is saved immediately for an existing conversation, and a new conversation keeps it when the first message creates the session. The sidebar menu still provides rename, pin, and archive. An archived conversation is hidden from the normal list and can be restored through **Settings → Archive**.

Delete is permanent: it removes saved history and cleans up associated artifacts and any fork worktree still bound to the conversation. Preserve outputs and changes you need before deleting. Wuu rejects deletion while the conversation, its side chat, or its child agents are active.

After the first send, the workspace sidebar immediately shows the pending conversation. You can switch away and return while it is being created. Stop cancels this wait and retains your input; a late creation response does not send the cancelled message or keep an unused empty conversation. Starting another conversation uses a separate draft.

## While a task is running

The composer supports these keyboard actions:

| Key | Action |
|---|---|
| Enter | Send the draft; when the engine supports steering a running task, add it to that task |
| Command + Enter (Ctrl + Enter on Windows and Linux) | Queue a non-empty draft for after the current turn, when queuing is available |
| Shift + Enter | Insert a new line |
| Escape | Interrupt a running task when the composer can do so |

The `/` menu handles Enter and Tab as command selection while it is open. Tab otherwise moves focus normally. The send control reflects whether the current engine can steer or only queue a message.

Once the final answer is ready, Enter or the send button starts a normal follow-up, including in split view. Background cleanup and refreshing an already loaded conversation do not block sending. A submission keeps its original conversation and workspace even if you switch away while attachments are preparing.

If sending fails, Wuu restores the input when that composer is still empty. If you have switched away or typed a newer draft, the failed input stays in the original conversation instead. If there is no conversation because its creation failed, the error notice lets you open the retained input as a separate draft or discard it. Failed input is not retried automatically.

Use Stop to interrupt a task. Stopping does not undo commands or file edits, and separately managed background work may need its own stop action. Review the [current diff and command results](workspace-tools.md#review) before continuing.

## Fork from an earlier message

Choose **Fork** on a historical message, then select where the new conversation should work:

| Choice | Files used by the new conversation |
|---|---|
| Fork locally | The same directory as the original conversation |
| Fork to a Git worktree | A separate directory created from the source repository's current `HEAD` |

A fork copies history through the selected message. It does **not** restore files to that moment. A worktree starts from committed content and does not copy uncommitted changes; review and commit any changes you want it to inherit before creating it. The dialog explains when worktree creation is unavailable.

## Ask a side question

Use `/side` for questions about progress, output, or alternatives without adding those messages to the main conversation. Availability depends on the engine. For a separate task you want to develop over time, start another conversation or fork instead.

## Continue delegated work yourself

[Collaboration](collaboration.md) can open ordinary work conversations. Viewing one does not change its control state. Sending your own message takes control and pauses Collaboration's automatic instructions and follow-up for that session.

## Use saved sessions from the CLI

```bash
wuu session list
wuu session show --last
wuu session search "keyword"
wuu exec resume THREAD_ID "Continue this task"
```

`wuu exec --continue` resumes the latest session; `--ephemeral` runs without saving one. See [`wuu exec`](../automation/exec.md) for session selection and script options.
