# Configuration

Wuu keeps model connections and execution choices in user configuration, while projects can supply additional behavior. Start with `wuu init` for CLI use or desktop onboarding, then edit only the settings you need. Wuu rejects unknown configuration fields rather than silently ignoring typos.

## User configuration and project layers

Normal startup selects the user file at `~/.wuu/config.json`, or `$WUU_HOME/config.json` when set. The legacy `~/.config/wuu/config.json` path is a migration and fallback source, not another overlay applied after the current user file.

Wuu then merges project files in this order, with later values taking precedence where allowed:

1. `.wuu.json`, or `wuu.json` if the first does not exist.
2. `.wuu/settings.json` for shared settings.
3. `.wuu/settings.local.json` for local settings.

Objects merge recursively; arrays and scalar values are replaced. Keep the local settings file out of version control. A missing user configuration does not silently turn a project file into the trusted base; the CLI asks you to initialize one.

## Protected user settings

Normal startup removes these fields from every project layer, including `settings.local.json`, and reports the ignored fields:

| Field | Why it stays user-owned |
|---|---|
| `default_provider`, `providers` | Select model services, endpoints, credentials, and connection options |
| `instructions`, legacy `memory` | Control instruction discovery, including user paths |
| `agent.model_roles`, `agent.model_aliases` | Route model work |
| `agent.permission_mode` | Set local execution authority |

Case changes in JSON keys do not bypass the restriction. Other allowed project fields can still affect prompts, tools, hooks, and services, so this filtering does not make an unfamiliar repository safe to execute.

## Model and engine configuration

A provider entry names a model connection. For example, this user-configuration fragment uses an OpenAI-compatible Chat endpoint:

```json
{
  "default_provider": "work",
  "providers": {
    "work": {
      "type": "openai-compatible",
      "base_url": "https://api.example.com/v1",
      "api_key_env": "WORK_MODEL_API_KEY",
      "model": "your-model-id"
    }
  },
  "agent": { "permission_mode": "standard" }
}
```

Replace the endpoint and model with values your service supports, and supply the named environment variable to the process running Wuu. For subscription login, provider types, and desktop setup, see [model services](../getting-started/model-services.md).

[External engines](../getting-started/external-engines.md) are separate programs, not provider types. Their machine-local `engines` configuration controls detection, executable selection, and the default engine. In the desktop, changing a conversation's model does not necessarily change the workspace default; use settings when you intend to change future sessions.

## Instructions and plugin settings

Put shared project rules in `AGENTS.md`. The core `instructions` object controls filenames, project-root markers, user directories, and optional legacy instruction discovery. The old top-level `memory` name is accepted for instruction-discovery migration; it is not the Memory plugin's settings interface.

[Memory](../customize/memory.md), [Dream](../customize/dream.md), and other plugins maintain their own product settings and storage. The core's persistent session working notes are separate from plugin memory. Use each plugin's settings page rather than copying obsolete `memory.dream` or delegation flags into the core config.

## Worker capacity

`agent.max_parallel` sets the generic anonymous-worker execution capacity. It defaults to `5`; `0` means use that default, and negative values are invalid.

```json
{ "agent": { "max_parallel": 5 } }
```

Queued workers and workers waiting for children do not occupy normal execution slots. This is an execution-capacity setting, not a request to delegate every task or a limit on all background processes. [Subagent behavior](../desktop/subagents.md) belongs to the enabled delegation plugin.

## Explicit automation configuration

`wuu exec --config /path/to/config.json` loads one complete file without normal project overlays. `wuu exec --ignore-user-config` instead trusts the project's `.wuu.json` or `wuu.json` as the base and applies its two settings layers.

Both choices deliberately accept connections, credential references, instruction paths, hooks, and MCP definitions from those files. Use them only with reviewed configuration. An empty `HOME` does not grant the same trust implicitly.

## Moving user state

Set `WUU_HOME` before starting Wuu to select a different user-state root. It affects configuration, authentication state, sessions, memory, plugins, and logs, not just the config file. For example, `WUU_HOME=/data/wuu` selects `/data/wuu/config.json`. Protect that directory and avoid sharing it in diagnostics.

On Windows, command execution requires Git Bash; `WUU_GIT_BASH_PATH` can select its executable. Command availability and sandbox support are separate requirements, described in [permissions](permissions.md) and the [command system](agent-command-system.md).
