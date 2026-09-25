# Completing your first task

Use a small project you can restore, such as a disposable copy of a Git repository. This walkthrough first asks the agent to explain the project, then makes one change and checks it.

## Choose the folder

In the sidebar, choose **Add workspace → Use existing folder** and select the project. To start without existing files, choose **Create blank project** instead. Open a conversation in that workspace and check the directory before sending.

Adding the folder does not copy it. The agent works on the real files, so save or commit any existing work you need to preserve.

## Ask for a read-only explanation

For the built-in Wuu engine, choose **Read only** in the permission menu beside the composer. Then send:

```text
Read this workspace without changing files. Explain what it is for, its main
directories, and how it can be verified.
```

Compare the answer with the files you expected it to read. If the directory is wrong, stop the task and select the correct workspace before continuing.

Read-only mode blocks file edits and confines command writes with a filesystem sandbox. It does not isolate network access or inherited environment variables. If a required sandbox is unavailable, the command fails rather than running without confinement. See [permission modes](../reference/permissions.md), including the differences for external engines.

## Make a small change

Choose **Standard** when you are ready to permit edits. Give the agent a bounded task with an observable result, for example:

```text
Fix the failing test for empty search results. Keep the change limited to that bug.
Run the relevant tests and report the command and result. Do not commit yet.
```

Name a real problem in your project rather than using the example unchanged. For a larger feature, request an investigation and plan first. You can stop a running task and clarify the scope, but stopping does not undo changes already made.

## Review the result

Use the composer shortcuts to inspect the work:

| Command | Purpose |
|---|---|
| `/files` | Browse and open project files |
| `/diff` | Review the current Git changes |
| `/terminal` | Run your own checks in a workspace shell |

Check that the diff contains only the intended change and that the reported test command actually succeeded. If a check could not run, decide whether to fix the environment or verify another way before committing or publishing.

Commands you enter in the terminal use your own OS permissions; the agent's read-only setting does not restrict that terminal. Do not paste a command there simply to bypass an agent permission error.

Reply in the same conversation to request a correction. You can reopen it from the sidebar later, or [fork the conversation](../desktop/conversations.md) to try a different approach. See [workspace tools](../desktop/workspace-tools.md) for file previews and delivered artifacts.

## Sending and stopping

The processing timer starts when you send and includes waiting for the local
request to be accepted. It stays continuous across updates and conversation
switches, and freezes when the answer is ready or execution ends. Reopened history
without a local send record uses the server's execution time.

You can queue more messages while a new conversation is being created. They are
sent in order after its first request is accepted. If creation fails or is
cancelled, the inputs are kept as drafts; a notice opens any recovery draft.

Stop shows a progress icon in the same button until execution has ended. Messages
still waiting to be submitted are retained without automatically starting. If
Wuu cannot confirm the stop, the button offers a retry; it does not claim the task
has stopped. Your current draft stays in the composer.
