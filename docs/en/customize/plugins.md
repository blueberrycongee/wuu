# Wuu Plugins

A Wuu Plugin is an installable and upgradeable extension package. It can provide one
capability or combine an Agent runtime, Desktop UI, themes, settings, Skills, Hooks,
MCP servers, and commands. Installing a plugin means you trust its code to run with
your user authority; Wuu does not sandbox it.

If you are not sure that you need a plugin, start with [Extend Wuu](index.md). A
repeatable instruction set may only need a Skill, and an existing tool service may
only need MCP. Use a Wuu Plugin when you need code lifecycle, host services, or
desktop UI.

The platform is local-first: there is no marketplace or central registry. Authors
normally develop and release plugins from their own repositories. The current installer
accepts a local directory or zip package; direct npm and Git-source installation are not
available yet.

## What one package can contain

| Type | What it contributes | Code required? |
| --- | --- | --- |
| Declarative contributions | Themes, settings, Skills, Hooks, MCP servers, and commands | Depends on the contribution |
| Agent plugin | Tools, context, request transforms, turn observers, provided and consumed services | Yes, separate process |
| Desktop plugin | Views, Slots, Presenters, Surfaces, styles, and interactive cards | Yes, Renderer code |

One package may declare both `runtime` and `desktop.entry`. For example, the runtime
can query a private service while the Desktop module presents the result. Both share
the plugin ID and the one install/trust lifecycle.

Plugin management, safe mode, crash recovery, and native window lifecycle remain
host-owned. Plugins cannot replace those recovery paths.

Choose a first path:

- [Agent plugin quickstart](plugin-quickstart.md) — register a model-visible tool and use Storage;
- [Desktop plugin quickstart](desktop-plugin-quickstart.md) — add a real Composer control;
- [Desktop UI extension map](desktop-plugins.md) — choose a View, Slot, Presenter, or Surface;
- [Desktop plugin recipes](plugin-recipes.md) — compose selection UI, draft actions, and full panels.

## Get and install plugins

In Wuu Desktop, choose a local directory or zip package in the plugin catalog. Wuu then
opens that plugin's detail page; **Approve and enable** is the single trust confirmation
and enables the package immediately.

The package-management CLI currently exposes the lower-level local-package flow:

```bash
wuu plugin install ./foo
wuu plugin install ./foo-1.0.0.zip
```

The CLI stages the local package; use `wuu plugin approve <id>` to activate that staged
fingerprint. This split CLI is a compatibility surface, not an extra trust model. Wuu
installs packages under `~/.wuu/plugins/`, or below `WUU_HOME` when set. In every entry
point, approving and enabling code is the trust decision: it runs with your user
authority.

## Trust, update, and user-visible state

- Approving and enabling a package means trusting that package's code.
- The current local-package updater stages each new package fingerprint and keeps the
  installed generation active until the replacement is confirmed.
- Extensions in a trusted project directory load with the project's trust, without
  per-plugin confirmation.
- A path passed to `wuu plugin dev` is explicit development execution and never inherits
  trust from an installed package.
- A failed update reports the failure and keeps a recoverable entry; the user is not
  sent through onboarding again.

A plugin is always in one of three user-visible states: `Enabled` (installed and
running), `Disabled` (the user turned it off), or `Failed` (load or run failure; the
error is viewable and the plugin can be disabled).

```bash
wuu plugin list
wuu plugin disable my-plugin
wuu plugin remove my-plugin
```

## Recovery and troubleshooting

- **Failed:** the error is visible in the plugin list and settings page; disable the
  plugin or reinstall it. Other enabled plugins keep running.
- **Render failure:** Wuu falls back only at the failed Slot, Presenter, Surface, or View;
  plugin management and default-UI recovery remain available.
- **Immediate isolation:** run `wuu plugin disable <id>`. The CLI can disable a plugin
  even when its Desktop contribution is broken. If Wuu enters safe mode after a crash,
  leave the suspected plugin disabled while investigating.
- **Removal:** run `wuu plugin remove <id>`. Wuu currently preserves plugin settings and
  Storage by default, so removing a package is not the same as erasing all user data.

## Common capabilities

- **Change themes:** declarative token themes appear under **Settings → Appearance**
  and are removed cleanly when disabled. See [plugin themes and settings](themes-settings.md)
  for user actions.
- **Add settings:** schema fields create host-rendered controls stored in the plugin
  namespace. Settings and Storage remain available after reinstall by default.
- **Extend the agent:** a managed runtime can register model-visible tools, contribute
  context, transform supported request fields, observe lifecycle events, and provide or
  consume versioned services.
- **Customize Desktop UI:** add persistent Views, insert fixed Slots, wrap or replace
  semantic Presenters and Surfaces, show conversation cards, and register styles.
- **Compose extension types:** carry Skills, Hooks, MCP servers, commands, Agent code,
  and Desktop code in one package with one install and upgrade lifecycle.

## Trust boundary

- Agent runtime processes run with the same user authority as Wuu.
- Desktop modules run in the Renderer and may register arbitrary CSS.
- Hooks have the same risk as running the declared local command directly.
- The Renderer never receives an absolute plugin path. App-server records the source
  identity; Electron imports desktop code through a content-addressed `wuu-plugin:`
  URL. CSP does not enable `unsafe-eval`.
- Wuu does not review, certify, or sandbox plugin code. Updates keep trust by source
  identity, not by per-change approval.

Install code plugins only from sources you trust.

## Current compatibility boundary

Keeping plugins working across compatible Wuu product releases without a fork is the platform's
current completion gate, but the compatibility matrix has not yet been verified.
Declare `minimum_wuu_version` and retest after Wuu upgrades.

Simple package relationships are available: missing `requires` blocks activation,
`breaks` prevents both plugins from being enabled, and `conflicts` shows a warning.
There is no version-range solver or automatic conflict resolution today.

## Develop and publish

```bash
wuu plugin create --type agent my-agent
wuu plugin create --type desktop my-ui
wuu plugin create --type full my-extension

wuu plugin validate ./my-extension
wuu plugin build ./my-extension
wuu plugin test ./my-extension
wuu plugin dev ./my-extension
wuu plugin pack ./my-extension
```

See the [plugin authoring reference](plugin-authoring.md) for the complete manifest,
Agent protocol, Desktop API, lifecycle, and security boundaries. See the
[plugin system architecture](plugin-system.md) for the design rationale and host
ownership model.
