# Use plugin themes and settings

Enabled plugins may contribute themes and settings. This page explains
how users select, reset, and manage those contributions. Authors can find declaration
fields in the [plugin authoring reference](plugin-authoring.md#declarative-contributions).

## Select or reset a theme

Open **Settings → Appearance**. Enabled plugin themes appear alongside
**System**, **Light**, and **Dark**. A choice applies immediately without a restart.

To stop using a plugin theme, choose any built-in option:

- **System** follows the operating-system appearance;
- **Light** or **Dark** pins the corresponding built-in theme.

Returning to a built-in theme removes plugin token overrides. Disabling or removing
the plugin also removes its theme contribution. Appearance plugins cannot hide
Settings, plugin management, or recovery entries.

## Adjust reading size and interface scale

Desktop starts with a 14.5px UI font and one Zoom Out step. Existing saved font
sizes are preserved. Adjust the UI font in settings independently of the code
font. Use the View menu's Zoom In, Zoom Out, or Actual Size controls to change
the whole interface scale; the desktop remembers this choice across reloads.
These defaults are a starting point, not a required combination.

## Manage plugin settings

Wuu renders declared boolean, text, number, and enum fields, so the plugin does not
need to build its own form. After install, settings are available from:

- the plugin-contributed page in the **Settings** sidebar;
- the plugin details in **Skills & Plugins**.

Boolean and enum changes save immediately. Text and number fields normally save when
the field loses focus. Each field shows its default, scope, and whether it applies live
or after restart. Use the inline retry action when saving fails.

Settings may be user-scoped or workspace-scoped. Workspace values affect only the
current workspace. Wuu preserves settings and Storage by default across disable,
upgrade, and removal so they can be restored later; data is not automatically erased.

## Theme or settings are missing

Check the following in order:

1. the plugin is installed and enabled;
2. its source identity is unchanged;
3. the manifest actually declares a theme or setting;
4. the setting does not require a restart;
5. plugin diagnostics in **Skills & Plugins** do not report a manifest or activation error.

For disable, inspection, and removal steps, see
[Wuu Plugin recovery](plugins.md#recovery-and-troubleshooting).
