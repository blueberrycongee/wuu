# Configuration

Wuu keeps model connections and execution choices in user configuration, while projects can supply additional behavior. Start with `wuu init` for CLI use or desktop onboarding, then edit only the settings you need. Wuu rejects unknown configuration fields rather than silently ignoring typos.

The built-in agent can use ordinary file tools to edit configuration in Standard or Unconfined mode; Read only permits inspection but refuses edits. Wuu home is part of the normal file scope, so no separate configuration tool is needed. To add a provider or change the default model, edit the user file described below; project-layer restrictions still apply. The desktop uses its existing refresh path for valid model changes, while other settings retain their documented restart requirements.

Prefer environment-variable references for provider credentials. Tool output may redact secret values: use focused edits rather than replacing the whole configuration with a copy of redacted output. Direct file access to Wuu login credential stores remains blocked; see [permissions](permissions.md).

If an external edit makes configuration invalid while the desktop is running, Wuu reports the error and retains the last valid model inventory for display. That inventory does not bypass configuration validation for changes or execution. Correct the file to resume automatic refresh; restoring its previous contents also clears the error. Unchanged invalid files do not produce repeated refresh errors.

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
| `agent.model_roles`, `agent.model_aliases`, `agent.project_models` | Route model work |
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

## Tool loading

`agent.tool_loading` defaults to `auto`. Supported first-party paths use native
loading: OpenAI Responses with supported GPT-5.4 or later models (excluding
[GPT-5.4 nano](https://developers.openai.com/api/docs/models/gpt-5.4-nano)),
[Claude models with tool search support](https://platform.claude.com/docs/en/agents-and-tools/tool-use/tool-search-tool#model-compatibility)
(including Haiku 4.5; excluding Opus 4.1 and earlier) on Anthropic, and `kimi-k3` Chat Completions at
`https://api.moonshot.ai/v1` or `https://api.moonshot.cn/v1`. Kimi uses a separate
message-level tools protocol; its Responses and Anthropic-compatible endpoints
are not enabled by this rule.

Native loading keeps deferred schemas out of the initial model context and
loads them through `tool_search`. The embedded browser is deferred when enabled.
Other paths use `client`: ordinary `tool_search` results load schemas on demand
without requiring a provider-specific protocol. The initial catalog preview is
bounded to 8 KiB; tools beyond it remain searchable. Loaded schemas are appended
to the direct-tool prefix, so discovery can change provider cache reuse. Ordinary
tool calling or a compatible URL alone does not establish native support.

Set `agent.tool_loading` to `client` to always use ordinary discovery, `flat` to
intentionally declare every tool, or `native` to opt into an implemented protocol
on a compatible endpoint. Unsupported native paths use client discovery and
report the change; this is a configuration-time choice, not an API retry. A model's `providers.<name>.models.<model>.options.native_tool_search`
can explicitly enable a compatible endpoint in auto mode, or disable native
discovery with `false` while keeping client discovery. Only enable it when the endpoint implements the model's native
protocol; accepting unknown fields is not sufficient.

## Project Agent model choices

In builds with Project Agent enabled, the lead uses the conversation model. Set
Side and Worker defaults independently in Settings → Runtime, or in the user
configuration:

```json
{ "agent": { "project_models": {
  "side": { "provider": "anthropic", "model": "your-side-model" },
  "worker": { "provider": "openai", "model": "your-worker-model" }
} } }
```

Use configured provider names and model IDs. Omit a role or leave it empty to
inherit the lead model. Each selection also accepts `effort` and `variant` when
the provider supports them. Defaults apply to newly created members; existing
sessions retain their saved selection. A creation-time `model_alias` overrides
the role default. See the [app-server protocol](../automation/app-server.md).

## Instructions and plugin settings

Put shared project rules in `AGENTS.md`. The core `instructions` object controls filenames, project-root markers, user directories, and optional legacy instruction discovery. The old top-level `memory` name is accepted for instruction-discovery migration; it is not the Memory plugin's settings interface.

[Memory](../customize/memory.md), [Dream](../customize/dream.md), and other plugins maintain their own product settings and storage. The core's persistent session working notes are separate from plugin memory. Use each plugin's settings page rather than copying obsolete `memory.dream` or delegation flags into the core config.

## Worker capacity

`agent.max_parallel` sets the generic anonymous-worker execution capacity. It defaults to `5`; `0` means use that default, and negative values are invalid.

```json
{ "agent": { "max_parallel": 5, "project_max_parallel": 0 } }
```

Queued workers and workers waiting for children do not occupy normal execution slots. This is an execution-capacity setting, not a request to delegate every task or a limit on all background processes. [Subagent behavior](../desktop/subagents.md) belongs to the enabled delegation plugin.

`agent.project_max_parallel` separately limits new managed-worker admissions for each Project Agent lead. `0` (the default) inherits the resolved `agent.max_parallel`; negative values are invalid. Lead and persistent side sessions are outside this worker pool. Legacy managed sessions with no explicit role count as workers.

The limit uses shared durable session membership and operating-system execution leases. Prelaunch reservations and short metadata mutations can conservatively occupy capacity. A stopped worker releases capacity only after execution cleanup finishes. Lowering the limit or adopting an already-running conversation does not cancel work; later admissions wait until occupancy falls below the limit.

The effective configuration is read on each new admission. Servers sharing a session store must use the same policy for a common bound; independent stores or machines do not share a global pool. Durable queued project input retries with bounded, coalesced backoff after remote completion or process exit, without model requests while waiting. Worker ordering is best-effort across servers, not global FIFO. Manual turn starts report full capacity before appending user input.

## Explicit automation configuration

`wuu exec --config /path/to/config.json` loads one complete file without normal project overlays. `wuu exec --ignore-user-config` instead trusts the project's `.wuu.json` or `wuu.json` as the base and applies its two settings layers.

Both choices deliberately accept connections, credential references, instruction paths, hooks, and MCP definitions from those files. Use them only with reviewed configuration. An empty `HOME` does not grant the same trust implicitly.

## Moving user state

Set `WUU_HOME` before starting Wuu to select a different user-state root. It affects configuration, authentication state, sessions, memory, plugins, and logs, not just the config file. For example, `WUU_HOME=/data/wuu` selects `/data/wuu/config.json`. Protect that directory and avoid sharing it in diagnostics.

On Windows, command execution requires Git Bash; `WUU_GIT_BASH_PATH` can select its executable. Command availability and sandbox support are separate requirements, described in [permissions](permissions.md) and the [command system](agent-command-system.md).
