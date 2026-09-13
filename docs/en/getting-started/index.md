# Quick start

For your first task, use a small project without sensitive files. You'll need an Apple silicon Mac and access to a model provider.

[Install wuu](installation.md) from GitHub Releases. The desktop preview is unsigned; the installation guide explains what to do if macOS blocks it.

Open Settings and [connect a model provider](model-services.md), entering its API key if required. Then add your local project folder as a workspace and start a conversation.

wuu can change files and run commands. Check the selected workspace and [permission mode](../reference/permissions.md) before sending a task. Prompts and relevant file contents may be sent to your model provider; see the [security model](../reference/security-model.md).

Start by asking the agent to read the project:

```text
Read this project without changing any files. Explain what it does and how to run its tests.
```

Then choose a small change with a clear way to check it. Tell the agent what should happen and ask it to run the relevant tests. The [first-task guide](first-task.md) has more examples.

Review the changed files and command output before accepting the result. You can continue in the same conversation to ask for corrections or pick up the work later.

## Use the CLI

The desktop app does not need a separate CLI. To work from a terminal or script, follow the [installation guide](installation.md), then run this from your project folder:

```bash
wuu exec "read this project and explain how to run its tests"
```

See the [`wuu exec` guide](../automation/exec.md) for more options. If something goes wrong, start with [troubleshooting](../help/troubleshooting.md).
