# Deep UI Example

This package is intentionally self-contained: `desktop.js` has no relative imports and uses the
React instance supplied by Wuu. It demonstrates:

- a declarative dark theme with allowlisted semantic and syntax tokens;
- an explicit wrapper for `wuu.default-conversation/conversation.turn`, including messages and tool activity;
- automatic cleanup when the package is disabled or replaced.

Install this directory from Wuu's Skills and Plugins catalog. Select
**Violet Night** under Settings → Appearance to apply the theme.

The wrapper CSS is deliberately small. It preserves every built-in fallback and therefore also
demonstrates the recovery behavior if the plugin is disabled.
