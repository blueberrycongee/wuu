<h1 align="center">wuu</h1>

<p align="center"><strong>Don't fork the agent. Extend it.</strong></p>

<p align="center">Open source · macOS · Bring your own model · Extensible</p>

<div align="center">
  <p>
    <a href="README.md">English</a> |
    <a href="README_zh.md">简体中文</a>
  </p>
  <p>
    <a href="https://github.com/blueberrycongee/wuu/actions/workflows/ci.yml"><img alt="CI" src="https://img.shields.io/github/actions/workflow/status/blueberrycongee/wuu/ci.yml?branch=main&style=flat-square&label=ci"></a>
    <a href="https://github.com/blueberrycongee/wuu/blob/main/LICENSE"><img alt="License" src="https://img.shields.io/github/license/blueberrycongee/wuu?style=flat-square"></a>
    <a href="https://github.com/blueberrycongee/wuu/releases"><img alt="GitHub release downloads" src="https://img.shields.io/github/downloads/blueberrycongee/wuu/total?style=flat-square&label=downloads"></a>
  </p>
</div>

<img width="2272" height="2494" alt="wuu desktop app" src="https://github.com/user-attachments/assets/2d9030aa-ca03-42b1-9333-f79cc5aff95b" />

If you have used OpenCode, Claude Code, or Codex, wuu will feel familiar. The shortest description is: **a desktop GUI for the local coding-agent workflow, built on an extension platform**.

You still open a project and work with an agent. wuu puts the rest of that workflow into one visible workspace: projects and conversations on the left; the current task in the center; files, diffs, terminals, browser, skills, and model settings alongside it.

wuu is independently developed and is not an official client for OpenCode, Claude Code, or Codex.

In our internal benchmark on real code repositories, standard wuu sessions cost about half as much per successful fix as [pi](https://github.com/badlogic/pi-mono).

## Built to be extended

wuu's main development focus is its extension system: a plugin platform so the ecosystem can grow the product without forking it.

- **One package, many capabilities.** A Wuu Plugin is an installable, upgradeable package that can add agent tools, context, desktop views, themes, settings, Skills, Hooks, MCP servers, and commands together, with one trust and upgrade lifecycle.
- **Feature plugins extend, appearance plugins reskin.** The two are orthogonal and composable; they compose without knowing each other exists.
- **First-party features use the same API.** Subagents, automation, memory, and todos ship as plugins on the same mechanisms available to third parties.
- **Local-first, no fork.** Install a local directory or zip package with one approval. There is no marketplace or central registry.

```bash
wuu plugin create --type agent my-agent
wuu plugin create --type desktop my-ui

wuu plugin pack ./my-agent
wuu plugin install ./my-agent.zip
wuu plugin approve my-agent
```

Start with the [Wuu Plugins guide](docs/en/customize/plugins.md), or see [Extend Wuu](docs/en/customize/index.md) to pick the smallest extension that fits.

> [!WARNING]
> wuu is an early preview and changes quickly. Packaged builds currently support Apple silicon Macs.

## Download

Download the latest build from [GitHub Releases](https://github.com/blueberrycongee/wuu/releases/latest), move `wuu.app` to `/Applications`, and open it.

The current preview is unsigned and not notarized. If macOS blocks an official release that you trust, run:

```bash
xattr -dr com.apple.quarantine /Applications/wuu.app && open /Applications/wuu.app
```

## What the desktop app adds

- **Projects and conversations** — switch between repositories, search conversations across workspaces, and resume or fork previous work.
- **Files, changes, and terminals** — inspect the repository, review the current Git diff, preview images and documents, and revisit command output without leaving the app.
- **A visual agent timeline** — follow tool calls, background processes, delegated work, and attachments as the task runs.
- **No configuration-file busywork** — manage model providers, permissions, skills, and memory from the desktop app, and inspect the project instructions it loaded.
- **A complete agent experience out of the box** — todos, subagents, background tasks, and persistent sessions are built in rather than left for you to assemble.

## Get started

1. Open **Settings → Model providers** and connect Anthropic or an OpenAI-compatible provider.
2. Add a local project folder.
3. Start a conversation.

See the [user guide](https://blueberrycongee.github.io/wuu/en/getting-started/) for provider setup, permissions, attachments, and sessions.

## CLI

The desktop app includes its own core and does not require a separate CLI. For scripts, CI, or non-interactive work, build the CLI from a checked-out source tree. Product releases use CalVer tags, which are not Go module major versions:

```bash
git clone https://github.com/blueberrycongee/wuu.git
cd wuu
make install
wuu --version
wuu init

wuu exec "fix the failing tests and verify the result"
wuu exec --json "review the current diff"
wuu exec review --uncommitted
```

See the [`wuu exec` guide](docs/en/automation/exec.md) for structured output, attachments, session controls, and review options.

## Models and local data

- You choose the provider and credentials. Prompts and relevant context are sent to that provider.
- Sessions, config, logs, and other local state live under `~/.wuu` by default.
- File changes and commands run locally in the selected workspace and active permission mode.

Read the [security model](docs/en/reference/security-model.md) before using wuu with untrusted repositories or sensitive data.

## Project

- Agent blob avatars are powered by [`blobatar`](https://github.com/Alain00/blobatar), an MIT-licensed deterministic geometric avatar library.
- [Documentation](https://blueberrycongee.github.io/wuu/en/)
- [Changelog](CHANGELOG.md)
- [Public evaluations](evals/)
- [Contributing](CONTRIBUTING.md)

If you run into a problem, [open an issue](https://github.com/blueberrycongee/wuu/issues). For security vulnerabilities, follow [SECURITY.md](SECURITY.md).

## License

[MIT](LICENSE)
