# Wuu plugins

A Wuu plugin packages agent behavior, desktop UI, or other extension contributions. A package can include tools, skills, hooks, MCP servers, themes, and settings. Enable only code you trust: plugins run with your user authority and are not sandboxed by Wuu.

## Install a local package

Open **Skills & Plugins**, choose a local directory or zip package, and open its details. In the current package flow, **Approve and enable** confirms trust and activates the package in one action. There is no central marketplace, and the installer does not fetch npm packages or Git repositories directly.

The CLI exposes the same local-package operations separately:

```bash
wuu plugin install ./my-plugin
wuu plugin approve my-plugin
wuu plugin list
```

Use the ID declared by the package. Installation copies files under `plugins/` in Wuu's home directory, normally `~/.wuu/plugins/`; `WUU_HOME` changes that location. The CLI's install operation stages the package but does not approve code execution by itself.

## Update, disable, and remove

Install a replacement package from its directory or zip, or use:

```bash
wuu plugin update my-plugin ./my-plugin-next.zip
wuu plugin approve my-plugin
```

The current local updater stages a replacement fingerprint and leaves the installed generation in place until it is accepted. Check the pending update in the detail page. A package whose content changed can require a refreshed trust decision; do not assume that copying new files has activated them.

Disable a package to stop its contributions for later conversations without removing its files. A conversation that is already running keeps the generation it started with. Remove it when it is no longer needed:

```bash
wuu plugin disable my-plugin
wuu plugin enable my-plugin
wuu plugin remove my-plugin
```

Settings and plugin storage are preserved by default. Removal does not erase all data the plugin created or undo completed operations.

## Recovery and troubleshooting

The detail page distinguishes pending trust, disabled, starting, active, failed, and update states. Read the actual error instead of treating every missing feature as an installation failure. A package may also be blocked by a missing requirement or an incompatible peer package.

If desktop code fails to render, Wuu isolates the failing contribution where possible and keeps plugin management and default-UI recovery available. Disable the suspected plugin and retry with the default interface. The CLI is useful when the desktop contribution itself is broken. Safe mode provides a recovery path after plugin-related startup failure; it does not certify the package as safe to re-enable.

## What plugins can provide

Agent runtimes can register tools, contribute context, observe supported lifecycle events, and provide or consume versioned services. Desktop modules can add views, fixed insertion points, semantic rendering replacements, conversation cards, and styles. Declarative themes and settings do not require a desktop module; see [themes and settings](themes-settings.md).

Bundled features use these mechanisms too. Guides cover [subagents](../desktop/subagents.md), [automations](../automation/scheduled-tasks.md), and [memory](memory.md). The Peers plugin lets Wuu and [external-engine conversations](../getting-started/external-engines.md#coordinate-with-peer-sessions) contact existing sessions in the same workspace, with one bounded request and terminal reply. It is separate from creating a child subagent or working with a named Collaboration identity.

## Develop a plugin

Start with the [agent quickstart](plugin-quickstart.md) or [desktop quickstart](desktop-plugin-quickstart.md). Local development is distinct from installing a distributable package. The [authoring reference](plugin-authoring.md) covers manifests, development commands, version requirements, and package relationships.

Wuu does not audit, certify, or host third-party extensions. Check compatibility with your Wuu build and review the [security model](../reference/security-model.md) before running unfamiliar code.
