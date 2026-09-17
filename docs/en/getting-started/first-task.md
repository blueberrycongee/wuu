# Completing your first task

On first use, confirm three basics: wuu entered the right workspace, the model service
can call tools, and you can inspect the final result.

## Choose a safe workspace

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

```text
Read this workspace without changing files. Explain what it is for, its main
directories, and how it can be verified.
```

Check that the directories, technology stack, and verification commands in the answer
match your expectations. If they do not, stop the current task and confirm the
workspace; do not let the agent keep making changes in the wrong directory.

## Then hand over a small task

A task that is easy to get right contains four parts:

1. **Result:** what you ultimately want to get.
2. **Scope:** which page, module, or files should be touched.
3. **Constraints:** which behavior, data, or interfaces must not change.
4. **Verification:** what check should be run when it is done.

For example:

```text
Fix the currently failing tests. Only change code related to the failure; do not do
unrelated refactoring. When done, run the relevant tests and tell me which files you
changed and what the test results were.
```

For a larger feature, you can first ask wuu to investigate and produce a plan, confirm
the direction, and only then let it implement.

## Watch the execution

wuu shows tool activity in the message stream: file reads, searches, edits, and
command executions. Tool activity tells you about progress, but it does not replace a
final check.

If the task is going the wrong way, stop the current reply immediately and add
constraints. When you keep sending messages, wuu preserves the current conversation
context.

## Check the result

When the task finishes, at least verify:

- the final answer clearly states what was done and the verification results;
- use `/files` to browse or open the project files you need to inspect;
- in `/diff`, confirm the changed-file set and look for unrelated changes, debug code,
  or sensitive information;
- the tests, build, or other verification actually ran and passed.

When you want to run commands yourself, use `/terminal` to open a shell in the current
workspace. For important changes, a human should still review the diff before commit
or release.

## Add attachments

You can attach images or PDFs in the desktop input box and explain in the message how
you want them used. No other attachment types are currently accepted:

```text
Using this screenshot as a reference, check the current UI and only fix obvious
inconsistencies.
```

An attachment only gives the current task read access to that content; it does not add
the original file to the workspace automatically. If you want it kept as a project
artifact, ask explicitly to write it to a target path in the workspace.

## Continue the same conversation

wuu saves sessions. Reopen the conversation in the sidebar to continue, for example:

```text
The implementation direction was right, but do not change the existing API. Adjust it
and rerun the tests.
```

In the CLI you can list and resume sessions:

```bash
wuu session list
wuu session show --last
wuu exec resume --last "continue and verify"
```

Use `--ephemeral` when an automated run should not create a persistent session.
Archiving only hides a session; deleting removes the saved history and should be used
carefully.

## Next steps

- Read [the configuration model](../reference/configuration.md) to understand the
  boundary between workspace rules and user configuration.
- Read [the `wuu exec` guide](../automation/exec.md) to use the same workflow in
  scripts and CI.
- Read [the security model](../reference/security-model.md) before working with
  untrusted repositories or sensitive data.
