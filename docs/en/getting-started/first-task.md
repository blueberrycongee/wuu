# Completing your first task

Choose a small project without sensitive files that you can restore. Read it first,
make a change, then check the result.

## Read the project first

In the **Workspaces** area of the sidebar, choose **Add workspace → Use existing
folder**. For the first attempt, use:

- a code repository you can re-clone;
- a test folder prepared for this purpose;
- or a directory created with **New blank project**.

The workspace is the primary boundary for files and commands. Sessions are also
organized by workspace; if you see the wrong files or stale sessions, check the
currently selected workspace first.

## Let wuu read first

Before sending, you can use the permission button next to the input box to select
**Read only**. The permission mode only controls Wuu's tool boundary; it does not put
the project inside an operating-system sandbox. Keep **Standard** for ordinary
modification tasks. **Approve for me** is a Standard-mode option that reviews
high-risk tool calls before they run; it does not raise the workspace boundary.

Start a new conversation in the target workspace and send a read-only task first:
In the sidebar, choose **Add workspace → Use existing folder**, or use **Create blank
project** to prepare a test folder. Check the active workspace and start a conversation
there. Before sending, select **Read only** in the permission menu next to the input:

```text
Read this workspace without changing files. Explain what it is for, its main
directories, and how it can be verified.
```

Check the directories and test commands in the answer. If it read the wrong project,
stop the task and select the correct workspace.

Read-only mode refuses file changes; command tools also apply a filesystem sandbox.
Network access and inherited environment variables still matter. Use a separate
isolated environment for untrusted code; see [permission modes](../reference/permissions.md).

## Make a small change

Switch to **Standard** mode and describe the result, scope, and verification you want:

```text
Fix the currently failing tests. Only change code related to the failure; do not do
unrelated refactoring. When done, run the relevant tests and tell me which files you
changed and what the test results were.
```

For larger features, ask for an investigation and plan before implementation.
The conversation shows reads, edits, and command execution. If the task goes off
track, stop it and add constraints before continuing.

## Check and continue

Use these commands in the input box to open inspection tools:

| Command | Purpose |
|---|---|
| `/files` | Browse and open project files |
| `/diff` | View Git changes and check scope, unrelated edits, and sensitive information |
| `/terminal` | Open a workspace shell to run checks yourself |

Compare the final answer with the actual output. Confirm tests or builds ran and
passed before committing or publishing. Reply to request corrections, or reopen
the same conversation from the sidebar later.

The input accepts images, PDFs, and MP4, WebM, and MOV videos. Processing support
depends on your chosen provider and model. Attachments do not automatically become
project files; specify a target path if you want them saved in the workspace.

For more, see [files, changes, and terminals](../desktop/workspace-tools.md) and
[conversations](../desktop/conversations.md). To run and resume tasks from the CLI,
see the [`wuu exec` guide](../automation/exec.md).
