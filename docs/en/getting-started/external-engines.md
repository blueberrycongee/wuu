# External agent engines

An external engine runs an installed agent with its own account, model configuration, tools, and native session storage. It is separate from a [model provider](model-services.md) used by the built-in Wuu engine. Wuu includes Codex and Claude Code integrations and the seven integrations below.

## Install and select an engine

Install the agent or adapter yourself using its upstream instructions. Wuu does not download executables. Open the engine section in Settings to check detection, set an executable path, or disable an engine. Then choose an available engine in the composer, or set the default for new conversations.

| Engine ID | Launch command | Integration and setup |
|---|---|---|
| `cursor` | `cursor-agent acp` | ACP v1; [Cursor CLI](https://cursor.com/docs/cli/acp) |
| `devin` | `devin acp` | ACP v1; [Devin CLI](https://docs.devin.ai/cli) |
| `grok` | `grok --no-auto-update agent --no-leader stdio` | ACP v1; [Grok CLI](https://x.ai/cli) |
| `hermes` | `hermes acp` | ACP v1; [Hermes ACP](https://hermes-agent.nousresearch.com/docs/user-guide/features/acp) |
| `pi` | `pi-acp` | ACP v1 through the [community adapter](https://github.com/svkozak/pi-acp), not the plain `pi` command |
| `opencode` | `opencode serve --hostname 127.0.0.1 --port 0` | Native HTTP/SSE, OpenCode 1.x; [OpenCode](https://opencode.ai/docs) |
| `antigravity` | `agy_acp_server` | ACP v1; install a compatible server separately; [Antigravity extensions](https://antigravity.google/docs/ide/extensions) |

Antigravity detection also accepts `agy_acp_server.par`; Linux launches it with `--uid=`. These commands describe the adapter contract, not a guarantee that every CLI release supports it. ACP must negotiate protocol version 1; OpenCode must report a healthy 1.x server. Incompatible versions fail explicitly.

For these seven engines, executable lookup uses `engines.<id>.binary_path`, then `WUU_<ID>_BINARY` (for example `WUU_DEVIN_BINARY`), then `PATH`. An invalid override fails instead of silently using another executable. Supply a path to a program, not a command with arguments. Detection only resolves the executable: it does not validate credentials or start a conversation.

The machine-local configuration accepts `enabled` and `binary_path` for each engine, and `default_engine` for new conversations. Omitting `enabled` enables automatic detection; `false` opts out. For example:

```json
{
  "engines": {
    "default_engine": "devin",
    "devin": { "enabled": true },
    "grok": { "enabled": false }
  }
}
```

A missing or disabled engine cannot run. Existing conversations keep their engine binding rather than silently switching engines. See [configuration](../reference/configuration.md) for configuration locations and scope.

## Sign in and choose a model

Use the agent's native CLI login and configuration. For ACP engines, Settings also offers **Sign in**: clicking it starts the agent to discover supported login methods. Choosing a method explicitly invokes its authentication flow, which may open a browser. Opening Settings, expanding an engine, or refreshing detection does not initiate this flow. Cancellation stops the sign-in process; credential storage remains the agent's responsibility.

Only agent-driven authentication methods are supported in this UI. If no method is offered, use the native CLI. Discovery is not an account-status check, and a successful login is not a guarantee of model access. Wuu does not import these engines' credentials into a provider.

ACP engines that advertise models on `session/new` appear in the composer picker. Grok uses that first-class model list (`grok-4.6`, `grok-4.5`, …) plus `session/set_model`; effort uses the agent's `thought_level` option when advertised. If an agent advertises nothing, the composer keeps **Agent default**. Wuu does not substitute its own provider catalog. Programmatic ACP model selection requires an advertised model; OpenCode model IDs use `provider/model`. Image input and host HTTP MCP tools depend on native capabilities; unsupported ACP capabilities produce an error rather than dropping the input or tools.

## Permissions and session recovery

These adapters launch trusted local programs with your user permissions. Wuu does not place their native tools inside its OS process sandbox, and rejects `read_only` turns before launching them because it cannot enforce that boundary. In Standard mode, permission requests received over the protocol go through Wuu's approval flow; missing approval support declines them. `unconfined` accepts supported one-shot requests. ACP approvals never become persistent `allow_always` rules. OpenCode refreshes an `ask` rule on each turn, including resumed turns. An agent that performs work without requesting permission is outside this approval boundary. New external-engine conversations default to `unconfined` when the caller omits a mode; choose deliberately. See [permissions](../reference/permissions.md).

Wuu saves the native session reference and reloads it on subsequent turns. If an ACP agent cannot load sessions, or a saved session fails to load, the turn fails without silently creating a replacement history. A turn completes on the native prompt response. Grok can also complete on its `x.ai/session/prompt_complete` extension when that notification arrives first, because its prompt RPC can hang after the turn has finished. Quiet output is not treated as completion; Grok surfaces an error if it never acknowledges a prompt. Stop requests cancel native work and clean up the child process. OpenCode uses a password-protected loopback server with a fresh password for each process; it is not exposed as a remote service.
