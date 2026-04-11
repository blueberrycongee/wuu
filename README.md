# wuu

[中文](README_zh.md)

Terminal-native AI coding agent. Written in Go.

Named after its author (Wu) — the goal is to build a coding companion so good that every developer goes *wuuuuu!*

## Install

```bash
# Homebrew
brew install blueberrycongee/tap/wuu

# Shell script
curl -fsSL https://raw.githubusercontent.com/blueberrycongee/wuu/main/install.sh | sh

# npm
npx wuu@latest

# From source
go install github.com/blueberrycongee/wuu/cmd/wuu@latest
```

## Quick Start

```bash
wuu                        # launch interactive TUI
wuu init                   # generate config template
wuu run "describe this repo"  # one-shot task
```

On first launch, `wuu` walks you through provider setup (API key, model, theme).

## Versioning

- `VERSION` is the single source of truth for the next SemVer release (for example `0.1.0`).
- Local builds use `vX.Y.Z-dev` by default:

```bash
make install
wuu version --long
```

- Release flow:

```bash
# 1) update VERSION
# 2) create release tag from VERSION
make tag-release

# 3) push tag to trigger GitHub Release workflow
git push origin v$(cat VERSION)
```

When a `v*` tag is pushed, GitHub Actions + GoReleaser publishes release artifacts.

## What It Does

- Interactive TUI with streaming markdown rendering, slash commands, and session memory
- Agentic tool-calling loop — reads, writes, edits, searches, and runs shell commands in your repo
- Supports OpenAI-compatible APIs (OpenAI / OpenRouter / one-api / etc.) and Anthropic Messages API
- Built-in tools: `run_shell`, `read_file`, `write_file`, `edit_file`, `list_files`, `grep`, `glob`, `web_search`, `web_fetch`
- Orchestration and session tools: `ask_user`, `spawn_agent`, `fork_agent`, `send_message_to_agent`, `stop_agent`, `list_agents`, `load_skill`
- Tool availability model:
  - Main interactive agent (TUI session): full tool set
  - Sub-agents: no `ask_user` and no orchestration tools (`spawn_agent`, `fork_agent`, `send_message_to_agent`, `stop_agent`, `list_agents`)
- Follow-up control: `send_message_to_agent` can queue short instructions for running workers; they are injected as user turns before the worker's next model round
- File tools are sandboxed to the current workspace
- Session isolation with resume support
- Context compaction for long conversations

## Configuration

Config is loaded from (highest priority first):

1. `.wuu.json` (project-local)
2. `wuu.json`
3. `~/.config/wuu/config.json` (global)

```json
{
  "default_provider": "openrouter",
  "providers": {
    "openrouter": {
      "type": "openai-compatible",
      "base_url": "https://openrouter.ai/api/v1",
      "api_key_env": "OPENROUTER_API_KEY",
      "model": "openai/gpt-4.1-mini"
    },
    "anthropic": {
      "type": "anthropic",
      "api_key_env": "ANTHROPIC_API_KEY",
      "model": "claude-sonnet-4-20250514"
    }
  }
}
```

## License

MIT
