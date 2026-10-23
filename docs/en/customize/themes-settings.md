# Themes and settings

Use **Settings → Appearance** to choose a theme and adjust reading preferences. Enabled plugins can add themes and their own settings without replacing the built-in settings interface.

## Choose a theme

**System** follows the operating system; **Light** and **Dark** keep a fixed appearance. **Content theme** selects an enabled plugin theme for the current appearance. Light and dark choices are saved separately. Choose **Default** to remove the current mode's plugin overrides.

Disabling or removing a plugin also removes its contributed themes. If a theme makes the interface difficult to use, return to a built-in theme or disable the plugin through plugin management.

## Shell skins

Shell skins decorate the ribbon and window frame. Choose a preview under **Settings → Appearance → Shell skin** to apply it immediately. The rounded workbench keeps its own content background, independently of the skin.

Plugins contribute paired light and dark variants. Disabling or removing a skin plugin restores the default frame; the saved choice returns when that plugin becomes available again. Safe mode also removes plugin skins. The shell skin example in `examples/plugins/shell-skins` provides two complete skins without a runtime or desktop module.

## Use a background image

On desktop, choose **Background image → Choose image** in Appearance. Import a local PNG, JPEG or WebP up to 20 MB and 64 megapixels. Wuu stores a resized copy (up to 2048 pixels on its longest edge) in this desktop profile; the original file can be moved or deleted. Images are not uploaded or synced to other devices. Animated images use a still frame.

The picture decorates only the outer frame and ribbon, including the expanded ribbon. Conversation, settings and workspace content keep their own surfaces. Importing selects **My image**; switching to another skin retains the saved picture. Wuu centers and softens the image with Gaussian blur, preserving its color and automatically adjusting contrast for light or dark shell text. This treatment is built in, with no effect, strength or position controls. Changes apply to open desktop windows. **Remove** deletes the saved copy; a failed import or save preserves the previous image. Existing saved pictures adopt this treatment.

## Adjust text and motion

The UI font-size preference controls the interface and conversation prose together. Code size is separate, so you can make messages easier to read without enlarging code blocks and editors by the same amount. Valid saved preferences are preserved across upgrades.

Choose fonts separately for the interface, content, and code. Content defaults to **Same as UI** and applies to messages, drafts, and Markdown previews; its size follows the UI preference. Inline code scales with the surrounding prose, while code blocks, diffs, and editors use the independent code size. **Reset fonts** restores all three font choices without changing sizes.

On macOS, **Font smoothing** defaults to the system rendering for fuller regular-text strokes; choose **Grayscale** for a lighter appearance. You can also reduce motion. Fonts must be installed locally; unavailable fonts use the fallback family.

## Control commit attribution

In **Settings → Runtime → Git**, **Agent commit attribution** controls whether Wuu adds `wuu-agent[bot]` as a co-author to commits it creates. Existing authors and other co-authors are preserved. You can save this setting while a conversation is running; that conversation keeps its current setting until its turn and background work finish, then adopts the new setting when it next runs.

## Change a plugin setting

Open the plugin's settings page or its details in **Skills & Plugins**. Wuu can render declared boolean, text, number, and enum fields; plugins can also provide custom settings content.

Boolean and enum fields save when changed. Text and number fields save when focus leaves the field. Check the save result before navigating away, and use the retry action if saving fails.

Each declared field identifies its user or workspace scope and whether it applies live or after restart. A workspace value affects only that workspace. Settings and plugin storage are preserved by default across disable, update, and removal; removing a package is not a data-erasure operation.

## Missing contributions

Check that the plugin is enabled, that it declares the theme or setting you expect, and that its detail page shows no trust, compatibility, or activation problem. Follow any restart requirement shown for the setting. Recovery commands are in [Wuu plugins](plugins.md#recovery-and-troubleshooting).

For theme and settings declarations, see the [authoring reference](plugin-authoring.md#declarative-contributions). The generated [theme surface matrix](theme-surface-matrix.md) maps theme tokens to interface surfaces.
