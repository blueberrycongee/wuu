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

The desktop app does not inherit a terminal PATH when it is opened from Finder or the Dock. If PATH does not contain the executable, lookup continues in the standard install locations (`~/.local/bin`, `~/bin`, Homebrew, `~/.opencode/bin`, and version-manager shims such as nvm and mise). When the process still has no user directories, it also reads the login shell PATH. Codex and Claude Code use this same fallback.

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

ACP engines that advertise models on `session/new` appear in the composer picker. Grok uses that first-class model list (`grok-4.7`, `grok-4.6`, `grok-4.5`, …) plus `session/set_model`; effort uses the agent's `thought_level` option when advertised. If an agent advertises nothing, the composer keeps **Agent default**. Wuu does not substitute its own provider catalog. Programmatic ACP model selection requires an advertised model; OpenCode model IDs use `provider/model`. ACP image attachments are written to local files and included as paths in the prompt so the agent can read them with its own tools. Wuu does not send ACP image content blocks, including when the agent advertises image prompt support. Host tools use HTTP MCP when advertised; otherwise ACP uses Wuu's local stdio client. A host endpoint without a stdio fallback fails explicitly instead of silently dropping tools.

## Permissions and session recovery

These adapters launch trusted local programs with your user permissions. Wuu does not place their native tools inside its OS process sandbox. ACP agents that advertise a read-only or plan mode receive that native setting; otherwise `read_only` is refused because Wuu cannot enforce that boundary itself. OpenCode has no equivalent native mode and is refused. In Standard mode, the host maps onto a prompting native mode when advertised, and permission requests received over the protocol go through Wuu's approval flow; missing approval support declines them. `unconfined` maps onto a no-prompts native mode when advertised, otherwise it accepts supported one-shot requests. ACP approvals never become persistent `allow_always` rules. OpenCode refreshes an `ask` rule on each turn, including resumed turns. An agent that performs work without requesting permission is outside this approval boundary. New external-engine conversations default to `unconfined` when the caller omits a mode; choose deliberately in the composer access menu. See [permissions](../reference/permissions.md).

Wuu saves the native session reference and reloads it on subsequent turns. If an ACP agent cannot load sessions, or a saved session fails to load, the turn fails without silently creating a replacement history. A turn completes on the native prompt response. Grok can also complete on its `x.ai/session/prompt_complete` extension when that notification arrives first, because its prompt RPC can hang after the turn has finished. Quiet output after the agent has acknowledged the prompt is not treated as completion. Total silence after the prompt — no queue bookkeeping, updates, or requests — surfaces an error, because that is a wedged agent rather than extra-high reasoning. Stop requests cancel native work and clean up the child process. OpenCode uses a password-protected loopback server with a fresh password for each process; it is not exposed as a remote service.

## Coordinate with peer sessions

With Peers enabled, ordinary Wuu, Codex, Claude Code, ACP, and OpenCode conversations can discover and contact one another in the same app-server workspace. User-visible, unarchived sessions are eligible by default, regardless of who created them. Plugin-private sessions are excluded. Use `/peer` or ask the agent to list peers, send a bounded request, or change its inbound policy.

Addresses are stable Wuu session IDs. Names are labels, and native engine session IDs are not peer addresses. An accepted request starts or queues a target turn; it does not steer an active tool. The target's terminal response returns to the source once, without generating an automatic reply loop. External engines receive replies in a follow-up turn when their current turn cannot consume them.

Only plain text is transferred, without source history or files. Peer messages grant no user authorization and do not transfer permissions: the target keeps its pinned access mode. Inbound requests default to accept; `peer_policy` can refuse them without starting a target turn. External tool calls check the currently active plugins, so disabling Peers also rejects calls through an external engine's cached catalog. Native Wuu conversations retain the existing [plugin generation lifecycle](../customize/plugins.md#update-disable-and-remove).

The host attaches `wuu_session_tools` to each eligible external turn. ACP engines without HTTP MCP use the bundled executable's `session-tools --stdio` client and a host-provided `WUU_SESSION_TOOLS_URL`. This connects to the running app-server; it does not launch another runtime or create a bridge conversation. No CLI installation on PATH is needed. The endpoint expires with the turn. This local session channel is separate from Collaboration rooms and named-agent orchestration.
