# Security model

Wuu sends task context to a model provider and can run tools on your machine. The important boundaries are what data leaves the machine, which local actions a session can take, and which installed code you trust.

## Data sent to providers

A request can include messages, conversation history, instructions, selected memory, attachments, file contents, diffs, search results, command output, and tool errors. Hooks and MCP servers can also contribute content that reaches the model.

Choose a provider whose privacy and retention terms suit that data. A custom API endpoint receives the prompt data sent through that provider configuration. Using a local desktop app does not make a remotely hosted model local.

Wuu avoids putting provider credentials in prompts and redacts common secret patterns from tool output. Neither measure can identify every secret. A file, command, extension, or user message can still expose credentials in a form the redactor does not recognize.

## Local execution

The built-in Wuu engine has Standard, Read only, and Unconfined modes. Dedicated tools enforce path and sensitive-file checks. In confined modes, command tools also require an enforcing filesystem sandbox; they fail if one is unavailable.

That sandbox restricts filesystem writes. It does not isolate network access, all file reads, process visibility, or inherited environment variables. Read only is useful for investigation, but is not a data-loss-prevention boundary. Unconfined removes the command sandbox and gives commands the authority of the user running Wuu.

**Approve for me** adds model review in Standard mode. It does not expand permissions or replace isolation. External Codex and Claude Code sessions use their own execution controls. See [permission modes](permissions.md) for the exact scope and adapter settings.

For hostile repositories or dependencies, use a disposable environment or a separate OS account. Commands typed manually in the desktop terminal are not agent tool calls and do not inherit the agent's permission checks.

## Configuration and repository content

Normal startup treats the user configuration as the base. Project layers cannot replace provider connections, model selection through protected fields, instruction discovery, or the permission mode. This restriction also applies to `.wuu/settings.local.json` during normal startup. Explicit `wuu exec --config` and `--ignore-user-config` paths deliberately trust project configuration; use them only with reviewed inputs. See [configuration](configuration.md).

Repository instructions, skills, tool output, and retrieved content can influence a model without being trustworthy instructions. Review a new project's `AGENTS.md`, Wuu settings, hooks, skills, and MCP configuration before relying on them.

Project `.mcp.json` entries require a local trust decision before loading. Trusting an MCP server means trusting its executable or remote service with the data and credentials you give it; its responses remain external input.

## Extensions and hooks

Enabled extensions are trusted code, not sandboxed code. The current local-package workflow separates staging files from approving and activating them; see [plugin installation and updates](../customize/plugins.md). Wuu's loader, diagnostics, and safe mode do not certify an extension's safety.

Hooks and MCP subprocesses likewise run code outside the narrow agent-command sandbox contract. Check the source, arguments, environment, and remote destinations of code you enable. Disabling an extension is a lifecycle control, not a way to undo actions it already performed.

## Credentials and local records

Provider configuration can refer to environment variables. Managed desktop OAuth credentials use macOS Keychain; a Keychain failure is surfaced rather than silently falling back to a plaintext credential file. Headless authentication has file-based storage options, so secure the account and state directory that run it.

Conversation history, tool results, logs, artifacts, and plugin data can contain source code and private text. Treat Wuu's state directory, session exports, and diagnostic bundles as sensitive even when obvious tokens have been redacted. Review them before sharing or backing them up to another service.

## Desktop and remote control

`wuu app-server` communicates over the subprocess's standard input and output. It does not itself open a network listener. The desktop shell exposes selected operations through its preload and IPC bridge.

Remote control is a separate capability, and the current release build disables its desktop UI. If you build or run the remote components, treat an enrolled device as a controller of the host's workspaces. Use secure relay transport and revoke devices you no longer trust; see [remote and mobile control](../automation/remote.md).

Report suspected boundary bypasses privately through [SECURITY.md](../../../SECURITY.md).
