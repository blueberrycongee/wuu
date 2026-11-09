# Themes and settings

Use **Settings → Appearance** to choose a theme and adjust reading preferences. Enabled plugins can add themes and their own settings without replacing the built-in settings interface.

## Choose a theme

**System** follows the operating system; **Light** and **Dark** keep a fixed appearance. **Color palette** offers **Default**, **Linear**, **Codex**, **Absolutely**, **GitHub**, **Notion**, **One**, and themes from enabled plugins. Each menu option previews its background, text and accent colors. The light and dark mode previews reflect their saved palettes. Palettes coordinate the conversation background, filled user messages, text, inputs, sidebar hover and selection, and focus accents while assistant responses keep their open layout.

Changes apply immediately. Light and dark choices are saved separately, including when following the system. Choose **Default** to restore the current mode's neutral colors without changing the other mode or your shell skin.

Disabling or removing a plugin also removes its contributed themes. If a theme makes the interface difficult to use, return to a built-in theme or disable the plugin through plugin management.

## Shell skins

A shell skin styles the window frame and ribbon; skins that opt into **canvas placement** also paint their artwork behind the main workbench canvas. A skin does not change content colors or layout: text, accents and controls follow the **Color palette**, and conversations, settings and plugin pages keep their structure. Choose a skin under **Settings → Appearance → Shell skin** to apply it immediately.

With frame placement (the default), the rounded workbench keeps its own content background, independently of the skin. With canvas placement, conversations sit under a translucent veil in every state — the empty home included — and Settings, Skills and plugin pages stay opaque unless a plugin view opts in. Floating panels such as the composer and cards become frosted over the artwork. A canvas skin may render the artwork as a screen of round dots or square pixels, one color per cell. Under **Reduce transparency** the canvases stay opaque.

Plugins contribute paired light and dark variants. Disabling or removing a skin plugin restores the default frame; the saved choice returns when that plugin becomes available again. Safe mode also removes plugin skins. The shell skin example in `examples/plugins/shell-skins` provides two frame-placement skins without a runtime or desktop module; `examples/plugins/deep-field` demonstrates canvas placement with your own picture and a pixel screen, also without code.

## Use a background image

Your picture is the artwork of the selected skin. On desktop, **Background image** appears under the shell skin when that skin can show a picture: always for **Default**, and for plugin skins that accept one. Choose **Background image → Choose image**. Import a local PNG, JPEG or WebP up to 20 MB and 64 megapixels. Wuu stores a resized copy (up to 2048 pixels on its longest edge) in this desktop profile; the original file can be moved or deleted. Images are not uploaded or synced to other devices. Animated images use a still frame.

The **Default** skin shows the picture in the outer frame and ribbon; conversation, settings and workspace content keep their own surfaces. There, Wuu centers and softens the image with Gaussian blur, preserving its color and automatically adjusting contrast for light or dark shell text. A canvas skin that accepts a picture shows it sharp behind the main canvas in its own style, such as a pixel screen, and limits its brightness so the content on top stays readable. Skins with fixed artwork do not offer the row; the saved picture is kept for skins that show it. These treatments are built in, with no effect, strength or position controls. Changes apply to open desktop windows. **Remove** deletes the saved copy; a failed import or save preserves the previous image. Existing saved pictures adopt this treatment.

## Adjust text and motion

The UI font-size preference controls the interface and conversation prose together. Code size is separate, so you can make messages easier to read without enlarging code blocks and editors by the same amount. Valid saved preferences are preserved across upgrades.

There are two font choices: the **Interface font** covers the interface, messages, drafts, and Markdown previews, and the **Code font** covers code blocks, diffs, and the terminal. Inline code scales with the surrounding prose, while code blocks, diffs, and editors use the independent code size. **Reset fonts** restores both font choices without changing sizes.

On macOS, **Font smoothing** defaults to the system rendering for fuller regular-text strokes; choose **Grayscale** for a lighter appearance. You can also reduce motion. Fonts must be installed locally; unavailable fonts use the fallback family.

## Control commit attribution

In **Settings → General → Git**, **Agent commit attribution** controls whether Wuu adds `wuu-agent[bot]` as a co-author to commits it creates. Existing authors and other co-authors are preserved. You can save this setting while a conversation is running; that conversation keeps its current setting until its turn and background work finish, then adopts the new setting when it next runs.

## Change a plugin setting

Open the plugin's settings page or its details in **Skills & Plugins**. Wuu can render declared boolean, text, number, and enum fields; plugins can also provide custom settings content.

Boolean and enum fields save when changed. Text and number fields save when focus leaves the field. Check the save result before navigating away, and use the retry action if saving fails.

Each declared field identifies its user or workspace scope and whether it applies live or after restart. A workspace value affects only that workspace. Settings and plugin storage are preserved by default across disable, update, and removal; removing a package is not a data-erasure operation.

## Missing contributions

Check that the plugin is enabled, that it declares the theme or setting you expect, and that its detail page shows no trust, compatibility, or activation problem. Follow any restart requirement shown for the setting. Recovery commands are in [Wuu plugins](plugins.md#recovery-and-troubleshooting).

For theme and settings declarations, see the [authoring reference](plugin-authoring.md#declarative-contributions). The generated [theme surface matrix](theme-surface-matrix.md) maps theme tokens to interface surfaces.
