# Automation UI preview

From `desktop`, run `npx vite --host 127.0.0.1 --port 5189`, then open
`http://127.0.0.1:5189/dev/automation/`.

The preview loads the real bundled automation module, public PluginHost and UI
primitives, and Wuu styles. Workspace, conversation, and runtime responses use
in-memory fixtures. Changes reset when the page reloads and never schedule work.

Inspect task selection, creating from a suggestion, editing schedules, searching
chat targets, pausing, deleting, dragging the editor divider (including its minimum
widths and keyboard arrows), and widths above and below 760px. Use `?theme=dark` for dark mode, `?font=18` for large text, and `?reference`
to show an actual Wuu SettingsRow and SelectMenu alongside the plugin.
