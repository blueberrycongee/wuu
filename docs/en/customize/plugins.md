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

## Get and install plugins

In Wuu Desktop, choose a local directory or zip package in the plugin catalog. Wuu then
opens that plugin's detail page; **Approve and enable** is the single trust confirmation
and enables the package immediately.

The package-management CLI currently exposes the lower-level local-package flow:

```bash
wuu plugin install ./foo
wuu plugin install ./foo-1.0.0.zip
```

The current CLI stages the local package; use `wuu plugin approve <id>` to enable it.
Packages are stored under `~/.wuu/plugins/`, or below `WUU_HOME` when set.

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

## Coordinate existing sessions

The bundled **Peers** plugin lets an agent contact another existing conversation.
It is enabled by default; an explicit disabled preference is preserved. Enable it
in plugin settings if needed, then ask the agent to contact a session by its copied
ID, or use `/peer` to discover available conversations in the current workspace.
Private, archived, and other-workspace sessions are excluded. Local forks in the
same workspace remain available; cross-workspace delivery is not supported.

Requests start a turn on an idle target or queue behind its current work. The
target's final response is returned once; that return does not automatically send
another reply. Messages keep their own source label and use the normal bubble,
including long-text expansion and copying. On Desktop, clicking the source opens
that conversation alongside the current one. Native phones also show the source.
Opt-in account history copies preserve attribution as a text heading for older
server compatibility; offline copies do not provide source navigation.
Cross-session messages are not direct user instructions and do not change the
target's permissions or goal. The agent can decline a request; `peer_policy` can
refuse incoming requests for a session. Disabling Peers removes its tools and
automatic coordination behavior.

A queued reply is not considered delivered until its receiving turn starts.
While Peers is enabled, it recovers replies lost from the host's pending queue
after a restart, using the original request identity and retained result. It
honors explicit queue cancellation. If a send is cancelled before its outcome
is known, an already accepted target can still return its result. Retryable
delivery failures do not turn a completed result into a request-timeout message.

## Trust boundary

Install code plugins only from sources you trust. Runtime processes have your user
authority, desktop modules can change the interface, and Hooks can run local commands.
Wuu does not review, certify, or sandbox third-party plugin code.

## Current compatibility boundary

Keeping plugins working across compatible Wuu product releases without a fork is the platform's
current completion gate, but the compatibility matrix has not yet been verified.
Declare `minimum_wuu_version` and retest after Wuu upgrades.

Simple package relationships are available: missing `requires` blocks activation,
`breaks` prevents both plugins from being enabled, and `conflicts` shows a warning.
There is no version-range solver or automatic conflict resolution today.

## Develop and publish

Start with the [Agent plugin quickstart](plugin-quickstart.md) or
[Desktop plugin quickstart](desktop-plugin-quickstart.md). For package formats and
development commands, see the [authoring reference](plugin-authoring.md).
