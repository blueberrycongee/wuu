# Subagent message-flow preview

Synthetic preview of inline subagent activities and a right sidebar. Run from `desktop/`:

```sh
./node_modules/.bin/vite --config dev/subagent/vite.config.ts
```

Open <http://127.0.0.1:5238/dev/subagent/>. Click an agent name in the message flow to open its details. Click “另外 N 个子代理” to open the overview, grouped into active and finished tasks. The detail view returns to the overview; closing the sidebar or pressing Escape within it restores focus to the original entry. The parent draft survives navigation.

The toolbar switches between 3, 5, or 12 agents, failure, completion, empty state, light/dark theme, and 15/20px UI text. The right sidebar scrolls independently and fills the conversation area on narrow screens. URLs support `panel=overview`, `panel=routing`, `panel=protocol`, `scenario=many`, `theme=dark`, and `font=20`.

The shell, activities, avatars, task descriptions, work records, and results are synthetic. This preview does not invoke models, load real sessions, or send messages.

Capture repeatable visual evidence while Vite is running:

```sh
./node_modules/.bin/electron dev/subagent/capture.cjs
```

To capture a preview served on another port, set `WUU_SUBAGENT_PREVIEW_URL` to its full preview URL (including the trailing slash).

Build the standalone preview from `desktop/`:

```sh
./node_modules/.bin/vite build dev/subagent --config dev/subagent/vite.config.ts --outDir ../../../artifacts/subagent-stream-preview/build
```

Screenshots and geometry/interaction reports are written to ignored `artifacts/subagent-stream-preview/`. The harness covers desktop and 390px windows, default/large text, light/dark themes, task states, name/overflow navigation, child results, independent scrolling, draft preservation, and keyboard closing. This is a visual review harness, not production integration verification.

## Data boundaries

- The production plugin and this preview keep completed tasks available in the message flow and overview.
- `listPluginSessions` currently reports running/queued/completed. Reliable failure or interruption display must use terminal turn data rather than infer success from an inactive session.
- Production persists task descriptions separately from pending completion-delivery records, so completed tasks remain addressable from conversation entries.
- Completed child work does not mean the parent's overall task is complete. This distinction remains visible in the completion scenario.
