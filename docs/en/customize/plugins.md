# Wuu plugins

Manage standalone [MCP services](mcp.md) in the Plugins catalog. MCP services supplied by a plugin appear on its detail page, with their connection and login controls.

A Wuu plugin packages agent behavior, desktop UI, or other extension contributions. A package can include tools, skills, hooks, MCP servers, themes, and settings. Enable only code you trust: plugins run with your user authority and are not sandboxed by Wuu.

## Install a local package

Open **Skills & Plugins** and install a local directory or zip package. Installing confirms trust in that source and enables the plugin. There is no central marketplace, and the installer does not fetch npm packages or Git repositories directly.

The CLI uses the same trust rule:

```bash
wuu plugin install ./my-plugin
wuu plugin list
```

Use the ID declared by the package. Installation copies files under `plugins/` in Wuu's home directory, normally `~/.wuu/plugins/`; `WUU_HOME` changes that location. Installation records the source and the exact package fingerprint in the selected configuration service. Activation still checks compatibility and dependencies; inspect diagnostics if the plugin cannot start.

## Update, disable, and remove

Install a replacement package from its directory or zip, or use:

```bash
wuu plugin update my-plugin ./my-plugin
```

Updates from the same recorded source retain trust and the enabled or disabled choice. A different source, or an older grant without a recorded source, leaves the update pending and the installed generation active. Confirm that source change in the detail page or with `wuu plugin approve my-plugin`. A changed version or content fingerprint alone does not require another approval. Copying files directly into the installed directory is not the update workflow.

For local packages, the source is the canonical directory or zip path, including symlink resolution. Replacing a zip at that same path is a same-source update; moving to a different path requires confirmation.

A committed disable stops the plugin across retained runtime generations without removing its files: native executions are canceled, owned MCP servers cannot reconnect, and future legacy hooks and transforms are removed. Already-running legacy hooks retain their caller lifetime; completed effects and conversation history remain. This differs from a same-ID update, which lets active work finish on its original generation. Remove the package when it is no longer needed:

```bash
wuu plugin disable my-plugin
wuu plugin enable my-plugin
wuu plugin remove my-plugin
```

Settings and plugin storage are preserved by default. Removal does not erase all data the plugin created or undo completed operations.

## Recovery and troubleshooting

If an operation reports that the change was committed but runtime activation failed, the settings or package change has already taken effect. Read the new inventory's failure diagnostics before retrying; the old runtime is not automatically restored. See the [generation lifecycle](plugin-system.md#runtime-generations).

The detail page distinguishes pending trust, disabled, starting, active, failed, and update states. Read the actual error instead of treating every missing feature as an installation failure. A package may also be blocked by a missing requirement or an incompatible peer package.

If desktop code fails to render, Wuu isolates the failing contribution where possible and keeps plugin management and default-UI recovery available. Disable the suspected plugin and retry with the default interface. The CLI is useful when the desktop contribution itself is broken. Safe mode provides a recovery path after plugin-related startup failure; it does not certify the package as safe to re-enable.

## What plugins can provide

Agent runtimes can register tools, contribute context, observe supported lifecycle events, and provide or consume versioned services. Desktop modules can add views, fixed insertion points, semantic rendering replacements, conversation cards, and styles. Declarative themes and settings do not require a desktop module; see [themes and settings](themes-settings.md).

Bundled features use these mechanisms too. Guides cover [subagents](../desktop/subagents.md), [automations](../automation/scheduled-tasks.md), and [memory](memory.md). The Peers plugin lets an agent contact existing conversations in the same workspace; it is separate from creating a child subagent.

## Monitor background events

The bundled Monitor plugin runs a shell script while the agent continues working.
Ask it, for example, to watch CI and report a failed check immediately, then finish
when every check passes. Scripts should print relevant changes; each output batch
can wake the conversation or steer its current turn. Monitor events are observations,
so they do not answer a question that is waiting for you.

Open **Monitors** above the composer to see descriptions, line counts, task IDs,
status, commands, deadlines and output. The running count remains visible while the
agent is idle. Expand a task to follow its output or stop its process tree. A manual
stop tells the agent not to restart that monitor unless you ask.

`monitor` accepts `command`, `description`, optional `timeout_ms` (up to 10 hours,
the default), and `persistent`. A persistent watch has no deadline and lasts until
stopped, its conversation is archived/deleted, or its plugin runtime ends. Watches
started in a private child conversation pass to its parent when the child finishes.
`monitor_tasks` lists, reads or stops watches in the current conversation.

Standard output and standard error are combined. Blank lines are ignored, incomplete
lines flush on exit, and events are batched every 200 ms. Delivery allows a burst of
10 batches and replenishes one every two seconds. Excess batches are counted; 30
seconds of sustained overload stops the script. Event lines are shortened to 500
bytes and batches to 3,000 bytes, while the output viewer uses the retained process
log. Use line-buffered pipelines and handle transient network failures in scripts.

Shell execution currently requires a local workspace and follows its shell permission
and sandbox policy. Disabling the plugin or retiring its runtime stops its processes.
On restart, running records become interrupted and retain their recent output;
scripts are never automatically restarted. Up to 64 task records are retained.

## Develop a plugin

Start with the [agent quickstart](plugin-quickstart.md) or [desktop quickstart](desktop-plugin-quickstart.md). Local development is distinct from installing a distributable package. The [authoring reference](plugin-authoring.md) covers manifests, development commands, version requirements, and package relationships.

Wuu does not audit, certify, or host third-party extensions. Check compatibility with your Wuu build and review the [security model](../reference/security-model.md) before running unfamiliar code.
