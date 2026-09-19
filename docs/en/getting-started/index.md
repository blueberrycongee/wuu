# Quick start

You need an Apple silicon Mac for the desktop preview and a model connection. Choose a small local project without sensitive files for your first task.

## Set up the app

1. [Install Wuu](installation.md) and open the copy in `/Applications`. The preview is self-signed and not notarized; the installation guide explains how to open an official download that macOS blocks.
2. Choose your optional plugins in the first-run setup. The recommended selection is TODO and Automation; you can change it later in the plugin settings.
3. Select an agent engine. Wuu is built in; Codex and Claude Code depend on a working local CLI installation.
4. For the Wuu engine, [configure a model service](model-services.md) or reuse a supported local subscription login.
5. Add your project folder as a workspace and start a conversation there.

You can also change model connections later in **Settings → Model providers**. Check the model shown in the composer before sending: the conversation's selection can differ from the saved defaults.

## Read, change, and check

Select **Read only** in the permission menu and ask:

```text
Read this project without changing any files. Explain what it does and how to run its tests.
```

Once you recognize the project and its test commands, switch to **Standard** for a small edit. Describe the result you want and ask the agent to run the relevant checks. The [first-task guide](first-task.md) walks through this step.

Open `/diff` to review Git changes and read the command results before accepting the work. A reply saying “done” does not establish that a test passed. Continue in the same conversation for corrections or follow-up work.

Prompts, relevant file contents, and tool results can be sent to the selected model service. Read [permissions](../reference/permissions.md) and the [security model](../reference/security-model.md) before using private projects.

## Use the CLI

The desktop does not require a separate CLI. To work from a terminal, [install it](installation.md#install-the-cli), [configure a provider](model-services.md#configure-the-cli), and run this from your project folder:

```bash
wuu exec --permission-mode read_only "read this project and explain how to run its tests"
```

See [`wuu exec`](../automation/exec.md) for script input, saved sessions, and machine-readable output. If setup fails, use [troubleshooting](../help/troubleshooting.md).
