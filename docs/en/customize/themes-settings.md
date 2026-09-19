# Themes and settings

Use **Settings → Appearance** to choose a theme and adjust reading preferences. Enabled plugins can add themes and their own settings without replacing the built-in settings interface.

## Choose a theme

**System** follows the operating system; **Light** and **Dark** keep a fixed appearance. Plugin themes appear alongside these choices and apply immediately. Choose a built-in option to remove the plugin's theme overrides.

Disabling or removing a plugin also removes its contributed themes. If a theme makes the interface difficult to use, return to a built-in theme or disable the plugin through plugin management.

## Adjust text and motion

The UI font-size preference controls the interface and conversation prose together. Code size is separate, so you can make messages easier to read without enlarging code blocks and editors by the same amount. Valid saved preferences are preserved across upgrades.

Appearance settings also let you choose UI and code fonts and reduce motion. A font must be available on the machine to render as intended; otherwise the interface uses its fallback fonts.

## Change a plugin setting

Open the plugin's settings page or its details in **Skills & Plugins**. Wuu can render declared boolean, text, number, and enum fields; plugins can also provide custom settings content.

Boolean and enum fields save when changed. Text and number fields save when focus leaves the field. Check the save result before navigating away, and use the retry action if saving fails.

Each declared field identifies its user or workspace scope and whether it applies live or after restart. A workspace value affects only that workspace. Settings and plugin storage are preserved by default across disable, update, and removal; removing a package is not a data-erasure operation.

## Missing contributions

Check that the plugin is enabled, that it declares the theme or setting you expect, and that its detail page shows no trust, compatibility, or activation problem. Follow any restart requirement shown for the setting. Recovery commands are in [Wuu plugins](plugins.md#recovery-and-troubleshooting).

For theme and settings declarations, see the [authoring reference](plugin-authoring.md#declarative-contributions). The generated [theme surface matrix](theme-surface-matrix.md) maps theme tokens to interface surfaces.
