# Security Model

Wuu is a local coding agent. It reads project context, sends selected context to
a model provider, and can run tools on the user's machine. That is useful
authority, so a repository opened in Wuu must be treated more like executable
code than a passive document.

This document describes the current boundary. Review model output and use a
container, virtual machine, or separate system account for further isolation of untrusted code.

## Permission modes

Wuu has three permission modes (`standard`, `read_only`, and `unconfined`) that
control which local paths the agent can access and modify; how to switch and
the CLI override are covered in [permission modes](permissions.md).

In `standard` and `read_only`, Wuu's command tools apply a filesystem sandbox to
restrict writes; see [permission modes](permissions.md#system-permissions-and-isolation)
for its scope and backend requirements. Processes retain their system identity,
inherited environment, and network access, so this protection does not guarantee
credential secrecy. `unconfined` removes this sandbox and the path boundary,
handing the agent full local authority at the current user's
privilege level and should be enabled only for trusted tasks; the sensitive-path
guards kept by the dedicated file and Git tools (`.env`, SSH private keys,
`~/.wuu` credential files, and so on) are defense in depth, and arbitrary shell
commands, scripts, and child processes can bypass them. In `standard` and
`read_only` modes, commands that expose the whole environment, read common
credential paths, use unsafe Git operations, or perform package/network
mutations receive extra classification or hard checks, and tool output is
redacted for common secret patterns.

For untrusted repositories, use a disposable VM, container, or a separate OS
account. Do not substitute permission modes for full execution-environment isolation.

## Data sent to model providers

Depending on the task and enabled features, Wuu may send the configured model
provider:

- user messages and conversation history;
- system prompts and applicable instruction files;
- file contents, search results, diffs, command output, and tool errors;
- memory selected by an enabled Memory plugin;
- images or other attachments the user adds;
- MCP or hook output returned to the agent.

Wuu does not intentionally place provider API keys in prompts. Keys may still
be exposed if a command, hook, MCP server, file, or user message prints them in
an unrecognized form. Review the privacy and retention terms of the selected
provider. A custom OpenAI-compatible base URL receives the same prompt data as
the provider it replaces.

## Trusted configuration and project input

The user config at `~/.wuu/config.json` (or `WUU_HOME/config.json`) is the
trusted base. Normal startup filters project attempts to replace provider
destinations, credential environment names, outside-workspace instruction paths,
model roles, or the permission mode. `wuu exec --config` and
`--ignore-user-config` explicitly trust the selected project configuration and
are intended for controlled automation.

The following project content can affect agent behavior and must be reviewed:

- `AGENTS.md` and other discovered instruction files;
- `.wuu.json`, `wuu.json`, `.wuu/settings.json`, and local settings;
- project skills and prompt additions;
- hooks and plugins;
- `.mcp.json` and native MCP server configuration.

Project MCP entries are not loaded until approved in local settings. Approval
means trusting that server or subprocess with the files, environment, network,
and credentials available to it; MCP output is also untrusted model input.
Hooks and local skills can carry prompt injection even when they do not execute
native code.

## Credentials and local state

- Provider keys are normally read from the environment named by
  `providers.<name>.api_key_env`.
- The macOS desktop stores managed OAuth credentials in Keychain and does not
  silently fall back to a plaintext file if Keychain fails.
- Headless flows can explicitly use `~/.wuu/auth.json` and
  `~/.wuu/credentials.json` (or the matching `WUU_HOME` paths). Wuu writes
  credential files with owner-only permissions, but their contents are not a
  replacement for an OS keychain.
- Remote identity and enrolled-device data live in `~/.wuu/remote.json`; phone
  credentials use `~/.wuu/phone.json`. These files are also owner-only and
  should be backed up and shared as secrets.
- Sessions, logs, tool results, plugin-owned memory, and workspace state live under
  `~/.wuu`. They can contain source code and conversation content even when
  common credential patterns have been redacted.

Do not include `~/.wuu`, environment files, logs, session exports, or diagnostic
bundles in bug reports without reviewing them first.

## App server and desktop boundary

`wuu app-server` uses a newline-delimited JSON request/response protocol over the
subprocess's standard input and output.
It does not open a network listener by itself. The Electron shell starts and
owns this process and exposes selected operations to the renderer through its
preload/IPC bridge. A renderer-to-main IPC bypass or an unsafe navigation is a
security issue.

## Remote and mobile control

`wuu relay` listens on `127.0.0.1:8787` by default. Binding it to another
address exposes it to that network and requires the operator to provide TLS and
deployment controls appropriate for the environment.

The remote host dials the relay; it does not open an inbound workspace server.
Phones enroll through a time-limited pairing link. Enrolled devices authenticate
with signed keys, and application frames are end-to-end encrypted between host
and phone. The relay routes opaque frames and content-free push hints, but it
still sees connection timing, device presence, pairing identifiers, and network
addresses. Anyone who gets a live pairing link or a phone credential file may
be able to enroll or act as that device.

Treat a paired phone as a remote controller with the same workspace authority
as the host session. Revoke devices that are lost or no longer trusted.

## Safe use checklist

1. Review a new repository's instructions, settings, hooks, skills, and MCP
   configuration before enabling tools.
2. Use `read_only` for inspection and a separate isolated environment for hostile code.
3. Keep provider endpoints and credential environment names in the user config.
4. Pair remote devices in private and use `wss://` for relays across untrusted
   networks.
5. Review diffs and command output before publishing them.
6. Report boundary bypasses privately using [SECURITY.md](../../../SECURITY.md).
