# External agent engines

An external engine runs an installed agent with its own account, model configuration, tools, and native session storage. It is separate from a [model provider](model-services.md) used by the built-in Wuu engine. Wuu includes Codex and Claude Code integrations and the integrations below.

## Install and select an engine

Install the agent or adapter yourself using its upstream instructions. Wuu does not download executables. Open **Settings → Agents** to check detection, set an executable path, or disable an engine. Choose the default for new conversations in **Settings → General**, or pick an available engine in the composer.

| Engine ID | Launch command | Integration and setup |
|---|---|---|
| `cursor` | `cursor-agent acp` | ACP v1; [Cursor CLI](https://cursor.com/docs/cli/acp) |
| `devin` | `devin acp` | ACP v1; [Devin CLI](https://docs.devin.ai/cli) |
| `dsh` | `dsh --profile acp` | ACP v1; [DeepSeek Harness ACP](https://github.com/deepseek-ai/deepseek-harness/tree/master/packages/acp/acp) |
| `grok` | `grok --no-auto-update agent --no-leader stdio` | ACP v1; [Grok CLI](https://x.ai/cli) |
| `hermes` | `hermes acp` | ACP v1; [Hermes ACP](https://hermes-agent.nousresearch.com/docs/user-guide/features/acp) |
| `pi` | `pi-acp` | ACP v1 through the [community adapter](https://github.com/svkozak/pi-acp), not the plain `pi` command |
| `opencode` | `opencode serve --hostname 127.0.0.1 --port 0` | Native HTTP/SSE, OpenCode 1.x; [OpenCode](https://opencode.ai/docs) |
| `antigravity` | `agy_acp_server` | ACP v1; install a compatible server separately; [Antigravity extensions](https://antigravity.google/docs/ide/extensions) |

Antigravity detection also accepts `agy_acp_server.par`; Linux launches it with `--uid=`. These commands describe the adapter contract, not a guarantee that every CLI release supports it. ACP must negotiate protocol version 1; OpenCode must report a healthy 1.x server. Incompatible versions fail explicitly.

For these engines, executable lookup uses `engines.<id>.binary_path`, then `WUU_<ID>_BINARY` (for example `WUU_DEVIN_BINARY`), then `PATH`. An invalid override fails instead of silently using another executable. Supply a path to a program, not a command with arguments. Detection only resolves the executable: it does not validate credentials or start a conversation.

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

## DeepSeek Harness setup

Install DeepSeek Harness with its official ACP profile and configure its provider and model before selecting it in Wuu. Wuu launches `dsh --profile acp` in the conversation's working directory; the plain TUI profile is not an ACP server. Use **Settings → Agents → DeepSeek Harness** to select a wrapper executable if your installation needs a custom profile or config path. The wrapper must forward Wuu's arguments and keep stdout reserved for JSON-RPC.

The official `@deepseek-ai/dsh-acp` `0.2.1-rc.2` contract advertises persistent `session/resume`, `session/close`, model/reasoning configuration and HTTP MCP. Install matching CLI/profile packages following the upstream instructions; the npm `latest` tag may still point to an older ACP implementation. DSH owns provider credentials and advertises no ACP sign-in methods. After configuring it, refresh detection in Wuu.

DSH publishes committed messages, thoughts and generic tool updates, rather than raw token deltas or its TUI's cards, plans and terminal views. Its ACP profile does not advertise a read-only mode. Wuu retains its own displayed transcript while DSH restores native context without replaying that transcript. DSH may retain unprompted model-discovery sessions in its own history; closing a session does not delete it.

## Sign in and choose a model

Use the agent's native CLI login and configuration. For ACP engines, Settings also offers **Sign in**: clicking it starts the agent to discover supported login methods. Choosing a method explicitly invokes its authentication flow, which may open a browser. Opening Settings, expanding an engine, or refreshing detection does not initiate this flow. Cancellation stops the sign-in process; credential storage remains the agent's responsibility.

Only agent-driven authentication methods are supported in this UI. If no method is offered, use the native CLI. Discovery is not an account-status check, and a successful login is not a guarantee of model access. Wuu does not import these engines' credentials into a provider.

Claude Code discovers models through its CLI initialization handshake without sending a prompt. The picker shows the reported model names and resolved targets, while selections keep the CLI's original aliases and context suffixes. Reasoning levels and fast-mode support come from the CLI rather than a fixed Wuu list. Discovery reflects the installed CLI and its configuration; it does not guarantee account access to every reported model.

Use **Settings → Agents → Detect again** after changing the native login or configuration. All external catalogs use the conversation's working directory, including a Git worktree, and cache successful results for two minutes. Refresh starts a native probe; overlapping refreshes share that probe. Wuu checks the executable, environment and known native configuration/authentication file metadata before reuse and before publishing results. Claude also checks native login status. Changes stored only in a keychain or remote account may require explicit refresh or cache expiry.

A failed refresh retains previous results only for the same discovery context and shows a warning. An authoritative empty list clears previous models; an unsupported catalog, a partial probe and a failed probe remain distinct. Wuu never inserts its provider catalog into an external engine. New conversations wait for engine discovery before submitting model parameters.

Codex uses its app-server model catalog. ACP engines use `session/new` model/configuration responses and `config_option_update` notifications, including Devin's delayed initial configuration. Model configuration options take precedence over the legacy model list. Wuu probes each model's reasoning, speed and additional select/boolean options independently, keeps native option IDs and values, and applies model changes before dependent selections. Rejected selections fail visibly. OpenCode uses its native HTTP API, lists connected providers' `provider/model` identities, and exposes advertised variants as a **Variant** option.

Existing conversations keep their engine identity, but their model and options remain editable while idle. Choosing **Agent default** clears model, reasoning, speed and additional option overrides; the next turn resolves native defaults rather than retaining an earlier session override. Individual options also offer **Default**. Resuming Claude requires its `get_settings` control to expose the configured model. OpenCode restores the default agent's model or its configured model; when neither is exposed, select a model explicitly before resuming. Its API does not expose the recent-model fallback. Unsupported native defaults produce an error instead of silently retaining an old override.

ACP image attachments are written to local files and included as paths in the prompt so the agent can read them with its own tools. Wuu does not send ACP image content blocks, including when the agent advertises image prompt support. Unsupported host HTTP MCP tools still produce an error rather than dropping the tools.

## Fast mode

Open the model popover and use the lightning button in its header when the selected model advertises acceleration. Speed is independent of reasoning effort. It is saved per conversation, remains editable while an existing conversation is idle, and applies to the next request. Reset restores the engine's configured default. `/fast`, `/fast on`, `/fast off`, and `/fast status` use the same selection. Faster processing can consume more credits or cost more; the account and service determine availability.

Codex discovers support from its live model catalog and sends `serviceTier: "fast"` or `"default"` on native thread creation, resume, and turns. The Claude engine passes an explicit `fastMode` setting for selections whose CLI metadata advertises support. ACP discovers per-model select options, including grouped choices, and uses the advertised `fast-mode`, `fast_mode`, `speed`, or `service_tier` ID and values with `session/set_config_option`. Model changes refresh the complete option list before speed is applied. Agents without a recognized advertised selector do not show this control; OpenCode's current integration does not advertise one.

These mappings follow the upstream [Codex speed configuration](https://learn.chatgpt.com/docs/agent-configuration/speed), [native CLI Fast mode](https://code.claude.com/docs/en/fast-mode), [ACP session configuration contract](https://agentclientprotocol.com/protocol/v1/session-config-options), and [Codex ACP adapter](https://github.com/agentclientprotocol/codex-acp/blob/main/src/FastModeConfig.ts). A saved choice records what Wuu requests, not a guarantee that the upstream service will grant accelerated processing.

## Native subagents

Claude background agents and their automatic follow-up belong to the same Wuu turn. A native result that only reports the launch does not finish the conversation turn while an observed agent is running or its result still needs a follow-up. Wuu keeps the process open, appends the main agent's summaries, and completes once the follow-ups settle. Child-agent messages stay out of the main reply. Stop cancels the whole turn and closes the process; it does not detach the agents for later background work.

Native subagent status is currently transient. Codex reports child activity through its parent turn; Wuu does not expose independently resumable child conversations. ACP completion still follows the agent's prompt protocol. Vendor-specific ACP subagent extensions are not a general background-agent lifecycle contract, and Wuu does not provide persistent child conversations through them.

## Permissions and session recovery

Codex keeps its native conversation while Wuu sends the admitted working directory and effective permission mode on every turn. Switching a conversation to a linked worktree also moves the native turn's working directory; the previous worktree is not added as an extra writable root. A process-scoped explicit permission override still takes precedence over the conversation's saved mode.

These adapters launch trusted local programs with your user permissions. Wuu does not place their native tools inside its OS process sandbox. ACP agents that advertise a read-only or plan mode receive that native setting; otherwise `read_only` is refused because Wuu cannot enforce that boundary itself. OpenCode has no equivalent native mode and is refused. In Standard mode, the host maps onto a prompting native mode when advertised, and permission requests received over the protocol go through Wuu's approval flow; missing approval support declines them. `unconfined` maps onto a no-prompts native mode when advertised, otherwise it accepts supported one-shot requests. ACP approvals never become persistent `allow_always` rules. OpenCode refreshes an `ask` rule on each turn, including resumed turns. An agent that performs work without requesting permission is outside this approval boundary. New external-engine conversations default to `unconfined` when the caller omits a mode; choose deliberately in the composer access menu. See [permissions](../reference/permissions.md).

Wuu saves the native session reference and restores it on subsequent turns, using `session/resume` when advertised or `session/load` otherwise. If neither is supported, or restoration fails, Wuu starts a replacement session and displays a notice that the agent no longer has the earlier context. Agents advertising `session/close` receive a bounded close request before process cleanup, including after cancellation and for model-discovery sessions; close failures are reported. Closing preserves the agent's persisted history for later recovery. An ACP turn completes on the native prompt response. Grok can also complete on its `x.ai/session/prompt_complete` extension when that notification arrives first, because its prompt RPC can hang after the turn has finished. Quiet output after the agent has acknowledged the prompt is not treated as completion. Total silence after the prompt — no queue bookkeeping, updates, or requests — surfaces an error, because that is a wedged agent rather than extra-high reasoning. Stop requests cancel native work and clean up the child process. OpenCode uses a password-protected loopback server with a fresh password for each process; it is not exposed as a remote service.


## Forking and editing history

For Codex, fork a completed turn or its final reply. Editing a user message
keeps the conversation before that native turn. Wuu creates an independent
native branch and saves its reference together with the matching visible
history; subsequent turns and restarts use that branch. The source native
history remains unchanged. Neither operation reverts files or tool side effects.

Codex must support bounded `thread/fork` and return complete turn history. Wuu
checks the returned turn and item identities, including native tool context.
An older CLI that ignores the cutoff fails explicitly; update the CLI rather
than accepting a branch with extra context. Wait for the source turn to finish
before forking. Cuts inside a native turn, such as a user message, commentary,
or one tool result, are refused; choose the turn's final reply instead. Older
Wuu conversations need a matching native output identity to establish the cut.

Claude Code and the other external adapters do not yet expose a verified native
history cutoff through Wuu. Their nonempty histories remain readable and
resumable, but fork/edit requests fail before changing them. Empty, never-run
threads can still be forked. Managed context-copy requests likewise reject
external history instead of treating its display transcript as a native replay.

If native forking or Wuu persistence fails, the original conversation stays
usable. A native branch created before a failed Wuu commit may remain in the
CLI's own history without a Wuu conversation; Wuu does not delete native audit
history to conceal that failure.

If a new Codex turn was sent but its native outcome cannot be confirmed, Wuu
remembers that uncertainty across restarts and does not send another prompt
into potentially running native work. When the turn ID is known, a later
attempt first reads native history and continues only after confirming that
turn has finished. If its ID could not be saved, fork an earlier verifiable
completed turn. Ordinary older conversations without these uncertainty markers
remain resumable. A Stop with a confirmed terminal outcome does not require this recovery.
