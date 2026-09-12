# User Guide

This guide covers the stable path from installing wuu to completing daily work.
For every task, wuu operates on a local workspace and calls a model provider with
credentials you control.

Step-by-step walkthroughs: [Installation](installation.md), [Model
services](model-services.md), and [Your first task](first-task.md).

## Choose how to use wuu

- **Desktop:** use the macOS app for interactive work, multiple conversations,
  attachments, and visual review.
- **CLI:** use `wuu exec` from a terminal, script, CI job, or another agent.

The desktop app includes its own private core. A separately installed CLI is not
required by the desktop app, and the two may have different versions.

## Install

### macOS desktop preview

Download the arm64 DMG or ZIP from
[GitHub Releases](https://github.com/blueberrycongee/wuu/releases), then move
`wuu.app` to `/Applications`.

The current preview is unsigned and not notarized. For a release you trust, remove
the quarantine attribute if macOS blocks the app:

```bash
xattr -dr com.apple.quarantine /Applications/wuu.app
open /Applications/wuu.app
```

Do not run this command for an app downloaded from an untrusted source.

### CLI

Build the CLI from a checked-out source tree. Product releases use CalVer tags,
which are not Go module major versions:

```bash
git clone https://github.com/blueberrycongee/wuu.git
cd wuu
make install
wuu --version
```

GitHub Releases do not contain standalone CLI archives.

## Connect a model provider

wuu is BYOK: model requests use a provider and credentials that you configure.

### Desktop

Open **Settings → Model providers**, choose or add a provider, then enter its model,
endpoint, and API key. Save the provider before starting a conversation.

### CLI

Create a starter user config once:

```bash
wuu init
```

This writes `~/.wuu/config.json`, or `$WUU_HOME/config.json` when `WUU_HOME` is
set. The starter config includes OpenAI, Anthropic, and OpenRouter entries. Set
the environment variable named by the selected provider, for example:

```bash
export OPENAI_API_KEY="..."
wuu exec "describe this repository"
```

To use a different configured provider for one run:

```bash
wuu exec --provider anthropic "review the current changes"
```

See the [configuration model](../../zh-cn/reference/configuration.md) for configuration
precedence and trust boundaries, and [`wuu exec`](../automation/exec.md) for all automation
options.

## Work in the right repository

The workspace is the boundary for file tools, commands, and visible sessions.

- In the desktop app, open or select the local project folder before starting a
  conversation.
- In the CLI, run wuu from the repository or pass `--workdir` explicitly.

```bash
cd path/to/project
wuu exec "run the tests and fix the failure"

wuu exec --workdir path/to/project "summarize this codebase"
```

Describe the outcome you want. Include constraints that affect product behavior,
security, compatibility, or data. wuu can inspect the repository to decide routine
implementation details.

## Complete and review a first desktop task

In the selected project, create a conversation and start with a read-only check:

```text
Read this workspace without changing files. Explain what it contains and how the
existing project should be verified.
```

After confirming that wuu sees the right directory, give it one small task with a
clear result, scope, constraints, and verification command. While it runs, follow
the tool activity in the conversation. When it finishes:

1. open **Files** or enter `/files` to browse or open the project files you need;
2. open **Review** or enter `/diff` to confirm the changed-file set and inspect the current Git diff;
3. confirm that the reported tests or build actually ran and passed;
4. use **Terminal** or `/terminal` when you want to run an independent check.

The permission control beside the composer switches between Standard, Read only,
and Unconfined. Keep Standard for normal project work, use Read only for
investigation, and read the security model before enabling Unconfined.

## Attach files and images

The desktop composer accepts attachments. In the CLI, pass them explicitly:

```bash
wuu exec --file report.pdf "summarize this report"
wuu exec --image screenshot.png "find the visual issue"
```

An attachment gives the current task access to that file; it does not add the file
to the repository automatically.

## Continue and inspect sessions

wuu stores persistent sessions so work can continue across runs:

```bash
wuu exec --continue "continue from the last session"
wuu exec resume THREAD_ID "continue this task"
wuu session list
wuu session show --last
```

Use `--ephemeral` when an automated run should not create a persistent session.
Use `wuu session archive` to hide a session from normal lists and
`wuu session delete` only when its stored history should be removed.

## Understand the trust boundary

- wuu works on local files and can run local commands according to the active
  permission mode.
- Prompts and relevant context are sent to the configured model provider. BYOK
  does not mean the model runs locally.
- API keys should come from the desktop provider settings or environment
  variables. Do not commit keys to a repository.
- Normal startup keeps provider endpoints, credentials, instruction paths, and
  permission mode under user control; project config cannot silently replace
  them.
- User config, sessions, logs, and other state live under `~/.wuu` by default.
  Set `WUU_HOME` to move that state as one unit.

Read the [security model](../reference/security-model.md) before using wuu with untrusted
repositories or sensitive data.

## Common problems

### `wuu: command not found`

Ensure Go's binary directory is on `PATH`:

```bash
export PATH="$(go env GOPATH)/bin:$PATH"
```

Add the equivalent line to your shell profile if it fixes the problem.

### The provider reports a missing API key

Confirm that the configured `api_key_env` name matches the variable you exported,
then start wuu from an environment that can see it. For the desktop app, enter the
key in **Settings → Model providers** instead of relying on a terminal-only export.

### The wrong repository or sessions appear

Check the selected desktop workspace, the current terminal directory, or the
`--workdir` value. Session lists are scoped to a workspace by default.

### `wuu init` says the config already exists

Edit the existing config instead of overwriting it. `wuu init --force` replaces
the file and should only be used after saving anything you need from it.

### macOS refuses to open the desktop app

Confirm that the app came from the official GitHub Releases page, then use the
quarantine command in the installation section. The current preview is not signed
or notarized.
