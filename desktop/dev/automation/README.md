# Automation UI preview

From `desktop`, run `npx vite --host 127.0.0.1 --port 5189`, then open
`http://127.0.0.1:5189/dev/automation/`.

The preview loads the real bundled automation module, public PluginHost and UI
primitives, and Wuu styles. Workspace, conversation, and runtime responses use
in-memory fixtures. Changes reset when the page reloads and never schedule work.

Inspect task selection, creating a new automation, editing schedules, searching
chat targets, pausing, deleting, dragging the editor divider (including its minimum
widths and keyboard arrows), and widths above and below 760px. Use `?theme=dark` for dark mode, `?font=18` for large text, and `?reference`
to show an actual Wuu SettingsRow and SelectMenu alongside the plugin.
`?region=primary` mounts the page under the production titlebar; add `compact`
for its narrow-window actions. Back returns to a placeholder conversation pane.

Use `?sidebar&active&long` to mount the production AppSidebar with running,
starting, queued, paused, and upcoming tasks. Select a sidebar row to open its
editor; pause/resume a scheduled task and switch to Empty project to inspect
synchronization. A dispatched one-shot opens its retained run snapshot.
Add `many` for scrolling, `empty` for empty groups, `error` for refresh failure,
`multiple` for independent catalog instances, or `locale=en-US` for English.
Combine these with `theme=dark` and `font=20` to check narrow layouts and large text.
