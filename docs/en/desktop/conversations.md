# Conversations and branches

Conversations save messages and tool activity. Reopen the same conversation to
continue work, or fork from an earlier message to try another approach.

## Start and continue conversations

Select a workspace, then start a conversation. Later, reopen it from the sidebar
and describe the next step. Use its menu to rename, pin, or archive it.

Archiving only hides a conversation from the default list; you can filter and restore
it in **Settings → Archive**. Deleting removes the saved history and the conversation
artifacts wuu can locate, and should only be used when you are sure you no longer need
them.

Collaboration can delegate work to an ordinary Harness conversation. Open that same
conversation from Collaboration to review progress or continue chatting. Viewing it
does not take control; sending a message takes control and pauses Collaboration's
automatic instructions and follow-up for that conversation. See [Collaboration](collaboration.md).

## While a task is running

While a task runs, you can add messages in the input box:

- **Enter:** add information or adjust the current task immediately.
- **Tab:** queue the message for after the current turn ends.
- **Shift + Enter: new line.**

When the input is empty or no task is running, Tab moves focus normally. With the
`/` menu open, Enter and Tab select a command.

Click Stop to interrupt. Neither sending more instructions nor stopping a task undoes
commands or file changes. Check the [current diff](workspace-tools.md#review) before
continuing, and check background commands separately.

## Fork from an earlier message

Choose **Fork** on a historical message to create a new conversation from that point:

- **Fork locally:** the new conversation uses the same folder, so both conversations share file changes.
- **Fork to a Git worktree:** create a separate directory from the current Git `HEAD`, without uncommitted changes from the original folder.

A fork copies conversation history up to the selected message; it does not restore
files to their state at that message. To include current changes in a worktree,
review and commit them first. When worktrees are unavailable, the option shows why.

## Side chat

Enter `/side` to ask about the current task, explain output, or compare approaches
without adding content to the main conversation. Start or fork a conversation for
work you want to continue long term. If side chat is unsupported, the entry shows why.

## Sessions in the CLI

```bash
wuu session list
wuu session show --last
wuu session search "keyword"
wuu exec --continue "continue the most recent session"
wuu exec resume THREAD_ID "continue this task"
```

Use `wuu exec --ephemeral` when an automated task should not save a session. See [the
`wuu exec` guide](../automation/exec.md) for the full options.
