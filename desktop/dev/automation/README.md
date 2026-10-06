# Automation UI preview

From `desktop`, run `npx vite --host 127.0.0.1 --port 5189`, then open
`http://127.0.0.1:5189/dev/automation/`.

The preview loads the real bundled automation module, public PluginHost and UI
primitives, and Wuu styles. Workspace, conversation, and runtime responses use
in-memory fixtures. Changes reset when the page reloads and never schedule work.

Task selection opens a read-only overview with recent runs, instructions, and
schedule details. Choose Edit to change a task; Cancel returns to its saved
overview. Completed one-shot snapshots remain read-only.

Inspect creating from a suggestion, editing schedules, searching chat targets,
pausing, deleting, dragging the detail divider (including its minimum widths and
keyboard arrows), and widths above and below 760px. Add `long&states` to inspect
long titles, running tasks, and interrupted runs on paused tasks. Use `?theme=dark`
for dark mode, `?font=18` for large text, and `?reference`
to show an actual Wuu SettingsRow and SelectMenu alongside the plugin.
`?region=primary` mounts the page under the production titlebar; add `compact`
for its narrow-window actions. Back returns to a placeholder conversation pane.

For repeatable Electron checks, run from `desktop`:

```sh
./node_modules/.bin/electron scripts/automation-e2e.cjs
```

The script builds this fixture and checks overview/edit separation, cancellation,
saving, pausing, completed snapshots, and template creation. Screenshots and
layout measurements for light/dark themes, 14px/18px text, and wide/narrow windows
are saved with `results.json` under `out/automation-e2e-*/`. It uses synthetic data,
not the live scheduler; screenshots still need visual review.
